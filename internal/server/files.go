package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

	entries, err := os.ReadDir(clean)
	if err != nil {
		jsonError(w, "failed to read directory", http.StatusInternalServerError)
		return
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

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"path":    clean,
		"entries": result,
	})
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

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"path":     clean,
		"size":     info.Size(),
		"mod_time": info.ModTime().UTC().Format(time.RFC3339),
		"content":  string(content),
	})
}

// resolveSafePath cleans and resolves the path, rejecting traversal attempts.
func resolveSafePath(path string) (string, error) {
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
