package server

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// See docs/specs/58-files-without-a-root.md and docs/specs/65-fast-files.md.
//
// One candidate list per root, kept fresh by an fsnotify watcher that patches
// it in place (indexwatch.go). Where the watcher cannot run — another platform,
// an exhausted watch budget, a setting — the root polls instead: it rebuilds
// when git's index or HEAD moves, when an agent hook says files were touched,
// and on a long TTL as the floor.

const (
	// indexTTL is the safety net, not the freshness mechanism. A live root is
	// patched as the disk changes; a polling root rebuilds on git and hook
	// signals. Both still rebuild this often, in case something slipped past.
	indexTTL = 5 * time.Minute
	// maxIndexedRoots bounds the memory a daemon holds for search. A root of
	// 100k paths is a few megabytes; an unbounded map of every directory
	// anybody ever typed is not.
	maxIndexedRoots = 8
	// coldWait is how long a query on a root with no list waits for the build
	// before answering with what the build has found so far.
	coldWait = 150 * time.Millisecond
	// publishEvery spaces the partial lists a cold build hands out, so the
	// answer at coldWait is never more than this stale.
	publishEvery = 40 * time.Millisecond
)

const (
	watchLive    = "live"
	watchPolling = "polling"
)

// indexEntry is one candidate path with everything the scorer would otherwise
// recompute on every keystroke.
type indexEntry struct {
	rel string
	// lower is rel lowercased with the same byte layout, so an offset found in
	// one is the same character in the other. See newIndexEntry.
	lower string
	// mask has one bit per distinct character in lower. A query needing a
	// character the path does not have is rejected by a single AND, which is
	// most of the tree while somebody is still typing.
	mask uint64
	// nameStart is the offset just past the last separator: matches in the file
	// name count for more than matches in its directories.
	nameStart int
}

// fileIndex is one immutable snapshot of a root's candidates. A change makes
// a new snapshot rather than editing this one, so a query that holds it keeps
// a consistent list for as long as it needs.
type fileIndex struct {
	// entries are sorted by rel once the build completes, which is what lets a
	// patch insert and delete by binary search. A partial list is in scan
	// order.
	entries []indexEntry
	built   time.Time
	// partial records that the walk hit maxCandidates or ran out of time, so
	// the list is a prefix of the tree rather than all of it.
	partial bool
	// indexing records that the build is still running and this is what it
	// had found when the query arrived.
	indexing bool
	// watch is watchLive or watchPolling.
	watch string
	// git records that git decided the list, so a path missing from it is one
	// git ignores.
	git bool
}

// charBit maps a byte to its slot in an entry mask. Characters outside the set
// contribute nothing, which only ever makes the filter weaker — never wrong,
// because the subsequence scan still has to agree.
func charBit(c byte) uint64 {
	switch {
	case c >= 'a' && c <= 'z':
		return 1 << (c - 'a')
	case c >= '0' && c <= '9':
		return 1 << (26 + c - '0')
	case c == '.':
		return 1 << 36
	case c == '_':
		return 1 << 37
	case c == '-':
		return 1 << 38
	case c == '/':
		return 1 << 39
	}
	return 0
}

func newIndexEntry(rel string) indexEntry {
	// strings.ToLower returns its argument when there is nothing to change, so
	// a path that is already lowercase costs a string header and no bytes.
	lower := strings.ToLower(rel)
	if len(lower) != len(rel) {
		// A few characters change length when lowered (the Kelvin sign, for
		// one). Match positions are byte offsets into lower reported against
		// rel, so such a path is lowered in ASCII only.
		lower = asciiLower(rel)
	}
	var mask uint64
	for i := 0; i < len(lower); i++ {
		mask |= charBit(lower[i])
	}
	return indexEntry{
		rel:       rel,
		lower:     lower,
		mask:      mask,
		nameStart: strings.LastIndexByte(lower, '/') + 1,
	}
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func newIndexEntries(rels []string) []indexEntry {
	entries := make([]indexEntry, 0, len(rels))
	for _, rel := range rels {
		entries = append(entries, newIndexEntry(rel))
	}
	return entries
}

// sortEntries orders a list by rel. It sorts a copy: a partial list shares its
// array with snapshots already handed to queries.
func sortEntries(entries []indexEntry) []indexEntry {
	sorted := make([]indexEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].rel < sorted[j].rel })
	return sorted
}

