package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const stateFileName = "tunnel.state"

// TunnelState persists tunnel process info across daemon restarts.
type TunnelState struct {
	PID       int       `json:"pid"`
	Provider  string    `json:"provider"`
	URL       string    `json:"url"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
}

func statePath(heliosDir string) string {
	return filepath.Join(heliosDir, stateFileName)
}

// SaveState writes tunnel state to disk.
func SaveState(heliosDir string, state TunnelState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tunnel state: %w", err)
	}
	return os.WriteFile(statePath(heliosDir), data, 0644)
}

// LoadState reads tunnel state from disk. Returns nil if no state file exists.
func LoadState(heliosDir string) (*TunnelState, error) {
	data, err := os.ReadFile(statePath(heliosDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tunnel state: %w", err)
	}

	var state TunnelState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse tunnel state: %w", err)
	}
	return &state, nil
}

// RemoveState deletes the tunnel state file.
func RemoveState(heliosDir string) error {
	err := os.Remove(statePath(heliosDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove tunnel state: %w", err)
	}
	return nil
}

// IsProcessAlive checks if a process with the given PID is still running.
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// KillTunnel reads the state file, kills the tunnel process, and removes the state.
func KillTunnel(heliosDir string) error {
	state, err := LoadState(heliosDir)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}

	if IsProcessAlive(state.PID) {
		if err := killProcess(state.PID); err != nil {
			return fmt.Errorf("kill tunnel (PID %d): %w", state.PID, err)
		}
	}
	return RemoveState(heliosDir)
}
