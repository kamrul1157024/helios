package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serve runs one request through a files route the way the mux does.
func serve(t *testing.T, h http.HandlerFunc, method, path string, header http.Header, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	fastFilesFunc(h).ServeHTTP(rec, req)
	return rec
}

func rawURL(path string) string { return "/api/file/raw?" + url.Values{"path": {path}}.Encode() }

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestReadRawServesBytesWithValidators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	write(t, path, "package main\n")
	s := &PublicServer{}

	rec := serve(t, s.handleReadRaw, http.MethodGet, rawURL(path), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "package main\n" {
		t.Fatalf("body %q", rec.Body.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	h := rec.Header()
	if got, want := h.Get("ETag"), fileETag(info); got != want || !strings.HasPrefix(got, `W/"13-`) {
		t.Errorf("ETag %q, want %q", got, want)
	}
	if h.Get("X-Helios-Encoding") != "utf8" || h.Get("X-Helios-Size") != "13" {
		t.Errorf("encoding %q size %q", h.Get("X-Helios-Encoding"), h.Get("X-Helios-Size"))
	}
	if !strings.HasSuffix(h.Get("Content-Type"), "charset=utf-8") {
		t.Errorf("text served as %q", h.Get("Content-Type"))
	}
	if h.Get("Cache-Control") != "no-cache" || h.Get("X-Helios-Mod-Time") == "" {
		t.Errorf("cache-control %q mod-time %q", h.Get("Cache-Control"), h.Get("X-Helios-Mod-Time"))
	}
	if !strings.HasPrefix(h.Get("Server-Timing"), "total;dur=") || !strings.Contains(h.Get("Server-Timing"), "read;dur=") {
		t.Errorf("Server-Timing %q", h.Get("Server-Timing"))
	}
}

// TypeScript is the trap: system MIME tables call .ts a video stream.
func TestReadRawNeverServesTextAsVideo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.ts")
	write(t, path, "export const x = 1\n")
	rec := serve(t, (&PublicServer{}).handleReadRaw, http.MethodGet, rawURL(path), nil, nil)
	if ctype := rec.Header().Get("Content-Type"); !strings.HasPrefix(ctype, "text/") {
		t.Fatalf("TypeScript served as %q", ctype)
	}
}

func TestReadRawLabelsBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixel.png")
	writeBytes(t, path, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	rec := serve(t, (&PublicServer{}).handleReadRaw, http.MethodGet, rawURL(path), nil, nil)
	if rec.Header().Get("X-Helios-Encoding") != "binary" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("encoding %q type %q", rec.Header().Get("X-Helios-Encoding"), rec.Header().Get("Content-Type"))
	}
}

// The 304 must come from the stat alone. A file nobody can open proves it:
// any read would fail, and the answer would not be a 304.
func TestReadRawAnswers304WithoutReading(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	path := filepath.Join(t.TempDir(), "a.txt")
	write(t, path, "hello")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	tag := fileETag(info)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	for name, h := range map[string]http.HandlerFunc{
		"raw":  (&PublicServer{}).handleReadRaw,
		"json": (&PublicServer{}).handleReadFile,
	} {
		rec := serve(t, h, http.MethodGet, rawURL(path), http.Header{"If-None-Match": {tag}}, nil)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("%s: status %d body %q, want an empty 304", name, rec.Code, rec.Body.String())
		}
	}

	// Without the validator the same file is a read, and the read fails.
	rec := serve(t, (&PublicServer{}).handleReadRaw, http.MethodGet, rawURL(path), nil, nil)
	if rec.Code != http.StatusForbidden || decodeBody(t, rec)["code"] != "permission_denied" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReadFileJSONCarriesTheETag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	write(t, path, "hello")
	rec := serve(t, (&PublicServer{}).handleReadFile, http.MethodGet, rawURL(path), nil, nil)
	body := decodeBody(t, rec)
	if body["etag"] == "" || body["etag"] != rec.Header().Get("ETag") || body["content"] != "hello" {
		t.Fatalf("header %q body %v", rec.Header().Get("ETag"), body)
	}
}

