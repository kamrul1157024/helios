package discovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTranscript puts a session's .jsonl in the project directory Claude Code
// would name for the given cwd, and returns its path.
func writeTranscript(t *testing.T, home, projectDir, sessionID string, modTime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

// A session that enters a git worktree leaves a transcript behind in the
// directory named after the cwd it started in.
func TestFindClaudeTranscriptPrefersTheNewestCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	old := time.Date(2026, 8, 27, 5, 30, 0, 0, time.UTC)
	writeTranscript(t, home, "-tmp-repo", "s1", old)
	moved := writeTranscript(t, home, "-tmp-repo--claude-worktrees-feature", "s1", old.Add(time.Hour))

	if got := FindClaudeTranscript("s1"); got != moved {
		t.Errorf("FindClaudeTranscript = %q, want %q", got, moved)
	}
}

func TestFindClaudeTranscriptEmptyWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTranscript(t, home, "-tmp-repo", "s1", time.Now())

	if got := FindClaudeTranscript("s2"); got != "" {
		t.Errorf("FindClaudeTranscript = %q, want \"\"", got)
	}
}
