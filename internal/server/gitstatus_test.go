package server

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// legacyGitStatus is the route as it was before spec 65: six git processes.
// The single porcelain v2 call must give the same answer.
func legacyGitStatus(t *testing.T, root string) *gitStatus {
	t.Helper()
	branch, err := gitCmd(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	ahead, behind := 0, 0
	if ab, err := gitCmd(root, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		if parts := strings.Fields(ab); len(parts) == 2 {
			ahead, _ = strconv.Atoi(parts[0])
			behind, _ = strconv.Atoi(parts[1])
		}
	}
	nameStatus := func(extra ...string) []gitChange {
		out, err := gitCmd(root, append([]string{"diff", "--name-status"}, extra...)...)
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		changes := []gitChange{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			status := parts[0]
			if strings.HasPrefix(status, "R") {
				status = "R"
			}
			changes = append(changes, gitChange{Path: parts[len(parts)-1], Status: status})
		}
		return changes
	}
	untracked := []gitChange{}
	if out, err := gitCmd(root, "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				untracked = append(untracked, gitChange{Path: line, Status: "?"})
			}
		}
	}
	s := &gitStatus{
		Root: root, Branch: strings.TrimSpace(branch), Ahead: ahead, Behind: behind,
		Staged: nameStatus("--cached"), Unstaged: nameStatus(), Untracked: untracked,
	}
	s.Dirty = len(s.Staged) > 0 || len(s.Unstaged) > 0 || len(s.Untracked) > 0
	return s
}

func TestGitStatusMatchesTheSixProcessRoute(t *testing.T) {
	root := indexTree(t, true)
	write(t, filepath.Join(root, "staged-only.txt"), "s")
	write(t, filepath.Join(root, "to-delete.txt"), "d")
	write(t, filepath.Join(root, "to-unstage-delete.txt"), "d")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "second")

	// Ahead and behind need an upstream: a branch that tracks main, with a
	// commit on each side.
	gitT(t, root, "checkout", "-qb", "feature")
	gitT(t, root, "branch", "-q", "--set-upstream-to=main")
	write(t, filepath.Join(root, "feature.txt"), "f")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feature work")
	gitT(t, root, "checkout", "-q", "main")
	write(t, filepath.Join(root, "main.txt"), "m")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "main work")
	gitT(t, root, "checkout", "-q", "feature")

	compare := func(stage string) {
		t.Helper()
		got, err := readGitStatus(root)
		if err != nil {
			t.Fatal(err)
		}
		want := legacyGitStatus(t, root)
		if !reflect.DeepEqual(got, want) {
			g, _ := json.MarshalIndent(got, "", " ")
			w, _ := json.MarshalIndent(want, "", " ")
			t.Fatalf("%s:\n got %s\nwant %s", stage, g, w)
		}
	}
	compare("clean, ahead 1 behind 1")

	write(t, filepath.Join(root, "staged-new.txt"), "n")               // staged A
	write(t, filepath.Join(root, "README.md"), "# changed")            // staged M, then unstaged M
	write(t, filepath.Join(root, "src", "main.go"), "package main //") // unstaged M
	write(t, filepath.Join(root, "untracked", "deep", "u.txt"), "u")   // untracked, nested
	write(t, filepath.Join(root, "debug.log"), "ignored")              // ignored: in no list
	gitT(t, root, "add", "staged-new.txt", "README.md")
	write(t, filepath.Join(root, "README.md"), "# changed again")
	gitT(t, root, "mv", "docs/a.md", "docs/b.md") // staged R
	gitT(t, root, "rm", "-q", "to-delete.txt")    // staged D
	mustRemove(t, filepath.Join(root, "to-unstage-delete.txt"))
	compare("every kind of change")

	if got, _ := readGitStatus(root); got.Ahead != 1 || got.Behind != 1 || got.Branch != "feature" {
		t.Fatalf("branch %q ahead %d behind %d", got.Branch, got.Ahead, got.Behind)
	}

	gitT(t, root, "checkout", "-q", "--detach")
	compare("detached")
}

func TestGitStatusCacheFollowsTheGeneration(t *testing.T) {
	if !liveWatchSupported {
		t.Skip("the cache is only trusted on a live root")
	}
	root := indexTree(t, true)
	cache := &gitStatusCache{entries: map[string]cachedStatus{}}
	fileIndexes.ensure(root)
	deadline := time.Now().Add(3 * time.Second)
	for _, ok := fileIndexes.generation(root); !ok; _, ok = fileIndexes.generation(root) {
		if time.Now().After(deadline) {
			t.Fatal("root never went live")
		}
		time.Sleep(10 * time.Millisecond)
	}

	first, err := cache.get(root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.get(root)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatal("an unchanged generation ran git again")
	}

	// Writing through the route bumps the generation before any event lands.
	putFile(t, filepath.Join(root, "README.md"), "# edited", "")
	second, err := cache.get(root)
	if err != nil {
		t.Fatal(err)
	}
	if second == first || !second.Dirty || len(second.Unstaged) != 1 {
		t.Fatalf("status after a write: %+v", second)
	}
}

func TestParsePorcelainV2KeepsSpacesAndConflicts(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid abc",
		"# branch.head (detached)",
		"1 .M N... 100644 100644 100644 aaa bbb dir/with space.txt",
		"2 R. N... 100644 100644 100644 aaa bbb R100 new name.txt",
		"old name.txt",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc both.txt",
		"? new file.txt",
		"",
	}, "\x00")
	got := parsePorcelainV2(out)
	want := &gitStatus{
		Branch:    "HEAD",
		Dirty:     true,
		Staged:    []gitChange{{Path: "new name.txt", Status: "R"}, {Path: "both.txt", Status: "U"}},
		Unstaged:  []gitChange{{Path: "dir/with space.txt", Status: "M"}, {Path: "both.txt", Status: "U"}},
		Untracked: []gitChange{{Path: "new file.txt", Status: "?"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
