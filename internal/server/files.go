package server

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
}

// handleListFiles lists entries in the directory at the given path query param.
func (s *PublicServer) handleListFiles(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, "missing path", http.StatusBadRequest)
		return
	}

	clean, err := resolveSafePath(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(clean)
	if err != nil {
		jsonError(w, "path not found", http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		jsonError(w, "path is not a directory", http.StatusBadRequest)
		return
	}

	result, err := listDir(clean)
	if err != nil {
		jsonError(w, "failed to read directory", http.StatusInternalServerError)
		return
	}

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
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, "missing path", http.StatusBadRequest)
		return
	}

	clean, err := resolveSafePath(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(clean)
	if err != nil {
		jsonError(w, "path not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		jsonError(w, "path is a directory", http.StatusBadRequest)
		return
	}

	if info.Size() > maxFileSize {
		jsonResponse(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":    "file_too_large",
			"message":  "file exceeds 10 MB server limit",
			"size":     info.Size(),
			"max_size": maxFileSize,
		})
		return
	}

	content, err := os.ReadFile(clean)
	if err != nil {
		jsonError(w, "failed to read file", http.StatusInternalServerError)
		return
	}

	// Reading a file is what subscribes to it. See filewatch.go.
	s.files().Watch(clean, WatchFile)

	body := map[string]interface{}{
		"path":     clean,
		"size":     info.Size(),
		"mod_time": info.ModTime().UTC().Format(time.RFC3339),
	}
	text, encoding := encodeFileContent(content, r.URL.Query().Get("encoding"))
	body["content"] = text
	if encoding != "" {
		body["encoding"] = encoding
	}
	jsonResponse(w, http.StatusOK, body)
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
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
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
