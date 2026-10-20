package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// See docs/specs/65-fast-files.md, 1.3.
//
// The watcher's one question of an event is "which directory's entries may
// have changed". It does not trust event types, which differ by platform: each
// dirty directory is read again and compared with the list. Raw events never
// reach SSE. They patch the list, bump the git status generation, and poke the
// spec 54 sweep for the paths somebody is looking at.

const (
	// watchDebounce gathers a burst of events into one patch.
	watchDebounce = 100 * time.Millisecond
	// bulkDirtyDirs is the size of a burst that is cheaper to rebuild than to
	// patch: a checkout or a branch switch.
	bulkDirtyDirs = 512
	// maxPatchAdds bounds what one patch walks into a new directory. A larger
	// arrival — an unpacked archive, say — is a rebuild.
	maxPatchAdds = 20000
)

var errWatchBudget = errors.New("watch budget exhausted")

// watchBudget is the share of the kernel's inotify watches the index may use,
// across every root. Other tools — editors, tsc --watch — draw on the same
// per-user limit, so the index takes half and no more.
type watchBudget struct {
	mu    sync.Mutex
	limit int
	used  int
}

func (b *watchBudget) reserve(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+n > b.limit {
		return false
	}
	b.used += n
	return true
}

func (b *watchBudget) release(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}

// rootWatcher keeps one root's list in step with the disk.
type rootWatcher struct {
	cache  *indexCache
	root   *indexRoot
	path   string
	gitDir string
	files  changeSink
	w      *fsnotify.Watcher
	// rootInfo identifies the root directory the watches were put on.
	rootInfo os.FileInfo

	mu  sync.Mutex
	git bool
	// watched holds root-relative directories with a watch on them.
	watched map[string]bool
	// ignoredDirs remembers directories git said it ignores, so a folder
	// beside node_modules does not ask about node_modules on every patch.
	ignoredDirs map[string]bool
	// closed is set once the watcher is shut, so a watch added by a patch
	// that raced the close goes straight back to the budget.
	closed bool

	stop      chan struct{}
	closeOnce sync.Once
}

// openRootWatcher reserves the root's watches and starts its loop. Either the
// whole set is watched or none of it: a half-watched root would miss changes
// and report itself live.
func openRootWatcher(
	c *indexCache, r *indexRoot, gitDir string, dirs map[string]bool, git bool, files changeSink,
) (*rootWatcher, error) {
	need := len(dirs)
	if gitDir != "" {
		need++
	}
	if !c.budget.reserve(need) {
		return nil, errWatchBudget
	}
	fw, err := openDirWatcher()
	if err != nil {
		c.budget.release(need)
		return nil, fmt.Errorf("open watcher: %w", err)
	}
	rootInfo, err := os.Stat(r.path)
	if err != nil {
		c.budget.release(need)
		_ = fw.Close()
		return nil, fmt.Errorf("stat root: %w", err)
	}
	rw := &rootWatcher{
		cache: c, root: r, path: r.path, gitDir: gitDir, files: files, w: fw, git: git,
		rootInfo:    rootInfo,
		watched:     make(map[string]bool, len(dirs)),
		ignoredDirs: map[string]bool{},
		stop:        make(chan struct{}),
	}
	for dir := range dirs {
		if err := fw.Add(filepath.Join(r.path, filepath.FromSlash(dir))); err != nil {
			rw.closeWith(need - len(rw.watched))
			return nil, fmt.Errorf("watch %s: %w", dir, err)
		}
		rw.watched[dir] = true
	}
	if gitDir != "" {
		if err := fw.Add(gitDir); err != nil {
			rw.closeWith(1)
			return nil, fmt.Errorf("watch %s: %w", gitDir, err)
		}
	}
	if err := rw.verify(); err != nil {
		rw.close()
		return nil, err
	}
	go rw.loop()
	return rw, nil
}

