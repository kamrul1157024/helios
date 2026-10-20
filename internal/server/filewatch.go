package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// See docs/specs/54-file-change-events.md.
//
// The daemon tells viewers which file moved by watching metadata for the paths
// somebody asked for, rather than by watching the tree. A read registers a
// path; a sweep re-checks what is registered; anything whose *content* changed
// goes out on the stream. So the work is proportional to what people are
// looking at, not to what changed on disk — which is what keeps a build from
// spending every client's 64-slot buffer (sse.go:36) on events nobody wants.

const (
	// watchTTL is how long a path stays watched after its last read. Long
	// enough to cover a reader who leaves a tab open and goes to lunch; short
	// enough that a directory walked once is not stat'd forever.
	watchTTL = 10 * time.Minute
	// maxWatched bounds the set so a client walking a tree cannot grow it
	// without limit. The least recently read entry goes first.
	maxWatched = 512
	// sweepEvery is the fallback clock. Most sweeps come from a poke.
	sweepEvery = time.Second
	// pokeSettle collapses a burst of tool calls into one sweep.
	pokeSettle = 250 * time.Millisecond
)

// WatchKind is what a watched path is, which decides how it is digested.
type WatchKind string

const (
	// WatchFile is a single file. Its digest is a hash of its contents, gated
	// by mtime and size so the common case costs one stat.
	WatchFile WatchKind = "file"
	// WatchDir is a directory listing. Its digest is built from the same
	// entries handleListFiles returns, so it cannot disagree with the screen.
	WatchDir WatchKind = "dir"
	// WatchRepo is a git checkout. Its digest is HEAD and the ref HEAD names.
	WatchRepo WatchKind = "repo"
)

// FileChange is one path whose content moved, as it appears in the event.
type FileChange struct {
	Path string    `json:"path"`
	Kind WatchKind `json:"kind"`
	// ModTime is informational — for a listing row and for logs. No client
	// decides anything from it, because the two read routes disagree about its
	// precision (files.go:129 seconds, filesearch.go:226 nanoseconds).
	ModTime string `json:"mod_time,omitempty"`
	Gone    bool   `json:"gone,omitempty"`
}

type watchEntry struct {
	kind     WatchKind
	lastRead time.Time
	// mtime and size gate the hash for a file. A directory and a repo have no
	// gate: their digests are cheap enough to take every sweep.
	mtime  time.Time
	size   int64
	digest string
	gone   bool
	// dirMtime is a directory's own mtime at its last digest. While a live
	// index watcher covers the directory, an unchanged mtime means no entry
	// came or went, and a child's own change arrives as a notice instead.
	dirMtime time.Time
	// notices counts the raw events seen for this path or its children. A
	// sweep that started before a notice must not store an mtime gate that
	// the notice meant to clear.
	notices uint64
	// seen is false until the first look. It is what makes a new entry compute
	// and stay quiet, and it is kept apart from an empty digest on purpose: a
	// path that disappears clears its digest, and a file restored with the
	// bytes it went away with must still count as news.
	seen bool
}

// FileWatcher holds the paths clients have read and reports the ones whose
// content changes.
type FileWatcher struct {
	mu sync.Mutex
	// entries is keyed by path and kind together. One folder is often both
	// a listing and a repository — the Files panel lists a checkout's root and
	// asks its git status — and keyed by path alone the first subscription
	// would swallow the second, so the listing would never be digested.
	entries map[watchKey]*watchEntry
	sse     *SSEBroadcaster
	poke    chan struct{}
	nudge   chan struct{}
	// sweeping serialises sweeps. The digests are taken outside mu, so two
	// sweeps at once would both see the same change and both announce it.
	sweeping sync.Mutex
	// covered reports whether a live index watcher sees entries come and go
	// in a directory. Nil means nothing is covered, and every directory is
	// listed on every sweep.
	covered func(dir string) bool
	// now is swapped in tests so TTL and eviction can be driven without
	// sleeping.
	now func() time.Time
}

