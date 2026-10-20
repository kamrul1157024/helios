package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

// referenceRank is the ranking as it was before the heap: score every entry,
// sort them all, cut. The heap must agree with it exactly.
func referenceRank(entries []indexEntry, query string, limit int) []string {
	q := normalizeQuery(query)
	var all []ranked
	for i := range entries {
		score, ok := 0, true
		if q != "" {
			score, ok = fuzzyScore(entries[i], q)
		}
		if ok {
			all = append(all, ranked{score: score, entry: &entries[i]})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return better(all[i], all[j]) })
	if len(all) > limit {
		all = all[:limit]
	}
	rels := make([]string, len(all))
	for i, r := range all {
		rels[i] = r.entry.rel
	}
	return rels
}

func rels(matches []searchMatch) []string {
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.Rel
	}
	return out
}

// Above parallelRankMin the scan splits across goroutines; the answer must not
// depend on how it was split.
func TestRankHeapMatchesAFullSort(t *testing.T) {
	for _, n := range []int{500, parallelRankMin + 5000} {
		entries := newIndexEntries(syntheticPaths(n))
		for _, q := range []string{"", "f", "srv", "file_0001", "filesrvg", "zzz"} {
			for _, limit := range []int{1, 50, 500} {
				got, want := rels(rankEntries(entries, q, limit)), referenceRank(entries, q, limit)
				if len(want) == 0 {
					want = []string{}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("n=%d q=%q limit=%d:\n got %v\nwant %v", n, q, limit, got, want)
				}
			}
		}
	}
}

// Each position must point at a character of the query, in order, in the
// string the client holds — which indexes by UTF-16 code unit.
func TestPositionsPointAtTheQuery(t *testing.T) {
	entries := newIndexEntries([]string{
		"internal/server/files.go",
		"desktop/src/renderer/components/FileTree.tsx",
		"docs/café/Ünïcode-notes.md",
		"emoji/😀/smile_test.go",
		"K\u212Aelvin/readme.md",
	})
	for _, q := range []string{"files", "ft", "notes", "smile", "readme", "srv go", "ü"} {
		for _, m := range rankEntries(entries, q, 10) {
			units := utf16.Encode([]rune(m.Rel))
			want := []rune(normalizeQuery(q))
			if len(m.Positions) != len(want) {
				t.Fatalf("%q in %q: %d positions for %d query characters", q, m.Rel, len(m.Positions), len(want))
			}
			prev := -1
			for i, pos := range m.Positions {
				if pos <= prev || pos >= len(units) {
					t.Fatalf("%q in %q: positions %v out of order or range", q, m.Rel, m.Positions)
				}
				prev = pos
				got := strings.ToLower(string(utf16.Decode(units[pos : pos+1])))
				if got != string(want[i]) {
					t.Fatalf("%q in %q: position %d is %q, want %q", q, m.Rel, pos, got, string(want[i]))
				}
			}
		}
	}
}

func TestPositionsSurviveJSON(t *testing.T) {
	matches := rankEntries(newIndexEntries([]string{"a.go"}), "zzz", 5)
	raw, err := json.Marshal(matches)
	if err != nil || string(raw) != "[]" {
		t.Fatalf("no matches marshals as %s (%v)", raw, err)
	}
	matches = rankEntries(newIndexEntries([]string{"a.go"}), "", 5)
	if raw, _ := json.Marshal(matches[0]); !strings.Contains(string(raw), `"positions":[]`) {
		t.Fatalf("empty query positions marshal as %s, want a list", raw)
	}
}

// Each range must cover text the pattern matches, in UTF-16 units.
func TestGrepRangesCoverTheMatch(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "café := Café + cafés\nnothing here\n")
	write(t, filepath.Join(root, "b_test.go"), "cafe test\n")
	write(t, filepath.Join(root, "vendor", "c.go"), "cafe vendored\n")

	cases := []struct {
		opts grepOptions
		want []string // matched texts, in order, across all matches
	}{
		{grepOptions{query: "café"}, []string{"café", "Café", "café"}},
		{grepOptions{query: "café", caseSensitive: true}, []string{"café", "café"}},
		{grepOptions{query: "caf[eé]s?", regex: true, word: true}, []string{"café", "Café", "cafés", "cafe"}},
	}
	for _, c := range cases {
		re, err := compileMatcher(c.opts)
		if err != nil {
			t.Fatal(err)
		}
		matches, _ := scanFiles(context.Background(), root, re, mustGlobs(t, "!vendor"), 50)
		var got []string
		for _, m := range matches {
			units := utf16.Encode([]rune(m.Text))
			for _, r := range m.Ranges {
				got = append(got, string(utf16.Decode(units[r[0]:r[1]])))
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: matched %q, want %q", c.opts, got, c.want)
		}
	}
}

func mustGlobs(t *testing.T, raw string) *globFilter {
	t.Helper()
	f, err := compileGlobs(splitGlobs(raw))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGlobFilterFollowsRipgrep(t *testing.T) {
	cases := []struct {
		globs string
		rel   string
		want  bool
	}{
		{"*.go", "internal/server/files.go", true},
		{"*.go", "docs/a.md", false},
		{"*.go, !*_test.go", "internal/a_test.go", false},
		{"*.go, !*_test.go", "internal/a.go", true},
		{"!node_modules", "web/node_modules/x/index.js", false},
		{"!node_modules", "web/src/index.js", true},
		{"internal/**", "internal/server/a.go", true},
		{"internal/**", "cmd/internal.go", false},
		{"desktop/src", "desktop/src/app.tsx", true},
		{"*.{ts,tsx}", "a/b.tsx", true},
		{"*.{ts,tsx}", "a/b.js", false},
		{"", "anything", true},
	}
	for _, c := range cases {
		if got := mustGlobs(t, c.globs).allows(c.rel); got != c.want {
			t.Errorf("globs %q on %q = %v, want %v", c.globs, c.rel, got, c.want)
		}
	}
}

// rg reports byte offsets into the line; the client wants UTF-16 offsets into
// the trimmed text.
func TestRipgrepRangesAreUTF16(t *testing.T) {
	line := `{"type":"match","data":{"path":{"text":"./a.go"},"lines":{"text":"😀 café café\n"},"line_number":3,"submatches":[{"match":{"text":"café"},"start":5,"end":10},{"match":{"text":"café"},"start":11,"end":16}]}}`
	matches, _ := readRipgrepJSON(strings.NewReader(line+"\n"), 10)
	if len(matches) != 1 {
		t.Fatalf("got %d matches", len(matches))
	}
	want := [][2]int{{3, 7}, {8, 12}}
	if !reflect.DeepEqual(matches[0].Ranges, want) {
		t.Fatalf("ranges %v, want %v", matches[0].Ranges, want)
	}
}

func TestTrimMatchLineKeepsRunesWhole(t *testing.T) {
	text := strings.Repeat("a", 399) + "é tail"
	got := trimMatchLine(text)
	if len(got) != 399 {
		t.Fatalf("cut at %d bytes, want 399 (before the é)", len(got))
	}
	if !regexp.MustCompile(`^a+$`).MatchString(got) {
		t.Fatal("cut left half a rune")
	}
}
