package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// touch rewrites a file with the given content and forces its mtime forward.
//
// Forced rather than left to the clock: a test writes twice inside one
// filesystem tick, and the gate would then see no metadata movement and skip
// the hash — which is the real behaviour, but not the one under test here.
func touch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func newWatcher(t *testing.T) *FileWatcher {
	t.Helper()
	return NewFileWatcher(nil)
}

func paths(changes []FileChange) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Path)
	}
	return out
}

func TestWatchDigestsWithoutAnnouncing(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	w.Watch(file, WatchFile)

	if got := w.Sweep(); len(got) != 0 {
		t.Fatalf("registering announced %v, want nothing", paths(got))
	}
}

func TestSweepNamesAChangedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	w.Watch(file, WatchFile)
	touch(t, file, "two")

	got := w.Sweep()
	if len(got) != 1 || got[0].Path != file {
		t.Fatalf("got %v, want just %s", paths(got), file)
	}
	if got[0].Kind != WatchFile {
		t.Errorf("kind = %q, want %q", got[0].Kind, WatchFile)
	}
	if got[0].ModTime == "" {
		t.Error("mod_time is empty")
	}
	if len(w.Sweep()) != 0 {
		t.Error("the same change was announced twice")
	}
}

// The point of the whole two-stage design: a formatter that rewrites a file
// with the bytes already in it must not raise "changed on disk" over a buffer
// somebody is typing into.
func TestIdenticalRewriteIsSilent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "same")

	w := newWatcher(t)
	w.Watch(file, WatchFile)
	touch(t, file, "same")

	if got := w.Sweep(); len(got) != 0 {
		t.Fatalf("identical rewrite announced %v, want nothing", paths(got))
	}
}

// Having decided the content is unchanged, the sweep must remember the new
// mtime — otherwise the same file is read and hashed on every tick forever.
func TestIdenticalRewriteStoresTheNewMtime(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "same")

	w := newWatcher(t)
	w.Watch(file, WatchFile)
	touch(t, file, "same")
	w.Sweep()

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	stored := w.entries[file].mtime
	w.mu.Unlock()
	if !stored.Equal(info.ModTime()) {
		t.Errorf("stored mtime %v, want %v", stored, info.ModTime())
	}
}

func TestUnchangedFileIsNeverRead(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	w.Watch(file, WatchFile)

	// Made unreadable after registering: a sweep that opens it would fail and
	// report the file gone, so silence proves the gate skipped the read.
	if err := os.Chmod(file, 0o000); err != nil {
		t.Skipf("cannot drop read permission here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o644) })
	if os.Geteuid() == 0 {
		t.Skip("running as root, where a mode of 000 is still readable")
	}

	if got := w.Sweep(); len(got) != 0 {
		t.Fatalf("sweep read a file whose metadata had not moved: %v", paths(got))
	}
}

// Size alone is not the gate, and neither is it the digest.
func TestSameLengthChangeIsNamed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "aaa")

	w := newWatcher(t)
	w.Watch(file, WatchFile)
	touch(t, file, "bbb")

	if got := w.Sweep(); len(got) != 1 {
		t.Fatalf("got %v, want the file named", paths(got))
	}
}

func TestDeletedFileIsNamedOnce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	w.Watch(file, WatchFile)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	got := w.Sweep()
	if len(got) != 1 || !got[0].Gone {
		t.Fatalf("got %+v, want one gone entry", got)
	}
	if len(w.Sweep()) != 0 {
		t.Error("a file that stayed deleted was announced twice")
	}
}

func TestRestoredFileIsNamedAgain(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	w.Watch(file, WatchFile)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	w.Sweep()
	// Put back with exactly the bytes it had: the client dropped it when it
	// heard "gone", so this is still news even though the content matches.
	touch(t, file, "one")

	got := w.Sweep()
	if len(got) != 1 || got[0].Gone {
		t.Fatalf("got %+v, want the file named as present", got)
	}
}

