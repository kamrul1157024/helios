package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunDir holds one socket and one JSON sidecar per live host.
func RunDir(heliosDir string) string { return filepath.Join(heliosDir, "run") }

// socketName is a short digest rather than the raw session ID because macOS
// caps sun_path at 104 bytes, which a UUID under a long home directory can
// exceed.
func socketName(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:8])
}

// SocketPath returns the unix socket path for a session.
func SocketPath(heliosDir, sessionID string) string {
	return filepath.Join(RunDir(heliosDir), socketName(sessionID)+".sock")
}

// SidecarPath returns the metadata path for a session.
func SidecarPath(heliosDir, sessionID string) string {
	return filepath.Join(RunDir(heliosDir), socketName(sessionID)+".json")
}

// Sidecar is the durable session→terminal mapping. It replaces the
// @helios_session_id tmux pane option, so the mapping survives without a
// multiplexer server holding it.
type Sidecar struct {
	SessionID string    `json:"session_id"`
	PID       int       `json:"pid"`
	ChildPID  int       `json:"child_pid"`
	Cwd       string    `json:"cwd"`
	Socket    string    `json:"socket"`
	StartedAt time.Time `json:"started_at"`
}

// WriteSidecar persists host metadata next to its socket.
func WriteSidecar(heliosDir string, s Sidecar) error {
	if err := os.MkdirAll(RunDir(heliosDir), 0o700); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sidecar: %w", err)
	}
	path := SidecarPath(heliosDir, s.SessionID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit sidecar: %w", err)
	}
	return nil
}

// ReadSidecar loads host metadata by path.
func ReadSidecar(path string) (Sidecar, error) {
	var s Sidecar
	b, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("read sidecar: %w", err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("parse sidecar %s: %w", path, err)
	}
	return s, nil
}

// RemoveHostFiles unlinks a host's socket and sidecar.
func RemoveHostFiles(heliosDir, sessionID string) {
	os.Remove(SocketPath(heliosDir, sessionID))
	os.Remove(SidecarPath(heliosDir, sessionID))
}

// ListSidecars returns every sidecar in the run dir. A missing run dir is not
// an error: it just means no hosts have started.
func ListSidecars(heliosDir string) ([]Sidecar, error) {
	entries, err := os.ReadDir(RunDir(heliosDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read run dir: %w", err)
	}
	var out []Sidecar
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		s, err := ReadSidecar(filepath.Join(RunDir(heliosDir), e.Name()))
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
