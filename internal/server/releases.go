package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// The newest published release, which is what the landing page hands out. Not
// the newest tag: a tag can be a prerelease or one pushed by mistake, and this
// endpoint skips both.
const latestReleaseAPI = "https://api.github.com/repos/kamrul1157024/helios/releases/latest"

// releaseResolver answers the newest release's tag, remembering it so a page
// load does not become a round trip to GitHub. An unreachable GitHub answers
// the empty string, which the page renders as its unversioned form rather than
// as an error — a download page that fails because an API did is no use.
type releaseResolver struct {
	url    string
	client *http.Client

	mu      sync.Mutex
	tag     string
	expires time.Time
}

var latestRelease = &releaseResolver{
	url:    latestReleaseAPI,
	client: &http.Client{Timeout: 5 * time.Second},
}

const (
	releaseTTL = time.Hour
	// Shorter, because a failure is usually a rate limit or a flap, and an hour
	// of unversioned pages is a long answer to a minute-long problem.
	releaseFailureTTL = 5 * time.Minute
)

func (r *releaseResolver) get(ctx context.Context) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Now().Before(r.expires) {
		return r.tag
	}

	r.tag = r.fetch(ctx)
	if r.tag == "" {
		r.expires = time.Now().Add(releaseFailureTTL)
	} else {
		r.expires = time.Now().Add(releaseTTL)
	}
	return r.tag
}

func (r *releaseResolver) fetch(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := r.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}
	return release.TagName
}
