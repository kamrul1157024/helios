package tunnel

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestAdoptRecoversPIDLessProvider is a regression test for a provider being
// silently dropped on every daemon restart. Providers that own no process
// persist PID 0, IsProcessAlive(0) is false, and the state file was therefore
// treated as stale. Reconcile answers the liveness question instead.
func TestAdoptRecoversPIDLessProvider(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	const url = "https://my-tunnel.example.com"
	if err := SaveState(dir, TunnelState{
		PID: 0, Provider: "custom", URL: url, Port: 7655, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := mgr.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got != url {
		t.Fatalf("Adopt() = %q, want %q", got, url)
	}

	status := mgr.Status()
	if active, _ := status["active"].(bool); !active {
		t.Error("manager reports no active tunnel after adopting")
	}
	if provider, _ := status["provider"].(string); provider != "custom" {
		t.Errorf("adopted provider = %q, want custom", provider)
	}

	state, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state == nil {
		t.Error("state file was removed despite a live tunnel")
	}
}

// TestAdoptRefreshesLocalURL covers the case the state file cannot answer: the
// machine's LAN address may have changed while the daemon was down, so the
// adopted URL is recomputed rather than trusted.
func TestAdoptRefreshesLocalURL(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := SaveState(dir, TunnelState{
		PID: 0, Provider: "local", URL: "http://192.0.2.55:9999", Port: 9999,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := mgr.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got == "" {
		t.Fatal("Adopt() returned no URL for the local provider")
	}
	if !strings.HasSuffix(got, ":9999") {
		t.Errorf("Adopt() = %q, want the persisted port 9999", got)
	}
	if !strings.HasPrefix(got, "http://") {
		t.Errorf("Adopt() = %q, want an http:// URL", got)
	}
}

// TestAdoptDiscardsDeadProcessProvider verifies the PID path still governs
// providers that do own a process.
func TestAdoptDiscardsDeadProcessProvider(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := SaveState(dir, TunnelState{
		PID: 0, Provider: "cloudflare", URL: "https://x.trycloudflare.com", Port: 7655,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := mgr.Adopt()
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got != "" {
		t.Errorf("Adopt() = %q, want empty for a dead process-backed tunnel", got)
	}

	state, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state != nil {
		t.Error("stale state file was not removed")
	}
}

// TestReconcilerImplementations pins which providers answer liveness
// themselves. Adding a process-less provider without Reconcile reintroduces
// the adoption bug, so this list is deliberately explicit.
func TestReconcilerImplementations(t *testing.T) {
	mgr := NewManager(t.TempDir())

	tests := []struct {
		provider string
		want     bool
	}{
		{"tailscale", true},
		{"custom", true},
		{"local", true},
		{"cloudflare", false},
		{"ngrok", false},
		{"zrok", false},
		{"localtunnel", false},
		{"localhostrun", false},
		{"localxpose", false},
	}

	for _, tt := range tests {
		tun, err := mgr.newTunnel(tt.provider, "https://example.com", 7655)
		if err != nil {
			t.Fatalf("newTunnel(%q): %v", tt.provider, err)
		}
		if _, ok := tun.(Reconciler); ok != tt.want {
			t.Errorf("%s implements Reconciler = %v, want %v", tt.provider, ok, tt.want)
		}
	}
}

// TestNewTunnelRejectsBadTailscaleConfig ensures an invalid funnel port fails
// at construction rather than when the CLI is invoked.
func TestNewTunnelRejectsBadTailscaleConfig(t *testing.T) {
	mgr := NewManager(t.TempDir())
	mgr.SetProviderConfig(ProviderConfig{
		Tailscale: TailscaleProviderConfig{Mode: "funnel", Port: 7655},
	})

	if _, err := mgr.newTunnel("tailscale", "", 7655); err == nil {
		t.Error("expected an error for funnel on port 7655")
	}

	mgr.SetProviderConfig(ProviderConfig{
		Tailscale: TailscaleProviderConfig{Mode: "sideways"},
	})
	if _, err := mgr.newTunnel("tailscale", "", 7655); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}

// TestCustomTunnelReconcile checks the trivial implementations directly.
func TestCustomTunnelReconcile(t *testing.T) {
	ct := &CustomTunnel{customURL: "https://example.com"}
	url, active, err := ct.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !active || url != "https://example.com" {
		t.Errorf("Reconcile() = (%q, %v), want (https://example.com, true)", url, active)
	}

	empty := &CustomTunnel{}
	if _, active, _ := empty.Reconcile(context.Background()); active {
		t.Error("an unconfigured custom tunnel reports itself active")
	}
}

// TestLocalTunnelReconcileWithoutPort guards the adopt path against a state
// file written before the port was recorded.
func TestLocalTunnelReconcileWithoutPort(t *testing.T) {
	lt := &LocalTunnel{}
	if _, active, _ := lt.Reconcile(context.Background()); active {
		t.Error("a portless local tunnel reports itself active")
	}
}