// files returns the watcher, or nil when the server has no shared state at all
// — the shape several handler tests construct, because these routes touch
// nothing else on it. Every method below tolerates a nil receiver, so callers
// need no guard of their own.
func (s *PublicServer) files() *FileWatcher {
	if s.shared == nil {
		return nil
	}
	return s.shared.Files
}

type watchKey struct {
	path string
	kind WatchKind
}

func NewFileWatcher(sse *SSEBroadcaster) *FileWatcher {
	return &FileWatcher{
		entries: make(map[watchKey]*watchEntry),
		sse:     sse,
		poke:    make(chan struct{}, 1),
		nudge:   make(chan struct{}, 1),
		now:     time.Now,
	}
}

// Watch registers a path, or refreshes one already registered.
//
// A path new to the set is digested here and announces nothing. Otherwise every
// read would report itself as a change.
//
// The first digest is taken outside the lock — it can hash a 10 MB file — so a
// read never waits behind a sweep, nor a sweep behind a read. If a sweep or
// another read registered the path meanwhile, that entry stands: it is at least
// as new as this one.
func (w *FileWatcher) Watch(path string, kind WatchKind) {
	if w == nil || path == "" {
		return
	}
	w.mu.Lock()
	key := watchKey{path: path, kind: kind}
	if entry, ok := w.entries[key]; ok {
		entry.lastRead = w.now()
		w.mu.Unlock()
		return
	}
	covered := w.covered
	w.mu.Unlock()

	entry := &watchEntry{kind: kind, lastRead: w.now()}
	w.refresh(path, entry, covered)

	w.mu.Lock()
	defer w.mu.Unlock()
	if existing, ok := w.entries[key]; ok {
		existing.lastRead = entry.lastRead
		return
	}
	w.entries[key] = entry
	w.evictLocked()
}

// Notice is the index watcher reporting a raw event at path. It clears the
// mtime gate of the folder holding path, because a child whose content moved
// changes that folder's listing without touching the folder's own mtime. It
// reports whether path or its folder is watched — whether a sweep has anything
// to look at.
func (w *FileWatcher) Notice(path string) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	watched := false
	for _, key := range []watchKey{{path, WatchFile}, {path, WatchDir}, {filepath.Dir(path), WatchDir}} {
		if entry, ok := w.entries[key]; ok {
			entry.notices++
			entry.dirMtime = time.Time{}
			watched = true
		}
	}
	return watched
}

// Nudge asks for a sweep now, with no settle. The index watcher has already
// debounced the burst, so waiting again would only add latency.
func (w *FileWatcher) Nudge() {
	if w == nil {
		return
	}
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// Poke asks for a sweep now rather than on the next tick. It never blocks: a
// sweep is already pending if the buffer is full, and that is the same answer.
func (w *FileWatcher) Poke() {
	if w == nil {
		return
	}
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// Run sweeps until the context ends. A poke sweeps after pokeSettle, so a run
// of tool calls costs one sweep rather than one each.
func (w *FileWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	var settle <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Sweep()
		case <-w.poke:
			// Restarting the timer on each poke is deliberate: the last call in
			// a burst is the one whose writes we want to have landed.
			settle = time.After(pokeSettle)
		case <-w.nudge:
			w.Sweep()
		case <-settle:
			settle = nil
			w.Sweep()
		}
	}
}

// Sweep re-digests the set and broadcasts one event naming everything that
// changed. Exported for tests, which drive it directly rather than by clock.
func (w *FileWatcher) Sweep() []FileChange {
	changes := w.collect()
	if len(changes) > 0 && w.sse != nil {
		w.sse.Broadcast(SSEEvent{
			Type: "file_changed",
			Data: map[string]interface{}{"paths": changes},
		})
	}
	return changes
}

