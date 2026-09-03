package server

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestIndexIsBuiltOnceAndReused(t *testing.T) {
	cache := newIndexCache()
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a")

	first := cache.get(context.Background(), root)
	if len(first.entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(first.entries))
	}

	// A file appearing after the build is not visible until the list is
	// refreshed. That is the deal the cache makes, and it should be explicit.
	write(t, filepath.Join(root, "b.go"), "package b")
	second := cache.get(context.Background(), root)
	if second != first {
		t.Fatal("expected the cached index, not a rebuild")
	}
}

func TestIndexRefreshesInTheBackgroundOnceStale(t *testing.T) {
	cache := newIndexCache()
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a")

	stale := cache.get(context.Background(), root)
	write(t, filepath.Join(root, "b.go"), "package b")

	// Age the entry rather than sleeping for the real TTL.
	cache.mu.Lock()
	cache.roots[root].built = time.Now().Add(-2 * indexTTL)
	cache.mu.Unlock()

	// The stale answer comes back immediately: a search must not wait for a walk.
	if got := cache.get(context.Background(), root); len(got.entries) != len(stale.entries) {
		t.Fatalf("expected the stale list to be served, got %d entries", len(got.entries))
	}

	waitForIndex(t, cache, root, 2)
}

func TestIndexBuildsOnceForConcurrentCallers(t *testing.T) {
	cache := newIndexCache()
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		write(t, filepath.Join(root, fmt.Sprintf("f%d.go", i)), "package f")
	}

	var wg sync.WaitGroup
	seen := make([]*fileIndex, 8)
	for i := range seen {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			seen[slot] = cache.get(context.Background(), root)
		}(i)
	}
	wg.Wait()

	for i, idx := range seen {
		if idx != seen[0] {
			t.Fatalf("caller %d got a different index: concurrent builds were not shared", i)
		}
	}
}

func TestIndexEvictsTheLeastRecentlyUsedRoot(t *testing.T) {
	cache := newIndexCache()
	roots := make([]string, maxIndexedRoots+1)
	for i := range roots {
		roots[i] = t.TempDir()
		write(t, filepath.Join(roots[i], "a.go"), "package a")
		cache.get(context.Background(), roots[i])
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.roots) != maxIndexedRoots {
		t.Fatalf("held %d roots, expected the cap of %d", len(cache.roots), maxIndexedRoots)
	}
	if _, held := cache.roots[roots[0]]; held {
		t.Fatal("the least recently used root should have been evicted")
	}
	if _, held := cache.roots[roots[len(roots)-1]]; !held {
		t.Fatal("the newest root should be held")
	}
}

func TestIndexEntryPrecomputesWhatTheScorerNeeds(t *testing.T) {
	entry := newIndexEntry("docs/specs/57-vim-mode.md")
	if entry.lower != "docs/specs/57-vim-mode.md" {
		t.Fatalf("lower is wrong: %q", entry.lower)
	}
	if entry.nameStart != 11 {
		t.Fatalf("nameStart is %d, expected 11", entry.nameStart)
	}
	if entry.mask&charBit('v') == 0 {
		t.Fatal("mask should record the 'v' in vim")
	}
	if entry.mask&charBit('q') != 0 {
		t.Fatal("mask should not record a character the path does not have")
	}

	// An already-lowercase path shares its bytes rather than copying them.
	upper := newIndexEntry("README.md")
	if upper.lower != "readme.md" {
		t.Fatalf("uppercase path was not lowered: %q", upper.lower)
	}
}

func waitForIndex(t *testing.T, cache *indexCache, root string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		got := len(cache.roots[root].entries)
		cache.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh never reached %d entries", want)
}
