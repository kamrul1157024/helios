package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestFuzzyScorePrefersFileNameMatches(t *testing.T) {
	deep, ok := fuzzyScore(newIndexEntry("internal/server/files.go"), "files")
	if !ok {
		t.Fatal("expected a match on the file name")
	}
	scattered, ok := fuzzyScore(newIndexEntry("fixtures/inline/lib/e/system.go"), "files")
	if !ok {
		t.Fatal("expected a scattered subsequence match")
	}
	if deep <= scattered {
		t.Fatalf("name match scored %d, scattered match %d", deep, scattered)
	}

	if _, ok := fuzzyScore(newIndexEntry("internal/server/files.go"), "zzz"); ok {
		t.Fatal("expected no match")
	}
}

func TestRankEntriesOrdersAndLimits(t *testing.T) {
	files := []string{
		"internal/server/files.go",
		"internal/server/filesearch.go",
		"docs/specs/31-desktop-app.md",
		"desktop/src/renderer/components/files.tsx",
	}
	entries := newIndexEntries(files)
	matches := rankEntries(entries, "files.go", 2)
	if len(matches) != 2 {
		t.Fatalf("limit ignored: got %d matches", len(matches))
	}
	if matches[0].Rel != "internal/server/files.go" {
		t.Fatalf("best match was %q", matches[0].Rel)
	}

	// An empty query is the quick-open list before anyone types.
	if all := rankEntries(entries, "", 10); len(all) != len(files) {
		t.Fatalf("empty query returned %d of %d files", len(all), len(files))
	}
}

// The mask must never reject something the scan would have matched. A
// disagreement is invisible in normal use: the row simply does not appear.
func TestMaskNeverRejectsARealMatch(t *testing.T) {
	files := []string{
		"internal/server/files.go",
		"docs/specs/57-vim-mode.md",
		"desktop/src/renderer/components/quick-open.tsx",
		"mobile/lib/screens/FileBrowser.dart",
		"README.md",
	}
	entries := newIndexEntries(files)
	queries := []string{"", "f", "vimmd", "57vim", "quickopen", "readme", "FILES.GO", "browser", "zzz", "/", "-", "_"}

	for _, query := range queries {
		survivors := map[string]bool{}
		for _, match := range rankEntries(entries, query, len(files)) {
			survivors[match.Rel] = true
		}
		q := strings.ToLower(query)
		for _, entry := range entries {
			if _, ok := fuzzyScore(entry, q); ok && !survivors[entry.rel] {
				t.Fatalf("query %q: mask rejected %q, which the scan matches", query, entry.rel)
			}
		}
	}
}

func TestWalkCandidatesSkipsHeavyDirs(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "main.go"), "package main")
	write(t, filepath.Join(root, "pkg", "util.go"), "package pkg")
	write(t, filepath.Join(root, "node_modules", "left-pad", "index.js"), "module.exports = 1")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main")

	files, truncated := walkCandidates(context.Background(), root)
	if truncated {
		t.Fatal("small tree reported as truncated")
	}
	got := strings.Join(files, ",")
	for _, skipped := range []string{"node_modules", ".git"} {
		if strings.Contains(got, skipped) {
			t.Fatalf("%s was walked: %s", skipped, got)
		}
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 source files, got %v", files)
	}
}

func TestScanFilesFindsMatchesAndSkipsBinaries(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a\nconst Marker = 1\n")
	write(t, filepath.Join(root, "b.bin"), "\x00\x01marker\x00")

	matches, truncated := scanFiles(context.Background(), root, regexp.MustCompile("(?i)marker"), 10)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %+v", matches)
	}
	if matches[0].Rel != "a.go" || matches[0].Line != 2 || matches[0].Column != 7 {
		t.Fatalf("wrong location: %+v", matches[0])
	}
	if matches[0].Text != "const Marker = 1" {
		t.Fatalf("wrong text: %q", matches[0].Text)
	}
}

