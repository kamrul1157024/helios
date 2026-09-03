package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDownloadRedirectsToTheCDN(t *testing.T) {
	latestRelease.mu.Lock()
	latestRelease.tag = "v3.9.0"
	latestRelease.expires = time.Now().Add(time.Hour)
	latestRelease.mu.Unlock()

	const cdn = "https://release-assets.githubusercontent.com/signed/helios.apk"
	asked := ""
	resolveDownloadURL = func(r *http.Request, url string) string {
		asked = url
		return cdn
	}
	defer func() { resolveDownloadURL = resolveDownload }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/download/helios.apk", nil)
	req.SetPathValue("asset", "helios.apk")
	(&PublicServer{}).handleDownload(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != cdn {
		t.Errorf("Location = %q, want %q", got, cdn)
	}
	if want := "https://github.com/kamrul1157024/helios/releases/download/v3.9.0/helios.apk"; asked != want {
		t.Errorf("resolved %q, want the pinned asset %q", asked, want)
	}
}

// GitHub unreachable: the browser gets the github.com URL and follows the
// redirect itself. One tap on Android may open the app, which is still better
// than a download that does not start.
func TestDownloadFallsBackToGitHub(t *testing.T) {
	resolveDownloadURL = func(*http.Request, string) string { return "" }
	defer func() { resolveDownloadURL = resolveDownload }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/download/helios.apk", nil)
	req.SetPathValue("asset", "helios.apk")
	(&PublicServer{}).handleDownload(rec, req)

	if got := rec.Header().Get("Location"); got == "" || got[:19] != "https://github.com/" {
		t.Errorf("Location = %q, want a github.com URL", got)
	}
}

// The asset name comes off the URL, and only the five a release carries turn
// into a request this daemon makes.
func TestDownloadRefusesAnAssetItDoesNotKnow(t *testing.T) {
	resolveDownloadURL = func(*http.Request, string) string {
		t.Error("fetched a URL for an unknown asset")
		return ""
	}
	defer func() { resolveDownloadURL = resolveDownload }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/download/../../etc/passwd", nil)
	req.SetPathValue("asset", "../../etc/passwd")
	(&PublicServer{}).handleDownload(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestResolveDownloadReadsTheRedirectWithoutFollowingIt(t *testing.T) {
	const cdn = "https://release-assets.githubusercontent.com/signed"
	served := false
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			w.Header().Set("Location", cdn)
			w.WriteHeader(http.StatusFound)
			return
		}
		served = true
	}))
	defer origin.Close()

	got := resolveDownload(httptest.NewRequest(http.MethodGet, "/", nil), origin.URL+"/asset")
	if got != cdn {
		t.Errorf("resolved %q, want %q", got, cdn)
	}
	if served {
		t.Error("the redirect was followed — the asset would download here")
	}
}

// A 200 is GitHub not redirecting, and an http:// Location is not something to
// send a browser to. Both mean: use the URL we already had.
func TestResolveDownloadRefusesAnythingButAnHTTPSRedirect(t *testing.T) {
	for _, answer := range []struct {
		name     string
		status   int
		location string
	}{
		{"no redirect", http.StatusOK, ""},
		{"plain http", http.StatusFound, "http://example.com/asset"},
	} {
		t.Run(answer.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if answer.location != "" {
					w.Header().Set("Location", answer.location)
				}
				w.WriteHeader(answer.status)
			}))
			defer origin.Close()

			if got := resolveDownload(httptest.NewRequest(http.MethodGet, "/", nil), origin.URL); got != "" {
				t.Errorf("resolved %q, want empty", got)
			}
		})
	}
}
