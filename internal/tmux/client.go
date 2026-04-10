package tmux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const defaultSession = "helios"

// Client wraps tmux shell commands.
type Client struct{}

// NewClient creates a new tmux client.
func NewClient() *Client {
	return &Client{}
}

// Available checks if tmux is installed and a server is running.
func (c *Client) Available() bool {
	return exec.Command("tmux", "list-sessions").Run() == nil
}

// EnsureSession creates the helios tmux session if it doesn't exist.
func (c *Client) EnsureSession() error {
	if c.hasSession(defaultSession) {
		return nil
	}
	return exec.Command("tmux", "new-session", "-d", "-s", defaultSession).Run()
}

// hasSession checks if a named tmux session exists.
func (c *Client) hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

// HasPane checks if a specific pane ID exists.
func (c *Client) HasPane(paneID string) bool {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == paneID {
			return true
		}
	}
	return false
}

// CreateWindow creates a new tmux window in the helios session, running the
// given command in the specified directory. Returns the new pane ID.
func (c *Client) CreateWindow(cwd, command string) (string, error) {
	if err := c.EnsureSession(); err != nil {
		return "", fmt.Errorf("ensure session: %w", err)
	}

	out, err := exec.Command("tmux", "new-window", "-t", defaultSession+":",
		"-c", cwd, "-P", "-F", "#{pane_id}", command).Output()
	if err != nil {
		return "", fmt.Errorf("create window: %w", err)
	}

	paneID := strings.TrimSpace(string(out))
	return paneID, nil
}

// SendKeys sends text to a pane followed by Enter.
func (c *Client) SendKeys(paneID, text string) error {
	return exec.Command("tmux", "send-keys", "-t", paneID, text, "Enter").Run()
}

// SendEscape sends the Escape key to a pane (stops Claude's current turn).
func (c *Client) SendEscape(paneID string) error {
	return exec.Command("tmux", "send-keys", "-t", paneID, "Escape").Run()
}

// Suspend sends Ctrl+C to a pane (kills the Claude process).
func (c *Client) Suspend(paneID string) error {
	return exec.Command("tmux", "send-keys", "-t", paneID, "C-c").Run()
}

// CapturePane captures the visible content of a pane.
func (c *Client) CapturePane(paneID string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-t", paneID, "-p").Output()
	if err != nil {
		return "", fmt.Errorf("capture pane: %w", err)
	}
	return string(out), nil
}

// PaneProcess describes a tmux pane running a Claude process.
type PaneProcess struct {
	PaneID    string
	ClaudePID int
	CWD       string
}

// ListClaudePanes walks the process tree to find tmux panes running Claude.
func (c *Client) ListClaudePanes() ([]PaneProcess, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_pid}").Output()
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}

	var result []PaneProcess
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		paneID := parts[0]
		panePID := parts[1]

		// Find child processes of the pane
		children, err := exec.Command("pgrep", "-P", panePID).Output()
		if err != nil {
			continue
		}

		for _, childLine := range strings.Split(strings.TrimSpace(string(children)), "\n") {
			childPID := strings.TrimSpace(childLine)
			if childPID == "" {
				continue
			}

			// Check if this child is a claude process
			comm, err := exec.Command("ps", "-o", "comm=", "-p", childPID).Output()
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(comm)) != "claude" {
				continue
			}

			pid, _ := strconv.Atoi(childPID)

			// Get CWD via lsof
			cwd := ""
			lsofOut, err := exec.Command("lsof", "-p", childPID, "-Fn").Output()
			if err == nil {
				for _, l := range strings.Split(string(lsofOut), "\n") {
					if strings.HasPrefix(l, "n") && strings.Contains(l, "/") {
						// lsof cwd line looks like: n/path/to/dir
						cwd = l[1:]
						break
					}
				}
			}

			result = append(result, PaneProcess{
				PaneID:    paneID,
				ClaudePID: pid,
				CWD:       cwd,
			})
		}
	}

	return result, nil
}
