package tmux

// TmuxClient is the subset of *Client methods used by hook handlers,
// the session reaper, and action handlers. Implemented by *Client.
type TmuxClient interface {
	// Hook handlers (hooks.go)
	RenameWindow(paneID, name string) error
	KillPane(paneID string) error

	// Reaper (reaper.go)
	Available() bool
	CreateWindow(cwd, command string) (string, error)
	SetPaneSessionID(paneID, sessionID string) error
	SweepDeadPanes(m *PaneMap) []string

	// Action handlers (actions.go)
	SendKeysRaw(paneID, keys string) error
}
