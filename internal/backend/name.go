package backend

import (
	"fmt"
	"path/filepath"
)

// Status icons for terminal display names.
const (
	iconHelios   = "🔥"
	iconStarting = "◌"
	iconActive   = "●"
	iconWaiting  = "◆"
	iconIdle     = "○"
	iconCompact  = "↻"
	iconError    = "✕"
)

// DisplayName builds a terminal label like "🔥● myapp: fix auth bug". It is
// what a backend shows for a session — a tmux window name, a TUI tab title.
func DisplayName(status, cwd, title string) string {
	name := fmt.Sprintf("%s%s %s", iconHelios, statusIcon(status), filepath.Base(cwd))
	if title != "" {
		return name + ": " + title
	}
	return name
}

func statusIcon(status string) string {
	switch status {
	case "starting":
		return iconStarting
	case "active":
		return iconActive
	case "compacting":
		return iconCompact
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
