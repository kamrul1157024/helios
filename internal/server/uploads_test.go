package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

func newUploadTest(t *testing.T) (*PublicServer, *Shared, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	shared := NewShared(db, notifications.NewManager(db), newStubBackend())
	return &PublicServer{shared: shared}, shared, home
}

type part struct {
	name    string
	content string
}

func upload(t *testing.T, s *PublicServer, parts ...part) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, p := range parts {
		w, err := form.CreateFormFile("file", p.name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := w.Write([]byte(p.content)); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	form.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()

	s.handleUpload(rec, req)

	var payload map[string]interface{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	return rec, payload
}

func uploadedPaths(t *testing.T, payload map[string]interface{}) []string {
	t.Helper()
	files, ok := payload["files"].([]interface{})
	if !ok {
		t.Fatalf("no files in response: %v", payload)
	}
	paths := make([]string, 0, len(files))
	for _, entry := range files {
		file, ok := entry.(map[string]interface{})
		if !ok {
			t.Fatalf("bad file entry: %v", entry)
		}
		paths = append(paths, file["path"].(string))
	}
	return paths
}

// The path in the response is the whole feature: the client pastes it into a
// prompt, so it has to be absolute and it has to hold the bytes that were sent.
func TestUpload_StoresTheFileAndReturnsItsPath(t *testing.T) {
	s, _, home := newUploadTest(t)

	rec, payload := upload(t, s, part{name: "diagram.png", content: "not really a png"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	paths := uploadedPaths(t, payload)
	if len(paths) != 1 {
		t.Fatalf("files: got %d, want 1", len(paths))
	}

	dir := filepath.Join(home, ".helios", "uploads")
	if got := filepath.Dir(paths[0]); got != dir {
		t.Errorf("directory: got %q, want %q", got, dir)
	}
	// Prefixed, but the name the user knows the file by survives on the end of
	// it: the agent reads this path out of a line of prose.
	if got := filepath.Base(paths[0]); !strings.HasSuffix(got, "-diagram.png") {
		t.Errorf("name: got %q, want something ending -diagram.png", got)
	}
	content, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(content) != "not really a png" {
		t.Errorf("content: got %q", content)
	}
}

// The filename arrives from a remote client, so a path inside it is either a
// browser quirk or an attempt to write somewhere it should not.
func TestUpload_FilenameCannotEscapeTheUploadDirectory(t *testing.T) {
	s, _, home := newUploadTest(t)

	_, payload := upload(t, s, part{name: "../../../etc/passwd", content: "x"})
	paths := uploadedPaths(t, payload)

	dir := filepath.Join(home, ".helios", "uploads")
	if filepath.Dir(paths[0]) != dir {
		t.Errorf("escaped the upload directory: %q", paths[0])
	}
	if strings.Contains(paths[0], "..") {
		t.Errorf("path still traverses: %q", paths[0])
	}
}

// The path is handed to the agent in a line of prose, and a macOS screenshot
// is named in four words that read as part of the sentence.
func TestUpload_SpacesInTheNameAreNotKept(t *testing.T) {
	s, _, _ := newUploadTest(t)

	_, payload := upload(t, s, part{name: "Screenshot 2026-08-15 at 2.33.50 PM.png", content: "x"})
	paths := uploadedPaths(t, payload)

	if got := filepath.Base(paths[0]); !strings.HasSuffix(got, "-Screenshot-2026-08-15-at-2.33.50-PM.png") {
		t.Errorf("name: got %q, still carries spaces or lost the name", got)
	}
	if strings.Contains(paths[0], " ") {
		t.Errorf("path still has a space in it: %q", paths[0])
	}
}

// Two screenshots are both called Screenshot.png. Neither may overwrite the
// other: the first one's path is already in a prompt by then. A directory per
// session used to be what promised that; in one flat directory the random
// prefix is all there is, so this is the test of it.
func TestUpload_SameNameTwiceKeepsBothFiles(t *testing.T) {
	s, _, _ := newUploadTest(t)

	_, first := upload(t, s, part{name: "shot.png", content: "one"})
	_, second := upload(t, s, part{name: "shot.png", content: "two"})

	firstPath, secondPath := uploadedPaths(t, first)[0], uploadedPaths(t, second)[0]
	if firstPath == secondPath {
		t.Fatalf("second upload reused the path %q", firstPath)
	}
	for _, path := range []string{firstPath, secondPath} {
		if !strings.HasSuffix(path, "-shot.png") {
			t.Errorf("name: got %q, want something ending -shot.png", path)
		}
	}

	content, err := os.ReadFile(firstPath)
	if err != nil || string(content) != "one" {
		t.Errorf("first file was overwritten: %q, %v", content, err)
	}
	if content, err := os.ReadFile(secondPath); err != nil || string(content) != "two" {
		t.Errorf("second file is wrong: %q, %v", content, err)
	}
}

// The prefix is the only thing keeping two files apart, so it has to actually
// differ. A hundred of the same name is a cheap way to notice a constant.
func TestUpload_PrefixIsDifferentEveryTime(t *testing.T) {
	s, _, _ := newUploadTest(t)

	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		_, payload := upload(t, s, part{name: "shot.png", content: "x"})
		name := filepath.Base(uploadedPaths(t, payload)[0])
		if seen[name] {
			t.Fatalf("name %q came back twice", name)
		}
		seen[name] = true
	}
}

func TestUpload_SeveralFilesInOneRequest(t *testing.T) {
	s, _, _ := newUploadTest(t)

	_, payload := upload(t, s,
		part{name: "a.txt", content: "a"},
		part{name: "b.txt", content: "b"},
	)
	if paths := uploadedPaths(t, payload); len(paths) != 2 {
		t.Fatalf("files: got %d, want 2", len(paths))
	}
}

func TestUpload_RequestWithoutFilesIsRejected(t *testing.T) {
	s, _, _ := newUploadTest(t)

	rec, _ := upload(t, s)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}
