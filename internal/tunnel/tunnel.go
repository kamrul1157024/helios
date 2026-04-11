package tunnel

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Tunnel is the interface for tunnel providers.
type Tunnel interface {
	Start(localPort int) error
	Stop() error
	URL() string
	Provider() string
	PID() int
}

// Manager manages a single active tunnel.
type Manager struct {
	mu        sync.Mutex
	active    Tunnel
	heliosDir string
}

func NewManager(heliosDir string) *Manager {
	return &Manager{heliosDir: heliosDir}
}

func (m *Manager) Status() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return map[string]interface{}{
			"active":   false,
			"provider": "",
		}
	}

	return map[string]interface{}{
		"active":     true,
		"provider":   m.active.Provider(),
		"public_url": m.active.URL(),
	}
}

// Adopt checks for an existing tunnel from a previous daemon run.
// If the tunnel process is still alive, it adopts it as the active tunnel.
func (m *Manager) Adopt() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := LoadState(m.heliosDir)
	if err != nil {
		return "", fmt.Errorf("load tunnel state: %w", err)
	}
	if state == nil {
		return "", nil
	}

	if !IsProcessAlive(state.PID) {
		log.Printf("tunnel: stale state file (PID %d dead), removing", state.PID)
		RemoveState(m.heliosDir)
		return "", nil
	}

	// Adopt the existing tunnel
	m.active = &adoptedTunnel{
		pid:      state.PID,
		url:      state.URL,
		provider: state.Provider,
	}
	log.Printf("tunnel: adopted existing %s tunnel (PID %d, URL %s)", state.Provider, state.PID, state.URL)
	return state.URL, nil
}

func (m *Manager) Start(provider string, customURL string, localPort int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing tunnel
	if m.active != nil {
		m.active.Stop()
		m.active = nil
	}

	var t Tunnel
	switch provider {
	case "cloudflare":
		t = &CloudflareTunnel{}
	case "ngrok":
		t = &NgrokTunnel{}
	case "tailscale":
		t = &TailscaleTunnel{}
	case "local":
		t = &LocalTunnel{}
	case "custom":
		t = &CustomTunnel{customURL: customURL}
	default:
		return "", fmt.Errorf("unknown tunnel provider: %s", provider)
	}

	if err := t.Start(localPort); err != nil {
		return "", err
	}

	m.active = t

	// Persist state so the tunnel can be adopted after daemon restart
	if err := SaveState(m.heliosDir, TunnelState{
		PID:       t.PID(),
		Provider:  t.Provider(),
		URL:       t.URL(),
		Port:      localPort,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("tunnel: failed to save state: %v", err)
	}

	return t.URL(), nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return nil
	}

	err := m.active.Stop()
	m.active = nil
	RemoveState(m.heliosDir)
	return err
}

// adoptedTunnel represents a tunnel process from a previous daemon run
// that we're now managing by PID only.
type adoptedTunnel struct {
	pid      int
	url      string
	provider string
}

func (t *adoptedTunnel) Start(_ int) error { return nil }
func (t *adoptedTunnel) URL() string       { return t.url }
func (t *adoptedTunnel) Provider() string  { return t.provider }
func (t *adoptedTunnel) PID() int          { return t.pid }

func (t *adoptedTunnel) Stop() error {
	return killProcess(t.pid)
}
