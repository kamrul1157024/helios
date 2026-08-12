package server

import "testing"

func TestRestartRequiredForBind(t *testing.T) {
	tests := []struct {
		provider string
		bind     string
		want     bool
	}{
		{"local", "127.0.0.1", true},
		{"local", "localhost", true},
		{"local", "::1", true},
		{"local", "0.0.0.0", false},
		{"tailscale", "127.0.0.1", false},
		{"cloudflare", "127.0.0.1", false},
		{"custom", "localhost", false},
	}

	for _, tt := range tests {
		if got := restartRequiredForBind(tt.provider, tt.bind); got != tt.want {
			t.Errorf("restartRequiredForBind(%q, %q) = %v, want %v", tt.provider, tt.bind, got, tt.want)
		}
	}
}
