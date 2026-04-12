package tunnel

import (
	"os"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	mgr := NewManager("/tmp/test-helios")
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}
	if mgr.heliosDir != "/tmp/test-helios" {
		t.Errorf("heliosDir: got %q, want %q", mgr.heliosDir, "/tmp/test-helios")
	}
}

func TestManagerStatusNoTunnel(t *testing.T) {
	mgr := NewManager(t.TempDir())
	status := mgr.Status()

	active, ok := status["active"].(bool)
	if !ok || active {
		t.Error("expected active=false when no tunnel")
	}
	provider, ok := status["provider"].(string)
	if !ok || provider != "" {
		t.Errorf("expected empty provider, got %q", provider)
	}
	if _, exists := status["public_url"]; exists {
		t.Error("public_url should not be present when no tunnel")
	}
}

func TestManagerStatusWithTunnel(t *testing.T) {
	mgr := NewManager(t.TempDir())
	mgr.active = &adoptedTunnel{
		pid:      42,
		url:      "https://test.example.com",
		provider: "test",
	}

	status := mgr.Status()

	active, ok := status["active"].(bool)
	if !ok || !active {
		t.Error("expected active=true")
	}
	provider, ok := status["provider"].(string)
	if !ok || provider != "test" {
		t.Errorf("provider: got %q, want %q", provider, "test")
	}
	url, ok := status["public_url"].(string)
	if !ok || url != "https://test.example.com" {
		t.Errorf("public_url: got %q, want %q", url, "https://test.example.com")
	}
}

func TestManagerStopNoTunnel(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop with no tunnel: %v", err)
	}
}

func TestManagerStartUnknownProvider(t *testing.T) {
	mgr := NewManager(t.TempDir())
	_, err := mgr.Start("unknown-provider", "", 8080)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestManagerAdoptNoState(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	url, err := mgr.Adopt()
	if err != nil {
		t.Fatalf("Adopt with no state: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL, got %q", url)
	}
	if mgr.active != nil {
		t.Error("active should be nil when no state")
	}
}

func TestManagerAdoptStalePID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Save a state with a dead PID
	state := TunnelState{
		PID:       999999999,
		Provider:  "cloudflare",
		URL:       "https://stale.trycloudflare.com",
		Port:      7655,
		StartedAt: time.Now().UTC(),
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	url, err := mgr.Adopt()
	if err != nil {
		t.Fatalf("Adopt with stale PID: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL for stale PID, got %q", url)
	}

	// State file should be cleaned up
	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState after adopt: %v", err)
	}
	if loaded != nil {
		t.Error("stale state file should be removed")
	}
}

func TestManagerAdoptAlivePID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Save a state with our own PID (which is alive)
	state := TunnelState{
		PID:       os.Getpid(),
		Provider:  "cloudflare",
		URL:       "https://alive.trycloudflare.com",
		Port:      7655,
		StartedAt: time.Now().UTC(),
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	url, err := mgr.Adopt()
	if err != nil {
		t.Fatalf("Adopt with alive PID: %v", err)
	}
	if url != "https://alive.trycloudflare.com" {
		t.Errorf("URL: got %q, want %q", url, "https://alive.trycloudflare.com")
	}
	if mgr.active == nil {
		t.Fatal("active should be set after successful adopt")
	}
	if mgr.active.Provider() != "cloudflare" {
		t.Errorf("provider: got %q, want %q", mgr.active.Provider(), "cloudflare")
	}
	if mgr.active.PID() != os.Getpid() {
		t.Errorf("PID: got %d, want %d", mgr.active.PID(), os.Getpid())
	}
}

// TestAdoptedTunnelInterface verifies the adoptedTunnel implements Tunnel correctly.
func TestAdoptedTunnelInterface(t *testing.T) {
	at := &adoptedTunnel{
		pid:      42,
		url:      "https://example.com",
		provider: "ngrok",
	}

	// Verify interface compliance
	var _ Tunnel = at

	if at.Provider() != "ngrok" {
		t.Errorf("Provider: got %q, want %q", at.Provider(), "ngrok")
	}
	if at.URL() != "https://example.com" {
		t.Errorf("URL: got %q, want %q", at.URL(), "https://example.com")
	}
	if at.PID() != 42 {
		t.Errorf("PID: got %d, want %d", at.PID(), 42)
	}
	if err := at.Start(8080); err != nil {
		t.Errorf("Start should be no-op: %v", err)
	}
}
