package server

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed landing.html
var landingHTML string

// handleLanding serves the download landing page, pinned to the newest release
// when GitHub can be asked which that is.
func handleLanding(w http.ResponseWriter, r *http.Request) {
	page := renderLanding(latestRelease.get(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(page))
}

// renderLanding fills the page in for one release. An empty tag is the offline
// answer: the links fall back to GitHub's "latest" redirect and the page names
// no version, rather than naming one it cannot stand behind.
func renderLanding(tag string) string {
	version := ""
	if tag != "" {
		version = " · " + strings.TrimPrefix(tag, "v")
	}

	return strings.NewReplacer(
		"{{APK_URL}}", releaseAsset(tag, "helios.apk"),
		"{{MAC_ARM64_URL}}", releaseAsset(tag, "helios-desktop-macos-arm64.dmg"),
		"{{MAC_INTEL_URL}}", releaseAsset(tag, "helios-desktop-macos-x64.dmg"),
		"{{LINUX_APPIMAGE_URL}}", releaseAsset(tag, "helios-desktop-linux-x86_64.AppImage"),
		"{{LINUX_DEB_URL}}", releaseAsset(tag, "helios-desktop-linux-amd64.deb"),
		"{{VERSION}}", version,
	).Replace(landingHTML)
}
