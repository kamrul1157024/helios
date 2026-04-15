package tmux

import (
	"os/exec"
	"strings"
	"sync"
)

const heliosSessionKey = "@helios_session_id"

// PaneMap is a thread-safe in-memory map of sessionID → paneID.
// Populated at daemon startup via full scan and kept up-to-date via
// SessionStart/SessionEnd hooks and the reaper sweep.
type PaneMap struct {
	mu   sync.RWMutex
	data map[string]string // sessionID → paneID
}

// NewPaneMap creates an empty PaneMap.
func NewPaneMap() *PaneMap {
	return &PaneMap{data: make(map[string]string)}
}

// Set registers a sessionID → paneID mapping.
func (m *PaneMap) Set(sessionID, paneID string) {
	m.mu.Lock()
	m.data[sessionID] = paneID
	m.mu.Unlock()
}

// Get returns the pane ID for a session, and whether it was found.
func (m *PaneMap) Get(sessionID string) (string, bool) {
	m.mu.RLock()
	paneID, ok := m.data[sessionID]
	m.mu.RUnlock()
	return paneID, ok
}

// Delete removes a sessionID mapping.
func (m *PaneMap) Delete(sessionID string) {
	m.mu.Lock()
	delete(m.data, sessionID)
	m.mu.Unlock()
}

// Snapshot returns a copy of the current map.
func (m *PaneMap) Snapshot() map[string]string {
	m.mu.RLock()
	out := make(map[string]string, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	m.mu.RUnlock()
	return out
}

// SetPaneSessionID stores a session ID on a tmux pane as a user option.
// Called by helios wrap immediately after the pane is known.
func (c *Client) SetPaneSessionID(paneID, sessionID string) error {
	return exec.Command(c.tmuxCmd(), "set-option", "-pt", paneID, heliosSessionKey, sessionID).Run()
}

// GetPaneSessionID reads the session ID stored on a tmux pane.
func (c *Client) GetPaneSessionID(paneID string) (string, error) {
	out, err := exec.Command(c.tmuxCmd(), "show-options", "-vpt", paneID, heliosSessionKey).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RebuildPaneMap scans all live panes for @helios_session_id and rebuilds m.
// Expensive — only call at startup.
func (c *Client) RebuildPaneMap(m *PaneMap) {
	out, err := exec.Command(c.tmuxCmd(), "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return
	}

	fresh := make(map[string]string)
	for _, paneID := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		paneID = strings.TrimSpace(paneID)
		if paneID == "" {
			continue
		}
		sessionID, err := c.GetPaneSessionID(paneID)
		if err != nil || sessionID == "" {
			continue
		}
		fresh[sessionID] = paneID
	}

	m.mu.Lock()
	m.data = fresh
	m.mu.Unlock()
}

// SweepDeadPanes removes entries from m where the pane no longer exists OR
// the claude process inside it has exited. Returns the session IDs removed.
func (c *Client) SweepDeadPanes(m *PaneMap) []string {
	livePanes := c.LivePanes()

	m.mu.Lock()
	defer m.mu.Unlock()

	var dead []string
	for sessionID, paneID := range m.data {
		_, paneAlive := livePanes[paneID]
		if !paneAlive || !c.claudeRunningInPane(paneID) {
			dead = append(dead, sessionID)
			delete(m.data, sessionID)
		}
	}
	return dead
}

// claudeRunningInPane returns true if a claude process exists in the pane's
// process tree. Uses ps to build a full pid→children map, then walks from
// the pane shell PID down, looking for a process named "claude".
func (c *Client) claudeRunningInPane(paneID string) bool {
	out, err := exec.Command(c.tmuxCmd(), "display-message", "-t", paneID, "-p", "#{pane_pid}").Output()
	if err != nil {
		return false
	}
	panePID := strings.TrimSpace(string(out))
	if panePID == "" {
		return false
	}

	// Build full process table: pid → (comm, ppid)
	psOut, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return false
	}

	type procInfo struct {
		comm string
		ppid string
	}
	procs := make(map[string]procInfo)
	children := make(map[string][]string) // ppid → []pid

	for _, line := range strings.Split(string(psOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, ppid, comm := fields[0], fields[1], fields[2]
		procs[pid] = procInfo{comm: comm, ppid: ppid}
		children[ppid] = append(children[ppid], pid)
	}

	// BFS from pane PID down the process tree
	queue := []string{panePID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if info, ok := procs[cur]; ok && strings.TrimSuffix(info.comm, ":") == "claude" {
			return true
		}
		queue = append(queue, children[cur]...)
	}
	return false
}