// verify checks the promise a live root makes: the kernel holds a watch for
// every directory the watcher believes it watches, and the root it watches is
// still the directory at its path. A tree deleted and made again under the
// watcher's feet — between a scan and the watches it led to — breaks both
// quietly, and a root that reports live while watching nothing is the worst
// answer there is. Any mismatch is an error, and the caller lets the root poll.
func (rw *rootWatcher) verify() error {
	if !rw.sameRoot() {
		return errors.New("root was replaced")
	}
	kernel := map[string]bool{}
	for _, p := range rw.w.WatchList() {
		kernel[p] = true
	}
	if rw.gitDir != "" && !kernel[rw.gitDir] {
		return fmt.Errorf("no kernel watch on %s", rw.gitDir)
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if !rw.watched[""] {
		return errors.New("root is not watched")
	}
	for dir := range rw.watched {
		if !kernel[filepath.Join(rw.path, filepath.FromSlash(dir))] {
			return fmt.Errorf("no kernel watch on %s", dir)
		}
	}
	return nil
}

// sameRoot reports whether the directory at the root's path is the one the
// watches were put on. One stat, so a query can afford to ask.
func (rw *rootWatcher) sameRoot() bool {
	info, err := os.Stat(rw.path)
	return err == nil && os.SameFile(info, rw.rootInfo)
}

// close stops the loop and returns every watch to the budget.
func (rw *rootWatcher) close() { rw.closeWith(0) }

// closeWith also returns extra, watches reserved but never added.
func (rw *rootWatcher) closeWith(extra int) {
	rw.closeOnce.Do(func() {
		close(rw.stop)
		if err := rw.w.Close(); err != nil {
			log.Printf("files: close watcher for %s: %v", rw.path, err)
		}
		rw.mu.Lock()
		held := len(rw.watched)
		rw.watched = map[string]bool{}
		rw.closed = true
		rw.mu.Unlock()
		if rw.gitDir != "" && extra == 0 {
			held++
		}
		rw.cache.budget.release(held + extra)
	})
}

func (rw *rootWatcher) isWatched(rel string) bool {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.watched[rel]
}

// sync brings the watch set to dirs after a rebuild: watch what is new, drop
// what is gone.
func (rw *rootWatcher) sync(dirs map[string]bool, git bool) error {
	rw.mu.Lock()
	rw.git = git
	rw.ignoredDirs = map[string]bool{}
	var gone, added []string
	for dir := range rw.watched {
		if !dirs[dir] {
			gone = append(gone, dir)
		}
	}
	for dir := range dirs {
		if !rw.watched[dir] {
			added = append(added, dir)
		}
	}
	rw.mu.Unlock()

	rw.unwatch(gone)
	if err := rw.watch(added); err != nil {
		return err
	}
	return rw.verify()
}

// watch adds directories, drawing on the budget for each.
func (rw *rootWatcher) watch(dirs []string) error {
	rw.mu.Lock()
	fresh := dirs[:0:0]
	for _, dir := range dirs {
		if !rw.watched[dir] {
			fresh = append(fresh, dir)
		}
	}
	rw.mu.Unlock()
	dirs = fresh
	if len(dirs) == 0 {
		return nil
	}
	if !rw.cache.budget.reserve(len(dirs)) {
		return errWatchBudget
	}
	for i, dir := range dirs {
		if err := rw.w.Add(filepath.Join(rw.path, filepath.FromSlash(dir))); err != nil {
			rw.cache.budget.release(len(dirs) - i)
			// Gone already is not a failure: the next event names its parent.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("watch %s: %w", dir, err)
		}
		rw.mu.Lock()
		if rw.closed {
			rw.cache.budget.release(1)
		} else {
			rw.watched[dir] = true
		}
		rw.mu.Unlock()
	}
	return nil
}

// unwatch drops directories and everything watched beneath them.
func (rw *rootWatcher) unwatch(dirs []string) {
	if len(dirs) == 0 {
		return
	}
	rw.mu.Lock()
	var drop []string
	for watched := range rw.watched {
		for _, dir := range dirs {
			if watched == dir || strings.HasPrefix(watched, dir+"/") {
				drop = append(drop, watched)
				break
			}
		}
	}
	for _, dir := range drop {
		delete(rw.watched, dir)
	}
	rw.mu.Unlock()

	for _, dir := range drop {
		// The kernel drops the watch of a deleted directory by itself, so an
		// error here is the usual case, not a problem.
		_ = rw.w.Remove(filepath.Join(rw.path, filepath.FromSlash(dir)))
	}
	rw.cache.budget.release(len(drop))
}

// batch is what one debounce window gathered.
type batch struct {
	dirty map[string]bool
	// replaced holds watched directories that were removed or renamed away.
	// A directory put back under the same name is a new inode with no watch,
	// so it is read again from scratch rather than trusted.
	replaced map[string]bool
	// rootGone is the root itself removed or renamed away. Every watch
	// under it died with it, even if a new tree appears at the same path.
	rootGone bool
	rebuild  bool
	nudge    bool
}

func newBatch() batch {
	return batch{dirty: map[string]bool{}, replaced: map[string]bool{}}
}

func (rw *rootWatcher) loop() {
	b := newBatch()
	var window <-chan time.Time
	for {
		select {
		case <-rw.stop:
			return
		case ev, ok := <-rw.w.Events:
			if !ok {
				return
			}
			rw.note(ev, &b)
			if window == nil {
				window = time.After(watchDebounce)
			}
		case err, ok := <-rw.w.Errors:
			if !ok {
				return
			}
			// An overflow means the kernel dropped events, so the list can no
			// longer be patched into the truth. Anything else is logged and
			// treated the same way, which is never wrong, only slower.
			if !errors.Is(err, fsnotify.ErrEventOverflow) {
				log.Printf("files: watcher for %s: %v", rw.path, err)
			}
			b.rebuild = true
			if window == nil {
				window = time.After(watchDebounce)
			}
		case <-window:
			window = nil
			rw.flush(b)
			b = newBatch()
		}
	}
}

// note folds one event into the batch.
func (rw *rootWatcher) note(ev fsnotify.Event, b *batch) {
	if rw.gitDir != "" && filepath.Dir(ev.Name) == rw.gitDir {
		switch filepath.Base(ev.Name) {
		case "index", "HEAD", "packed-refs":
			b.rebuild = true
			rw.cache.bumpRoot(rw.root)
		}
		return
	}
	rw.cache.bumpRoot(rw.root)
	if ev.Op.Has(fsnotify.Create) || ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename) {
		b.dirty[relTo(rw.path, filepath.Dir(ev.Name))] = true
	}
	if ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename) {
		if ev.Name == rw.path {
			b.rootGone = true
		} else if rel := relTo(rw.path, ev.Name); rel != "" && rw.isWatched(rel) {
			b.replaced[rel] = true
		}
	}
	if rw.files != nil && rw.files.Notice(ev.Name) {
		b.nudge = true
	}
}

