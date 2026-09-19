package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realPath resolves the symlink macOS puts in front of every temp dir, so a
// path git reports and a path the test built compare equal.
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

func TestSanitizeBranchFlattensPaths(t *testing.T) {
	cases := map[string]string{
		"fix-auth":      "fix-auth",
		"feat/auth/v2":  "feat-auth-v2",
		"feat//auth":    "feat-auth",
		"feat  auth":    "feat-auth",
		"-leading-":     "leading",
		"...":           "worktree",
		"release/1.2.0": "release-1.2.0",
	}
	for branch, want := range cases {
		if got := sanitizeBranch(branch); got != want {
			t.Errorf("sanitizeBranch(%q) = %q, want %q", branch, got, want)
		}
	}
}

// The sibling convention is the whole point of the endpoint: a worktree inside
// the repository would show in git status and die to git clean -fdx.
func TestCreateWorktreeUsesSiblingDirectory(t *testing.T) {
	root := realPath(t, gitRepo(t))

	created, err := createWorktree(root, "feat/try-a-queue")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}

	want := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", "feat-try-a-queue")
	if created.Path != want {
		t.Errorf("path = %q, want %q", created.Path, want)
	}
	if created.Branch != "feat/try-a-queue" {
		t.Errorf("branch = %q, want the unsanitised name", created.Branch)
	}
	if !strings.HasPrefix(created.Path, filepath.Dir(root)) || strings.HasPrefix(created.Path, root+string(filepath.Separator)) {
		t.Errorf("worktree %q is inside the repository", created.Path)
	}
	if _, err := os.Stat(filepath.Join(created.Path, ".git")); err != nil {
		t.Errorf("worktree has no .git: %v", err)
	}
}

// An existing branch is checked out rather than re-cut: `git worktree add -b`
// against a branch that already exists is a hard failure.
func TestCreateWorktreeChecksOutAnExistingBranch(t *testing.T) {
	root := gitRepo(t)
	if _, err := gitCmd(root, "branch", "already-here"); err != nil {
		t.Fatalf("make branch: %v", err)
	}

	created, err := createWorktree(root, "already-here")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	if created.Branch != "already-here" {
		t.Errorf("branch = %q, want already-here", created.Branch)
	}
}

func TestCreateWorktreeRefusesABranchAlreadyCheckedOut(t *testing.T) {
	root := gitRepo(t)

	if _, err := createWorktree(root, "taken"); err != nil {
		t.Fatalf("first createWorktree: %v", err)
	}
	_, err := createWorktree(root, "taken")
	if err == nil {
		t.Fatal("second createWorktree succeeded, want a conflict")
	}
	if got := StatusOf(err); got != http.StatusConflict {
		t.Errorf("status = %d, want %d", got, http.StatusConflict)
	}
}

// A linked worktree must not become the parent of the next one, or every fork
// of a fork nests one directory deeper than the last.
func TestCreateWorktreeHangsOffTheMainWorktree(t *testing.T) {
	root := gitRepo(t)

	first, err := createWorktree(root, "first")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	second, err := createWorktree(first.Path, "second")
	if err != nil {
		t.Fatalf("createWorktree from a linked worktree: %v", err)
	}

	if filepath.Dir(second.Path) != filepath.Dir(first.Path) {
		t.Errorf("second worktree %q is not a sibling of %q", second.Path, first.Path)
	}
}

func TestCreateWorktreeRejectsBadInput(t *testing.T) {
	root := gitRepo(t)

	if _, err := createWorktree(root, "  "); StatusOf(err) != http.StatusBadRequest {
		t.Errorf("empty branch: status %d, want 400", StatusOf(err))
	}
	if _, err := createWorktree(root, "bad..name"); StatusOf(err) != http.StatusBadRequest {
		t.Errorf("invalid ref: status %d, want 400", StatusOf(err))
	}
	if _, err := createWorktree(t.TempDir(), "fine"); StatusOf(err) != http.StatusBadRequest {
		t.Errorf("not a repo: status %d, want 400", StatusOf(err))
	}
}

// A fork that is refused after its worktree was made must not leave the branch
// behind — but must never throw away work either.
func TestDiscardEmptyWorktreeRemovesOnlyAnUntouchedOne(t *testing.T) {
	root := realPath(t, gitRepo(t))

	created, err := createWorktree(root, "abandoned")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	discardEmptyWorktree(root, created.Path)

	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Errorf("worktree %s survived", created.Path)
	}
	if _, err := gitCmd(root, "rev-parse", "--verify", "--quiet", "refs/heads/abandoned"); err == nil {
		t.Error("branch abandoned survived")
	}
}

func TestDiscardEmptyWorktreeKeepsOneWithWorkInIt(t *testing.T) {
	root := realPath(t, gitRepo(t))

	created, err := createWorktree(root, "has-work")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	write(t, filepath.Join(created.Path, "notes.txt"), "something the agent wrote\n")

	discardEmptyWorktree(root, created.Path)

	if _, err := os.Stat(created.Path); err != nil {
		t.Errorf("a dirty worktree was thrown away: %v", err)
	}
}
