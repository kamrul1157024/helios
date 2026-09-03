package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A placeholder left in the page renders as a dead link, and adding a download
// to the HTML without a matching entry in renderLanding is the easy way to do
// it.
func TestLandingSubstitutesEveryPlaceholder(t *testing.T) {
	for _, tag := range []string{"", "v3.9.0"} {
		body := renderLanding(tag)
		if strings.Contains(body, "{{") {
			t.Errorf("tag %q left an unreplaced placeholder:\n%s", tag, body)
		}
	}
}

// Every link on the page is one this daemon serves, and every one of them is an
// asset the download handler will fetch. A github.com link here is the bug:
// Android gives those to the GitHub app.
func TestLandingLinksItsOwnDownloadPaths(t *testing.T) {
	body := renderLanding("v3.9.0")

	if strings.Contains(body, "github.com/kamrul1157024/helios/releases") {
		t.Error("landing page links github.com directly")
	}
	for asset := range releaseAssets {
		if !strings.Contains(body, "/download/"+asset) {
			t.Errorf("landing page is missing the download link for %s", asset)
		}
	}
}

// The page says which release it is offering, so a reader comparing it with a
// daemon's version has something to compare.
func TestLandingNamesTheRelease(t *testing.T) {
	if !strings.Contains(renderLanding("v3.9.0"), "3.9.0") {
		t.Error("landing page does not name the version it is serving")
	}
	// Offline, rate-limited, or asked before the daemon ever reached GitHub: a
	// page that names no version beats one naming a version it invented.
	if strings.Contains(renderLanding(""), "from your phone ·") {
		t.Error("landing page named a version it was never given")
	}
}

func TestLandingServesOK(t *testing.T) {
	rec := httptest.NewRecorder()
	handleLanding(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "/download/helios.apk") {
		t.Error("the page the handler served has no download link")
	}
}

func TestReleaseResolverReadsTheTagOnce(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"tag_name": "v4.1.0"}`))
	}))
	defer api.Close()

	resolver := &releaseResolver{url: api.URL, client: api.Client()}
	if got := resolver.get(context.Background()); got != "v4.1.0" {
		t.Fatalf("tag = %q, want v4.1.0", got)
	}
	if got := resolver.get(context.Background()); got != "v4.1.0" {
		t.Fatalf("cached tag = %q, want v4.1.0", got)
	}
	if calls != 1 {
		t.Errorf("asked GitHub %d times, want 1", calls)
	}
}

// A rate limit is the common one, and it must not take the page down with it.
func TestReleaseResolverAnswersNothingWhenGitHubRefuses(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer api.Close()

	resolver := &releaseResolver{url: api.URL, client: api.Client()}
	if got := resolver.get(context.Background()); got != "" {
		t.Errorf("tag = %q, want empty", got)
	}
	// Cached as a failure, and briefly: the next page load must not wait on
	// GitHub all over again.
	if resolver.expires.After(time.Now().Add(releaseTTL)) {
		t.Error("a failure was cached for as long as an answer")
	}
}