// collect takes a copy of the set under the lock, does the stats and hashes
// with the lock released, and locks again only to write the results back. A
// read that arrives mid-sweep is served at once instead of waiting for every
// watched file to be looked at.
func (w *FileWatcher) collect() []FileChange {
	w.sweeping.Lock()
	defer w.sweeping.Unlock()

	type work struct {
		key   watchKey
		live  *watchEntry
		entry watchEntry
	}
	w.mu.Lock()
	cutoff := w.now().Add(-watchTTL)
	jobs := make([]work, 0, len(w.entries))
	for key, entry := range w.entries {
		if entry.lastRead.Before(cutoff) {
			delete(w.entries, key)
			continue
		}
		jobs = append(jobs, work{key: key, live: entry, entry: *entry})
	}
	covered := w.covered
	w.mu.Unlock()

	var changes []FileChange
	moved := make([]bool, len(jobs))
	found := make([]FileChange, len(jobs))
	for i := range jobs {
		moved[i], found[i] = w.refresh(jobs[i].key.path, &jobs[i].entry, covered)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	for i, job := range jobs {
		if w.entries[job.key] != job.live {
			// Evicted mid-sweep: nobody is listening for it any more.
			continue
		}
		job.live.storeDigest(&job.entry)
		if moved[i] {
			changes = append(changes, found[i])
		}
	}
	// Stable order so a test can assert one, and so a log reads the same twice.
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}

// storeDigest writes a sweep's result into the live entry, keeping what a read
// changed meanwhile. A notice that landed during the sweep wins over the mtime
// gate the sweep computed, so the next sweep lists the directory again.
func (e *watchEntry) storeDigest(from *watchEntry) {
	e.mtime, e.size, e.digest = from.mtime, from.size, from.digest
	e.gone, e.seen = from.gone, from.seen
	if e.notices == from.notices {
		e.dirMtime = from.dirMtime
	} else {
		e.dirMtime = time.Time{}
	}
}

// refresh brings one entry up to date and says whether its content moved. It
// touches nothing but the entry, so callers need not hold the lock for it.
func (w *FileWatcher) refresh(path string, entry *watchEntry, covered func(string) bool) (bool, FileChange) {
	switch entry.kind {
	case WatchDir:
		return w.refreshDir(path, entry, covered)
	case WatchRepo:
		return w.refreshDigest(path, entry, repoDigest)
	default:
		return w.refreshFile(path, entry)
	}
}

// refreshFile gates on metadata and decides on content.
//
// Changed metadata does not mean changed content: a formatter rewrites a file
// with identical output, codegen rewrites unconditionally, a checkout restores
// bytes that were already there. Announcing those would raise "changed on disk"
// over a dirty buffer that did not need it, which is the one false alarm that
// costs the user work.
func (w *FileWatcher) refreshFile(path string, entry *watchEntry) (bool, FileChange) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return w.markGone(path, entry, WatchFile)
	}
	wasGone := entry.gone
	if entry.seen && !wasGone && info.ModTime().Equal(entry.mtime) && info.Size() == entry.size {
		// The cheap answer, and the one taken on almost every sweep: no read,
		// no hash, nothing to say.
		return false, FileChange{}
	}

	sum, err := hashFile(path)
	if err != nil {
		return w.markGone(path, entry, WatchFile)
	}
	// A file that came back is news even with the bytes it went away with,
	// because the client dropped it when it heard "gone". Otherwise: metadata
	// moved and content did not, so keep the new mtime — which stops the same
	// file being hashed again on the next sweep — and say nothing.
	moved := entry.seen && (sum != entry.digest || wasGone)
	entry.seen, entry.gone = true, false
	entry.mtime, entry.size, entry.digest = info.ModTime(), info.Size(), sum
	return moved, changeOf(path, WatchFile, info)
}

// refreshDir skips the listing when the directory's own mtime has not moved
// and a live index watcher covers it. A create, remove or rename moves the
// mtime; a child's content change does not, but the watcher's notice clears
// the gate for that. Without a watcher there is no notice, so every sweep
// lists the directory, as it always has.
func (w *FileWatcher) refreshDir(path string, entry *watchEntry, covered func(string) bool) (bool, FileChange) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return w.markGone(path, entry, WatchDir)
	}
	if entry.seen && !entry.gone && !entry.dirMtime.IsZero() &&
		info.ModTime().Equal(entry.dirMtime) && covered != nil && covered(path) {
		return false, FileChange{}
	}
	moved, change := w.refreshDigest(path, entry, dirDigest)
	if !entry.gone {
		entry.dirMtime = info.ModTime()
	}
	return moved, change
}

