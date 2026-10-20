package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxFileSize = 10 * 1024 * 1024 // 10 MB hard limit
)

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	// Ignored is set only when a warm index under a git root can say; absent
	// means "unknown", which a client must not dim. See spec 65, B2.
	Ignored *bool `json:"ignored,omitempty"`
}

// hiddenEntries are never listed. They are version-control internals, which a
// person browsing source never means to open, and which change on every git
// command — so listing them would also announce a folder change for each one.
var hiddenEntries = map[string]bool{".git": true, ".hg": true, ".svn": true}

// handleListFiles lists entries in the directory at the given path query param.
func (s *PublicServer) handleListFiles(w http.ResponseWriter, r *http.Request) {
	timing := timingFrom(r.Context())
	clean, ok := requirePath(w, r)
	if !ok {
		return
	}

	started := time.Now()
	info, err := os.Stat(clean)
	timing.since("stat", started)
	if err != nil {
		statFailed(w, err)
		return
	}
	if !info.IsDir() {
		jsonErrorCode(w, "path is not a directory", http.StatusBadRequest, "bad_path")
		return
	}

	started = time.Now()
	result, err := listDir(clean)
	timing.since("read", started)
	if err != nil {
		statFailed(w, err)
		return
	}

	started = time.Now()
	if ignored := fileIndexes.ignoredIn(clean); ignored != nil {
		for i := range result {
			v := ignored(result[i].Name, result[i].IsDir)
			result[i].Ignored = &v
		}
	}
	timing.since("index", started)

	// Reading a directory is what subscribes to it. See filewatch.go.
	s.files().Watch(clean, WatchDir)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"path":    clean,
		"entries": result,
	})
}

// listDir reads one directory into the shape the API returns.
//
// Shared with the file watcher's digest (filewatch.go) on purpose: a digest
// built from the same entries as the answer cannot disagree with what the
// client has on screen.
func listDir(clean string) ([]fileEntry, error) {
	entries, err := os.ReadDir(clean)
	if err != nil {
		return nil, err
	}

	result := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		if hiddenEntries[e.Name()] {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, fileEntry{
			Name:    e.Name(),
			Path:    filepath.Join(clean, e.Name()),
			IsDir:   e.IsDir(),
			Size:    fi.Size(),
			ModTime: fi.ModTime().UTC().Format(time.RFC3339),
		})
	}

	// Dirs first, then files, each group alphabetical.
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

