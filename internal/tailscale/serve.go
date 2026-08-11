package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Mode selects how the local server is exposed.
type Mode string

const (
	// ModeServe publishes on the tailnet only, over plain HTTP inside the
	// WireGuard tunnel. No certificate required.
	ModeServe Mode = "serve"
	// ModeFunnel publishes on the public internet over TLS. Requires HTTPS
	// certificates to be enabled for the tailnet.
	ModeFunnel Mode = "funnel"
)

// DefaultServePort mirrors the helios public port so the tailnet URL carries a
// familiar number. Serve accepts any port.
const DefaultServePort = 7655

// DefaultFunnelPort is the only funnel port that yields a portless URL.
const DefaultFunnelPort = 443

// funnelPorts are the only ports Tailscale Funnel will terminate on.
var funnelPorts = []int{443, 8443, 10000}

// ParseMode validates a configured mode string, defaulting to Serve.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ModeServe):
		return ModeServe, nil
	case string(ModeFunnel):
		return ModeFunnel, nil
	default:
		return "", fmt.Errorf("unknown tailscale mode %q (want %q or %q)", s, ModeServe, ModeFunnel)
	}
}

// ValidatePort checks the port against the rules for the mode. Funnel is
// restricted to 443, 8443 and 10000; Serve accepts any port. Rejecting here is
// better than discovering it when the CLI fails.
func ValidatePort(mode Mode, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("tailscale port %d out of range", port)
	}
	if mode != ModeFunnel {
		return nil
	}
	for _, p := range funnelPorts {
		if p == port {
			return nil
		}
	}
	return fmt.Errorf("tailscale funnel port must be one of %v, got %d", funnelPorts, port)
}

// DefaultPort returns the default port for a mode.
func DefaultPort(mode Mode) int {
	if mode == ModeFunnel {
		return DefaultFunnelPort
	}
	return DefaultServePort
}

// serveConfig is the subset of tailscaled's serve configuration we read.
type serveConfig struct {
	TCP         map[string]*tcpHandler `json:"TCP,omitempty"`
	Web         map[string]*webConfig  `json:"Web,omitempty"`
	AllowFunnel map[string]bool        `json:"AllowFunnel,omitempty"`
}

type tcpHandler struct {
	HTTP  bool `json:"HTTP,omitempty"`
	HTTPS bool `json:"HTTPS,omitempty"`
}

type webConfig struct {
	Handlers map[string]*webHandler `json:"Handlers,omitempty"`
}

type webHandler struct {
	Proxy string `json:"Proxy,omitempty"`
}

func (c serveConfig) empty() bool {
	return len(c.TCP) == 0 && len(c.Web) == 0
}

// proxyTarget returns the proxy destination configured for hostPort's root
// path, or "" when nothing is published there.
func (c serveConfig) proxyTarget(hostPort string) string {
	web, ok := c.Web[hostPort]
	if !ok || web == nil {
		return ""
	}
	handler, ok := web.Handlers["/"]
	if !ok || handler == nil {
		return ""
	}
	return handler.Proxy
}

func loadServeConfig(ctx context.Context, binary string) (serveConfig, error) {
	var cfg serveConfig
	out, err := run(ctx, binary, "serve", "status", "--json")
	if err != nil {
		return cfg, err
	}
	// An unconfigured node prints "{}", which unmarshals to the zero value.
	if err := json.Unmarshal(out, &cfg); err != nil {
		return cfg, fmt.Errorf("parse serve config: %w", err)
	}
	return cfg, nil
}

// Tunnel publishes a local port on the tailnet. It owns no process: the
// mapping lives in tailscaled and outlives the helios daemon, which is why it
// implements Reconcile rather than reporting a PID.
type Tunnel struct {
	mode Mode
	port int

	binary   string
	dnsName  string
	url      string
	target   string
	hostPort string
}

// New returns a Tunnel for the given mode and port. A port of 0 selects the
// default for the mode.
func New(mode Mode, port int) *Tunnel {
	if port == 0 {
		port = DefaultPort(mode)
	}
	return &Tunnel{mode: mode, port: port}
}

// Mode reports the configured exposure mode.
func (t *Tunnel) Mode() Mode { return t.mode }

// Port reports the tailnet-facing port.
func (t *Tunnel) Port() int { return t.port }

func (t *Tunnel) Provider() string { return "tailscale" }

func (t *Tunnel) URL() string { return t.url }

// PID reports 0: the published mapping is owned by tailscaled, not by any
// process helios spawned. Liveness is answered by Reconcile instead.
func (t *Tunnel) PID() int { return 0 }

// buildURL computes the public URL. Both modes can derive it from the node's
// DNS name before anything is published, so no output scraping is needed.
func (t *Tunnel) buildURL() string {
	if t.mode == ModeFunnel {
		if t.port == 443 {
			return fmt.Sprintf("https://%s", t.dnsName)
		}
		return fmt.Sprintf("https://%s:%d", t.dnsName, t.port)
	}
	if t.port == 80 {
		return fmt.Sprintf("http://%s", t.dnsName)
	}
	return fmt.Sprintf("http://%s:%d", t.dnsName, t.port)
}