// indexRoot is everything the cache knows about one root. Guarded by the
// cache's mutex, except gen, which the watcher bumps from its own goroutine.
type indexRoot struct {
	path string
	idx  *fileIndex
	// building is true while a build runs; done closes when it finishes.
	building bool
	done     chan struct{}
	// again asks for one more build after the running one, because something
	// changed while it was scanning.
	again bool
	// stale is a polling root's "rebuild on the next query", set by the
	// FilesTouched hook.
	stale bool
	// gitDir is the repository's git directory when the root is the top of a
	// checkout, and "" otherwise.
	gitDir string
	stamp  gitStamp
	// gen counts changes seen under the root. The git status cache compares
	// it, so it only means something while the root is live.
	gen  uint64
	live *rootWatcher
	// builds counts full scans, so a test can tell a patch from a rebuild.
	builds int
}

// changeSink is the spec 54 file watcher as the index sees it.
type changeSink interface {
	// Notice says path changed on disk, and reports whether anybody is
	// watching it or its folder.
	Notice(path string) bool
	// Nudge sweeps now rather than after the usual settle.
	Nudge()
}

// indexCache holds one candidate list per root, shared by every session and
// every client looking at that root. Without it each keystroke re-walks the
// tree, which is fine for a git checkout and unusable for anything else.
type indexCache struct {
	mu    sync.Mutex
	roots map[string]*indexRoot
	// recent is least-recently-used first.
	recent []string

	// live is false where no watcher backend exists, and in tests that want
	// the polling behaviour.
	live   bool
	budget *watchBudget
	// polling, when set, is the files.watch setting: true forces polling.
	polling func() bool
	files   changeSink
}

func newIndexCache() *indexCache {
	return newIndexCacheWith(liveWatchSupported, maxUserWatches()/2)
}

// newIndexCacheWith builds a cache with an explicit backend switch and watch
// budget, which is how tests drive the fallback.
func newIndexCacheWith(live bool, budget int) *indexCache {
	return &indexCache{
		roots:  map[string]*indexRoot{},
		live:   live,
		budget: &watchBudget{limit: budget},
	}
}

// fileIndexes is process-wide on purpose: two sessions on one repository, and
// the desktop and mobile clients viewing it, should walk it once between them.
var fileIndexes = newIndexCache()

// useFiles connects the spec 54 watcher, so a raw event under a watched path
// sweeps at once instead of on the next tick.
func (c *indexCache) useFiles(files changeSink) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files = files
}

// usePollingSetting connects the files.watch setting.
func (c *indexCache) usePollingSetting(polling func() bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polling = polling
}

