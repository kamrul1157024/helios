package server

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed landing.html
var landingHTML string

var landingURLs = strings.NewReplacer(
	"{{APK_URL}}", APKDownloadURL,
	"{{MAC_ARM64_URL}}", MacArm64DownloadURL,
	"{{MAC_INTEL_URL}}", MacIntelDownloadURL,
	"{{LINUX_APPIMAGE_URL}}", LinuxAppImageDownloadURL,
	"{{LINUX_DEB_URL}}", LinuxDebDownloadURL,
)

// handleLanding serves the download landing page with injected URLs.
func handleLanding(w http.ResponseWriter, r *http.Request) {
	page := landingURLs.Replace(landingHTML)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(page))
}
