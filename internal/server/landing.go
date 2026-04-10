package server

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed landing.html
var landingHTML string

// handleLanding serves the download landing page with injected URLs.
func handleLanding(w http.ResponseWriter, r *http.Request) {
	page := landingHTML
	page = strings.ReplaceAll(page, "{{APK_URL}}", APKDownloadURL)
	page = strings.ReplaceAll(page, "{{DMG_URL}}", DMGDownloadURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(page))
}
