package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Status icons for tmux window names.
const (
	iconHelios   = "🔥"
	iconStarting = "◌"
	iconActive   = "●"
	iconWaiting  = "◆"
	iconIdle     = "○"
	iconCompact  = "↻"
	iconError    = "✕"
)

// WindowName builds a tmux window name like "🔥● myapp: fix auth bug".
func WindowName(status, cwd, title string) string {
	icon := statusIcon(status)
	project := filepath.Base(cwd)
	if title != "" {
		return fmt.Sprintf("%s%s %s: %s", iconHelios, icon, project, title)
	}
	return fmt.Sprintf("%s%s %s", iconHelios, icon, project)
}

func statusIcon(status string) string {
	switch status {
	case "starting":
		return iconStarting
	case "active", "compacting":
		if status == "compacting" {
			return iconCompact
		}
		return iconActive
	case "waiting_permission":
		return iconWaiting
	case "idle":
		return iconIdle
	case "error":
		return iconError
	default:
		return iconIdle
	}
}

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

// LivePanes returns the set of all currently existing pane IDs.
func (c *Client) LivePanes() map[string]struct{} {
	out, err := exec.Command(c.tmuxCmd(), "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return nil
	}
	set := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			set[id] = struct{}{}
		}
	}
	return set
}

// CreateWindow creates a new tmux window in the helios session, then sends
// the command via send-keys. This ensures the user's login shell runs first
// (loading PATH, nvm, homebrew, etc.) before the command executes.
// Returns the new pane ID.
func (c *Client) CreateWindow(cwd, command string) (string, error) {
	if err := c.EnsureSession(); err != nil {
		return "", fmt.Errorf("ensure session: %w", err)
	}

	name := WindowName("starting", cwd, "")

	// Create a bare window (starts the user's default shell).
	out, err := exec.Command(c.tmuxCmd(), "new-window", "-t", defaultSession+":",
		"-n", name, "-c", cwd, "-P", "-F", "#{pane_id}").Output()
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

// RenameWindow renames the window containing the given pane.
func (c *Client) RenameWindow(paneID, name string) error {
	return exec.Command(c.tmuxCmd(), "rename-window", "-t", paneID, name).Run()
}

// KillWindow kills the window containing the given pane.
func (c *Client) KillWindow(paneID string) error {
	return exec.Command(c.tmuxCmd(), "kill-window", "-t", paneID).Run()
}

// JoinPaneHorizontal moves srcPaneID into the window of targetPaneID
// as a horizontal split (side-by-side). Uses -l to set the source pane
// width as a percentage of the window.
func (c *Client) JoinPaneHorizontal(srcPaneID, targetPaneID string, widthPercent int) error {
	pct := fmt.Sprintf("%d%%", widthPercent)
	return exec.Command(c.tmuxCmd(), "join-pane", "-h", "-l", pct, "-s", srcPaneID, "-t", targetPaneID).Run()
}

// BreakPane sends a pane back to its own window in the background.
// The pane keeps running; focus stays on the current pane.
func (c *Client) BreakPane(paneID string) error {
	return exec.Command(c.tmuxCmd(), "break-pane", "-d", "-s", paneID).Run()
}

// SwapPane swaps two panes in the same window.
func (c *Client) SwapPane(srcPaneID, dstPaneID string) error {
	return exec.Command(c.tmuxCmd(), "swap-pane", "-s", srcPaneID, "-t", dstPaneID).Run()
}

// ResizePane resizes a pane to the given width in columns.
func (c *Client) ResizePane(paneID string, width int) error {
	return exec.Command(c.tmuxCmd(), "resize-pane", "-t", paneID, "-x", fmt.Sprintf("%d", width)).Run()
}

// SelectPane makes the given pane the active pane in its window.
func (c *Client) SelectPane(paneID string) error {
	return exec.Command(c.tmuxCmd(), "select-pane", "-t", paneID).Run()
}

// PaneInWindow checks if a pane belongs to the same window as targetPaneID.
func (c *Client) PaneInWindow(paneID, targetPaneID string) bool {
	// Get window ID for both panes
	getWindow := func(pid string) string {
		out, err := exec.Command(c.tmuxCmd(), "display-message", "-t", pid, "-p", "#{window_id}").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	w1 := getWindow(paneID)
	w2 := getWindow(targetPaneID)
	return w1 != "" && w1 == w2
}

// WindowWidth returns the total width (columns) of the window containing the given pane.
func (c *Client) WindowWidth(paneID string) int {
	out, err := exec.Command(c.tmuxCmd(), "display-message", "-t", paneID, "-p", "#{window_width}").Output()
	if err != nil {
		return 0
	}
	w, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return w
}

// Attach attaches the current terminal to the helios tmux session,
// selecting the given pane. This replaces the current process.
func (c *Client) Attach(paneID string) error {
	// Select the target pane first so the user lands on the right window.
	exec.Command(c.tmuxCmd(), "select-window", "-t", paneID).Run()
	exec.Command(c.tmuxCmd(), "select-pane", "-t", paneID).Run()

	bin := c.tmuxCmd()
	return syscall.Exec(bin, []string{bin, "attach-session", "-t", defaultSession}, os.Environ())
}

// AttachSession attaches the current terminal to the helios tmux session
// without selecting a specific pane. This replaces the current process.
// Returns an error if the session does not exist.
func (c *Client) AttachSession() error {
	if !c.hasSession(defaultSession) {
		return fmt.Errorf("no helios tmux session found")
	}
	bin := c.tmuxCmd()
	return syscall.Exec(bin, []string{bin, "attach-session", "-t", defaultSession}, os.Environ())
}

// OpenWindow opens a new window in the helios session running the given
// command directly (not via a shell). The session is created if needed.
// Returns the pane ID of the new window.
func (c *Client) OpenWindow(name string, args ...string) (string, error) {
	if err := c.EnsureSession(); err != nil {
		return "", fmt.Errorf("ensure session: %w", err)
	}
	cmdArgs := []string{"new-window", "-t", defaultSession + ":", "-n", name, "-P", "-F", "#{pane_id}"}
	cmdArgs = append(cmdArgs, args...)
	out, err := exec.Command(c.tmuxCmd(), cmdArgs...).Output()
	if err != nil {
		return "", fmt.Errorf("open window: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
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
