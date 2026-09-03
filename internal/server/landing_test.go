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

// Offline, rate-limited, or asked before the daemon has ever reached GitHub:
// the page is still a working download page.
func TestLandingWithoutATagFallsBackToLatest(t *testing.T) {
	body := renderLanding("")

	for _, url := range []string{
		APKDownloadURL,
		MacArm64DownloadURL,
		MacIntelDownloadURL,
		LinuxAppImageDownloadURL,
		LinuxDebDownloadURL,
	} {
		if !strings.Contains(body, url) {
			t.Errorf("landing page is missing the download link %s", url)
		}
	}
	if strings.Contains(body, "releases/download/") {
		t.Error("landing page pinned a tag it was never given")
	}
}

// The version the page prints and the file it hands over come from the same
// release, which is the whole point of resolving the tag.
func TestLandingPinsTheTagItWasGiven(t *testing.T) {
	body := renderLanding("v3.9.0")

	want := "https://github.com/kamrul1157024/helios/releases/download/v3.9.0/helios.apk"
	if !strings.Contains(body, want) {
		t.Errorf("landing page does not link %s", want)
	}
	if strings.Contains(body, "releases/latest/download/") {
		t.Error("landing page still links the unpinned form")
	}
	if !strings.Contains(body, "3.9.0") {
		t.Error("landing page does not name the version it is serving")
	}
}

func TestLandingServesOK(t *testing.T) {
	// Answered from the cache, so the test does not reach the network.
	latestRelease.mu.Lock()
	latestRelease.tag = "v3.9.0"
	latestRelease.expires = time.Now().Add(time.Hour)
	latestRelease.mu.Unlock()

	rec := httptest.NewRecorder()
	handleLanding(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "releases/download/v3.9.0/") {
		t.Error("the handler did not use the resolved tag")
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
}
