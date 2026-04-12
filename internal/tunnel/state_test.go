package tunnel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestSaveAndLoadState(t *testing.T) {
	dir := tempDir(t)

	now := time.Now().UTC().Truncate(time.Second)
	state := TunnelState{
		PID:       12345,
		Provider:  "cloudflare",
		URL:       "https://abc-xyz.trycloudflare.com",
		Port:      7655,
		StartedAt: now,
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatalf("state file should exist: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadState returned nil")
	}

	if loaded.PID != state.PID {
		t.Errorf("PID: got %d, want %d", loaded.PID, state.PID)
	}
	if loaded.Provider != state.Provider {
		t.Errorf("Provider: got %q, want %q", loaded.Provider, state.Provider)
	}
	if loaded.URL != state.URL {
		t.Errorf("URL: got %q, want %q", loaded.URL, state.URL)
	}
	if loaded.Port != state.Port {
		t.Errorf("Port: got %d, want %d", loaded.Port, state.Port)
	}
	if !loaded.StartedAt.Equal(state.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", loaded.StartedAt, state.StartedAt)
	}
}

func TestLoadStateNoFile(t *testing.T) {
	dir := tempDir(t)

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected nil state when no file exists, got %+v", loaded)
	}
}

func TestLoadStateCorruptedJSON(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, stateFileName)

	if err := os.WriteFile(path, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected error for corrupted JSON")
	}
}

func TestRemoveState(t *testing.T) {
	dir := tempDir(t)

	state := TunnelState{PID: 1, Provider: "test", URL: "http://test", Port: 80}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if err := RemoveState(dir); err != nil {
		t.Fatalf("RemoveState: %v", err)
	}

	// File should be gone
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); !os.IsNotExist(err) {
		t.Fatal("state file should not exist after RemoveState")
	}
}

func TestRemoveStateNoFile(t *testing.T) {
	dir := tempDir(t)

	// Should not error when file doesn't exist
	if err := RemoveState(dir); err != nil {
		t.Fatalf("RemoveState on nonexistent file: %v", err)
	}
}

func TestStatePath(t *testing.T) {
	got := statePath("/home/user/.helios")
	want := "/home/user/.helios/tunnel.state"
	if got != want {
		t.Errorf("statePath: got %q, want %q", got, want)
	}
}

func TestSaveStateOverwrite(t *testing.T) {
	dir := tempDir(t)

	state1 := TunnelState{PID: 100, Provider: "cloudflare", URL: "https://first.trycloudflare.com", Port: 7655}
	if err := SaveState(dir, state1); err != nil {
		t.Fatalf("SaveState first: %v", err)
	}

	state2 := TunnelState{PID: 200, Provider: "ngrok", URL: "https://second.ngrok.io", Port: 7655}
	if err := SaveState(dir, state2); err != nil {
		t.Fatalf("SaveState second: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.PID != 200 {
		t.Errorf("expected overwritten PID 200, got %d", loaded.PID)
	}
	if loaded.Provider != "ngrok" {
		t.Errorf("expected overwritten provider 'ngrok', got %q", loaded.Provider)
	}
}

func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive
	if !IsProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}

	// PID 0 is special — skip. Use a very high PID that shouldn't exist.
	if IsProcessAlive(999999999) {
		t.Error("PID 999999999 should not be alive")
	}
}
