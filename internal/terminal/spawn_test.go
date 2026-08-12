package terminal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A missing working directory must be named as such. Left to exec, it reports
// the helios binary as the missing file instead, which reads as a broken
// install and sends the reader looking in entirely the wrong place.
func TestSpawnHostRejectsMissingCwd(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "deleted-worktree")

	err := SpawnHost(dir, "sess-1", missing, []string{"/bin/echo"})
	if err == nil {
		t.Fatal("expected an error for a missing working directory")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name %q", err, missing)
	}
	if strings.Contains(err.Error(), "fork/exec") {
		t.Errorf("error = %q, want the directory blamed rather than the binary", err)
	}
}

// A path that exists but is a file is the same failure with a different shape.
func TestSpawnHostRejectsFileAsCwd(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SpawnHost(dir, "sess-1", file, []string{"/bin/echo"}); err == nil {
		t.Fatal("expected an error for a file used as the working directory")
	}
}

// Empty argv is the resume path: the host picks the agent and its --resume
// flag itself, so nothing is passed down.
func TestHostArgs_NoCommandMeansResume(t *testing.T) {
	got := hostArgs("sess-1", "/work", nil)

	want := []string{"ptyhost", "sess-1", "--cwd", "/work"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestHostArgs_CommandBecomesOneFlagPerWord(t *testing.T) {
	got := hostArgs("sess-1", "/work", []string{"/bin/claude", "--session-id", "sess-1"})

	want := []string{
		"ptyhost", "sess-1", "--cwd", "/work",
		"--cmd", "/bin/claude", "--arg", "--session-id", "--arg", "sess-1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// One flag per word is what makes quoting unnecessary: a prompt keeps its
// spaces, quotes and backticks all the way to the agent.
func TestHostArgs_ArgumentsSurviveWhole(t *testing.T) {
	prompt := "fix `git log` and say \"hi\", it's $HOME"

	got := hostArgs("sess-1", "", []string{"claude", prompt})

	want := []string{"ptyhost", "sess-1", "--cmd", "claude", "--arg", prompt}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}
