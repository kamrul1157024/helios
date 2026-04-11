package tunnel

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// CloudflareTunnel uses `cloudflared tunnel --url` for quick tunnels.
type CloudflareTunnel struct {
	cmd *exec.Cmd
	url string
}

func (t *CloudflareTunnel) Provider() string { return "cloudflare" }
func (t *CloudflareTunnel) URL() string      { return t.url }

func (t *CloudflareTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *CloudflareTunnel) Start(localPort int) error {
	binary, err := exec.LookPath("cloudflared")
	if err != nil {
		return fmt.Errorf("cloudflared not found: install with 'brew install cloudflared'")
	}

	localURL := fmt.Sprintf("http://localhost:%d", localPort)
	t.cmd = exec.Command(binary, "tunnel", "--url", localURL)
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start cloudflared: %w", err)
	}

	// Detach — we manage via PID, not parent-child
	go t.cmd.Wait()

	// Parse stderr for the tunnel URL
	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
		for scanner.Scan() {
			line := scanner.Text()
			if match := re.FindString(line); match != "" {
				urlCh <- match
				return
			}
		}
	}()

	select {
	case url := <-urlCh:
		t.url = url
		return nil
	case <-time.After(30 * time.Second):
		killProcess(t.cmd.Process.Pid)
		return fmt.Errorf("timeout waiting for cloudflare tunnel URL")
	}
}

func (t *CloudflareTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

// NgrokTunnel uses `ngrok http` and queries the local API for the URL.
type NgrokTunnel struct {
	cmd *exec.Cmd
	url string
}

func (t *NgrokTunnel) Provider() string { return "ngrok" }
func (t *NgrokTunnel) URL() string      { return t.url }

func (t *NgrokTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *NgrokTunnel) Start(localPort int) error {
	binary, err := exec.LookPath("ngrok")
	if err != nil {
		return fmt.Errorf("ngrok not found: install from https://ngrok.com/download")
	}

	t.cmd = exec.Command(binary, "http", fmt.Sprintf("%d", localPort))
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Discard stdout/stderr so the pipe doesn't block after we detach
	t.cmd.Stdout = nil
	t.cmd.Stderr = nil

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start ngrok: %w", err)
	}

	go t.cmd.Wait()

	// Poll ngrok API for tunnel URL
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second)

		url, err := getNgrokURL()
		if err == nil && url != "" {
			t.url = url
			return nil
		}
	}

	killProcess(t.cmd.Process.Pid)
	return fmt.Errorf("timeout waiting for ngrok tunnel URL")
}

func (t *NgrokTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

func getNgrokURL() (string, error) {
	// ngrok exposes a local API at http://127.0.0.1:4040/api/tunnels
	resp, err := defaultHTTPClient.Get("http://127.0.0.1:4040/api/tunnels")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Simple JSON parsing — look for public_url with https
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, `"public_url"`); idx >= 0 {
			// Find the https URL
			re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.ngrok[a-zA-Z0-9.-]*`)
			if match := re.FindString(line); match != "" {
				return match, nil
			}
		}
	}
	return "", fmt.Errorf("no tunnel URL found")
}

// TailscaleTunnel uses tailscale funnel.
type TailscaleTunnel struct {
	cmd *exec.Cmd
	url string
}

func (t *TailscaleTunnel) Provider() string { return "tailscale" }
func (t *TailscaleTunnel) URL() string      { return t.url }

func (t *TailscaleTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *TailscaleTunnel) Start(localPort int) error {
	binary, err := exec.LookPath("tailscale")
	if err != nil {
		return fmt.Errorf("tailscale not found: install from https://tailscale.com/download")
	}

	// Get the tailscale DNS name
	out, err := exec.Command(binary, "status", "--json").Output()
	if err != nil {
		return fmt.Errorf("tailscale status: %w", err)
	}

	// Extract DNS name from JSON output
	re := regexp.MustCompile(`"DNSName"\s*:\s*"([^"]+)"`)
	match := re.FindSubmatch(out)
	if match == nil {
		return fmt.Errorf("could not determine tailscale DNS name")
	}
	dnsName := strings.TrimSuffix(string(match[1]), ".")
	t.url = fmt.Sprintf("https://%s:%d", dnsName, localPort)

	// Start tailscale funnel
	t.cmd = exec.Command(binary, "funnel", fmt.Sprintf("%d", localPort))
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start tailscale funnel: %w", err)
	}

	go t.cmd.Wait()

	// Give it a moment to start
	time.Sleep(2 * time.Second)
	return nil
}

func (t *TailscaleTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

// LocalTunnel discovers LAN IP — no tunnel process needed.
type LocalTunnel struct {
	url string
}

func (t *LocalTunnel) Provider() string { return "local" }
func (t *LocalTunnel) URL() string      { return t.url }
func (t *LocalTunnel) PID() int         { return 0 }

func (t *LocalTunnel) Start(localPort int) error {
	ip, err := getLANIP()
	if err != nil {
		return err
	}
	t.url = fmt.Sprintf("http://%s:%d", ip, localPort)
	return nil
}

func (t *LocalTunnel) Stop() error { return nil }

// CustomTunnel uses a user-provided URL — no process to manage.
type CustomTunnel struct {
	customURL string
}

func (t *CustomTunnel) Provider() string { return "custom" }
func (t *CustomTunnel) URL() string      { return t.customURL }
func (t *CustomTunnel) PID() int         { return 0 }

func (t *CustomTunnel) Start(_ int) error {
	if t.customURL == "" {
		return fmt.Errorf("custom URL is required")
	}
	return nil
}

func (t *CustomTunnel) Stop() error { return nil }

// killProcess sends SIGTERM, waits briefly, then SIGKILL if needed.
func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	// Try graceful SIGTERM first
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return nil // already dead
	}

	// Wait up to 3 seconds for graceful exit
	done := make(chan struct{})
	go func() {
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				close(done)
				return
			}
		}
		close(done)
	}()

	<-done

	// Force kill if still alive
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		proc.Signal(syscall.SIGKILL)
	}
	return nil
}
