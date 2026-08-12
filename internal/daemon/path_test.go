package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergePATH(t *testing.T) {
	sep := string(os.PathListSeparator)

	tests := []struct {
		name    string
		current string
		extra   string
		want    string
	}{
		{
			name:    "launchd default gains the profile's directories",
			current: "/usr/bin" + sep + "/bin",
			extra:   "/opt/homebrew/bin" + sep + "/usr/bin",
			want:    "/usr/bin" + sep + "/bin" + sep + "/opt/homebrew/bin",
		},
		{
			name:    "no extra leaves the current PATH untouched",
			current: "/usr/bin" + sep + "/bin",
			extra:   "",
			want:    "/usr/bin" + sep + "/bin",
		},
		{
			name:    "duplicates within either side collapse",
			current: "/usr/bin" + sep + "/usr/bin",
			extra:   "/bin" + sep + "/bin",
			want:    "/usr/bin" + sep + "/bin",
		},
		{
			name:    "empty segments are dropped",
			current: "/usr/bin" + sep + sep + "/bin",
			extra:   sep + "/opt/homebrew/bin",
			want:    "/usr/bin" + sep + "/bin" + sep + "/opt/homebrew/bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergePATH(tt.current, tt.extra); got != tt.want {
				t.Errorf("mergePATH(%q, %q) = %q, want %q", tt.current, tt.extra, got, tt.want)
			}
		})
	}
}

// TestMergePATHKeepsCurrentFirst pins the precedence rule: an explicitly set
// PATH must not be reordered by whatever the user's profile prefers.
func TestMergePATHKeepsCurrentFirst(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := mergePATH("/custom/bin", "/opt/homebrew/bin"+sep+"/custom/bin")
	want := "/custom/bin" + sep + "/opt/homebrew/bin"
	if got != want {
		t.Errorf("mergePATH = %q, want %q", got, want)
	}
}

func TestLoginShellPATH(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fakeshell")
	script := "#!/bin/sh\nprintf %s \"/opt/homebrew/bin:/usr/bin\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got := loginShellPATH(context.Background(), shell)
	if got != "/opt/homebrew/bin:/usr/bin" {
		t.Errorf("loginShellPATH = %q, want %q", got, "/opt/homebrew/bin:/usr/bin")
	}
}

// TestLoginShellPATHFailureIsEmpty covers the case that matters most: a shell
// that cannot run must leave the caller with the PATH it already had.
func TestLoginShellPATHFailureIsEmpty(t *testing.T) {
	if got := loginShellPATH(context.Background(), filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Errorf("loginShellPATH = %q, want empty", got)
	}
}

// TestImportLoginPATHWidensLaunchdDefault exercises the whole path with a
// stub shell, since that is the launchd case the daemon actually hits.
func TestImportLoginPATHWidensLaunchdDefault(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fakeshell")
	script := "#!/bin/sh\nprintf %s \"/opt/homebrew/bin:/usr/bin:/bin\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	got := importLoginPATH()
	if !strings.Contains(got, "/opt/homebrew/bin") {
		t.Errorf("importLoginPATH = %q, want it to contain /opt/homebrew/bin", got)
	}
	if got != os.Getenv("PATH") {
		t.Errorf("importLoginPATH returned %q but set PATH to %q", got, os.Getenv("PATH"))
	}
	if !strings.HasPrefix(got, "/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Errorf("importLoginPATH = %q, want the inherited PATH to keep priority", got)
	}
}
