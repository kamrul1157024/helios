package tunnel

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"syscall"
	"time"
)

// LocaltunnelTunnel uses the `lt` CLI (localtunnel) for simple tunneling.
type LocaltunnelTunnel struct {
	cmd                  *exec.Cmd
	url                  string
	subdomain            string
	host                 string
	onSubdomainAssigned  func(subdomain string)
}

func (t *LocaltunnelTunnel) Provider() string { return "localtunnel" }
func (t *LocaltunnelTunnel) URL() string      { return t.url }

func (t *LocaltunnelTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *LocaltunnelTunnel) Start(localPort int) error {
	binary, err := exec.LookPath("lt")
	if err != nil {
		// Fallback to npx
		npx, npxErr := exec.LookPath("npx")
		if npxErr != nil {
			return fmt.Errorf("localtunnel not found: install with 'npm install -g localtunnel' or 'brew install localtunnel'")
		}
		return t.startWithArgs(npx, t.buildNpxArgs(localPort))
	}
	return t.startWithArgs(binary, t.buildArgs(localPort))
}

func (t *LocaltunnelTunnel) buildArgs(localPort int) []string {
	args := []string{"--port", fmt.Sprintf("%d", localPort)}
	if t.subdomain != "" {
		args = append(args, "--subdomain", t.subdomain)
	}
	if t.host != "" {
		args = append(args, "--host", t.host)
	}
	return args
}

func (t *LocaltunnelTunnel) buildNpxArgs(localPort int) []string {
	args := []string{"localtunnel", "--port", fmt.Sprintf("%d", localPort)}
	if t.subdomain != "" {
		args = append(args, "--subdomain", t.subdomain)
	}
	if t.host != "" {
		args = append(args, "--host", t.host)
	}
	return args
}

func (t *LocaltunnelTunnel) startWithArgs(binary string, args []string) error {
	t.cmd = exec.Command(binary, args...)
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start localtunnel: %w", err)
	}

	go t.cmd.Wait()

	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if match := localtunnelURLRegex.FindStringSubmatch(line); match != nil {
				urlCh <- match[0]
				// Extract and persist subdomain for reuse
				if len(match) > 1 && t.onSubdomainAssigned != nil {
					t.onSubdomainAssigned(match[1])
				}
				return
			}
		}
	}()

	select {
	case url := <-urlCh:
		t.url = url
		log.Printf("localtunnel: tunnel URL: %s", url)
		return nil
	case <-time.After(30 * time.Second):
		killProcess(t.cmd.Process.Pid)
		return fmt.Errorf("timeout waiting for localtunnel URL")
	}
}

func (t *LocaltunnelTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

// localtunnelURLRegex matches localtunnel URLs.
// Captures: (subdomain).(host domain)
// Handles loca.lt, localtunnel.me, and custom server domains.
var localtunnelURLRegex = regexp.MustCompile(`https://([a-zA-Z0-9-]+)\.(loca\.lt|localtunnel\.me|[a-zA-Z0-9.-]+\.[a-z]{2,})`)
