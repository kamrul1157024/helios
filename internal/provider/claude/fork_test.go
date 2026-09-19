package claude

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
)

// seedTranscript writes a transcript for sessionID into the project directory
// that cwd names, the way Claude would.
func seedTranscript(t *testing.T, cwd, sessionID, body string) string {
	t.Helper()
	dir := projectDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make project dir: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// The rule Claude uses to name a project directory: every character that is not
// a letter or a digit becomes a dash. Asserted because a copy written under the
// wrong name lands in a directory the agent never reads, and the failure looks
// exactly like no fix at all.
func TestProjectDirReplacesEveryNonAlphanumeric(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got := filepath.Base(projectDir("/Users/md.kamrul.hassan/workspace/helios"))
	if want := "-Users-md-kamrul-hassan-workspace-helios"; got != want {
		t.Errorf("projectDir = %q, want %q", got, want)
	}

	// A dot is not special: `.claude` becomes `-claude`, which is why a worktree
	// under .claude/worktrees reads as a double dash.
	got = filepath.Base(projectDir("/Users/x/repo/.claude/worktrees/one"))
	if want := "-Users-x-repo--claude-worktrees-one"; got != want {
		t.Errorf("projectDir = %q, want %q", got, want)
	}
}

// The bug this whole file exists for. `claude --resume` looks only in the
// project its working directory names, so a fork given a worktree of its own
// used to launch into a directory holding no conversation, fail, and exit
// before any hook could say so.
func TestPrepareForkCarriesTheConversationIntoANewDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	parentCWD := filepath.Join(home, "repo")
	forkCWD := filepath.Join(home, "repo-worktrees", "try-a-queue")
	for _, dir := range []string{parentCWD, forkCWD} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}
	seedTranscript(t, parentCWD, "parent-1", `{"type":"user","text":"remember PELICAN"}`)

	p := &Provider{}
	if err := p.PrepareFork(parentCWD, forkCWD, "parent-1", ""); err != nil {
		t.Fatalf("PrepareFork: %v", err)
	}

	landed := filepath.Join(projectDir(forkCWD), "parent-1.jsonl")
	body, err := os.ReadFile(landed)
	if err != nil {
		t.Fatalf("the fork's project has no parent transcript: %v", err)
	}
	if string(body) != `{"type":"user","text":"remember PELICAN"}` {
		t.Errorf("transcript copied wrong: %q", body)
	}

	// The parent keeps its own conversation and stays resumable.
	if _, err := os.Stat(filepath.Join(projectDir(parentCWD), "parent-1.jsonl")); err != nil {
		t.Errorf("the parent lost its transcript: %v", err)
	}
}

// A parent that has never written a transcript cannot be forked. Answering that
// here is what stops the daemon starting an agent that exits on its first line
// and leaves a session reading as idle with nothing in it.
func TestPrepareForkRefusesAParentWithNoConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := &Provider{}
	err := p.PrepareFork(filepath.Join(home, "repo"), filepath.Join(home, "elsewhere"), "never-started", "")
	if !errors.Is(err, provider.ErrNoConversation) {
		t.Fatalf("PrepareFork = %v, want ErrNoConversation", err)
	}
}

// Same folder: the transcript is already where the agent will look, and copying
// a file over itself would truncate it.
func TestPrepareForkLeavesASameFolderForkAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("make %s: %v", cwd, err)
	}
	path := seedTranscript(t, cwd, "parent-1", "the whole conversation")

	p := &Provider{}
	if err := p.PrepareFork(cwd, cwd, "parent-1", ""); err != nil {
		t.Fatalf("PrepareFork: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "the whole conversation" {
		t.Errorf("same-folder fork damaged the transcript: %q", body)
	}
}

// Forking twice, or retrying after a failure, must not be an error.
func TestPrepareForkIsRepeatable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	parentCWD := filepath.Join(home, "repo")
	forkCWD := filepath.Join(home, "worktree")
	for _, dir := range []string{parentCWD, forkCWD} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}
	seedTranscript(t, parentCWD, "parent-1", "one")

	p := &Provider{}
	for attempt := range 2 {
		if err := p.PrepareFork(parentCWD, forkCWD, "parent-1", ""); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(projectDir(forkCWD), "parent-1.jsonl"))
	if err != nil || string(body) != "one" {
		t.Errorf("second run left %q, %v", body, err)
	}
}

// Claude is a ForkPreparer, so the daemon actually calls it. Without this the
// interface could be renamed out from under the provider and every other test
// here would still pass.
func TestClaudeIsAForkPreparer(t *testing.T) {
	var _ provider.ForkPreparer = &Provider{}
}
