package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// ServeFrontend returns a handler that serves the embedded frontend files
// with SPA fallback (non-API/non-hook routes fall back to index.html).
func ServeFrontend(frontendFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(frontendFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Skip API and hook routes
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/hooks/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the file directly
		if path != "/" {
			// Check if file exists
			cleanPath := strings.TrimPrefix(path, "/")
			if f, err := frontendFS.Open(cleanPath); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: serve index.html for all other routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
