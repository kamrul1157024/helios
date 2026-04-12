package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// LocalxposeTunnel uses the `loclx` CLI for feature-rich tunneling.
type LocalxposeTunnel struct {
	cmd            *exec.Cmd
	url            string
	subdomain      string // ephemeral subdomain
	reservedDomain string // reserved domain
	region         string // us | eu | ap
	basicAuth      string // user:pass
	accessToken    string // access token override
}

func (t *LocalxposeTunnel) Provider() string { return "localxpose" }
func (t *LocalxposeTunnel) URL() string      { return t.url }

func (t *LocalxposeTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *LocalxposeTunnel) Start(localPort int) error {
	binary, err := exec.LookPath("loclx")
	if err != nil {
		return fmt.Errorf("localxpose not found: install from https://localxpose.io or 'npm install -g loclx'")
	}

	if err := checkLocalxposeAuth(binary); err != nil {
		return err
	}

	args := t.buildArgs(localPort)

	t.cmd = exec.Command(binary, args...)
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if t.accessToken != "" {
		t.cmd.Env = append(os.Environ(), "ACCESS_TOKEN="+t.accessToken)
	}

	return t.startAndParseURL()
}

func (t *LocalxposeTunnel) buildArgs(localPort int) []string {
	args := []string{
		"tunnel", "http",
		"--port", fmt.Sprintf("%d", localPort),
		"--raw-mode",
	}

	if t.reservedDomain != "" {
		args = append(args, "--reserved-domain", t.reservedDomain)
	} else if t.subdomain != "" {
		args = append(args, "--subdomain", t.subdomain)
	}

	if t.region != "" {
		args = append(args, "--region", t.region)
	}

	if t.basicAuth != "" {
		args = append(args, "--basic-auth", t.basicAuth)
	}

	return args
}

func (t *LocalxposeTunnel) startAndParseURL() error {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start localxpose: %w", err)
	}

	go t.cmd.Wait()

	urlCh := make(chan string, 1)

	scanForURL := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if match := localxposeURLRegex.FindString(line); match != "" {
				select {
				case urlCh <- match:
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
		log.Printf("localxpose: tunnel URL: %s", url)
		return nil
	case <-time.After(30 * time.Second):
		killProcess(t.cmd.Process.Pid)
		return fmt.Errorf("timeout waiting for localxpose tunnel URL")
	}
}

func (t *LocalxposeTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

// checkLocalxposeAuth verifies the user is authenticated with localxpose.
func checkLocalxposeAuth(binary string) error {
	out, err := exec.Command(binary, "account", "status").CombinedOutput()
	if err != nil {
		return fmt.Errorf("localxpose auth check failed: run 'loclx account login' first: %w", err)
	}
	lower := strings.ToLower(string(out))
	if strings.Contains(lower, "not logged") || strings.Contains(lower, "unauthenticated") {
		return fmt.Errorf("localxpose not authenticated: run 'loclx account login' with your access token from https://localxpose.io/dashboard/access")
	}
	return nil
}

// localxposeURLRegex matches localxpose tunnel URLs.
// Handles: https://xxx.loclx.io and custom reserved domains.
var localxposeURLRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.(loclx\.io|[a-zA-Z0-9.-]+\.[a-z]{2,})`)