// get returns the candidate list for root, building it when there is none.
//
// A warm list is returned at once; when it needs a rebuild, the rebuild runs
// behind the answer. A cold root waits up to coldWait and then answers with
// whatever the build has found, marked indexing, so the panel is never blank
// for the length of a walk.
func (c *indexCache) get(ctx context.Context, root string) *fileIndex {
	c.mu.Lock()
	r := c.rootLocked(root)
	if idx := r.idx; idx != nil && !idx.indexing {
		var dead *rootWatcher
		if r.live != nil && !r.live.sameRoot() {
			// The folder was replaced under a live watcher, whose watches all
			// went with the old one. Poll until a rebuild watches the new one.
			dead = r.detachLocked()
		}
		if !r.building && c.needsRebuildLocked(r) {
			c.startBuildLocked(r)
		}
		idx = r.idx
		c.mu.Unlock()
		if dead != nil {
			dead.close()
		}
		return idx
	}
	done := r.done
	c.mu.Unlock()

	timer := time.NewTimer(coldWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if r.idx == nil {
		return &fileIndex{indexing: true, watch: watchPolling, built: time.Now()}
	}
	return r.idx
}

// ensure starts a build for root if there is none, without waiting for it.
// The git status route calls it so that the repository it reports on gets a
// watcher, which is what makes its cache safe to use.
func (c *indexCache) ensure(root string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rootLocked(root)
}

// rootLocked returns root's state, creating it and starting its first build
// when it is new, and marks it most recently used. The caller holds mu.
func (c *indexCache) rootLocked(root string) *indexRoot {
	r := c.roots[root]
	if r == nil {
		r = &indexRoot{path: root}
		c.roots[root] = r
		c.startBuildLocked(r)
	}
	c.touchLocked(root)
	c.evictLocked()
	return r
}

// needsRebuildLocked decides whether a warm root's list is out of date. A live
// root asks only the TTL; its watcher handles the rest. The caller holds mu.
func (c *indexCache) needsRebuildLocked(r *indexRoot) bool {
	if time.Since(r.idx.built) > indexTTL {
		return true
	}
	if r.live != nil {
		return false
	}
	return r.stale || (r.gitDir != "" && readStamp(r.gitDir) != r.stamp)
}

func (c *indexCache) startBuildLocked(r *indexRoot) {
	r.building, r.again, r.stale = true, false, false
	r.builds++
	r.done = make(chan struct{})
	go c.build(r, r.done)
}

// build scans root and publishes the result, then decides how the list will
// be kept fresh.
func (c *indexCache) build(r *indexRoot, done chan struct{}) {
	defer close(done)
	// Not the request's context: a cold query answers before the build ends,
	// and a build that died with that connection would never complete.
	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	repoDir := ""
	if dir, err := gitDir(r.path); err == nil {
		repoDir = dir
	}
	stamp := readStamp(repoDir)
	scan := scanRoot(ctx, r.path, func(partial []indexEntry) { c.publishPartial(r, partial) })
	idx := &fileIndex{
		entries: scan.entries,
		built:   time.Now(),
		partial: scan.partial,
		git:     scan.git,
		watch:   watchPolling,
	}
	if c.watchRoot(r, repoDir, scan) {
		idx.watch = watchLive
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roots[r.path] != r {
		// Evicted while it was building. The watcher, if one started, has
		// nobody to report to.
		if r.live != nil {
			go r.live.close()
			r.live = nil
		}
		return
	}
	r.idx, r.gitDir, r.stamp = idx, repoDir, stamp
	r.gen++
	r.building = false
	if r.again {
		c.startBuildLocked(r)
	}
}

// publishPartial hands queries what a cold build has found so far. It never
// replaces a complete list: a rebuild serves the old one until it is done.
func (c *indexCache) publishPartial(r *indexRoot, partial []indexEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.idx != nil && !r.idx.indexing {
		return
	}
	r.idx = &fileIndex{entries: partial, indexing: true, built: time.Now(), watch: watchPolling}
}

// watchRoot starts or re-syncs the root's watcher and reports whether the
// root is live.
func (c *indexCache) watchRoot(r *indexRoot, gitDir string, scan scanResult) bool {
	c.mu.Lock()
	live, files, polling := r.live, c.files, c.polling
	wanted := c.live
	c.mu.Unlock()
	if polling != nil && polling() {
		wanted = false
	}

	if !wanted || scan.partial {
		// A truncated list cannot be patched into a true one, so a root that
		// big polls.
		if live != nil {
			c.dropWatcher(r)
		}
		return false
	}
	if live != nil {
		if err := live.sync(scan.dirs, scan.git); err != nil {
			c.dropWatcher(r)
			return false
		}
		return true
	}
	w, err := openRootWatcher(c, r, gitDir, scan.dirs, scan.git, files)
	if err != nil {
		return false
	}
	c.mu.Lock()
	r.live = w
	c.mu.Unlock()
	return true
}

// dropWatcher closes a root's watcher and lets it poll. The next query
// rebuilds, because events may have been lost on the way down.
func (c *indexCache) dropWatcher(r *indexRoot) {
	c.mu.Lock()
	w := r.detachLocked()
	c.mu.Unlock()
	if w != nil {
		w.close()
	}
}

// detachLocked takes the root's watcher away and marks the root to rebuild,
// returning the watcher for the caller to close outside the lock. The caller
// holds the cache's mutex.
func (r *indexRoot) detachLocked() *rootWatcher {
	w := r.live
	r.live = nil
	r.stale = true
	if r.idx != nil && r.idx.watch == watchLive {
		copied := *r.idx
		copied.watch = watchPolling
		r.idx = &copied
	}
	return w
}

// requestRebuild is the watcher's way of saying a patch will not do: git's
// index moved, the dirty set was too big, or the kernel dropped events.
func (c *indexCache) requestRebuild(r *indexRoot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roots[r.path] != r {
		return
	}
	if r.building {
		r.again = true
		return
	}
	c.startBuildLocked(r)
}

// replace publishes a patched list, provided nothing else replaced the one
// the patch was computed from. It reports whether it did.
func (c *indexCache) replace(r *indexRoot, from *fileIndex, entries []indexEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.idx != from || r.building {
		return false
	}
	next := *from
	next.entries = entries
	r.idx = &next
	return true
}

// snapshot returns the root's current list and whether a build is running.
func (c *indexCache) snapshot(r *indexRoot) (*fileIndex, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return r.idx, r.building
}

// bumpRoot records a change under r. The git status cache reads it.
func (c *indexCache) bumpRoot(r *indexRoot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.gen++
}

// bump records a change at path under every root holding it — the write route
// calls this, so its own write never waits on an event to invalidate status.
func (c *indexCache) bump(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for root, r := range c.roots {
		if within(root, path) {
			r.gen++
		}
	}
}

// generation reports root's change counter, and whether it can be trusted: only
// a live root at the top of a checkout sees every change that moves git status,
// working tree and .git alike.
func (c *indexCache) generation(root string) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.roots[root]
	if r == nil || r.live == nil || r.gitDir == "" || r.building || r.idx == nil {
		return 0, false
	}
	return r.gen, true
}

// poke is the FilesTouched hook: an agent wrote something. A polling root
// rebuilds on its next query; a live root already knows.
func (c *indexCache) poke() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.roots {
		if r.live == nil {
			r.stale = true
		}
	}
}