func TestScanFilesStopsAtLimit(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "hit\nhit\nhit\n")

	matches, truncated := scanFiles(context.Background(), root, regexp.MustCompile("hit"), 2)
	if len(matches) != 2 || !truncated {
		t.Fatalf("expected 2 matches and truncation, got %d / %v", len(matches), truncated)
	}
}

func TestSearchFilesEndpoint(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "internal", "server", "files.go"), "package server")
	write(t, filepath.Join(root, "README.md"), "# hi")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {root},
		"q":    {"filesgo"},
	})
	matches := body["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %v", matches)
	}
	first := matches[0].(map[string]interface{})
	if first["rel"] != "internal/server/files.go" {
		t.Fatalf("wrong rel: %v", first["rel"])
	}
	// The server reports the resolved root — on macOS a temp dir sits behind a
	// symlink — so the absolute path is checked against that, not against root.
	if first["path"] != filepath.Join(body["root"].(string), "internal", "server", "files.go") {
		t.Fatalf("path is not absolute: %v", first["path"])
	}
}

// An absolute path is what someone pastes out of a chat message or a stack
// trace. Before pathQuery it matched nothing at all: no candidate is absolute,
// so every one failed on the leading separator.
func TestSearchFilesResolvesAnAbsolutePath(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs", "specs", "57-vim-mode.md"), "# vim")
	write(t, filepath.Join(root, "docs", "specs", "56-versions.md"), "# versions")
	write(t, filepath.Join(root, "internal", "server", "files.go"), "package server")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {root},
		"q":    {filepath.Join(root, "docs", "specs", "57-vim")},
	})
	if body["resolved_from"] != "path" {
		t.Fatalf("expected the path rule to fire, got %v", body["resolved_from"])
	}
	matches := body["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %v", matches)
	}
	// Re-rooted at the directory that was named, so rel is relative to it.
	if first := matches[0].(map[string]interface{}); first["rel"] != "57-vim-mode.md" {
		t.Fatalf("wrong rel: %v", first["rel"])
	}
	// The search space is the named directory, not the whole tree.
	if scanned := body["scanned"].(float64); scanned != 2 {
		t.Fatalf("expected to scan the two files in docs/specs, scanned %v", scanned)
	}
}

func TestSearchFilesResolvesATildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, "notes", "todo.md"), "- thing")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {home},
		"q":    {"~/notes/todo"},
	})
	if body["resolved_from"] != "path" {
		t.Fatalf("expected the path rule to fire, got %v", body["resolved_from"])
	}
	matches := body["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %v", matches)
	}
	if first := matches[0].(map[string]interface{}); first["rel"] != "todo.md" {
		t.Fatalf("wrong rel: %v", first["rel"])
	}
}

// A trailing separator names the directory itself and nothing inside it yet.
func TestSearchFilesListsADirectoryForAnEmptyTail(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs", "a.md"), "a")
	write(t, filepath.Join(root, "docs", "b.md"), "b")
	write(t, filepath.Join(root, "other", "c.md"), "c")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {root},
		"q":    {filepath.Join(root, "docs") + "/"},
	})
	if body["resolved_from"] != "path" {
		t.Fatalf("expected the path rule to fire, got %v", body["resolved_from"])
	}
	if matches := body["matches"].([]interface{}); len(matches) != 2 {
		t.Fatalf("expected the two files in docs, got %v", matches)
	}
}

// Half a path is the normal state of a field being typed into, and it must not
// blank the list: fall back to fuzzy rather than reporting nothing.
func TestSearchFilesFallsBackWhenTheDirectoryIsNotReal(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "internal", "server", "files.go"), "package server")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {root},
		"q":    {"/no/such/place/filesgo"},
	})
	if body["resolved_from"] != "query" {
		t.Fatalf("expected a fuzzy fallback, got %v", body["resolved_from"])
	}
	if matches := body["matches"].([]interface{}); len(matches) != 0 {
		t.Fatalf("a path naming nothing should match nothing, got %v", matches)
	}
}

