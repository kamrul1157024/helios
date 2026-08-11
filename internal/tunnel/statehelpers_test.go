package tunnel

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestStateLivenessPIDBacked pins the PID path: a provider that owns a process
// is judged by that process, and the persisted URL is returned unchanged.
func TestStateLivenessPIDBacked(t *testing.T) {
	state := TunnelState{
		PID: 0, Provider: "cloudflare", URL: "https://x.trycloudflare.com",
		Port: 7655, StartedAt: time.Now().UTC(),
	}

	url, active := StateLiveness(context.Background(), state, ProviderConfig{})
	if active {
		t.Error("a dead PID was reported as an active tunnel")
	}
	if url != state.URL {
		t.Errorf("url = %q, want the persisted %q", url, state.URL)
	}
}

// TestStateLivenessProcessLess is the CLI-side counterpart of the adoption
// regression: `helios tunnel status` must not delete the state file of a
// working tunnel just because its PID is 0.
func TestStateLivenessProcessLess(t *testing.T) {
	state := TunnelState{
		PID: 0, Provider: "custom", URL: "https://my-tunnel.example.com",
		Port: 7655, StartedAt: time.Now().UTC(),
	}

	url, active := StateLiveness(context.Background(), state, ProviderConfig{})
	if !active {
		t.Error("a process-less tunnel was reported as dead")
	}
	if url != state.URL {
		t.Errorf("url = %q, want %q", url, state.URL)
	}
}

// TestStateLivenessRecomputesLocalURL covers the LAN address changing while no
// helios process was running.
func TestStateLivenessRecomputesLocalURL(t *testing.T) {
	state := TunnelState{
		PID: 0, Provider: "local", URL: "http://192.0.2.55:9999",
		Port: 9999, StartedAt: time.Now().UTC(),
	}

	url, active := StateLiveness(context.Background(), state, ProviderConfig{})
	if !active {
		t.Fatal("the local provider was reported as dead")
	}
	if !strings.HasSuffix(url, ":9999") {
		t.Errorf("url = %q, want the persisted port 9999", url)
	}
}

// TestStateTunnelUsesProviderConfig checks that a tunnel rebuilt outside the
// daemon still honours provider settings — the funnel port rules in particular,
// which is the difference between a clear error and a confusing CLI failure.
func TestStateTunnelUsesProviderConfig(t *testing.T) {
	state := TunnelState{Provider: "tailscale", Port: 7655}

	if _, err := StateTunnel(state, ProviderConfig{
		Tailscale: TailscaleProviderConfig{Mode: "funnel", Port: 7655},
	}); err == nil {
		t.Error("expected an error for funnel on port 7655")
	}

	if _, err := StateTunnel(state, ProviderConfig{
		Tailscale: TailscaleProviderConfig{Mode: "serve"},
	}); err != nil {
		t.Errorf("serve on the default port: %v", err)
	}
}

// TestKillTunnelRemovesStateForProcessLessProvider guards against the state
// file outliving a torn-down tunnel, which would make every later status call
// report a tunnel that is not there.
func TestKillTunnelRemovesStateForProcessLessProvider(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, TunnelState{
		PID: 0, Provider: "custom", URL: "https://my-tunnel.example.com",
		Port: 7655, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if err := KillTunnel(dir, ProviderConfig{}); err != nil {
		t.Fatalf("KillTunnel: %v", err)
	}

	state, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state != nil {
		t.Error("state file survived KillTunnel")
	}
}
