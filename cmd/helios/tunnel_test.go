package main

import (
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/tunnel"
)

// TestTailscaleModeFromURL pins the scheme-to-mode mapping the CLI relies on
// when it reports a tunnel it did not start. Serve always publishes plain HTTP
// inside the tailnet; Funnel always terminates TLS.
func TestTailscaleModeFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"http://box.tail1234.ts.net:7655", "serve"},
		{"https://box.tail1234.ts.net", "funnel"},
		{"https://box.tail1234.ts.net:8443", "funnel"},
	}

	for _, tt := range tests {
		if got := tailscaleMode(tt.url); got != tt.want {
			t.Errorf("tailscaleMode(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// TestTunnelExposure checks that the two Tailscale modes are described
// differently. Collapsing them would tell a Serve user their machine is on the
// public internet, or a Funnel user that it is not.
func TestTunnelExposure(t *testing.T) {
	serve := tunnelExposure("tailscale", "http://box.tail1234.ts.net:7655")
	if !strings.Contains(serve, "tailnet") {
		t.Errorf("serve exposure = %q, want it to mention the tailnet", serve)
	}

	funnel := tunnelExposure("tailscale", "https://box.tail1234.ts.net")
	if !strings.Contains(funnel, "public internet") {
		t.Errorf("funnel exposure = %q, want it to mention the public internet", funnel)
	}

	if got := tunnelExposure("cloudflare", "https://x.trycloudflare.com"); got != "" {
		t.Errorf("cloudflare exposure = %q, want empty", got)
	}
}

// TestTunnelDescriptionOmitsZeroPID keeps "PID 0" out of the output for
// providers that own no process — it reads as a bug rather than as a fact.
func TestTunnelDescriptionOmitsZeroPID(t *testing.T) {
	state := tunnel.TunnelState{Provider: "tailscale", PID: 0}
	got := tunnelDescription(state, "http://box.tail1234.ts.net:7655")
	if strings.Contains(got, "PID") {
		t.Errorf("description = %q, want no PID for a process-less provider", got)
	}
	if !strings.Contains(got, "serve") {
		t.Errorf("description = %q, want the exposure mode named", got)
	}

	withPID := tunnel.TunnelState{Provider: "cloudflare", PID: 4242}
	if got := tunnelDescription(withPID, "https://x.trycloudflare.com"); !strings.Contains(got, "PID 4242") {
		t.Errorf("description = %q, want the PID reported", got)
	}
}