// Agents write paths relative to the directory they are working in, which is
// not always the session's. The directory then resolves and holds nothing, and
// the name is the only usable part left.
func TestSearchFilesFallsBackToTheNameWhenTheDirectoryHasNoSuchFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs", "empty.md"), "nothing here")
	write(t, filepath.Join(root, "workspace", "helios", "docs", "specs", "57-vim-mode.md"), "# vim")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {root},
		// docs/ exists but does not hold the file: the path is wrong, the name is not.
		"q": {filepath.Join(root, "docs", "57-vim-mode.md")},
	})
	if body["resolved_from"] != "query" {
		t.Fatalf("expected the fallback to report a fuzzy search, got %v", body["resolved_from"])
	}
	matches := body["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected the name to be found under the root, got %v", matches)
	}
	first := matches[0].(map[string]interface{})
	if first["rel"] != "workspace/helios/docs/specs/57-vim-mode.md" {
		t.Fatalf("wrong rel: %v", first["rel"])
	}
}

// The guard that keeps the change from swallowing ordinary queries: a relative
// path with separators is how people fuzzy-match today and must stay fuzzy.
func TestSearchFilesKeepsRelativeQueriesFuzzy(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "internal", "terminal", "host.go"), "package terminal")
	write(t, filepath.Join(root, "internal", "terminal", "session.go"), "package terminal")

	body := getJSON(t, (&PublicServer{}).handleSearchFiles, url.Values{
		"path": {root},
		"q":    {"internal/terminal/host"},
	})
	if body["resolved_from"] != "query" {
		t.Fatalf("a relative query must stay fuzzy, got %v", body["resolved_from"])
	}
	matches := body["matches"].([]interface{})
	if len(matches) == 0 {
		t.Fatal("expected the fuzzy matcher to find host.go")
	}
	if first := matches[0].(map[string]interface{}); first["rel"] != "internal/terminal/host.go" {
		t.Fatalf("wrong first match: %v", first["rel"])
	}
}

func TestGrepFilesEndpoint(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a\n// findMe here\n")

	body := getJSON(t, (&PublicServer{}).handleGrepFiles, url.Values{
		"path": {root},
		"q":    {"findme"},
	})
	matches := body["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %v", matches)
	}
	first := matches[0].(map[string]interface{})
	if first["line"].(float64) != 2 || first["rel"] != "a.go" {
		t.Fatalf("wrong match: %v", first)
	}
}

func TestGrepFilesRejectsBadRegex(t *testing.T) {
	root := t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/api/files/grep?path="+url.QueryEscape(root)+"&q=%5B&regex=true", nil)
	rec := httptest.NewRecorder()
	(&PublicServer{}).handleGrepFiles(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWriteFileRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	write(t, path, "before")

	body := putJSON(t, map[string]string{"path": path, "content": "after"}, http.StatusOK)
	if body["size"].(float64) != 5 {
		t.Fatalf("wrong size: %v", body["size"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "after" {
		t.Fatalf("file holds %q", content)
	}
}

func TestWriteFileRefusesStaleSave(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	write(t, path, "before")

	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	body := putJSON(t, map[string]string{
		"path": path, "content": "after", "base_mod_time": stale,
	}, http.StatusConflict)
	if body["error"] != "stale_write" {
		t.Fatalf("wrong error: %v", body)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "before" {
		t.Fatalf("file was overwritten: %q", content)
	}
}

func TestWriteFileAcceptsMatchingModTime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	write(t, path, "before")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	putJSON(t, map[string]string{
		"path":          path,
		"content":       "after",
		"base_mod_time": info.ModTime().UTC().Format(time.RFC3339Nano),
	}, http.StatusOK)
}

func TestWriteFileRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	putJSON(t, map[string]string{"path": root, "content": "x"}, http.StatusBadRequest)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, handler http.HandlerFunc, query url.Values) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
	return body
}

func putJSON(t *testing.T, payload map[string]string, want int) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/file", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	(&PublicServer{}).handleWriteFile(rec, req)
	if rec.Code != want {
		t.Fatalf("status %d (want %d): %s", rec.Code, want, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
	return body
}
