package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// searchTimeout bounds one request: a search over an unexpectedly huge tree
	// returns what it found rather than holding the connection open.
	searchTimeout = 10 * time.Second
	// maxCandidates caps how many paths a single walk collects.
	maxCandidates = 100000
	// maxGrepFileSize skips the generated and vendored blobs nobody greps for.
	maxGrepFileSize    = 2 << 20
	maxGrepLineBytes   = 1 << 20
	defaultSearchLimit = 50
	defaultGrepLimit   = 200
	maxResultLimit     = 500
)

// skipDirs are the trees a person browsing source never means to search. Only
// the fallback walk consults this: when the root is a repository, git's own
// ignore rules decide instead.
var skipDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"out":          true,
	".next":        true,
	".nuxt":        true,
	".svelte-kit":  true,
	".dart_tool":   true,
	".gradle":      true,
	".idea":        true,
	".terraform":   true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	"Pods":         true,
	"DerivedData":  true,
	"coverage":     true,
}

type searchMatch struct {
	Path  string `json:"path"`
	Rel   string `json:"rel"`
	Score int    `json:"score"`
}

// handleSearchFiles ranks the file names under a root against a fuzzy query —
// the quick-open list. An empty query returns the head of the file list.
func (s *PublicServer) handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	root, ok := searchRoot(w, r)
	if !ok {
		return
	}
	limit := queryLimit(r, defaultSearchLimit)

	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()

	files, truncated := candidateFiles(ctx, root)
	matches := rankPaths(files, r.URL.Query().Get("q"), limit)
	for i := range matches {
		matches[i].Path = filepath.Join(root, matches[i].Rel)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"root":      root,
		"matches":   matches,
		"scanned":   len(files),
		"truncated": truncated,
	})
}