func TestDirectoryNamedOnEveryStructuralChange(t *testing.T) {
	cases := []struct {
		name string
		act  func(t *testing.T, dir string)
	}{
		{"added", func(t *testing.T, dir string) { touch(t, filepath.Join(dir, "new.txt"), "x") }},
		{"removed", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"renamed", func(t *testing.T, dir string) {
			if err := os.Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		// The listing carries each entry's size and mod time, so a child's own
		// content moving is a row that changed.
		{"child content", func(t *testing.T, dir string) { touch(t, filepath.Join(dir, "a.txt"), "much longer") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			touch(t, filepath.Join(dir, "a.txt"), "one")

			w := newWatcher(t)
			w.Watch(dir, WatchDir)
			tc.act(t, dir)

			got := w.Sweep()
			if len(got) != 1 || got[0].Path != dir || got[0].Kind != WatchDir {
				t.Fatalf("got %+v, want the directory named", got)
			}
		})
	}
}

func TestDirectoryUnchangedIsSilent(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a.txt"), "one")

	w := newWatcher(t)
	w.Watch(dir, WatchDir)

	if got := w.Sweep(); len(got) != 0 {
		t.Fatalf("got %v, want nothing", paths(got))
	}
}

func TestEntryLeavesTheSetAfterTheTTL(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	now := time.Now()
	w.now = func() time.Time { return now }
	w.Watch(file, WatchFile)

	now = now.Add(watchTTL + time.Minute)
	touch(t, file, "two")
	if got := w.Sweep(); len(got) != 0 {
		t.Fatalf("an expired entry still reported %v", paths(got))
	}
	w.mu.Lock()
	remaining := len(w.entries)
	w.mu.Unlock()
	if remaining != 0 {
		t.Errorf("%d entries left, want 0", remaining)
	}
}

func TestReadingAgainExtendsTheTTL(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	touch(t, file, "one")

	w := newWatcher(t)
	now := time.Now()
	w.now = func() time.Time { return now }
	w.Watch(file, WatchFile)

	now = now.Add(watchTTL - time.Minute)
	w.Watch(file, WatchFile)
	now = now.Add(2 * time.Minute)
	touch(t, file, "two")

	if got := w.Sweep(); len(got) != 1 {
		t.Fatalf("got %v, want the file still watched", paths(got))
	}
}

func TestSetIsCappedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	w := newWatcher(t)
	now := time.Now()
	w.now = func() time.Time { return now }

	first := filepath.Join(dir, "first.txt")
	touch(t, first, "x")
	w.Watch(first, WatchFile)

	for i := 0; i < maxWatched; i++ {
		now = now.Add(time.Second)
		path := filepath.Join(dir, "f"+itoa(i)+".txt")
		touch(t, path, "x")
		w.Watch(path, WatchFile)
	}

	w.mu.Lock()
	_, stillThere := w.entries[first]
	size := len(w.entries)
	w.mu.Unlock()
	if stillThere {
		t.Error("the least recently read entry survived the cap")
	}
	if size > maxWatched {
		t.Errorf("%d entries, want at most %d", size, maxWatched)
	}
}

func TestOneSweepIsOneEvent(t *testing.T) {
	dir := t.TempDir()
	sse := NewSSEBroadcaster()
	w := NewFileWatcher(sse)

	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	touch(t, a, "one")
	touch(t, b, "one")
	w.Watch(a, WatchFile)
	w.Watch(b, WatchFile)
	touch(t, a, "two")
	touch(t, b, "two")

	got := w.Sweep()
	if len(got) != 2 {
		t.Fatalf("got %v, want both files in one answer", paths(got))
	}
	if got[0].Path > got[1].Path {
		t.Error("changes are not in a stable order")
	}
}

func TestRepoNamedOnCommitAndSilentOnStatus(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	touch(t, filepath.Join(dir, "a.txt"), "one")
	git("add", ".")
	git("commit", "-qm", "first")

	w := newWatcher(t)
	w.Watch(dir, WatchRepo)

	// A status read refreshes .git/index. Watching the index would make this
	// look like a change, and the refetch it triggers would refresh it again.
	git("status", "--porcelain")
	if got := w.Sweep(); len(got) != 0 {
		t.Fatalf("git status announced %v — the index loop is back", paths(got))
	}

	touch(t, filepath.Join(dir, "b.txt"), "two")
	git("add", ".")
	git("commit", "-qm", "second")

	got := w.Sweep()
	if len(got) != 1 || got[0].Path != dir || got[0].Kind != WatchRepo {
		t.Fatalf("got %+v, want the repo named after a commit", got)
	}
}

func TestNilWatcherTolerates(t *testing.T) {
	var w *FileWatcher
	w.Watch("/tmp/whatever", WatchFile)
	w.Poke()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
