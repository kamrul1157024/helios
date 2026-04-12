package tunnel

import (
	"os/exec"
	"regexp"
	"testing"
)

func TestLocalxposeProviderName(t *testing.T) {
	lx := &LocalxposeTunnel{}
	if got := lx.Provider(); got != "localxpose" {
		t.Errorf("Provider() = %q, want %q", got, "localxpose")
	}
}

func TestLocalxposePIDBeforeStart(t *testing.T) {
	lx := &LocalxposeTunnel{}
	if pid := lx.PID(); pid != 0 {
		t.Errorf("PID() before start = %d, want 0", pid)
	}
}

func TestLocalxposeURLBeforeStart(t *testing.T) {
	lx := &LocalxposeTunnel{}
	if url := lx.URL(); url != "" {
		t.Errorf("URL() before start = %q, want empty", url)
	}
}

func TestLocalxposeStopBeforeStart(t *testing.T) {
	lx := &LocalxposeTunnel{}
	if err := lx.Stop(); err != nil {
		t.Errorf("Stop() before start: %v", err)
	}
}

func TestLocalxposeBuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		tunnel   *LocalxposeTunnel
		port     int
		wantArgs []string
	}{
		{
			name:     "basic",
			tunnel:   &LocalxposeTunnel{},
			port:     8080,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:8080"},
		},
		{
			name:     "with subdomain",
			tunnel:   &LocalxposeTunnel{subdomain: "my-helios"},
			port:     8080,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:8080", "--subdomain", "my-helios"},
		},
		{
			name:     "with reserved domain",
			tunnel:   &LocalxposeTunnel{reservedDomain: "my-helios.loclx.io"},
			port:     8080,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:8080", "--reserved-domain", "my-helios.loclx.io"},
		},
		{
			name:     "reserved domain takes precedence over subdomain",
			tunnel:   &LocalxposeTunnel{subdomain: "sub", reservedDomain: "my-helios.loclx.io"},
			port:     8080,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:8080", "--reserved-domain", "my-helios.loclx.io"},
		},
		{
			name:     "with region",
			tunnel:   &LocalxposeTunnel{region: "eu"},
			port:     3000,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:3000", "--region", "eu"},
		},
		{
			name:     "with basic auth",
			tunnel:   &LocalxposeTunnel{basicAuth: "admin:secret"},
			port:     8080,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:8080", "--basic-auth", "admin:secret"},
		},
		{
			name: "full config",
			tunnel: &LocalxposeTunnel{
				reservedDomain: "my-helios.loclx.io",
				region:         "ap",
				basicAuth:      "user:pass",
			},
			port:     7655,
			wantArgs: []string{"tunnel", "http", "--to", "127.0.0.1:7655", "--reserved-domain", "my-helios.loclx.io", "--region", "ap", "--basic-auth", "user:pass"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tunnel.buildArgs(tt.port)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("args length: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.wantArgs), got, tt.wantArgs)
			}
			for i := range got {
				if got[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d]: got %q, want %q", i, got[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestLocalxposeBuildArgsHasTo(t *testing.T) {
	lx := &LocalxposeTunnel{}
	args := lx.buildArgs(8080)

	found := false
	for i, arg := range args {
		if arg == "--to" && i+1 < len(args) && args[i+1] == "127.0.0.1:8080" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("args should contain --to 127.0.0.1:8080, got %v", args)
	}
}

func TestLocalxposeURLRegex(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "https://abc123.loclx.io",
			expected: "https://abc123.loclx.io",
		},
		{
			input:    "Tunnel URL: https://my-helios.loclx.io",
			expected: "https://my-helios.loclx.io",
		},
		{
			input:    "https://custom.example.com active",
			expected: "https://custom.example.com",
		},
		{
			input:    "no url here",
			expected: "",
		},
		{
			input:    "http://not-https.loclx.io",
			expected: "",
		},
	}

	for _, tt := range tests {
		match := localxposeURLRegex.FindString(tt.input)
		if match != tt.expected {
			t.Errorf("input %q: got %q, want %q", tt.input, match, tt.expected)
		}
	}
}

func TestManagerStartLocalxposeNoBinary(t *testing.T) {
	if _, err := exec.LookPath("loclx"); err == nil {
		t.Skip("loclx is installed, skipping not-found test")
	}

	dir := t.TempDir()
	mgr := NewManager(dir)
	mgr.SetProviderConfig(ProviderConfig{
		Localxpose: LocalxposeProviderConfig{Region: "us"},
	})

	_, err := mgr.Start("localxpose", "", 8080)
	if err == nil {
		mgr.Stop()
		t.Fatal("expected error when localxpose is not installed")
	}

	if got := err.Error(); !regexp.MustCompile(`localxpose not found`).MatchString(got) {
		t.Errorf("error should mention 'localxpose not found', got: %q", got)
	}
}

func TestManagerProviderConfigWiring(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	cfg := ProviderConfig{
		Zrok: ZrokProviderConfig{
			ShareMode:  "reserved",
			ShareToken: "abc123",
		},
		Localtunnel: LocaltunnelProviderConfig{
			Subdomain: "test-sub",
			Host:      "https://lt.example.com",
		},
		LocalhostRun: LocalhostRunProviderConfig{
			SSHUser:           "plan",
			CustomDomain:      "myapp.lhr.rocks",
			KeepaliveInterval: 120,
			UseAutossh:        true,
		},
		Localxpose: LocalxposeProviderConfig{
			Subdomain:      "my-sub",
			ReservedDomain: "my.loclx.io",
			Region:         "eu",
			BasicAuth:      "admin:pass",
			AccessToken:    "tok123",
		},
	}

	mgr.SetProviderConfig(cfg)

	// Verify config was stored
	if mgr.providerConfig.Zrok.ShareToken != "abc123" {
		t.Errorf("Zrok ShareToken: got %q, want %q", mgr.providerConfig.Zrok.ShareToken, "abc123")
	}
	if mgr.providerConfig.Localtunnel.Subdomain != "test-sub" {
		t.Errorf("Localtunnel Subdomain: got %q, want %q", mgr.providerConfig.Localtunnel.Subdomain, "test-sub")
	}
	if mgr.providerConfig.LocalhostRun.KeepaliveInterval != 120 {
		t.Errorf("LocalhostRun KeepaliveInterval: got %d, want %d", mgr.providerConfig.LocalhostRun.KeepaliveInterval, 120)
	}
	if mgr.providerConfig.Localxpose.Region != "eu" {
		t.Errorf("Localxpose Region: got %q, want %q", mgr.providerConfig.Localxpose.Region, "eu")
	}
}