type grepMatch struct {
	Path   string `json:"path"`
	Rel    string `json:"rel"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

// handleGrepFiles searches file contents under a root — find-in-files. The
// query is a literal unless regex=true; matching is case-insensitive unless
// case=true.
func (s *PublicServer) handleGrepFiles(w http.ResponseWriter, r *http.Request) {
	root, ok := searchRoot(w, r)
	if !ok {
		return
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		jsonError(w, "missing q", http.StatusBadRequest)
		return
	}
	limit := queryLimit(r, defaultGrepLimit)
	opts := grepOptions{
		query:         query,
		regex:         r.URL.Query().Get("regex") == "true",
		caseSensitive: r.URL.Query().Get("case") == "true",
		limit:         limit,
	}

	// Reject a bad pattern before the search rather than after: ripgrep would
	// only report it on stderr, and the fallback needs the compiled form anyway.
	matcher, err := compileMatcher(opts)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()

	matches, truncated, ok := ripgrep(ctx, root, opts)
	if !ok {
		matches, truncated = scanFiles(ctx, root, matcher, limit)
	}
	for i := range matches {
		matches[i].Path = filepath.Join(root, matches[i].Rel)
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"root":      root,
		"matches":   matches,
		"truncated": truncated,
	})
}

type writeFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// BaseModTime is the mod time the editor last saw. When it no longer
	// matches, someone else — usually the agent — has written the file since,
	// and the save is refused instead of silently discarding their work.
	BaseModTime string `json:"base_mod_time"`
}

// handleWriteFile replaces the content of a single existing-or-new file.
func (s *PublicServer) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	var req writeFileRequest
	body := http.MaxBytesReader(w, r.Body, maxFileSize+64*1024)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		jsonError(w, "missing path", http.StatusBadRequest)
		return
	}
	if len(req.Content) > maxFileSize {
		jsonResponse(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":    "file_too_large",
			"message":  "file exceeds 10 MB server limit",
			"size":     len(req.Content),
			"max_size": maxFileSize,
		})
		return
	}

	clean, err := resolveSafePath(req.Path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	mode := os.FileMode(0o644)
	info, statErr := os.Stat(clean)
	switch {
	case statErr == nil && info.IsDir():
		jsonError(w, "path is a directory", http.StatusBadRequest)
		return
	case statErr == nil:
		mode = info.Mode().Perm()
		if stale(req.BaseModTime, info) {
			jsonResponse(w, http.StatusConflict, map[string]interface{}{
				"error":    "stale_write",
				"message":  "file changed on disk since it was read",
				"mod_time": info.ModTime().UTC().Format(time.RFC3339Nano),
			})
			return
		}
	case !errors.Is(statErr, os.ErrNotExist):
		jsonError(w, "failed to stat file", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(clean, []byte(req.Content), mode); err != nil {
		jsonError(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	written, err := os.Stat(clean)
	if err != nil {
		jsonError(w, "failed to stat file", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"path":     clean,
		"size":     written.Size(),
		"mod_time": written.ModTime().UTC().Format(time.RFC3339Nano),
	})
}

// stale reports whether the file has been written since the editor read it.
func stale(base string, info os.FileInfo) bool {
	if base == "" {
		return false
	}
	seen, err := time.Parse(time.RFC3339Nano, base)
	if err != nil {
		return false
	}
	// Some filesystems keep whole seconds only, so compare at that resolution.
	return !seen.Truncate(time.Second).Equal(info.ModTime().UTC().Truncate(time.Second))
}

// searchRoot resolves the path query param and confirms it is a directory.
func searchRoot(w http.ResponseWriter, r *http.Request) (string, bool) {
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, "missing path", http.StatusBadRequest)
		return "", false
	}
	clean, err := resolveSafePath(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	info, err := os.Stat(clean)
	if err != nil {
		jsonError(w, "path not found", http.StatusNotFound)
		return "", false
	}
	if !info.IsDir() {
		jsonError(w, "path is not a directory", http.StatusBadRequest)
		return "", false
	}
	return clean, true
}

func queryLimit(r *http.Request, fallback int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return fallback
	}
	if n > maxResultLimit {
		return maxResultLimit
	}
	return n
}

// candidateFiles lists files under root, relative to it. git is asked first so
// that .gitignore is honoured — the answer an editor's user expects — and a
// plain walk covers everything that is not a repository.
func candidateFiles(ctx context.Context, root string) ([]string, bool) {
	if files, ok := gitCandidates(ctx, root); ok {
		return files, len(files) >= maxCandidates
	}
	return walkCandidates(ctx, root)
}

func gitCandidates(ctx context.Context, root string) ([]string, bool) {
	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"ls-files", "--cached", "--others", "--exclude-standard", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	files := make([]string, 0, 256)
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		files = append(files, name)
		if len(files) >= maxCandidates {
			break
		}
	}
	return files, true
}

func walkCandidates(ctx context.Context, root string) ([]string, bool) {
	files := make([]string, 0, 256)
	truncated := false
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable corner of the tree is not worth failing the search.
			return nil
		}
		if ctx.Err() != nil {
			truncated = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		files = append(files, rel)
		if len(files) >= maxCandidates {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		truncated = true
	}
	return files, truncated
}

// rankPaths keeps the best `limit` fuzzy matches for query, best first.
func rankPaths(files []string, query string, limit int) []searchMatch {
	q := strings.ToLower(strings.Join(strings.Fields(query), ""))
	matches := make([]searchMatch, 0, limit)
	for _, rel := range files {
		score := 0
		if q != "" {
			var ok bool
			score, ok = fuzzyScore(rel, q)
			if !ok {
				continue
			}
		}
		matches = append(matches, searchMatch{Rel: rel, Score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if len(matches[i].Rel) != len(matches[j].Rel) {
			return len(matches[i].Rel) < len(matches[j].Rel)
		}
		return matches[i].Rel < matches[j].Rel
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// fuzzyScore matches query as a subsequence of text, rewarding hits in the file
// name, runs of adjacent characters, and matches at word boundaries. query must
// already be lowercase. The scan is greedy rather than optimal: it runs over
// every path in the tree on each keystroke.
func fuzzyScore(text, query string) (int, bool) {
	lower := strings.ToLower(text)
	nameStart := strings.LastIndexByte(lower, '/') + 1
	score, from, prev := 0, 0, -2
	for i := 0; i < len(query); i++ {
		offset := strings.IndexByte(lower[from:], query[i])
		if offset < 0 {
			return 0, false
		}
		at := from + offset
		switch {
		case at == prev+1:
			score += 15
		case at == 0 || isWordBoundary(lower[at-1]):
			score += 10
		default:
			score++
		}
		if at >= nameStart {
			score += 12
		}
		prev, from = at, at+1
	}
	// A short path that matched beats a long one that happened to contain the
	// same letters spread across its directories.
	return score - len(text)/12, true
}

func isWordBoundary(c byte) bool {
	switch c {
	case '/', '_', '-', '.', ' ':
		return true
	}
	return false
}

type grepOptions struct {
	query         string
	regex         bool
	caseSensitive bool
	limit         int
}

// compileMatcher turns the request into a pattern the fallback scan can run,
// and validates a user-supplied regex on the way.
func compileMatcher(opts grepOptions) (*regexp.Regexp, error) {
	pattern := opts.query
	if !opts.regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !opts.caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	return re, nil
}

// ripgrep runs rg when it is installed, reporting whether it could be used at
// all. It is worth preferring: on a large tree it is an order of magnitude
// faster than the fallback and it already knows about .gitignore.
func ripgrep(ctx context.Context, root string, opts grepOptions) ([]grepMatch, bool, bool) {
	bin, err := exec.LookPath("rg")
	if err != nil {
		return nil, false, false
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{"--json", "--no-messages", "--max-filesize", strconv.Itoa(maxGrepFileSize)}
	if opts.caseSensitive {
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	if !opts.regex {
		args = append(args, "--fixed-strings")
	}
	args = append(args, "--regexp", opts.query, "--", ".")

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false, false
	}

	matches, truncated := readRipgrepJSON(stdout, opts.limit)

	// Killing rg once the cap is hit makes Wait report the signal, and rg exits
	// 1 when nothing matched; neither is a failure worth falling back over.
	cancel()
	waitErr := cmd.Wait()
	if waitErr != nil && !truncated && len(matches) == 0 {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 {
			return nil, false, false
		}
	}
	return matches, truncated, true
}

// ripgrepEvent is the slice of rg's --json stream this cares about.
type ripgrepEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       struct{ Text string } `json:"path"`
		Lines      struct{ Text string } `json:"lines"`
		LineNumber int                   `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
		} `json:"submatches"`
	} `json:"data"`
}

