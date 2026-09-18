package server

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/store"
)

func TestDeriveBranchWalksPastNamesAlreadyTaken(t *testing.T) {
	root := gitRepo(t)

	first := deriveBranch(root, "Refactor the launch path")
	if first != "refactor-the-launch-path" {
		t.Fatalf("branch = %q, want refactor-the-launch-path", first)
	}

	if _, err := gitCmd(root, "branch", first); err != nil {
		t.Fatalf("make branch: %v", err)
	}
	second := deriveBranch(root, "Refactor the launch path")
	if second != "refactor-the-launch-path-2" {
		t.Errorf("branch = %q, want the -2 suffix", second)
	}
}

func TestDeriveBranchHandlesAnUnnamedSession(t *testing.T) {
	root := gitRepo(t)

	// A session that has not reported a title yet is the common case, not an
	// edge one: the fork sheet is often opened seconds after the parent starts.
	got := deriveBranch(root, "")
	if got != "fork" {
		t.Errorf("branch = %q, want fork", got)
	}
	if _, err := gitCmd(root, "check-ref-format", "--branch", got); err != nil {
		t.Errorf("derived %q, which git will not accept: %v", got, err)
	}
}

func TestDeriveBranchTruncatesALongTitle(t *testing.T) {
	root := gitRepo(t)

	got := deriveBranch(root, strings.Repeat("a very long session title ", 10))
	if len(got) > 40 {
		t.Errorf("branch %q is %d characters, want 40 or fewer", got, len(got))
	}
	if _, err := gitCmd(root, "check-ref-format", "--branch", got); err != nil {
		t.Errorf("derived %q, which git will not accept: %v", got, err)
	}
}

func TestForkWorkspaceMakesAWorktreeByDefault(t *testing.T) {
	root := realPath(t, gitRepo(t))
	// gitRepo leaves an untracked file behind, and an untracked file is
	// uncommitted work the fork would be warned about. This case is about the
	// worktree, so start from a clean tree.
	if _, err := gitCmd(root, "clean", "-fd"); err != nil {
		t.Fatalf("clean: %v", err)
	}
	parent := &store.Session{SessionID: "p", CWD: root, Title: strPtr("queue variant")}

	cwd, workspace, warnings := forkWorkspaceFor(parent, forkWorkspaceWorktree, "")
	if workspace != forkWorkspaceWorktree {
		t.Fatalf("workspace = %q, want worktree", workspace)
	}
	if cwd == root {
		t.Error("the fork was given the parent's own directory")
	}
	if filepath.Dir(filepath.Dir(cwd)) != filepath.Dir(root) {
		t.Errorf("worktree %q is not in a sibling of the repository", cwd)
	}
	if len(warnings) != 0 {
		t.Errorf("clean repository warned: %v", warnings)
	}
}

// A session in /tmp is still worth forking. Refusing would be worse than
// sharing a folder and saying so.
func TestForkWorkspaceFallsBackOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	parent := &store.Session{SessionID: "p", CWD: dir}

	cwd, workspace, warnings := forkWorkspaceFor(parent, forkWorkspaceWorktree, "")
	if workspace != forkWorkspaceSame {
		t.Errorf("workspace = %q, want same", workspace)
	}
	if cwd != dir {
		t.Errorf("cwd = %q, want the parent's %q", cwd, dir)
	}
	if len(warnings) == 0 {
		t.Error("fell back silently; the user has no way to know")
	}
}

// The fork starts at the last commit, so uncommitted work is the one thing the
// user must be told before typing at the agent.
func TestForkWorkspaceWarnsAboutUncommittedWork(t *testing.T) {
	root := realPath(t, gitRepo(t))
	write(t, filepath.Join(root, "dirty.txt"), "uncommitted\n")
	parent := &store.Session{SessionID: "p", CWD: root}

	_, workspace, warnings := forkWorkspaceFor(parent, forkWorkspaceWorktree, "")
	if workspace != forkWorkspaceWorktree {
		t.Fatalf("workspace = %q, want worktree", workspace)
	}
	if len(warnings) == 0 {
		t.Fatal("dirty parent produced no warning")
	}
	if !strings.Contains(warnings[0], "uncommitted") {
		t.Errorf("warning %q does not mention uncommitted work", warnings[0])
	}
}

func TestForkWorkspaceSameLeavesTheParentsFolder(t *testing.T) {
	root := gitRepo(t)
	parent := &store.Session{SessionID: "p", CWD: root}

	cwd, workspace, warnings := forkWorkspaceFor(parent, forkWorkspaceSame, "")
	if cwd != root || workspace != forkWorkspaceSame {
		t.Errorf("got %q/%q, want the parent's folder", cwd, workspace)
	}
	if len(warnings) != 0 {
		t.Errorf("asking for the parent's folder warned: %v", warnings)
	}
}

func strPtr(s string) *string { return &s }
