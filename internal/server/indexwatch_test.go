package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// liveCache is a cache that watches, with room for any test tree.
func liveCache(t *testing.T) *indexCache {
	t.Helper()
	if !liveWatchSupported {
		t.Skip("no directory watcher on this platform")
	}
	c := newIndexCacheWith(true, 1<<20)
	t.Cleanup(func() { closeCache(c) })
	return c
}

func closeCache(c *indexCache) {
	c.mu.Lock()
	var watchers []*rootWatcher
	for _, r := range c.roots {
		if r.live != nil {
			watchers = append(watchers, r.live)
			r.live = nil
		}
	}
	c.mu.Unlock()
	for _, w := range watchers {
		w.close()
	}
}

// gitT runs git in dir for a test, failing it on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// indexTree is the tree every patch test starts from.
func indexTree(t *testing.T, git bool) string {
	t.Helper()
	root := realPath(t, t.TempDir())
	write(t, filepath.Join(root, "README.md"), "# r")
	write(t, filepath.Join(root, "src", "main.go"), "package main")
	write(t, filepath.Join(root, "src", "util", "strings.go"), "package util")
	write(t, filepath.Join(root, "docs", "a.md"), "a")
	if git {
		write(t, filepath.Join(root, ".gitignore"), "*.log\nnode_modules/\n")
		gitT(t, root, "init", "-q", "--initial-branch=main")
		gitT(t, root, "add", ".")
		gitT(t, root, "commit", "-qm", "first")
	}
	return root
}

func indexRels(idx *fileIndex) []string {
	out := make([]string, len(idx.entries))
	for i, e := range idx.entries {
		out[i] = e.rel
	}
	return out
}

func freshRels(t *testing.T, root string) []string {
	t.Helper()
	scan := scanRoot(context.Background(), root, nil)
	return indexRels(&fileIndex{entries: scan.entries})
}

func waitLive(t *testing.T, c *indexCache, root string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if idx := c.get(context.Background(), root); idx.watch == watchLive && !idx.indexing {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("root never went live")
}

// waitFresh waits for the cached list to equal a fresh scan of the disk.
func waitFresh(t *testing.T, c *indexCache, root string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got, want []string
	for time.Now().Before(deadline) {
		got, want = indexRels(c.get(context.Background(), root)), freshRels(t, root)
		if reflect.DeepEqual(got, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("patched list never matched a fresh build:\n got %v\nwant %v", got, want)
}

func builds(c *indexCache, root string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.roots[root].builds
}

func TestIndexPatchMatchesAFreshBuild(t *testing.T) {
	type op struct {
		name string
		git  bool // only meaningful on a git root
		act  func(t *testing.T, root string)
	}
	ops := []op{
		{"create", false, func(t *testing.T, root string) { write(t, filepath.Join(root, "src", "new.go"), "x") }},
		{"remove", false, func(t *testing.T, root string) { mustRemove(t, filepath.Join(root, "docs", "a.md")) }},
		{"rename", false, func(t *testing.T, root string) {
			mustRename(t, filepath.Join(root, "src", "main.go"), filepath.Join(root, "src", "app.go"))
		}},
		{"new directory", false, func(t *testing.T, root string) {
			write(t, filepath.Join(root, "pkg", "deep", "er", "x.go"), "x")
			write(t, filepath.Join(root, "pkg", "y.go"), "y")
		}},
		{"rename directory", false, func(t *testing.T, root string) {
			mustRename(t, filepath.Join(root, "src", "util"), filepath.Join(root, "src", "helpers"))
		}},
		{"remove directory", false, func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "src")); err != nil {
				t.Fatal(err)
			}
		}},
		{"gitignored", true, func(t *testing.T, root string) {
			write(t, filepath.Join(root, "src", "debug.log"), "x")
			write(t, filepath.Join(root, "node_modules", "dep", "index.js"), "x")
			write(t, filepath.Join(root, "src", "kept.go"), "x")
		}},
	}
	for _, git := range []bool{true, false} {
		for _, o := range ops {
			if o.git && !git {
				continue
			}
			t.Run(fmt.Sprintf("git=%v/%s", git, o.name), func(t *testing.T) {
				c := liveCache(t)
				root := indexTree(t, git)
				waitLive(t, c, root)
				o.act(t, root)
				waitFresh(t, c, root)
				if n := builds(c, root); n != 1 {
					t.Errorf("rebuilt %d times; a patch should have done", n-1)
				}
			})
		}
	}
}

