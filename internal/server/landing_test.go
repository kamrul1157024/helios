package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A placeholder left in the page renders as a dead link, and adding a download
// to the HTML without a matching entry in landingURLs is the easy way to do it.
func TestLandingSubstitutesEveryPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	handleLanding(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, "{{") {
		t.Errorf("landing page has an unreplaced placeholder:\n%s", body)
	}
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
}
