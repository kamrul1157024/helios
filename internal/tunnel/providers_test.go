package tunnel

import (
	"regexp"
	"testing"
)

// TestProviderNames verifies each provider returns the correct name.
func TestProviderNames(t *testing.T) {
	tests := []struct {
		tunnel   Tunnel
		expected string
	}{
		{&CloudflareTunnel{}, "cloudflare"},
		{&NgrokTunnel{}, "ngrok"},
		{&TailscaleTunnel{}, "tailscale"},
		{&LocalTunnel{}, "local"},
		{&CustomTunnel{customURL: "https://example.com"}, "custom"},
		{&ZrokTunnel{}, "zrok"},
		{&LocaltunnelTunnel{}, "localtunnel"},
		{&LocalhostRunTunnel{}, "localhostrun"},
		{&LocalxposeTunnel{}, "localxpose"},
	}

	for _, tt := range tests {
		if got := tt.tunnel.Provider(); got != tt.expected {
			t.Errorf("%T.Provider() = %q, want %q", tt.tunnel, got, tt.expected)
		}
	}
}

// TestProviderPIDZeroBeforeStart verifies PID is 0 before Start.
func TestProviderPIDZeroBeforeStart(t *testing.T) {
	providers := []Tunnel{
		&CloudflareTunnel{},
		&NgrokTunnel{},
		&TailscaleTunnel{},
		&LocalTunnel{},
		&CustomTunnel{customURL: "https://example.com"},
		&ZrokTunnel{},
		&LocaltunnelTunnel{},
		&LocalhostRunTunnel{},
		&LocalxposeTunnel{},
	}

	for _, p := range providers {
		if pid := p.PID(); pid != 0 {
			t.Errorf("%T.PID() before start = %d, want 0", p, pid)
		}
	}
}

// TestProviderURLEmptyBeforeStart verifies URL is empty before Start.
func TestProviderURLEmptyBeforeStart(t *testing.T) {
	providers := []struct {
		name   string
		tunnel Tunnel
	}{
		{"cloudflare", &CloudflareTunnel{}},
		{"ngrok", &NgrokTunnel{}},
		{"tailscale", &TailscaleTunnel{}},
		{"local", &LocalTunnel{}},
		{"zrok", &ZrokTunnel{}},
		{"localtunnel", &LocaltunnelTunnel{}},
		{"localhostrun", &LocalhostRunTunnel{}},
		{"localxpose", &LocalxposeTunnel{}},
	}

	for _, tt := range providers {
		if url := tt.tunnel.URL(); url != "" {
			t.Errorf("%s.URL() before start = %q, want empty", tt.name, url)
		}
	}
}

// TestCustomTunnelURL verifies CustomTunnel returns the configured URL.
func TestCustomTunnelURL(t *testing.T) {
	ct := &CustomTunnel{customURL: "https://my-custom-tunnel.example.com"}
	if got := ct.URL(); got != "https://my-custom-tunnel.example.com" {
		t.Errorf("URL: got %q, want %q", got, "https://my-custom-tunnel.example.com")
	}
}

// TestCustomTunnelStartNoURL verifies CustomTunnel errors without URL.
func TestCustomTunnelStartNoURL(t *testing.T) {
	ct := &CustomTunnel{}
	if err := ct.Start(8080); err == nil {
		t.Error("expected error when custom URL is empty")
	}
}

// TestCustomTunnelStartWithURL verifies CustomTunnel succeeds with URL.
func TestCustomTunnelStartWithURL(t *testing.T) {
	ct := &CustomTunnel{customURL: "https://example.com"}
	if err := ct.Start(8080); err != nil {
		t.Errorf("Start with URL: %v", err)
	}
}

// TestCustomTunnelStop verifies CustomTunnel stop is always no-op.
func TestCustomTunnelStop(t *testing.T) {
	ct := &CustomTunnel{customURL: "https://example.com"}
	if err := ct.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestLocalTunnelStop verifies LocalTunnel stop is always no-op.
func TestLocalTunnelStop(t *testing.T) {
	lt := &LocalTunnel{}
	if err := lt.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestCloudflareURLRegex tests the regex used to parse cloudflare URLs.
func TestCloudflareURLRegex(t *testing.T) {
	re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    `2024-01-01 INF |  https://abc-xyz-123.trycloudflare.com`,
			expected: "https://abc-xyz-123.trycloudflare.com",
		},
		{
			input:    `https://my-tunnel.trycloudflare.com`,
			expected: "https://my-tunnel.trycloudflare.com",
		},
		{
			input:    `Registered tunnel connection connIndex=0 url=https://test-tunnel.trycloudflare.com`,
			expected: "https://test-tunnel.trycloudflare.com",
		},
		{
			input:    `no match here`,
			expected: "",
		},
		{
			input:    `http://not-https.trycloudflare.com`,
			expected: "",
		},
	}

	for _, tt := range tests {
		match := re.FindString(tt.input)
		if match != tt.expected {
			t.Errorf("input %q: got %q, want %q", tt.input, match, tt.expected)
		}
	}
}

