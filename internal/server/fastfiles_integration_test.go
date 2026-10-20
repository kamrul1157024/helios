package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// filesClient talks to a real public server — mux, auth, middleware and all —
// the way the desktop and mobile apps do.
type filesClient struct {
	t      *testing.T
	base   string
	token  string
	shared *Shared
}

func newFilesClient(t *testing.T) *filesClient {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	kp, err := auth.GenerateKeypair("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(&store.Device{KID: "dev1", Name: "test", PublicKey: kp.PublicKeyBase64(), Status: "active"}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.CreateTestJWT(kp.PrivateKey, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	shared := NewShared(db, notifications.NewManager(db), newStubBackend())
	ts := httptest.NewServer(NewPublicServer("", 0, shared).httpServer.Handler)
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go shared.Files.Run(ctx)
	return &filesClient{t: t, base: ts.URL, token: token, shared: shared}
}

func (c *filesClient) do(method, path string, query url.Values, header http.Header, body []byte) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path+"?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (c *filesClient) json(resp *http.Response, want int, into any) {
	c.t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: status %d (want %d): %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, raw)
	}
	if resp.Header.Get("Server-Timing") == "" {
		c.t.Errorf("%s has no Server-Timing", resp.Request.URL.Path)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			c.t.Fatalf("decode %s: %v", raw, err)
		}
	}
}

type searchAnswer struct {
	Matches  []searchMatch `json:"matches"`
	Indexing bool          `json:"indexing"`
	Watch    string        `json:"watch"`
}

func (c *filesClient) search(root, q string) searchAnswer {
	c.t.Helper()
	var ans searchAnswer
	c.json(c.do(http.MethodGet, "/api/files/search", url.Values{"path": {root}, "q": {q}}, nil, nil), http.StatusOK, &ans)
	return ans
}