// A checkout touches hundreds of folders at once. Patching each is slower than
// one rebuild, and the answer must be the same either way.
func TestIndexBulkChangeRebuilds(t *testing.T) {
	c := liveCache(t)
	root := realPath(t, t.TempDir())
	for i := 0; i < bulkDirtyDirs+40; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("d%03d", i), "a.txt"), "a")
	}
	waitLive(t, c, root)

	for i := 0; i < bulkDirtyDirs+40; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("d%03d", i), "b.txt"), "b")
	}
	waitFresh(t, c, root)
	if n := builds(c, root); n < 2 {
		t.Errorf("%d dirty folders were patched; expected one rebuild", bulkDirtyDirs+40)
	}
}

// A change to git's index is a rebuild: a commit, a checkout or an add can
// change what git lists without any working-tree folder changing.
func TestIndexRebuildsWhenGitIndexMoves(t *testing.T) {
	c := liveCache(t)
	root := indexTree(t, true)
	waitLive(t, c, root)

	gitT(t, root, "rm", "-q", "--cached", "docs/a.md")
	write(t, filepath.Join(root, ".gitignore"), "*.log\nnode_modules/\ndocs/\n")
	waitFresh(t, c, root)
	if n := builds(c, root); n < 2 {
		t.Error("git rm did not rebuild")
	}
}

// Out of watches, the root polls, and still finds a file an agent wrote once
// the FilesTouched hook pokes it.
func TestIndexFallsBackToPollingOverBudget(t *testing.T) {
	if !liveWatchSupported {
		t.Skip("no directory watcher on this platform")
	}
	c := newIndexCacheWith(true, 10)
	t.Cleanup(func() { closeCache(c) })
	root := realPath(t, t.TempDir())
	for i := 0; i < 50; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("d%02d", i), "a.txt"), "a")
	}

	waitFresh(t, c, root)
	if idx := c.get(context.Background(), root); idx.watch != watchPolling {
		t.Fatalf("watch = %q over budget, want polling", idx.watch)
	}
	if c.budget.used != 0 {
		t.Fatalf("a refused root still holds %d watches", c.budget.used)
	}

	write(t, filepath.Join(root, "d07", "new.txt"), "n")
	c.poke()
	waitFresh(t, c, root)
}

// A polling git root rebuilds when the index moves, with no hook at all.
func TestPollingRootRebuildsOnGitIndex(t *testing.T) {
	c := newIndexCacheWith(false, 0)
	root := indexTree(t, true)
	waitFresh(t, c, root)

	write(t, filepath.Join(root, "late.go"), "x")
	gitT(t, root, "add", "late.go")
	waitFresh(t, c, root)
}

// A cold root answers by coldWait with what the build has so far.
func TestColdIndexAnswersWithPartialResults(t *testing.T) {
	c := newIndexCacheWith(false, 0)
	root := "/cold/root"
	partial := newIndexEntries([]string{"a.go", "b.go"})
	c.mu.Lock()
	c.roots[root] = &indexRoot{
		path:     root,
		building: true,
		done:     make(chan struct{}),
		idx:      &fileIndex{entries: partial, indexing: true, watch: watchPolling},
	}
	c.recent = append(c.recent, root)
	c.mu.Unlock()

	start := time.Now()
	idx := c.get(context.Background(), root)
	if elapsed := time.Since(start); elapsed > coldWait+100*time.Millisecond {
		t.Fatalf("cold query waited %v", elapsed)
	}
	if !idx.indexing || len(idx.entries) != 2 {
		t.Fatalf("got indexing=%v with %d entries", idx.indexing, len(idx.entries))
	}
}

func TestEvictionClosesTheWatcher(t *testing.T) {
	c := liveCache(t)
	roots := make([]string, maxIndexedRoots+1)
	for i := range roots {
		roots[i] = indexTree(t, false)
		waitLive(t, c, roots[i])
	}

	c.mu.Lock()
	_, held := c.roots[roots[0]]
	c.mu.Unlock()
	if held {
		t.Fatal("the least recently used root was not evicted")
	}
	// Each tree has four folders: the root, src, src/util and docs.
	want := maxIndexedRoots * 4
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.budget.mu.Lock()
		used := c.budget.used
		c.budget.mu.Unlock()
		if used == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("budget holds %d watches, want %d after eviction", c.budget.used, want)
}

