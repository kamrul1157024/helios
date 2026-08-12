package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCWDExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got, err := resolveCWD("~")
	if err != nil {
		t.Fatalf("resolveCWD(~) = %v", err)
	}
	if got != filepath.Clean(home) {
		t.Errorf("resolveCWD(~) = %q, want %q", got, home)
	}
}

func TestResolveCWDRejectsRelativePaths(t *testing.T) {
	_, err := resolveCWD("workspace/helios")
	if err == nil {
		t.Fatal("expected an error for a relative path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %q, want it to mention absolute paths", err)
	}
}

func TestResolveCWDNamesTheMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := resolveCWD(missing)
	if err == nil {
		t.Fatal("expected an error for a missing directory")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name %q", err, missing)
	}
}

func TestResolveCWDRejectsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveCWD(file); err == nil {
		t.Fatal("expected an error for a file")
	}
}

func TestResolveCWDCleansAndKeepsValidPaths(t *testing.T) {
	dir := t.TempDir()

	got, err := resolveCWD(dir + "/./")
	if err != nil {
		t.Fatalf("resolveCWD = %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Errorf("resolveCWD = %q, want %q", got, filepath.Clean(dir))
	}
}

// Whitespace is what a paste into a text field leaves behind.
func TestResolveCWDTrimsSurroundingWhitespace(t *testing.T) {
	dir := t.TempDir()

	got, err := resolveCWD("  " + dir + "  ")
	if err != nil {
		t.Fatalf("resolveCWD = %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Errorf("resolveCWD = %q, want %q", got, filepath.Clean(dir))
	}
}
