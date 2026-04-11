package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const defaultSession = "helios"

// Status describes the availability of tmux and recommended plugins.
type Status struct {
	Installed        bool   `json:"installed"`
	Version          string `json:"version,omitempty"`
	ServerRunning    bool   `json:"server_running"`
	ResurrectPlugin  bool   `json:"resurrect_plugin"`
	ContinuumPlugin  bool   `json:"continuum_plugin"`
	SessionMgmtReady bool   `json:"session_mgmt_ready"`
}

// Client wraps tmux shell commands.
type Client struct {
	bin string // resolved path to the tmux binary
}

// NewClient creates a new tmux client.
func NewClient() *Client {
	return &Client{bin: findTmux()}
}

// tmuxCmd returns the resolved tmux binary path, falling back to "tmux"
// (i.e. plain PATH lookup) if findTmux found nothing at construction time.
func (c *Client) tmuxCmd() string {
	if c.bin != "" {
		return c.bin
	}
	return "tmux"
}

// Available checks if tmux is installed and a server is running.
func (c *Client) Available() bool {
	return exec.Command(c.tmuxCmd(), "list-sessions").Run() == nil
}

// EnsureSession creates the helios tmux session if it doesn't exist.
func (c *Client) EnsureSession() error {
	if c.hasSession(defaultSession) {
		return nil
	}
	return exec.Command(c.tmuxCmd(), "new-session", "-d", "-s", defaultSession).Run()
}

// hasSession checks if a named tmux session exists.
func (c *Client) hasSession(name string) bool {
	return exec.Command(c.tmuxCmd(), "has-session", "-t", name).Run() == nil
}

// HasPane checks if a specific pane ID exists.
func (c *Client) HasPane(paneID string) bool {
	out, err := exec.Command(c.tmuxCmd(), "list-panes", "-a", "-F", "#{pane_id}").Output()
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

// CreateWindow creates a new tmux window in the helios session, then sends
// the command via send-keys. This ensures the user's login shell runs first
// (loading PATH, nvm, homebrew, etc.) before the command executes.
// Returns the new pane ID.
func (c *Client) CreateWindow(cwd, command string) (string, error) {
	if err := c.EnsureSession(); err != nil {
		return "", fmt.Errorf("ensure session: %w", err)
	}

	// Create a bare window (starts the user's default shell).
	out, err := exec.Command(c.tmuxCmd(), "new-window", "-t", defaultSession+":",
		"-c", cwd, "-P", "-F", "#{pane_id}").Output()
	if err != nil {
		return "", fmt.Errorf("create window: %w", err)
	}

	paneID := strings.TrimSpace(string(out))

	// Type the command into the shell.
	if err := c.SendKeys(paneID, command); err != nil {
		return "", fmt.Errorf("send command: %w", err)
	}

	return paneID, nil
}

// SendKeys sends text to a pane followed by Enter.
func (c *Client) SendKeys(paneID, text string) error {
	return exec.Command(c.tmuxCmd(), "send-keys", "-t", paneID, text, "Enter").Run()
}

// SendKeysRaw sends keys to a pane without appending Enter.
func (c *Client) SendKeysRaw(paneID, keys string) error {
	return exec.Command(c.tmuxCmd(), "send-keys", "-t", paneID, keys).Run()
}

// SendEscape sends the Escape key to a pane (stops Claude's current turn).
func (c *Client) SendEscape(paneID string) error {
	return exec.Command(c.tmuxCmd(), "send-keys", "-t", paneID, "Escape").Run()
}

// Suspend sends Ctrl+C to a pane (kills the Claude process).
func (c *Client) Suspend(paneID string) error {
	return exec.Command(c.tmuxCmd(), "send-keys", "-t", paneID, "C-c").Run()
}

// CapturePane captures the visible content of a pane.
func (c *Client) CapturePane(paneID string) (string, error) {
	out, err := exec.Command(c.tmuxCmd(), "capture-pane", "-t", paneID, "-p").Output()
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
	out, err := exec.Command(c.tmuxCmd(), "list-panes", "-a", "-F", "#{pane_id} #{pane_pid}").Output()
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

// userShell returns the current user's login shell, defaulting to /bin/sh.
func userShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// findTmux locates the tmux binary. It first tries exec.LookPath (which
// searches the current process PATH). If that fails — common when the daemon
// is launched from a non-interactive context that doesn't source the user's
// shell profile — it spawns a login shell to resolve the full user PATH and
// runs "which tmux" inside it.
func findTmux() string {
	// Fast path: already on PATH.
	if p, err := exec.LookPath("tmux"); err == nil && p != "" {
		return p
	}

	// Slow path: ask the user's login shell (loads ~/.zshrc, ~/.bashrc, etc.).
	shell := userShell()
	out, err := exec.Command(shell, "-l", "-c", "which tmux").Output()
	if err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if info, statErr := os.Stat(p); statErr == nil && !info.IsDir() {
				return p
			}
		}
	}

	return ""
}

// CheckStatus returns the tmux installation and plugin status.
func (c *Client) CheckStatus() Status {
	s := Status{}

	// Check if tmux is installed
	if c.bin == "" {
		return s
	}
	s.Installed = true

	// Get version
	out, err := exec.Command(c.bin, "-V").Output()
	if err == nil {
		s.Version = strings.TrimSpace(string(out))
	}

	// Check if tmux server is running
	s.ServerRunning = exec.Command(c.bin, "list-sessions").Run() == nil

	// Check for resurrect plugin
	s.ResurrectPlugin = pluginExists("tmux-resurrect")

	// Check for continuum plugin
	s.ContinuumPlugin = pluginExists("tmux-continuum")

	// Session management requires tmux installed and server running
	s.SessionMgmtReady = s.Installed && s.ServerRunning

	return s
}

// pluginExists checks if a tmux plugin is installed in the standard TPM location.
func pluginExists(name string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	pluginDir := home + "/.tmux/plugins/" + name
	info, err := os.Stat(pluginDir)
	return err == nil && info.IsDir()
}
