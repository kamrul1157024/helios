package server

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	// indexTTL is how long a candidate list is served before a refresh is
	// started behind it. Short, because an agent creating a file and the user
	// pressing ⌘P for it are seconds apart.
	indexTTL = 5 * time.Second
	// maxIndexedRoots bounds the memory a daemon holds for search. A root of
	// 100k paths is a few megabytes; an unbounded map of every directory
	// anybody ever typed is not.
	maxIndexedRoots = 8
)

// indexEntry is one candidate path with everything the scorer would otherwise
// recompute on every keystroke.
type indexEntry struct {
	rel   string
	lower string
	// mask has one bit per distinct character in lower. A query needing a
	// character the path does not have is rejected by a single AND, which is
	// most of the tree while somebody is still typing.
	mask uint64
	// nameStart is the offset just past the last separator: matches in the file
	// name count for more than matches in its directories.
	nameStart int
}

type fileIndex struct {
	entries []indexEntry
	built   time.Time
	// partial records that the walk hit maxCandidates or ran out of time, so
	// the list is a prefix of the tree rather than all of it.
	partial bool
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

func newIndexEntries(rels []string) []indexEntry {
	entries := make([]indexEntry, 0, len(rels))
	for _, rel := range rels {
		entries = append(entries, newIndexEntry(rel))
	}
	return entries
}

// indexCache holds one candidate list per root, shared by every session and
// every client looking at that root. Without it each keystroke re-walks the
// tree, which is fine for a git checkout and unusable for anything else.
type indexCache struct {
	mu      sync.Mutex
	roots   map[string]*fileIndex
	pending map[string]chan struct{}
	// recent is least-recently-used first.
	recent []string
}

func newIndexCache() *indexCache {
	return &indexCache{roots: map[string]*fileIndex{}, pending: map[string]chan struct{}{}}
}

// fileIndexes is process-wide on purpose: two sessions on one repository, and
// the desktop and mobile clients viewing it, should walk it once between them.
var fileIndexes = newIndexCache()

// get returns the candidate list for root, building it when there is none.
//
// A stale list is returned as it stands and refreshed behind the answer: a
// search that waits for a rebuild is the slow behaviour this cache exists to
// remove. Callers arriving during a build wait for it rather than starting a
// second one.
func (c *indexCache) get(ctx context.Context, root string) *fileIndex {
	for {
		c.mu.Lock()
		if idx := c.roots[root]; idx != nil {
			if time.Since(idx.built) > indexTTL && c.pending[root] == nil {
				c.refresh(root)
			}
			c.touch(root)
			c.mu.Unlock()
			return idx
		}
		if wait := c.pending[root]; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return &fileIndex{built: time.Now()}
			}
		}
		done := make(chan struct{})
		c.pending[root] = done
		c.mu.Unlock()

		idx := buildIndex(ctx, root)
		c.store(root, idx)
		close(done)
		return idx
	}
}

// refresh rebuilds root in the background. The caller holds mu.
func (c *indexCache) refresh(root string) {
	done := make(chan struct{})
	c.pending[root] = done
	go func() {
		// Not the request's context: the answer already went out, and a refresh
		// that dies with the connection would never complete.
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		c.store(root, buildIndex(ctx, root))
		close(done)
	}()
}

func (c *indexCache) store(root string, idx *fileIndex) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roots[root] = idx
	delete(c.pending, root)
	c.touch(root)
	for len(c.recent) > maxIndexedRoots {
		delete(c.roots, c.recent[0])
		c.recent = c.recent[1:]
	}
}

// touch moves root to the most-recently-used end. The caller holds mu.
func (c *indexCache) touch(root string) {
	for i, seen := range c.recent {
		if seen == root {
			c.recent = append(c.recent[:i], c.recent[i+1:]...)
			break
		}
	}
	c.recent = append(c.recent, root)
}

func buildIndex(ctx context.Context, root string) *fileIndex {
	files, partial := candidateFiles(ctx, root)
	return &fileIndex{entries: newIndexEntries(files), built: time.Now(), partial: partial}
}
