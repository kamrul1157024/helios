package daemon

import "testing"

// TestResolvePublicBind covers the derivation that replaced the hardcoded
// 0.0.0.0. Only the "local" provider hands out a LAN URL, so only it needs a
// LAN listener; everything else proxies from loopback.
func TestResolvePublicBind(t *testing.T) {
	tests := []struct {
		name     string
		bind     string
		provider string
		want     string
	}{
		{"default config, no provider", "localhost", "", "127.0.0.1"},
		{"default config, tailscale", "localhost", "tailscale", "127.0.0.1"},
		{"default config, cloudflare", "localhost", "cloudflare", "127.0.0.1"},
		{"default config, local needs LAN", "localhost", "local", "0.0.0.0"},
		{"empty bind, local needs LAN", "", "local", "0.0.0.0"},
		{"empty bind, tailscale", "", "tailscale", "127.0.0.1"},
		{"explicit bind wins over derivation", "0.0.0.0", "tailscale", "0.0.0.0"},
		{"explicit loopback wins over local", "127.0.0.1", "local", "127.0.0.1"},
		{"explicit interface is honoured", "192.168.1.10", "cloudflare", "192.168.1.10"},
		{"whitespace is trimmed", "  0.0.0.0  ", "cloudflare", "0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Server.Bind = tt.bind
			cfg.Tunnel.Provider = tt.provider

			if got := ResolvePublicBind(cfg); got != tt.want {
				t.Errorf("ResolvePublicBind() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultConfigDoesNotExposePublicPort pins the posture change: a fresh
// install no longer listens on every interface.
func TestDefaultConfigDoesNotExposePublicPort(t *testing.T) {
	if got := ResolvePublicBind(DefaultConfig()); got != "127.0.0.1" {
		t.Errorf("default install binds %q, want 127.0.0.1", got)
	}
}