func TestFastFilesEndToEnd(t *testing.T) {
	c := newFilesClient(t)
	root := indexTree(t, true)
	write(t, filepath.Join(root, "debug.log"), "ignored")
	main := filepath.Join(root, "src", "main.go")

	// Quick-open warms the index; a live root is what the rest relies on.
	deadline := time.Now().Add(3 * time.Second)
	var ans searchAnswer
	for ans = c.search(root, "main"); ans.Indexing || (liveWatchSupported && ans.Watch != watchLive); ans = c.search(root, "main") {
		if time.Now().After(deadline) {
			t.Fatalf("index never settled: %+v", ans)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(ans.Matches) == 0 || ans.Matches[0].Rel != "src/main.go" || len(ans.Matches[0].Positions) != 4 {
		t.Fatalf("search: %+v", ans.Matches)
	}

	// The listing hides .git and dims what git ignores.
	var listing struct {
		Entries []fileEntry `json:"entries"`
	}
	c.json(c.do(http.MethodGet, "/api/files", url.Values{"path": {root}}, nil, nil), http.StatusOK, &listing)
	seen := map[string]*bool{}
	for _, e := range listing.Entries {
		seen[e.Name] = e.Ignored
	}
	if _, ok := seen[".git"]; ok {
		t.Error("listing shows .git")
	}
	if ig := seen["debug.log"]; ig == nil || !*ig {
		t.Errorf("debug.log ignored = %v, want true", ig)
	}
	if ig := seen["src"]; ig == nil || *ig {
		t.Errorf("src ignored = %v, want false", ig)
	}

	// Raw read, then a 304 for the same validator.
	raw := c.do(http.MethodGet, "/api/file/raw", url.Values{"path": {main}}, nil, nil)
	body, _ := io.ReadAll(raw.Body)
	tag := raw.Header.Get("ETag")
	if raw.StatusCode != http.StatusOK || string(body) != "package main" || tag == "" {
		t.Fatalf("raw: %d %q etag %q", raw.StatusCode, body, tag)
	}
	again := c.do(http.MethodGet, "/api/file/raw", url.Values{"path": {main}}, http.Header{"If-None-Match": {tag}}, nil)
	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation: %d", again.StatusCode)
	}

	// A write that names what it read lands; a second one with the same,
	// now stale, validator is refused.
	put := func(ifMatch string, want int) map[string]any {
		payload, _ := json.Marshal(map[string]string{"path": main, "content": "package main // edited"})
		var out map[string]any
		c.json(c.do(http.MethodPut, "/api/file", nil, http.Header{"If-Match": {ifMatch}}, payload), want, &out)
		return out
	}
	if out := put(tag, http.StatusOK); out["etag"] == tag {
		t.Fatal("the write did not move the ETag")
	}
	if out := put(tag, http.StatusConflict); out["code"] != "conflict" {
		t.Fatalf("stale write: %v", out)
	}

	// An agent writes a file; quick-open finds it within a second.
	created := time.Now()
	write(t, filepath.Join(root, "src", "brand_new.go"), "package main\n\nfunc brandNew() {}\n")
	for {
		if m := c.search(root, "brand_new").Matches; len(m) > 0 && m[0].Rel == "src/brand_new.go" {
			break
		}
		if liveWatchSupported && time.Since(created) > time.Second {
			t.Fatal("a new file was not searchable within a second")
		}
		if time.Since(created) > 5*time.Second {
			t.Fatal("a new file never became searchable")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Find in files, with ranges that cover the match.
	var grep struct {
		Matches []grepMatch `json:"matches"`
	}
	c.json(c.do(http.MethodGet, "/api/files/grep",
		url.Values{"path": {root}, "q": {"brandnew"}, "glob": {"*.go,!*_test.go"}}, nil, nil), http.StatusOK, &grep)
	if len(grep.Matches) != 1 || len(grep.Matches[0].Ranges) != 1 {
		t.Fatalf("grep: %+v", grep.Matches)
	}
	m := grep.Matches[0]
	units := utf16.Encode([]rune(m.Text))
	if hit := string(utf16.Decode(units[m.Ranges[0][0]:m.Ranges[0][1]])); !strings.EqualFold(hit, "brandnew") {
		t.Fatalf("range covers %q", hit)
	}

	// Git status sees the write and the new file.
	var status gitStatus
	c.json(c.do(http.MethodGet, "/api/git/status", url.Values{"path": {root}}, nil, nil), http.StatusOK, &status)
	if len(status.Unstaged) != 1 || status.Unstaged[0].Path != "src/main.go" ||
		len(status.Untracked) != 1 || status.Untracked[0].Path != "src/brand_new.go" {
		t.Fatalf("status: %+v", status)
	}

	// gzip on the wire for a body worth compressing.
	big := filepath.Join(root, "big.txt")
	write(t, big, strings.Repeat("lorem ipsum dolor sit amet\n", 400))
	gz := c.do(http.MethodGet, "/api/file/raw", url.Values{"path": {big}}, http.Header{"Accept-Encoding": {"gzip"}}, nil)
	zipped, _ := io.ReadAll(gz.Body)
	if gz.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("large text went uncompressed")
	}
	info, err := os.Stat(big)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(zipped)) >= info.Size()/4 {
		t.Fatalf("gzip sent %d bytes for %d", len(zipped), info.Size())
	}
}

// events subscribes to the stream and delivers each file_changed payload.
func (c *filesClient) events() <-chan string {
	c.t.Helper()
	resp := c.do(http.MethodGet, "/api/events", nil, nil, nil)
	out := make(chan string, 16)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(resp.Body)
		fileChanged := false
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "event: file_changed":
				fileChanged = true
			case strings.HasPrefix(line, "data: ") && fileChanged:
				out <- strings.TrimPrefix(line, "data: ")
				fileChanged = false
			}
		}
	}()
	return out
}

// A folder somebody is looking at names itself when an agent creates a file
// in it — on a live root, through the index watcher's nudge, well inside the
// one-second sweep.
func TestListedFolderAnnouncesANewChild(t *testing.T) {
	c := newFilesClient(t)
	root := indexTree(t, true)
	deadline := time.Now().Add(3 * time.Second)
	for ans := c.search(root, "main"); ans.Indexing || (liveWatchSupported && ans.Watch != watchLive); ans = c.search(root, "main") {
		if time.Now().After(deadline) {
			t.Fatalf("index never settled: %+v", ans)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The Files panel asks for git status of the same folder it lists, and
	// the two subscriptions must not collapse into one.
	c.json(c.do(http.MethodGet, "/api/git/status", url.Values{"path": {root}}, nil, nil), http.StatusOK, nil)
	c.json(c.do(http.MethodGet, "/api/files", url.Values{"path": {root}}, nil, nil), http.StatusOK, nil)
	events := c.events()
	time.Sleep(100 * time.Millisecond)

	created := time.Now()
	write(t, filepath.Join(root, "zz-new.txt"), "x")
	timeout := time.After(time.Second)
	for {
		select {
		case data := <-events:
			if strings.Contains(data, `"path":"`+root+`"`) {
				if liveWatchSupported && time.Since(created) > 500*time.Millisecond {
					t.Errorf("the folder took %v to announce itself", time.Since(created))
				}
				return
			}
		case <-timeout:
			t.Fatal("no file_changed named the folder within a second")
		}
	}
}
