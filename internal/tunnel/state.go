package tunnel

import (
	"context"
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

// KillTunnel reads the state file, tears the tunnel down, and removes the
// state. Providers whose tunnel is not a helios-owned process are stopped
// through their own teardown, because there is no PID to signal.
func KillTunnel(heliosDir string, cfg ProviderConfig) error {
	state, err := LoadState(heliosDir)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}

	if t, err := StateTunnel(*state, cfg); err == nil {
		if rc, ok := t.(Reconciler); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Reconcile first: this tunnel was rebuilt from a file and was
			// never started in this process, so it does not yet know how to
			// address the mapping it is about to remove.
			if _, active, err := rc.Reconcile(ctx); err == nil && active {
				if err := t.Stop(); err != nil {
					return fmt.Errorf("stop %s tunnel: %w", state.Provider, err)
				}
			}
			return RemoveState(heliosDir)
		}
	}

	if IsProcessAlive(state.PID) {
		if err := killProcess(state.PID); err != nil {
			return fmt.Errorf("kill tunnel (PID %d): %w", state.PID, err)
		}
	}
	return RemoveState(heliosDir)
}

// StateTunnel rebuilds the provider implementation described by a persisted
// state file. It lets callers outside the daemon — the CLI, which has no
// Manager — inspect and stop a tunnel exactly the way the daemon would.
func StateTunnel(state TunnelState, cfg ProviderConfig) (Tunnel, error) {
	m := &Manager{providerConfig: cfg}
	return m.newTunnel(state.Provider, state.URL, state.Port)
}

// StateLiveness reports whether the tunnel described by state is still up, and
// its current URL. Process-backed providers are answered by PID; the rest are
// asked directly, since their tunnel outlives every helios process and a dead
// PID says nothing about it.
func StateLiveness(ctx context.Context, state TunnelState, cfg ProviderConfig) (url string, active bool) {
	if t, err := StateTunnel(state, cfg); err == nil {
		if rc, ok := t.(Reconciler); ok {
			url, active, err := rc.Reconcile(ctx)
			if err != nil || !active {
				return "", false
			}
			return url, true
		}
	}
	return state.URL, IsProcessAlive(state.PID)
}