func TestIgnoredEntriesInAListing(t *testing.T) {
	c := newIndexCacheWith(false, 0)
	root := indexTree(t, true)
	write(t, filepath.Join(root, "debug.log"), "x")
	write(t, filepath.Join(root, "node_modules", "x.js"), "x")
	waitFresh(t, c, root)

	ignored := c.ignoredIn(root)
	if ignored == nil {
		t.Fatal("a warm git root gave no answer")
	}
	cases := map[string]bool{"debug.log": true, "README.md": false, "node_modules": true, "src": false}
	for name, want := range cases {
		if got := ignored(name, !strings.Contains(name, ".")); got != want {
			t.Errorf("%s ignored = %v, want %v", name, got, want)
		}
	}
	if c.ignoredIn(t.TempDir()) != nil {
		t.Error("a folder outside every index got an answer")
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

// Deleting the root and making it again — a fixture rebuilt, a branch cloned
// fresh into the same folder — kills every watch with the old inodes. The
// root must come back live on the new tree, not report live and see nothing.
func TestIndexSurvivesTheRootBeingReplaced(t *testing.T) {
	c := liveCache(t)
	root := indexTree(t, true)
	waitLive(t, c, root)

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src", "main.go"), "package main")
	gitT(t, root, "init", "-q", "--initial-branch=main")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "again")
	waitFresh(t, c, root)
	waitLive(t, c, root)

	// From here a new file must arrive by patch, through watches on the new
	// tree: no rebuild may be what finds it.
	settled := builds(c, root)
	write(t, filepath.Join(root, "src", "later.go"), "package main")
	waitFresh(t, c, root)
	if n := builds(c, root); n != settled {
		t.Fatalf("the new file needed %d rebuilds; the new tree has no watches", n-settled)
	}
}

// makeProbeRepo builds a repository the way the e2e probe does: files first,
// then git init and a commit, with a little work after.
func makeProbeRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "main.go"), "package main")
	write(t, filepath.Join(root, "internal", "server", "files.go"), "package server")
	write(t, filepath.Join(root, ".gitignore"), "node_modules/\n")
	write(t, filepath.Join(root, "node_modules", "dep", "index.js"), "x")
	gitT(t, root, "init", "-q", "--initial-branch=main")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-qm", "init")
	write(t, filepath.Join(root, "docs", "notes.txt"), "n")
}

// expectWatchesCover checks the invariant a live root promises: a real kernel
// watch on every directory it believes it watches.
func expectWatchesCover(t *testing.T, c *indexCache, root string) {
	t.Helper()
	expectWatchesCoverDirs(t, c, root, "", "internal", "internal/server", "docs")
}

func expectWatchesCoverDirs(t *testing.T, c *indexCache, root string, want ...string) {
	t.Helper()
	c.mu.Lock()
	w := c.roots[root].live
	c.mu.Unlock()
	if w == nil {
		t.Fatal("root is not live")
	}
	kernel := map[string]bool{}
	for _, p := range w.w.WatchList() {
		kernel[p] = true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for dir := range w.watched {
		if abs := filepath.Join(root, filepath.FromSlash(dir)); !kernel[abs] {
			t.Errorf("%s is in the watched set with no kernel watch", abs)
		}
	}
	for _, dir := range want {
		if !w.watched[dir] {
			t.Errorf("%q is not watched", dir)
		}
	}
}

// Recreating the root again and again — the second time while the rebuild
// from the first is still running — must leave a live root that really
// watches its tree.
func TestIndexSurvivesRepeatedRootRecreation(t *testing.T) {
	c := liveCache(t)
	root := realPath(t, t.TempDir())
	makeProbeRepo(t, root)
	waitLive(t, c, root)

	for cycle := 0; cycle < 4; cycle++ {
		makeProbeRepo(t, root)
		c.get(context.Background(), root)
		time.Sleep(time.Duration(cycle*40) * time.Millisecond)
	}
	waitFresh(t, c, root)
	waitLive(t, c, root)

	write(t, filepath.Join(root, "internal", "server", "later.go"), "package server")
	waitFresh(t, c, root)
	expectWatchesCover(t, c, root)
}

// The root's own delete event can be lost in the burst that makes the new
// tree. A query still notices the folder is not the one being watched, and the
// root goes back to live on the new folder.
func TestQueryNoticesAReplacedRootWithoutItsEvent(t *testing.T) {
	c := liveCache(t)
	root := indexTree(t, false)
	waitLive(t, c, root)

	// Stand in for the lost event: make the watcher believe it watches some
	// other directory, which is what a replaced root looks like to a stat.
	other, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.roots[root].live.rootInfo = other
	c.mu.Unlock()

	if idx := c.get(context.Background(), root); idx.watch != watchPolling {
		t.Fatalf("a replaced root still reports %q", idx.watch)
	}
	waitLive(t, c, root)
	write(t, filepath.Join(root, "src", "util", "later.go"), "x")
	waitFresh(t, c, root)
	expectWatchesCoverDirs(t, c, root, "", "src", "src/util", "docs")
}
