package tunnel

import (
	"os/exec"
	"regexp"
	"testing"
)

func TestLocaltunnelProviderName(t *testing.T) {
	lt := &LocaltunnelTunnel{}
	if got := lt.Provider(); got != "localtunnel" {
		t.Errorf("Provider() = %q, want %q", got, "localtunnel")
	}
}

func TestLocaltunnelPIDBeforeStart(t *testing.T) {
	lt := &LocaltunnelTunnel{}
	if pid := lt.PID(); pid != 0 {
		t.Errorf("PID() before start = %d, want 0", pid)
	}
}

func TestLocaltunnelURLBeforeStart(t *testing.T) {
	lt := &LocaltunnelTunnel{}
	if url := lt.URL(); url != "" {
		t.Errorf("URL() before start = %q, want empty", url)
	}
}

func TestLocaltunnelStopBeforeStart(t *testing.T) {
	lt := &LocaltunnelTunnel{}
	if err := lt.Stop(); err != nil {
		t.Errorf("Stop() before start: %v", err)
	}
}

func TestLocaltunnelBuildArgs(t *testing.T) {
	tests := []struct {
		name      string
		tunnel    *LocaltunnelTunnel
		port      int
		wantArgs  []string
	}{
		{
			name:     "basic",
			tunnel:   &LocaltunnelTunnel{},
			port:     8080,
			wantArgs: []string{"--port", "8080"},
		},
		{
			name:     "with subdomain",
			tunnel:   &LocaltunnelTunnel{subdomain: "my-helios"},
			port:     3000,
			wantArgs: []string{"--port", "3000", "--subdomain", "my-helios"},
		},
		{
			name:     "with host",
			tunnel:   &LocaltunnelTunnel{host: "https://lt.mycompany.com"},
			port:     8080,
			wantArgs: []string{"--port", "8080", "--host", "https://lt.mycompany.com"},
		},
		{
			name:     "with subdomain and host",
			tunnel:   &LocaltunnelTunnel{subdomain: "test", host: "https://lt.example.com"},
			port:     9090,
			wantArgs: []string{"--port", "9090", "--subdomain", "test", "--host", "https://lt.example.com"},
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

func TestLocaltunnelBuildNpxArgs(t *testing.T) {
	lt := &LocaltunnelTunnel{subdomain: "my-helios"}
	got := lt.buildNpxArgs(8080)

	expected := []string{"localtunnel", "--port", "8080", "--subdomain", "my-helios"}
	if len(got) != len(expected) {
		t.Fatalf("npx args length: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(expected), got, expected)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, got[i], expected[i])
		}
	}
}

func TestLocaltunnelURLRegex(t *testing.T) {
	tests := []struct {
		input        string
		wantMatch    string
		wantSubdomain string
	}{
		{
			input:         "your url is: https://abc123.loca.lt",
			wantMatch:     "https://abc123.loca.lt",
			wantSubdomain: "abc123",
		},
		{
			input:         "your url is: https://my-helios.loca.lt",
			wantMatch:     "https://my-helios.loca.lt",
			wantSubdomain: "my-helios",
		},
		{
			input:         "https://test.localtunnel.me is ready",
			wantMatch:     "https://test.localtunnel.me",
			wantSubdomain: "test",
		},
		{
			input:         "https://my-tunnel.lt.mycompany.com",
			wantMatch:     "https://my-tunnel.lt.mycompany.com",
			wantSubdomain: "my-tunnel",
		},
		{
			input:        "no url here",
			wantMatch:    "",
			wantSubdomain: "",
		},
		{
			input:        "http://not-https.loca.lt",
			wantMatch:    "",
			wantSubdomain: "",
		},
	}

	for _, tt := range tests {
		match := localtunnelURLRegex.FindStringSubmatch(tt.input)
		got := ""
		gotSubdomain := ""
		if match != nil {
			got = match[0]
			if len(match) > 1 {
				gotSubdomain = match[1]
			}
		}
		if got != tt.wantMatch {
			t.Errorf("input %q: match got %q, want %q", tt.input, got, tt.wantMatch)
		}
		if gotSubdomain != tt.wantSubdomain {
			t.Errorf("input %q: subdomain got %q, want %q", tt.input, gotSubdomain, tt.wantSubdomain)
		}
	}
}

func TestLocaltunnelSubdomainCallback(t *testing.T) {
	var captured string
	lt := &LocaltunnelTunnel{
		onSubdomainAssigned: func(subdomain string) {
			captured = subdomain
		},
	}

	// Simulate the callback
	if lt.onSubdomainAssigned != nil {
		lt.onSubdomainAssigned("my-subdomain")
	}
	if captured != "my-subdomain" {
		t.Errorf("callback captured %q, want %q", captured, "my-subdomain")
	}
}

func TestManagerStartLocaltunnelNoBinary(t *testing.T) {
	// Skip this test if lt or npx is available, because it would attempt
	// a real connection and timeout (30s).
	if _, err := exec.LookPath("lt"); err == nil {
		t.Skip("lt is installed, skipping not-found test")
	}
	if _, err := exec.LookPath("npx"); err == nil {
		t.Skip("npx is installed, would attempt real connection — skipping")
	}

	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.Start("localtunnel", "", 8080)
	if err == nil {
		mgr.Stop()
		t.Fatal("expected error when localtunnel is not installed")
	}

	if got := err.Error(); !regexp.MustCompile(`localtunnel not found`).MatchString(got) {
		t.Errorf("error should mention 'localtunnel not found', got: %q", got)
	}
}