// Start publishes localPort on the tailnet and verifies that tailscaled
// accepted the mapping before returning.
func (t *Tunnel) Start(localPort int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return t.start(ctx, localPort)
}

func (t *Tunnel) start(ctx context.Context, localPort int) error {
	if err := ValidatePort(t.mode, t.port); err != nil {
		return err
	}

	state, err := Detect(ctx)
	if err != nil {
		return err
	}
	if t.mode == ModeFunnel {
		if problem := state.FunnelProblem(); problem != "" {
			return fmt.Errorf("%s", problem)
		}
	} else if problem := state.Problem(); problem != "" {
		return fmt.Errorf("%s", problem)
	}

	t.binary = state.BinaryPath
	t.dnsName = state.DNSName
	t.target = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	t.hostPort = fmt.Sprintf("%s:%d", t.dnsName, t.port)
	t.url = t.buildURL()

	// Refuse to clobber a mapping the user set up themselves.
	cfg, err := loadServeConfig(ctx, t.binary)
	if err != nil {
		return fmt.Errorf("read serve config: %w", err)
	}
	if existing := cfg.proxyTarget(t.hostPort); existing != "" && existing != t.target {
		return fmt.Errorf("tailscale %s port %d is already serving %s — "+
			"choose a different port or remove that mapping with `tailscale serve --%s=%d off`",
			t.mode, t.port, existing, t.schemeFlag(), t.port)
	}

	if _, err := run(ctx, t.binary, t.publishArgs()...); err != nil {
		return err
	}

	if err := t.waitPublished(ctx); err != nil {
		// Leave nothing half-published behind.
		if _, offErr := run(ctx, t.binary, t.offArgs()...); offErr != nil {
			return fmt.Errorf("%w (cleanup also failed: %v)", err, offErr)
		}
		return err
	}

	return nil
}

// schemeFlag is the CLI flag naming the listener scheme for this mode.
func (t *Tunnel) schemeFlag() string {
	if t.mode == ModeFunnel {
		return "https"
	}
	return "http"
}

// publishArgs builds the CLI invocation. --yes is required because the daemon
// is non-interactive and funnel prompts before exposing a port publicly.
func (t *Tunnel) publishArgs() []string {
	return []string{
		string(t.mode),
		"--bg",
		"--yes",
		fmt.Sprintf("--%s=%d", t.schemeFlag(), t.port),
		t.target,
	}
}

func (t *Tunnel) offArgs() []string {
	return []string{
		string(t.mode),
		fmt.Sprintf("--%s=%d", t.schemeFlag(), t.port),
		"off",
	}
}

// waitPublished polls the serve config until our mapping appears. tailscaled
// applies the change asynchronously, so this replaces a blind sleep.
func (t *Tunnel) waitPublished(ctx context.Context) error {
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for {
		cfg, err := loadServeConfig(ctx, t.binary)
		if err != nil {
			lastErr = err
		} else if cfg.proxyTarget(t.hostPort) == t.target {
			if t.mode == ModeFunnel && !cfg.AllowFunnel[t.hostPort] {
				lastErr = fmt.Errorf("mapping published but funnel is not enabled for %s", t.hostPort)
			} else {
				return nil
			}
		} else {
			lastErr = fmt.Errorf("tailscale did not publish %s", t.hostPort)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("tailscale %s did not become active: %w", t.mode, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Stop removes only the mapping we created, leaving any other serve config the
// user has in place.
func (t *Tunnel) Stop() error {
	if t.binary == "" || t.url == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout*2)
	defer cancel()

	if _, err := run(ctx, t.binary, t.offArgs()...); err != nil {
		return fmt.Errorf("stop tailscale %s: %w", t.mode, err)
	}
	return nil
}

// Reconcile asks tailscaled whether our mapping is still published. It is how
// the tunnel is adopted across daemon restarts: the mapping survives helios
// exiting, so a dead PID would be the wrong question.
func (t *Tunnel) Reconcile(ctx context.Context) (string, bool, error) {
	binary, err := Binary()
	if err != nil {
		return "", false, nil
	}
	t.binary = binary

	if t.dnsName == "" {
		state, err := Detect(ctx)
		if err != nil {
			return "", false, err
		}
		if !state.Ready() {
			return "", false, nil
		}
		t.dnsName = state.DNSName
		t.hostPort = fmt.Sprintf("%s:%d", t.dnsName, t.port)
		t.url = t.buildURL()
	}

	cfg, err := loadServeConfig(ctx, binary)
	if err != nil {
		return "", false, err
	}

	target := cfg.proxyTarget(t.hostPort)
	if target == "" {
		return "", false, nil
	}
	if t.mode == ModeFunnel && !cfg.AllowFunnel[t.hostPort] {
		return "", false, nil
	}

	t.target = target
	return t.url, true, nil
}
