package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"syscall"
	"time"
)

// LocalhostRunTunnel uses SSH reverse tunnels via localhost.run.
// No client installation required — uses the system SSH binary.
type LocalhostRunTunnel struct {
	cmd          *exec.Cmd
	url          string
	sshUser      string // "" or "nokey" (anonymous) or "plan" (custom domain)
	customDomain string // custom domain (e.g., "myapp.lhr.rocks")
	keepalive    int    // ServerAliveInterval in seconds
	useAutossh   bool   // prefer autossh for auto-reconnect
}

func (t *LocalhostRunTunnel) Provider() string { return "localhostrun" }
func (t *LocalhostRunTunnel) URL() string      { return t.url }

func (t *LocalhostRunTunnel) PID() int {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Pid
	}
	return 0
}

func (t *LocalhostRunTunnel) Start(localPort int) error {
	binary, args := t.buildCommand(localPort)

	binPath, err := exec.LookPath(binary)
	if err != nil {
		if binary == "autossh" {
			// Fallback to plain ssh
			log.Printf("localhostrun: autossh not found, falling back to ssh")
			binary = "ssh"
			args = t.buildSSHArgs(localPort)
			binPath, err = exec.LookPath(binary)
			if err != nil {
				return fmt.Errorf("ssh not found (this should not happen on any modern OS)")
			}
		} else {
			return fmt.Errorf("ssh not found (this should not happen on any modern OS)")
		}
	}

	t.cmd = exec.Command(binPath, args...)
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	return t.startAndParseURL()
}

func (t *LocalhostRunTunnel) buildCommand(localPort int) (string, []string) {
	if t.useAutossh {
		return "autossh", t.buildAutosshArgs(localPort)
	}
	return "ssh", t.buildSSHArgs(localPort)
}

func (t *LocalhostRunTunnel) buildSSHArgs(localPort int) []string {
	keepalive := t.keepalive
	if keepalive == 0 {
		keepalive = 60
	}

	args := []string{
		"-o", fmt.Sprintf("ServerAliveInterval=%d", keepalive),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ExitOnForwardFailure=yes",
		"-R", t.buildRemoteSpec(localPort),
	}

	if t.sshUser != "" {
		args = append(args, fmt.Sprintf("%s@localhost.run", t.sshUser))
	} else {
		args = append(args, "localhost.run")
	}

	return args
}

func (t *LocalhostRunTunnel) buildAutosshArgs(localPort int) []string {
	// autossh-specific flags first, then SSH flags
	args := []string{"-M", "0"}
	args = append(args, t.buildSSHArgs(localPort)...)
	return args
}

func (t *LocalhostRunTunnel) buildRemoteSpec(localPort int) string {
	localTarget := fmt.Sprintf("localhost:%d", localPort)
	if t.customDomain != "" {
		return fmt.Sprintf("%s:80:%s", t.customDomain, localTarget)
	}
	return fmt.Sprintf("80:%s", localTarget)
}

func (t *LocalhostRunTunnel) startAndParseURL() error {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start ssh tunnel: %w", err)
	}

	go t.cmd.Wait()

	urlCh := make(chan string, 1)

	scanForURL := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			// Only match lines containing "tunneled" to skip banner URLs
			if !localhostRunTunnelLine.MatchString(line) {
				continue
			}
			if match := localhostRunURLRegex.FindString(line); match != "" {
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
		log.Printf("localhostrun: tunnel URL: %s", url)
		return nil
	case <-time.After(30 * time.Second):
		killProcess(t.cmd.Process.Pid)
		return fmt.Errorf("timeout waiting for localhost.run tunnel URL")
	}
}

func (t *LocalhostRunTunnel) Stop() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return killProcess(t.cmd.Process.Pid)
	}
	return nil
}

// localhostRunURLRegex matches the actual tunnel URL from localhost.run output.
// The tunnel line looks like: "xxxx.lhr.life tunneled with tls termination, https://xxxx.lhr.life"
// We match on "tunneled" to avoid matching banner URLs like admin.localhost.run.
var localhostRunTunnelLine = regexp.MustCompile(`tunneled`)
var localhostRunURLRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.(lhr\.[a-z]+|localhost\.run|lhr\.rocks|[a-zA-Z0-9.-]+\.[a-z]{2,})`)
