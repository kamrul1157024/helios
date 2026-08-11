package tunnel

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/tailscale"
)

// Tunnel is the interface for tunnel providers.
type Tunnel interface {
	Start(localPort int) error
	Stop() error
	URL() string
	Provider() string
	PID() int
}

// Reconciler is implemented by providers whose liveness lives outside a
// helios-owned process — the published mapping survives the daemon exiting, so
// a dead PID is the wrong question to ask. Adopt prefers it over the PID check.
type Reconciler interface {
	Reconcile(ctx context.Context) (url string, active bool, err error)
}

// ProviderConfig holds provider-specific settings passed from daemon config.
type ProviderConfig struct {
	Zrok         ZrokProviderConfig
	Localtunnel  LocaltunnelProviderConfig
	LocalhostRun LocalhostRunProviderConfig
	Localxpose   LocalxposeProviderConfig
	Tailscale    TailscaleProviderConfig
}

// TailscaleProviderConfig holds Tailscale-specific settings.
type TailscaleProviderConfig struct {
	Mode string // serve | funnel (default: serve)
	Port int    // 0 selects the default for the mode
}

// ZrokProviderConfig holds zrok-specific settings.
type ZrokProviderConfig struct {
	ShareMode  string
	ShareToken string
}

// LocaltunnelProviderConfig holds localtunnel-specific settings.
type LocaltunnelProviderConfig struct {
	Subdomain string
	Host      string
}

// LocalhostRunProviderConfig holds localhost.run-specific settings.
type LocalhostRunProviderConfig struct {
	SSHUser           string
	CustomDomain      string
	KeepaliveInterval int
	UseAutossh        bool
}

// LocalxposeProviderConfig holds localxpose-specific settings.
type LocalxposeProviderConfig struct {
	Subdomain      string
	ReservedDomain string
	Region         string
	BasicAuth      string
	AccessToken    string
}

// Manager manages a single active tunnel.
type Manager struct {
	mu             sync.Mutex
	active         Tunnel
	heliosDir      string
	providerConfig ProviderConfig

	// OnZrokTokenCreated is called when a new zrok reservation token is created.
	OnZrokTokenCreated func(token string)

	// OnLocaltunnelSubdomainAssigned is called when localtunnel assigns a subdomain.
	OnLocaltunnelSubdomainAssigned func(subdomain string)
}

func NewManager(heliosDir string) *Manager {
	return &Manager{heliosDir: heliosDir}
}

// SetProviderConfig updates the provider-specific configuration.
func (m *Manager) SetProviderConfig(cfg ProviderConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerConfig = cfg
}

// SetTailscaleMode switches the Tailscale exposure mode, leaving the rest of
// the provider configuration alone. The port is reset because the two modes do
// not share a valid range: Funnel only terminates on 443, 8443 and 10000, so a
// Serve port carried over would be rejected.
func (m *Manager) SetTailscaleMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerConfig.Tailscale.Mode = mode
	m.providerConfig.Tailscale.Port = 0
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

	// Providers that own no process report their own liveness. Without this
	// branch they are never adopted, because their persisted PID is 0.
	if t, err := m.newTunnel(state.Provider, state.URL, state.Port); err == nil {
		if rc, ok := t.(Reconciler); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			url, active, err := rc.Reconcile(ctx)
			if err != nil {
				log.Printf("tunnel: reconcile %s: %v", state.Provider, err)
			}
			if !active {
				log.Printf("tunnel: %s tunnel no longer published, removing state", state.Provider)
				RemoveState(m.heliosDir)
				return "", nil
			}

			m.active = t
			if url != state.URL {
				log.Printf("tunnel: %s URL changed since last run: %s -> %s", state.Provider, state.URL, url)
				if err := SaveState(m.heliosDir, TunnelState{
					PID:       t.PID(),
					Provider:  t.Provider(),
					URL:       url,
					Port:      state.Port,
					StartedAt: state.StartedAt,
				}); err != nil {
					log.Printf("tunnel: failed to refresh state: %v", err)
				}
			}
			log.Printf("tunnel: adopted existing %s tunnel (URL %s)", state.Provider, url)
			return url, nil
		}
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

// newTunnel builds a provider implementation from the manager's configuration.
// Both Start and Adopt go through it so that an adopted tunnel is the same type
// as a freshly started one, and can therefore be stopped and reconciled the
// same way. localPort is only consulted by providers that can rebuild their URL
// without having been started.
func (m *Manager) newTunnel(provider, customURL string, localPort int) (Tunnel, error) {
	switch provider {
	case "cloudflare":
		return &CloudflareTunnel{}, nil
	case "ngrok":
		return &NgrokTunnel{}, nil
	case "tailscale":
		mode, err := tailscale.ParseMode(m.providerConfig.Tailscale.Mode)
		if err != nil {
			return nil, err
		}
		port := m.providerConfig.Tailscale.Port
		if port == 0 {
			port = tailscale.DefaultPort(mode)
		}
		if err := tailscale.ValidatePort(mode, port); err != nil {
			return nil, err
		}
		return tailscale.New(mode, port), nil
	case "local":
		return &LocalTunnel{port: localPort}, nil
	case "custom":
		return &CustomTunnel{customURL: customURL}, nil
	case "zrok":
		return &ZrokTunnel{
			shareMode:      m.providerConfig.Zrok.ShareMode,
			shareToken:     m.providerConfig.Zrok.ShareToken,
			onTokenCreated: m.OnZrokTokenCreated,
		}, nil
	case "localtunnel":
		return &LocaltunnelTunnel{
			subdomain:           m.providerConfig.Localtunnel.Subdomain,
			host:                m.providerConfig.Localtunnel.Host,
			onSubdomainAssigned: m.OnLocaltunnelSubdomainAssigned,
		}, nil
	case "localhostrun":
		return &LocalhostRunTunnel{
			sshUser:      m.providerConfig.LocalhostRun.SSHUser,
			customDomain: m.providerConfig.LocalhostRun.CustomDomain,
			keepalive:    m.providerConfig.LocalhostRun.KeepaliveInterval,
			useAutossh:   m.providerConfig.LocalhostRun.UseAutossh,
		}, nil
	case "localxpose":
		return &LocalxposeTunnel{
			subdomain:      m.providerConfig.Localxpose.Subdomain,
			reservedDomain: m.providerConfig.Localxpose.ReservedDomain,
			region:         m.providerConfig.Localxpose.Region,
			basicAuth:      m.providerConfig.Localxpose.BasicAuth,
			accessToken:    m.providerConfig.Localxpose.AccessToken,
		}, nil
	default:
		return nil, fmt.Errorf("unknown tunnel provider: %s", provider)
	}
}

func (m *Manager) Start(provider string, customURL string, localPort int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing tunnel
	if m.active != nil {
		m.active.Stop()
		m.active = nil
	}

	t, err := m.newTunnel(provider, customURL, localPort)
	if err != nil {
		return "", err
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
