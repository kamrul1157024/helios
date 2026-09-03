package server

import (
	"net/http"
	"strings"
)

// The assets a release carries. The path is user input, and only these five
// names become a URL this daemon will fetch.
var releaseAssets = map[string]bool{
	"helios.apk":                           true,
	"helios-desktop-macos-arm64.dmg":       true,
	"helios-desktop-macos-x64.dmg":         true,
	"helios-desktop-linux-x86_64.AppImage": true,
	"helios-desktop-linux-amd64.deb":       true,
}

// handleDownload sends the browser to the release asset on GitHub's CDN.
//
// Not a github.com link on the page, because Android hands those to the GitHub
// app: the app opens on the release page, and a tap that was meant to start a
// download does not. GitHub answers a request for an asset with a redirect to
// release-assets.githubusercontent.com, which no app claims — so that redirect is
// followed here and its destination given to the browser. The bytes still come
// from GitHub; only the address the browser is asked to visit does not say so.
func (s *PublicServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	if !releaseAssets[name] {
		http.NotFound(w, r)
		return
	}

	target := releaseAsset(latestRelease.get(r.Context()), name)
	if cdn := resolveDownloadURL(r, target); cdn != "" {
		target = cdn
	}
	// Found, not moved permanently: the CDN's URL is signed and expires, and the
	// release it belongs to changes.
	http.Redirect(w, r, target, http.StatusFound)
}

// Swapped in tests, which have no business reaching GitHub.
var resolveDownloadURL = resolveDownload

// resolveDownload asks GitHub where an asset really lives, without following
// the answer. An empty string means the caller should use the github.com URL as
// it is: one more redirect for the browser, and the download still works.
func resolveDownload(r *http.Request, url string) string {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return ""
	}

	// The default client follows redirects, which would download the asset here
	// rather than say where it is.
	client := &http.Client{
		Timeout: latestRelease.client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || !strings.HasPrefix(location, "https://") {
		return ""
	}
	return location
}