func readRipgrepJSON(src io.Reader, limit int) ([]grepMatch, bool) {
	matches := make([]grepMatch, 0, limit)
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), maxGrepLineBytes)
	for scanner.Scan() {
		var event ripgrepEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "match" {
			continue
		}
		// A path or line rg could not decode as UTF-8 arrives base64 in a
		// different field; skipping it is better than showing mojibake.
		if event.Data.Path.Text == "" || event.Data.Lines.Text == "" {
			continue
		}
		column := 1
		if len(event.Data.Submatches) > 0 {
			column = event.Data.Submatches[0].Start + 1
		}
		matches = append(matches, grepMatch{
			Rel:    strings.TrimPrefix(event.Data.Path.Text, "./"),
			Line:   event.Data.LineNumber,
			Column: column,
			Text:   trimMatchLine(event.Data.Lines.Text),
		})
		if len(matches) >= limit {
			return matches, true
		}
	}
	return matches, false
}

// scanFiles is the search for hosts without ripgrep: the same walk quick open
// uses, read line by line.
func scanFiles(ctx context.Context, root string, re *regexp.Regexp, limit int) ([]grepMatch, bool) {
	files, truncated := candidateFiles(ctx, root)
	matches := make([]grepMatch, 0, limit)
	for _, rel := range files {
		if ctx.Err() != nil {
			return matches, true
		}
		found, capped := scanFile(filepath.Join(root, rel), rel, re, limit-len(matches))
		matches = append(matches, found...)
		if capped || len(matches) >= limit {
			return matches, true
		}
	}
	return matches, truncated
}

func scanFile(path, rel string, re *regexp.Regexp, room int) ([]grepMatch, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxGrepFileSize {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	if head, err := reader.Peek(8000); err == nil || len(head) > 0 {
		if bytes.IndexByte(head, 0) >= 0 {
			return nil, false
		}
	}

	var matches []grepMatch
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxGrepLineBytes)
	for line := 1; scanner.Scan(); line++ {
		loc := re.FindIndex(scanner.Bytes())
		if loc == nil {
			continue
		}
		matches = append(matches, grepMatch{
			Rel:    rel,
			Line:   line,
			Column: loc[0] + 1,
			Text:   trimMatchLine(scanner.Text()),
		})
		if len(matches) >= room {
			return matches, true
		}
	}
	return matches, false
}

// trimMatchLine keeps a result row to something a list can show.
func trimMatchLine(text string) string {
	text = strings.TrimRight(text, "\r\n")
	if len(text) > 400 {
		return text[:400]
	}
	return text
}
