package tunnel

import (
	"context"
	"fmt"
	"sync"
)

// Tunnel is the interface for tunnel providers.
type Tunnel interface {
	Start(ctx context.Context, localPort int) error
	Stop() error
	URL() string
	Provider() string
}

// Manager manages a single active tunnel.
type Manager struct {
	mu     sync.Mutex
	active Tunnel
	ctx    context.Context
	cancel context.CancelFunc
}

func NewManager() *Manager {
	return &Manager{}
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

func (m *Manager) Start(provider string, customURL string, localPort int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing tunnel
	if m.active != nil {
		m.active.Stop()
		if m.cancel != nil {
			m.cancel()
		}
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

	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	if err := t.Start(ctx, localPort); err != nil {
		cancel()
		return "", err
	}

	m.active = t
	return t.URL(), nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return nil
	}

	err := m.active.Stop()
	if m.cancel != nil {
		m.cancel()
	}
	m.active = nil
	return err
}