// TestNgrokURLRegex tests the regex used to parse ngrok URLs.
func TestNgrokURLRegex(t *testing.T) {
	re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.ngrok[a-zA-Z0-9.-]*`)

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    `{"public_url":"https://abc123.ngrok.io","proto":"https"}`,
			expected: "https://abc123.ngrok.io",
		},
		{
			input:    `{"public_url":"https://abc123.ngrok-free.app","proto":"https"}`,
			expected: "https://abc123.ngrok-free.app",
		},
		{
			input:    `{"public_url":"https://def456.ngrok.dev","proto":"https"}`,
			expected: "https://def456.ngrok.dev",
		},
		{
			input:    `no ngrok url here`,
			expected: "",
		},
	}

	for _, tt := range tests {
		match := re.FindString(tt.input)
		if match != tt.expected {
			t.Errorf("input %q: got %q, want %q", tt.input, match, tt.expected)
		}
	}
}

// TestTailscaleDNSNameRegex tests the regex used to parse tailscale DNS names.
func TestTailscaleDNSNameRegex(t *testing.T) {
	re := regexp.MustCompile(`"DNSName"\s*:\s*"([^"]+)"`)

	tests := []struct {
		input    string
		wantName string
	}{
		{
			input:    `{"DNSName":"my-machine.tailnet-abc.ts.net.","Online":true}`,
			wantName: "my-machine.tailnet-abc.ts.net.",
		},
		{
			input:    `{"DNSName": "server.example.ts.net.", "OS": "linux"}`,
			wantName: "server.example.ts.net.",
		},
		{
			input:    `no dns name here`,
			wantName: "",
		},
	}

	for _, tt := range tests {
		match := re.FindSubmatch([]byte(tt.input))
		got := ""
		if match != nil {
			got = string(match[1])
		}
		if got != tt.wantName {
			t.Errorf("input %q: got %q, want %q", tt.input, got, tt.wantName)
		}
	}
}

// TestManagerStartLocalProvider tests starting the local provider (no external process).
func TestManagerStartLocalProvider(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	url, err := mgr.Start("local", "", 8080)
	if err != nil {
		t.Fatalf("Start local: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty URL for local provider")
	}

	// URL should contain the port
	if !regexp.MustCompile(`:8080$`).MatchString(url) {
		t.Errorf("URL %q should end with :8080", url)
	}

	// Should have http:// scheme (not https)
	if !regexp.MustCompile(`^http://`).MatchString(url) {
		t.Errorf("URL %q should start with http://", url)
	}

	// Status should show active
	status := mgr.Status()
	if active, ok := status["active"].(bool); !ok || !active {
		t.Error("expected active=true after start")
	}

	// State file should exist
	state, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state == nil {
		t.Fatal("state file should exist after start")
	}
	if state.Provider != "local" {
		t.Errorf("state provider: got %q, want %q", state.Provider, "local")
	}

	// Stop
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// State file should be removed
	state, err = LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState after stop: %v", err)
	}
	if state != nil {
		t.Error("state file should be removed after stop")
	}
}

// TestManagerStartCustomProvider tests starting the custom provider.
func TestManagerStartCustomProvider(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	url, err := mgr.Start("custom", "https://my-tunnel.example.com", 8080)
	if err != nil {
		t.Fatalf("Start custom: %v", err)
	}
	if url != "https://my-tunnel.example.com" {
		t.Errorf("URL: got %q, want %q", url, "https://my-tunnel.example.com")
	}

	status := mgr.Status()
	if provider, ok := status["provider"].(string); !ok || provider != "custom" {
		t.Errorf("provider: got %q, want %q", provider, "custom")
	}
}

// TestManagerStartReplacesExisting verifies starting a new tunnel stops the old one.
func TestManagerStartReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Start first tunnel
	url1, err := mgr.Start("custom", "https://first.example.com", 8080)
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if url1 != "https://first.example.com" {
		t.Errorf("first URL: got %q, want %q", url1, "https://first.example.com")
	}

	// Start second tunnel — should replace first
	url2, err := mgr.Start("custom", "https://second.example.com", 8080)
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if url2 != "https://second.example.com" {
		t.Errorf("second URL: got %q, want %q", url2, "https://second.example.com")
	}

	// Status should show second tunnel
	status := mgr.Status()
	if publicURL, ok := status["public_url"].(string); !ok || publicURL != "https://second.example.com" {
		t.Errorf("public_url: got %q, want %q", publicURL, "https://second.example.com")
	}
}