// flush applies one window: a rebuild when the change is too big or too
// uncertain to patch, a patch otherwise, then the sweep.
func (rw *rootWatcher) flush(b batch) {
	defer func() {
		if b.nudge && rw.files != nil {
			rw.files.Nudge()
		}
	}()
	if !b.rootGone && !rw.sameRoot() {
		// The root's own event can be lost — it lands in the same burst as
		// the new tree's — so its identity is checked, not only its events.
		b.rootGone = true
	}
	if b.rootGone {
		// The kernel dropped every watch along with the old tree, while the
		// watched set still lists them all. Patching on would report live and
		// see nothing, so the watcher goes, and the rebuild opens a new one.
		rw.cache.dropWatcher(rw.root)
		rw.cache.requestRebuild(rw.root)
		return
	}
	if b.rebuild || len(b.dirty) > bulkDirtyDirs {
		rw.cache.requestRebuild(rw.root)
		return
	}
	if len(b.dirty) == 0 {
		return
	}
	if err := rw.patch(b.dirty, b.replaced); err != nil {
		if errors.Is(err, errWatchBudget) || !errors.Is(err, errNeedRebuild) {
			// Out of watches, or the kernel refused one: this root polls
			// from here on.
			log.Printf("files: %s falls back to polling: %v", rw.path, err)
			rw.cache.dropWatcher(rw.root)
		}
		rw.cache.requestRebuild(rw.root)
	}
}

var errNeedRebuild = errors.New("patch needs a rebuild")

