package server

import (
	"container/heap"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// parallelRankMin is the index size above which scoring is split across
// cores. Below it, starting goroutines costs more than the scan they share.
const parallelRankMin = 20000

// ranked is one scored candidate. It holds an index into the entry slice
// rather than a copy, so a scan over 100k paths allocates nothing per path.
type ranked struct {
	score int
	entry *indexEntry
}

// better is the result order: higher score, then the shorter path, then the
// path itself. The last key makes the order total, so the same query over the
// same index always answers the same list however the scan was split.
func better(a, b ranked) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if len(a.entry.rel) != len(b.entry.rel) {
		return len(a.entry.rel) < len(b.entry.rel)
	}
	return a.entry.rel < b.entry.rel
}

// worstFirst is a min-heap by result order: its root is the weakest of the
// best k seen so far, which is the one a better candidate replaces.
type worstFirst []ranked

func (h worstFirst) Len() int           { return len(h) }
func (h worstFirst) Less(i, j int) bool { return better(h[j], h[i]) }
func (h worstFirst) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *worstFirst) Push(x any)        { *h = append(*h, x.(ranked)) }
func (h *worstFirst) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// normalizeQuery is what every query is matched as: lowercase, with the spaces
// people type between words removed.
func normalizeQuery(query string) string {
	return strings.ToLower(strings.Join(strings.Fields(query), ""))
}

// rankEntries keeps the best `limit` fuzzy matches for query, best first.
//
// A heap of size limit rather than a sort of every match: a one-letter query
// matches most of a 100k tree, and sorting all of it to return 50 was where
// the time went. Above parallelRankMin the scan is split across cores, each
// part keeping its own heap, and the heaps are merged at the end.
func rankEntries(entries []indexEntry, query string, limit int) []searchMatch {
	if limit <= 0 {
		return []searchMatch{}
	}
	q := normalizeQuery(query)
	var wanted uint64
	for i := 0; i < len(q); i++ {
		wanted |= charBit(q[i])
	}

	var best []ranked
	parts := runtime.GOMAXPROCS(0)
	if len(entries) < parallelRankMin || parts < 2 {
		best = topK(entries, q, wanted, limit)
	} else {
		best = topKParallel(entries, q, wanted, limit, parts)
	}

	sort.Slice(best, func(i, j int) bool { return better(best[i], best[j]) })
	matches := make([]searchMatch, len(best))
	for i, r := range best {
		matches[i] = searchMatch{
			Rel:       r.entry.rel,
			Score:     r.score,
			Positions: matchPositions(*r.entry, q),
		}
	}
	return matches
}

func topKParallel(entries []indexEntry, q string, wanted uint64, limit, parts int) []ranked {
	chunk := (len(entries) + parts - 1) / parts
	results := make([][]ranked, parts)
	var wg sync.WaitGroup
	for p := 0; p < parts; p++ {
		lo := p * chunk
		if lo >= len(entries) {
			break
		}
		hi := min(lo+chunk, len(entries))
		wg.Add(1)
		go func(slot int, part []indexEntry) {
			defer wg.Done()
			results[slot] = topK(part, q, wanted, limit)
		}(p, entries[lo:hi])
	}
	wg.Wait()

	merged := make(worstFirst, 0, limit)
	for _, part := range results {
		for _, r := range part {
			offer(&merged, r, limit)
		}
	}
	return merged
}

// topK scans one run of entries and keeps the best limit of them.
func topK(entries []indexEntry, q string, wanted uint64, limit int) []ranked {
	h := make(worstFirst, 0, limit)
	for i := range entries {
		entry := &entries[i]
		score := 0
		if q != "" {
			// One AND against the character mask before the scan, because most
			// candidates are missing a letter outright and finding that out
			// should not cost a walk over the path.
			if entry.mask&wanted != wanted {
				continue
			}
			var ok bool
			score, ok = fuzzyScore(*entry, q)
			if !ok {
				continue
			}
		}
		offer(&h, ranked{score: score, entry: entry}, limit)
	}
	return h
}

func offer(h *worstFirst, r ranked, limit int) {
	if h.Len() < limit {
		heap.Push(h, r)
		return
	}
	if better(r, (*h)[0]) {
		(*h)[0] = r
		heap.Fix(h, 0)
	}
}

// matchPositions repeats fuzzyScore's greedy scan to say which characters
// matched, as UTF-16 offsets into rel — the unit a JavaScript string and a
// Dart string both index by. Run for the returned rows only, never during the
// scan.
//
// lower and rel have the same byte layout (see newIndexEntry), so a byte offset
// in one is the same byte in the other. A query character that matched inside
// a multi-byte rune names that rune once.
func matchPositions(entry indexEntry, q string) []int {
	positions := []int{}
	if q == "" {
		return positions
	}
	lower, rel := entry.lower, entry.rel
	from, lastRune := 0, -1
	byteAt := make([]int, 0, len(q))
	for i := 0; i < len(q); i++ {
		offset := strings.IndexByte(lower[from:], q[i])
		if offset < 0 {
			return []int{}
		}
		at := from + offset
		// The scan is byte-wise, so a multi-byte query character matches one
		// byte at a time. Its last byte is the one reported: that is where
		// the scan finished matching the character.
		if i+1 == len(q) || utf8.RuneStart(q[i+1]) {
			byteAt = append(byteAt, at)
		}
		from = at + 1
	}

	// Walk rel once, converting byte offsets to UTF-16 offsets as they come.
	next, units := 0, 0
	for pos := 0; pos < len(rel) && next < len(byteAt); {
		r, size := utf8.DecodeRuneInString(rel[pos:])
		for next < len(byteAt) && byteAt[next] < pos+size {
			if units != lastRune {
				positions = append(positions, units)
				lastRune = units
			}
			next++
		}
		units += utf16Len(r)
		pos += size
	}
	return positions
}

// utf16Len is how many UTF-16 code units r takes. An invalid byte decodes to
// U+FFFD, which is one unit, and that is also what a client sees in its place.
func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// utf16Offsets converts ascending byte offsets into s to UTF-16 offsets, in one
// pass over s. Offsets past the end clamp to its length.
func utf16Offsets(s string, byteOffsets []int) []int {
	out := make([]int, len(byteOffsets))
	next, units := 0, 0
	for pos := 0; pos <= len(s) && next < len(byteOffsets); {
		for next < len(byteOffsets) && byteOffsets[next] <= pos {
			out[next] = units
			next++
		}
		if pos == len(s) {
			break
		}
		r, size := utf8.DecodeRuneInString(s[pos:])
		units += utf16Len(r)
		pos += size
	}
	for ; next < len(byteOffsets); next++ {
		out[next] = units
	}
	return out
}