// liveCovers reports whether a live watcher sees entries come and go in dir,
// which is what lets the spec 54 sweep skip a directory whose mtime held.
func (c *indexCache) liveCovers(dir string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for root, r := range c.roots {
		if r.live == nil || !within(root, dir) {
			continue
		}
		if r.live.isWatched(relTo(root, dir)) {
			return true
		}
	}
	return false
}

// ignoredIn returns a test for the entries of dir, or nil when no warm list
// under a git root can answer. An entry is ignored when git's list holds
// neither it nor anything under it.
func (c *indexCache) ignoredIn(dir string) func(name string, isDir bool) bool {
	c.mu.Lock()
	var best *indexRoot
	for root, r := range c.roots {
		idx := r.idx
		if idx == nil || idx.indexing || idx.partial || !idx.git || !within(root, dir) {
			continue
		}
		if best == nil || len(root) > len(best.path) {
			best = r
		}
	}
	if best == nil {
		c.mu.Unlock()
		return nil
	}
	idx, live, base := best.idx, best.live, relTo(best.path, dir)
	c.mu.Unlock()

	return func(name string, isDir bool) bool {
		rel := joinRel(base, name)
		if !isDir {
			return !hasEntry(idx.entries, rel)
		}
		if live != nil && live.isWatched(rel) {
			return false
		}
		return !hasPrefix(idx.entries, rel+"/")
	}
}

// touchLocked moves root to the most-recently-used end. The caller holds mu.
func (c *indexCache) touchLocked(root string) {
	for i, seen := range c.recent {
		if seen == root {
			c.recent = append(c.recent[:i], c.recent[i+1:]...)
			break
		}
	}
	c.recent = append(c.recent, root)
}

// evictLocked drops the least recently used roots over the cap, and closes
// their watchers so the watches go back to the budget. The caller holds mu.
func (c *indexCache) evictLocked() {
	for len(c.recent) > maxIndexedRoots {
		victim := c.roots[c.recent[0]]
		delete(c.roots, c.recent[0])
		c.recent = c.recent[1:]
		if victim != nil && victim.live != nil {
			go victim.live.close()
			victim.live = nil
		}
	}
}

// gitStamp is what a polling root watches of git: the index and HEAD mtimes.
// Two stats, and they move on every commit, checkout, add and pull.
type gitStamp struct {
	index, head time.Time
}

func readStamp(gitDir string) gitStamp {
	if gitDir == "" {
		return gitStamp{}
	}
	var s gitStamp
	if info, err := os.Stat(filepath.Join(gitDir, "index")); err == nil {
		s.index = info.ModTime()
	}
	if info, err := os.Stat(filepath.Join(gitDir, "HEAD")); err == nil {
		s.head = info.ModTime()
	}
	return s
}

// within reports whether path is root or under it.
func within(root, path string) bool {
	if path == root {
		return true
	}
	if root == string(filepath.Separator) {
		return strings.HasPrefix(path, root)
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// relTo is path relative to root in index form: slash-separated, "" for the
// root itself.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// hasEntry finds rel in a sorted list.
func hasEntry(entries []indexEntry, rel string) bool {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].rel >= rel })
	return i < len(entries) && entries[i].rel == rel
}

// hasPrefix reports whether any entry of a sorted list starts with prefix.
func hasPrefix(entries []indexEntry, prefix string) bool {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].rel >= prefix })
	return i < len(entries) && strings.HasPrefix(entries[i].rel, prefix)
}
