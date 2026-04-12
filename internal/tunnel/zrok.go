package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ZrokTunnel uses `zrok share` for zero-trust tunneling via OpenZiti.
type ZrokTunnel struct {
	cmd            *exec.Cmd
	url            string
	shareMode      string // public | reserved
	shareToken     string // reserved share token
	onTokenCreated func(token string)
}

func (t *ZrokTunnel) Provider() string { return "zrok" }
func (t *ZrokTunnel) URL() string      { return t.url }

func (t *ZrokTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *ZrokTunnel) Start(localPort int) error {
	binary, err := lookupZrokBinary()
	if err != nil {
		return err
	}

	if err := checkZrokEnabled(binary); err != nil {
		return err
	}

	switch t.shareMode {
	case "public":
		return t.startPublic(binary, localPort)
	default:
		// Default to reserved for URL stability
		return t.startReserved(binary, localPort)
	}
}

func (t *ZrokTunnel) startReserved(binary string, localPort int) error {
	if t.shareToken == "" {
		token, err := t.createReservation(binary, localPort)
		if err != nil {
			log.Printf("zrok: reservation failed, falling back to public share: %v", err)
			return t.startPublic(binary, localPort)
		}
		t.shareToken = token
		if t.onTokenCreated != nil {
			t.onTokenCreated(token)
		}
	}

	t.cmd = exec.Command(binary, "share", "reserved", t.shareToken, "--headless")
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	return t.startAndParseURL()
}

func (t *ZrokTunnel) createReservation(binary string, localPort int) (string, error) {
	target := fmt.Sprintf("%d", localPort)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "reserve", "public",
		"--backend-mode", "proxy",
		target,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("zrok reserve: %s: %w", strings.TrimSpace(string(out)), err)
	}

	token := parseZrokShareToken(string(out))
	if token == "" {
		return "", fmt.Errorf("could not parse share token from zrok reserve output: %s", strings.TrimSpace(string(out)))
	}
	return token, nil
}

func (t *ZrokTunnel) startPublic(binary string, localPort int) error {
	t.cmd = exec.Command(binary, "share", "public",
		fmt.Sprintf("%d", localPort), "--headless",
	)
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	return t.startAndParseURL()
}

func (t *ZrokTunnel) startAndParseURL() error {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start zrok: %w", err)
	}

	go t.cmd.Wait()

	urlCh := make(chan string, 1)
	re := zrokURLRegex

	scanForURL := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if match := re.FindStringSubmatch(line); match != nil {
				host := match[1]
				url := host
				if !strings.HasPrefix(url, "https://") {
					url = "https://" + url
				}
				select {
				case urlCh <- url:
				default:
				}
				return
			}
		}
	}

	go scanForURL(stdout)
	go scanForURL(stderr)

	select {
	case url := <-urlCh:
		t.url = url
		return nil
	case <-time.After(60 * time.Second):
		killProcess(t.cmd.Process.Pid)
		return fmt.Errorf("timeout waiting for zrok share URL")
	}
}

func (t *ZrokTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

// lookupZrokBinary tries "zrok" first, then "zrok2".
func lookupZrokBinary() (string, error) {
	if path, err := exec.LookPath("zrok"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("zrok2"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("zrok not found: install with 'brew install openziti/tap/zrok' or from https://zrok.io")
}

// checkZrokEnabled verifies the zrok environment is enabled.
func checkZrokEnabled(binary string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "status").CombinedOutput()
	if err != nil {
		return fmt.Errorf("zrok not enabled: run 'zrok enable <token>' first: %w", err)
	}
	lower := strings.ToLower(string(out))
	if strings.Contains(lower, "not enabled") || strings.Contains(lower, "not yet enabled") {
		return fmt.Errorf("zrok environment not enabled: run 'zrok enable <token>' to set up")
	}
	return nil
}

// parseZrokShareToken extracts a share token from zrok reserve output.
// Example output: "your reserved share token is abc123xyz"
var zrokTokenRegex = regexp.MustCompile(`(?i)(?:token\s+(?:is\s+)?|reserved\s+)([a-zA-Z0-9]{6,})`)

func parseZrokShareToken(output string) string {
	if match := zrokTokenRegex.FindStringSubmatch(output); match != nil {
		return match[1]
	}
	// Fallback: look for a standalone alphanumeric token on its own line
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 6 && regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(trimmed) {
			return trimmed
		}
	}
	return ""
}

// zrokURLRegex matches zrok share URLs.
// Handles both zrok.io hosted (https://xxx.share.zrok.io) and self-hosted instances.
// Also matches bare hostnames without https:// prefix (zrok v2 outputs just the hostname).
var zrokURLRegex = regexp.MustCompile(`(?:https://)?([a-zA-Z0-9-]+\.(?:shares?\.)?zrok[a-zA-Z0-9.-]*\.[a-z]{2,})`)