// patch reads each dirty directory, compares it with the list, and publishes
// the list with the differences applied.
func (rw *rootWatcher) patch(dirty, replaced map[string]bool) error {
	idx, building := rw.cache.snapshot(rw.root)
	if idx == nil || building || idx.indexing {
		return errNeedRebuild
	}
	d := rw.diff(idx.entries, dirty)
	for dir := range replaced {
		d.goneDirs = append(d.goneDirs, dir)
		if info, err := os.Stat(filepath.Join(rw.path, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			d.newDirs = append(d.newDirs, dir)
		}
	}
	rw.unwatch(d.goneDirs)
	adds, err := rw.expand(d)
	if err != nil {
		return err
	}
	entries := applyDiff(idx.entries, adds, d.removed, d.goneDirs)
	if !rw.cache.replace(rw.root, idx, entries) {
		return errNeedRebuild
	}
	return nil
}

// dirDiff is what reading the dirty directories found.
type dirDiff struct {
	removed  map[string]bool
	goneDirs []string
	newFiles []string
	newDirs  []string
}

// diff compares each dirty directory on disk with the list.
func (rw *rootWatcher) diff(entries []indexEntry, dirty map[string]bool) dirDiff {
	d := dirDiff{removed: map[string]bool{}}
	dirs := make([]string, 0, len(dirty))
	for dir := range dirty {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		rw.diffDir(entries, dir, &d)
	}
	return d
}

func (rw *rootWatcher) diffDir(entries []indexEntry, dir string, d *dirDiff) {
	abs := filepath.Join(rw.path, filepath.FromSlash(dir))
	listing, err := os.ReadDir(abs)
	if err != nil {
		if dir != "" {
			// Gone, or no longer readable. Its parent is dirty too and says
			// the same thing; dropping it twice is harmless.
			d.goneDirs = append(d.goneDirs, dir)
		}
		return
	}
	onDisk := map[string]os.DirEntry{}
	for _, e := range listing {
		if !hiddenEntries[e.Name()] {
			onDisk[e.Name()] = e
		}
	}

	files, subdirs := childrenOf(entries, dir)
	rw.mu.Lock()
	for watched := range rw.watched {
		if watched != "" && path.Dir(watched) == dirOrDot(dir) {
			subdirs[path.Base(watched)] = true
		}
	}
	git, ignoredDirs := rw.git, rw.ignoredDirs
	watched := map[string]bool{}
	for name := range subdirs {
		if rel := joinRel(dir, name); rw.watched[rel] {
			watched[rel] = true
		}
	}
	rw.mu.Unlock()

	for name := range files {
		e, ok := onDisk[name]
		if ok && rw.isFile(e) {
			continue
		}
		if ok && e.IsDir() && isNestedRepo(filepath.Join(abs, name)) {
			// A submodule is one entry in git's list, and a folder on disk.
			continue
		}
		d.removed[joinRel(dir, name)] = true
	}
	for name := range subdirs {
		if e, ok := onDisk[name]; !ok || !e.IsDir() {
			d.goneDirs = append(d.goneDirs, joinRel(dir, name))
		}
	}
	for name, e := range onDisk {
		rel := joinRel(dir, name)
		switch {
		case e.IsDir():
			// Asked of the watched set, not the list: a folder the list knows
			// may have been deleted and made again, and the new one has no
			// watch until it is walked.
			if watched[rel] || ignoredDirs[rel] || (!git && skipDirs[name]) {
				continue
			}
			d.newDirs = append(d.newDirs, rel)
		case rw.isFile(e) && !files[name]:
			d.newFiles = append(d.newFiles, rel)
		}
	}
}

// isFile is what counts as a file: git lists symlinks and the walk does not,
// so each kind of root keeps to what its own full scan would list.
func (rw *rootWatcher) isFile(e os.DirEntry) bool {
	rw.mu.Lock()
	git := rw.git
	rw.mu.Unlock()
	if git {
		return !e.IsDir()
	}
	return e.Type().IsRegular()
}

// expand filters the new paths through git's ignore rules and walks each new
// directory, watching what it enters. It returns the paths to insert.
func (rw *rootWatcher) expand(d dirDiff) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	rw.mu.Lock()
	git := rw.git
	rw.mu.Unlock()

	adds := rw.unignored(ctx, d.newFiles, false)
	frontier := rw.unignored(ctx, d.newDirs, true)
	for len(frontier) > 0 {
		var files, next []string
		for _, dir := range frontier {
			if isNestedRepo(filepath.Join(rw.path, filepath.FromSlash(dir))) {
				continue
			}
			if err := rw.watch([]string{dir}); err != nil {
				return nil, err
			}
			listing, err := os.ReadDir(filepath.Join(rw.path, filepath.FromSlash(dir)))
			if err != nil {
				continue
			}
			for _, e := range listing {
				rel := joinRel(dir, e.Name())
				switch {
				case hiddenEntries[e.Name()]:
				case e.IsDir():
					if git || !skipDirs[e.Name()] {
						next = append(next, rel)
					}
				case rw.isFile(e):
					files = append(files, rel)
				}
			}
		}
		adds = append(adds, rw.unignored(ctx, files, false)...)
		if len(adds) > maxPatchAdds {
			return nil, errNeedRebuild
		}
		frontier = rw.unignored(ctx, next, true)
	}
	return adds, nil
}