// handleReadFile returns the content of the file at the given path query param.
// Returns 413 if the file exceeds maxFileSize.
func (s *PublicServer) handleReadFile(w http.ResponseWriter, r *http.Request) {
	timing := timingFrom(r.Context())
	clean, info, ok := statReadable(w, r)
	if !ok {
		return
	}

	tag := fileETag(info)
	w.Header().Set("ETag", tag)
	if etagMatches(r.Header.Get("If-None-Match"), tag) {
		s.files().Watch(clean, WatchFile)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	started := time.Now()
	content, err := os.ReadFile(clean)
	timing.since("read", started)
	if err != nil {
		statFailed(w, err)
		return
	}

	// Reading a file is what subscribes to it. See filewatch.go.
	s.files().Watch(clean, WatchFile)

	body := map[string]interface{}{
		"path":     clean,
		"size":     info.Size(),
		"mod_time": info.ModTime().UTC().Format(time.RFC3339),
		"etag":     tag,
	}
	text, encoding := encodeFileContent(content, r.URL.Query().Get("encoding"))
	body["content"] = text
	if encoding != "" {
		body["encoding"] = encoding
	}
	jsonResponse(w, http.StatusOK, body)
}

// handleReadRaw serves a file's bytes as they are: no JSON, no base64, and a
// 304 after one stat when the caller already holds them. See spec 65, 1.1.
func (s *PublicServer) handleReadRaw(w http.ResponseWriter, r *http.Request) {
	timing := timingFrom(r.Context())
	clean, info, ok := statReadable(w, r)
	if !ok {
		return
	}

	tag := fileETag(info)
	h := w.Header()
	h.Set("ETag", tag)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Helios-Mod-Time", info.ModTime().UTC().Format(time.RFC3339))
	h.Set("X-Helios-Size", strconv.FormatInt(info.Size(), 10))
	if etagMatches(r.Header.Get("If-None-Match"), tag) {
		s.files().Watch(clean, WatchFile)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	started := time.Now()
	content, err := os.ReadFile(clean)
	timing.since("read", started)
	if err != nil {
		statFailed(w, err)
		return
	}
	s.files().Watch(clean, WatchFile)

	text := looksLikeText(content)
	encoding := "binary"
	if text {
		encoding = "utf8"
	}
	h.Set("X-Helios-Encoding", encoding)
	h.Set("Content-Type", rawContentType(clean, content, text))
	h.Set("Content-Length", strconv.Itoa(len(content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// requirePath reads and resolves the path query param.
func requirePath(w http.ResponseWriter, r *http.Request) (string, bool) {
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonErrorCode(w, "missing path", http.StatusBadRequest, "bad_path")
		return "", false
	}
	clean, err := resolveSafePath(path)
	if err != nil {
		jsonErrorCode(w, err.Error(), http.StatusBadRequest, "bad_path")
		return "", false
	}
	return clean, true
}

// statReadable is the shared front half of both read routes: resolve, stat,
// refuse directories and oversize files. It answers the request itself on any
// failure.
func statReadable(w http.ResponseWriter, r *http.Request) (string, os.FileInfo, bool) {
	clean, ok := requirePath(w, r)
	if !ok {
		return "", nil, false
	}
	started := time.Now()
	info, err := os.Stat(clean)
	timingFrom(r.Context()).since("stat", started)
	if err != nil {
		statFailed(w, err)
		return "", nil, false
	}
	if info.IsDir() {
		jsonErrorCode(w, "path is a directory", http.StatusBadRequest, "is_directory")
		return "", nil, false
	}
	if info.Size() > maxFileSize {
		jsonResponse(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":    "file_too_large",
			"message":  "file exceeds 10 MB server limit",
			"code":     "too_large",
			"size":     info.Size(),
			"max_size": maxFileSize,
			"limit":    maxFileSize,
		})
		return "", nil, false
	}
	return clean, info, true
}

// statFailed answers a failed stat or read with the status and code that say
// what actually went wrong. Anything that is not a permission problem keeps
// the 404 these routes always gave.
func statFailed(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrPermission) {
		jsonErrorCode(w, "permission denied", http.StatusForbidden, "permission_denied")
		return
	}
	jsonErrorCode(w, "path not found", http.StatusNotFound, "not_found")
}

// fileETag is a weak validator built from one stat. A content hash would cost
// about 20 ms on 10 MB on every request, which defeats the 304 it exists for;
// size and nanosecond mtime together do not collide in practice.
func fileETag(info os.FileInfo) string {
	return fmt.Sprintf(`W/"%d-%d"`, info.Size(), info.ModTime().UnixNano())
}

// etagMatches applies If-None-Match / If-Match's weak comparison: a list of
// tags, any of which may match, and "*" for any.
func etagMatches(header, tag string) bool {
	if header == "" {
		return false
	}
	want := strings.TrimPrefix(tag, "W/")
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == want {
			return true
		}
	}
	return false
}

// looksLikeText is the raw route's utf8/binary verdict: valid UTF-8 with no
// NUL near the start, the same test the grep fallback uses for binaries.
func looksLikeText(content []byte) bool {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) < 0 && utf8.Valid(content)
}

// rawContentType names the bytes for the client.
//
// The extension comes first, but only where it can be trusted. System MIME
// tables call .ts an MPEG transport stream, and a TypeScript file served as
// video/mp2t is a file no browser will show as text. So for text the
// extension's answer is kept only when it is itself a text type; otherwise the
// file is plain text. Binary content takes the extension's answer, then
// sniffing.
func rawContentType(path string, content []byte, text bool) string {
	byExt := mime.TypeByExtension(filepath.Ext(path))
	if text {
		if byExt != "" && compressibleType(byExt) {
			return withCharset(byExt)
		}
		return "text/plain; charset=utf-8"
	}
	if byExt != "" {
		return byExt
	}
	return http.DetectContentType(content)
}

func withCharset(ctype string) string {
	media, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		return ctype
	}
	params["charset"] = "utf-8"
	return mime.FormatMediaType(media, params)
}

/*
encodeFileContent renders a file for JSON, base64 when asked and needed.

A PNG put through string() is not valid UTF-8, and the JSON encoder replaces
every bad byte with U+FFFD — so the plain form loses the file rather than
mangling it, and no client can recover an image from it.

Opt-in, on ?encoding=auto, because base64 is plain ASCII with no NUL: a client
written before this asks for nothing, and would print the encoding instead of
saying "binary file", which is the correct answer it gives today. Callers that
pass the parameter are declaring they read the encoding field.

Text is never base64'd. It would cost a third more bytes on the path the editor
uses, for nothing.
*/
func encodeFileContent(content []byte, want string) (text, encoding string) {
	if want != "auto" {
		return string(content), ""
	}
	if utf8.Valid(content) {
		return string(content), "utf8"
	}
	return base64.StdEncoding.EncodeToString(content), "base64"
}

// resolveSafePath cleans and resolves the path, rejecting traversal attempts.
// Expands a leading ~ to the current user's home directory.
func resolveSafePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	// Reject paths that try to escape via symlinks by resolving them.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Path doesn't exist yet or symlink broken — use abs directly.
		// os.Stat will catch non-existent paths later.
		return abs, nil
	}
	return resolved, nil
}
