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

// A fork left unnamed falls back to its project, so the row reads "helios",
// sits under a parent called something else, and looks unrelated. The
// automatic titler cannot rescue it: it skips a session that already has a
// title, and answers SKIP for one that has said nothing of its own.
func TestForkTitleNamesTheParentAndTheBranch(t *testing.T) {
	parent := &store.Session{
		SessionID: "p",
		Project:   "helios",
		Title:     strPtr("[FEAT] Session forking"),
	}

	got := forkTitle(parent, "/x/helios-worktrees/try-a-queue", forkWorkspaceWorktree)
	if want := "[FEAT] Session forking ⑂ try-a-queue"; got != want {
		t.Errorf("forkTitle = %q, want %q", got, want)
	}

	// No branch to name when the fork shares the parent's folder.
	got = forkTitle(parent, "/x/helios", forkWorkspaceSame)
	if want := "[FEAT] Session forking ⑂"; got != want {
		t.Errorf("forkTitle = %q, want %q", got, want)
	}
}

// An untitled parent still gives the fork something better than the bare
// project name it would otherwise inherit from nowhere.
func TestForkTitleFallsBackToTheProject(t *testing.T) {
	parent := &store.Session{SessionID: "p", Project: "helios"}

	got := forkTitle(parent, "/x/helios-worktrees/try", forkWorkspaceWorktree)
	if want := "helios ⑂ try"; got != want {
		t.Errorf("forkTitle = %q, want %q", got, want)
	}
}

// A parent named at length must not push the branch off the end of the row.
func TestForkTitleTrimsALongParentName(t *testing.T) {
	parent := &store.Session{
		SessionID: "p",
		Project:   "helios",
		Title:     strPtr(strings.Repeat("a very long session title ", 6)),
	}

	got := forkTitle(parent, "/x/helios-worktrees/try", forkWorkspaceWorktree)
	if !strings.HasSuffix(got, "⑂ try") {
		t.Errorf("forkTitle = %q, want it to still end with the branch", got)
	}
	if len([]rune(got)) > 64 {
		t.Errorf("forkTitle is %d runes: %q", len([]rune(got)), got)
	}
}