// refreshDigest handles the kinds whose digest is cheap enough to take whole.
func (w *FileWatcher) refreshDigest(
	path string,
	entry *watchEntry,
	digest func(string) (string, error),
) (bool, FileChange) {
	sum, err := digest(path)
	if err != nil {
		return w.markGone(path, entry, entry.kind)
	}
	moved := entry.seen && (sum != entry.digest || entry.gone)
	entry.seen, entry.gone = true, false
	entry.digest = sum
	return moved, FileChange{Path: path, Kind: entry.kind}
}

// markGone reports a disappearance once, and stays quiet after it. A path
// registered while already missing announces nothing, and announces its arrival
// when it turns up.
func (w *FileWatcher) markGone(path string, entry *watchEntry, kind WatchKind) (bool, FileChange) {
	if entry.gone {
		return false, FileChange{}
	}
	moved := entry.seen
	entry.seen, entry.gone = true, true
	entry.digest = ""
	entry.mtime, entry.size = time.Time{}, 0
	entry.dirMtime = time.Time{}
	return moved, FileChange{Path: path, Kind: kind, Gone: true}
}

func changeOf(path string, kind WatchKind, info os.FileInfo) FileChange {
	return FileChange{
		Path:    path,
		Kind:    kind,
		ModTime: info.ModTime().UTC().Format(time.RFC3339Nano),
	}
}

// evictLocked drops the least recently read entry while the set is over its
// cap. Callers hold the lock.
func (w *FileWatcher) evictLocked() {
	for len(w.entries) > maxWatched {
		var oldest watchKey
		var at time.Time
		for key, entry := range w.entries {
			if oldest.path == "" || entry.lastRead.Before(at) {
				oldest, at = key, entry.lastRead
			}
		}
		delete(w.entries, oldest)
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	// Bounded by the same limit the read route enforces (files.go:110), so a
	// file nobody can open cannot be hashed either.
	if _, err := io.Copy(sum, io.LimitReader(f, maxFileSize+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// dirDigest hashes the listing handleListFiles would return.
//
// Built from the same entries as the answer, so the digest cannot disagree with
// what is on the screen. For a listing the metadata *is* the content: the client
// draws each entry's size and mod time, so an entry whose mtime moved is a row
// that changed.
func dirDigest(path string) (string, error) {
	entries, err := listDir(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\x00%s\x00%d\x00%t\n", e.Name, e.ModTime, e.Size, e.IsDir)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

// repoDigest reads HEAD and the ref it names.
//
// Deliberately not .git/index: `git status` refreshes the index as a side
// effect, so watching it would make the daemon's own answer change the thing it
// watches — a status read moves the index, the sweep sees it, clients refetch
// status, and the loop never settles. The cost is that a bare `git add` does
// not announce itself, which is a staged/unstaged split lagging by one real
// change.
func repoDigest(root string) (string, error) {
	dir, err := gitDir(root)
	if err != nil {
		return "", err
	}
	head, err := os.ReadFile(filepath.Join(dir, "HEAD"))
	if err != nil {
		return "", err
	}
	body := string(head)
	if ref, ok := strings.CutPrefix(strings.TrimSpace(body), "ref: "); ok {
		// A packed ref has no file, and a branch with no commits has neither.
		// Both are stable states, and HEAD alone already distinguishes them.
		if target, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref))); err == nil {
			body += string(target)
		}
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:]), nil
}

// gitDir finds the real git directory for a checkout. A linked worktree has a
// .git file pointing at one, which is how the worktree view (spec 35) leaves
// them on disk.
func gitDir(root string) (string, error) {
	path := filepath.Join(root, ".git")
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return path, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir: ")
	if !ok {
		return "", os.ErrNotExist
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	return filepath.Join(root, target), nil
}