func TestReadErrorsCarryCodes(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxFileSize + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cases := []struct {
		path   string
		status int
		code   string
	}{
		{filepath.Join(dir, "missing"), http.StatusNotFound, "not_found"},
		{dir, http.StatusBadRequest, "is_directory"},
		{big, http.StatusRequestEntityTooLarge, "too_large"},
	}
	for _, c := range cases {
		for name, h := range map[string]http.HandlerFunc{
			"raw":  (&PublicServer{}).handleReadRaw,
			"json": (&PublicServer{}).handleReadFile,
		} {
			rec := serve(t, h, http.MethodGet, rawURL(c.path), nil, nil)
			body := decodeBody(t, rec)
			if rec.Code != c.status || body["code"] != c.code {
				t.Errorf("%s %s: status %d code %v, want %d %s", name, c.path, rec.Code, body["code"], c.status, c.code)
			}
		}
	}
	rec := serve(t, (&PublicServer{}).handleReadRaw, http.MethodGet, rawURL(big), nil, nil)
	if body := decodeBody(t, rec); body["limit"] != float64(maxFileSize) || body["size"] != float64(maxFileSize+1) {
		t.Errorf("413 body %v", body)
	}
}

func putFile(t *testing.T, path, content, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	if ifMatch != "" {
		header.Set("If-Match", ifMatch)
	}
	return serve(t, (&PublicServer{}).handleWriteFile, http.MethodPut, "/api/file", header, bytes.NewReader(raw))
}

func TestWriteFileHonoursIfMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	write(t, path, "one")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := fileETag(info)

	ok := putFile(t, path, "two", stale)
	if ok.Code != http.StatusOK {
		t.Fatalf("matching If-Match refused: %d %s", ok.Code, ok.Body.String())
	}
	fresh := decodeBody(t, ok)["etag"]
	if fresh == "" || fresh == stale || ok.Header().Get("ETag") != fresh {
		t.Fatalf("write answered etag %v (header %q), stale was %q", fresh, ok.Header().Get("ETag"), stale)
	}

	conflict := putFile(t, path, "three", stale)
	body := decodeBody(t, conflict)
	if conflict.Code != http.StatusConflict || body["code"] != "conflict" || body["etag"] != fresh {
		t.Fatalf("stale If-Match: status %d body %v", conflict.Code, body)
	}
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Fatalf("a refused write still landed: %q", got)
	}
}

func gunzip(t *testing.T, body []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGzipAppliesToLargeText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.go")
	content := strings.Repeat("func main() { println(\"hello\") }\n", 200)
	write(t, path, content)
	gz := http.Header{"Accept-Encoding": {"gzip, deflate"}}

	rec := serve(t, (&PublicServer{}).handleReadRaw, http.MethodGet, rawURL(path), gz, nil)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Length") != "" {
		t.Fatalf("encoding %q length %q", rec.Header().Get("Content-Encoding"), rec.Header().Get("Content-Length"))
	}
	if string(gunzip(t, rec.Body.Bytes())) != content {
		t.Fatal("gzip body does not round-trip")
	}

	// The JSON route too: its body is built before the decision.
	rec = serve(t, (&PublicServer{}).handleReadFile, http.MethodGet, rawURL(path), gz, nil)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("large JSON was not compressed")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(gunzip(t, rec.Body.Bytes()), &body); err != nil || body["content"] != content {
		t.Fatalf("JSON did not round-trip: %v", err)
	}
}

func TestGzipSkipsWhatItShould(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	write(t, small, "tiny")
	image := filepath.Join(dir, "big.png")
	writeBytes(t, image, append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 4096)...))
	text := filepath.Join(dir, "big.txt")
	write(t, text, strings.Repeat("x", 4096))

	cases := []struct {
		name   string
		path   string
		header http.Header
	}{
		{"small body", small, http.Header{"Accept-Encoding": {"gzip"}}},
		{"image", image, http.Header{"Accept-Encoding": {"gzip"}}},
		{"no Accept-Encoding", text, nil},
		{"gzip refused", text, http.Header{"Accept-Encoding": {"gzip;q=0"}}},
	}
	for _, c := range cases {
		rec := serve(t, (&PublicServer{}).handleReadRaw, http.MethodGet, rawURL(c.path), c.header, nil)
		if rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: compressed", c.name)
		}
	}
	if compressibleType("text/event-stream") {
		t.Error("an event stream would be compressed")
	}
}

func TestListFilesHidesVersionControl(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".git", ".hg", ".svn", "src"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, ".gitignore"), "x")
	entries, err := listDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "src,.gitignore" {
		t.Fatalf("listed %v", names)
	}
}