// unignored drops what git ignores, in one check-ignore for the lot. A root
// git does not decide passes everything.
func (rw *rootWatcher) unignored(ctx context.Context, rels []string, dirs bool) []string {
	rw.mu.Lock()
	git := rw.git
	rw.mu.Unlock()
	if !git || len(rels) == 0 {
		return rels
	}
	query := rels
	if dirs {
		query = make([]string, len(rels))
		for i, rel := range rels {
			query[i] = rel + "/"
		}
	}
	ignored := checkIgnored(ctx, rw.path, query)
	kept := rels[:0:0]
	for _, rel := range rels {
		if !ignored[rel] {
			kept = append(kept, rel)
			continue
		}
		if dirs {
			rw.mu.Lock()
			rw.ignoredDirs[rel] = true
			rw.mu.Unlock()
		}
	}
	return kept
}

// childrenOf returns the file names directly in dir, and the names of the
// directories in dir that hold entries, from a sorted list.
func childrenOf(entries []indexEntry, dir string) (map[string]bool, map[string]bool) {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	files, dirs := map[string]bool{}, map[string]bool{}
	i := sort.Search(len(entries), func(i int) bool { return entries[i].rel >= prefix })
	for ; i < len(entries) && strings.HasPrefix(entries[i].rel, prefix); i++ {
		rest := entries[i].rel[len(prefix):]
		if cut := strings.IndexByte(rest, '/'); cut >= 0 {
			dirs[rest[:cut]] = true
			// Skip the rest of this subdirectory in one search rather than
			// stepping over every path in it.
			next := prefix + rest[:cut] + "0" // '0' sorts just after '/'
			i = sort.Search(len(entries), func(j int) bool { return entries[j].rel >= next }) - 1
			continue
		}
		files[rest] = true
	}
	return files, dirs
}

// applyDiff returns a new sorted list: the old one minus removed paths and
// everything under gone directories, plus adds.
func applyDiff(old []indexEntry, adds []string, removed map[string]bool, goneDirs []string) []indexEntry {
	sort.Strings(adds)
	gone := func(rel string) bool {
		if removed[rel] {
			return true
		}
		for _, dir := range goneDirs {
			if strings.HasPrefix(rel, dir+"/") {
				return true
			}
		}
		return false
	}
	out := make([]indexEntry, 0, len(old)+len(adds))
	i, j := 0, 0
	for i < len(old) || j < len(adds) {
		switch {
		case j >= len(adds) || (i < len(old) && old[i].rel < adds[j]):
			if !gone(old[i].rel) {
				out = append(out, old[i])
			}
			i++
		case i >= len(old) || adds[j] < old[i].rel:
			if len(out) == 0 || out[len(out)-1].rel != adds[j] {
				out = append(out, newIndexEntry(adds[j]))
			}
			j++
		default:
			// Already listed: keep the entry that is there.
			if !gone(old[i].rel) {
				out = append(out, old[i])
			}
			i++
			j++
		}
	}
	return out
}

func dirOrDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// isNestedRepo reports a directory that is a repository of its own. Its files
// belong to that repository's list, not to this one.
func isNestedRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}
