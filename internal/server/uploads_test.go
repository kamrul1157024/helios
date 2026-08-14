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

func upload(t *testing.T, s *PublicServer, sessionID string, parts ...part) (*httptest.ResponseRecorder, map[string]interface{}) {
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

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/files", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()

	s.handleSessionUpload(rec, req)

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
	s, shared, home := newUploadTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	rec, payload := upload(t, s, "sess-1", part{name: "diagram.png", content: "not really a png"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	paths := uploadedPaths(t, payload)
	if len(paths) != 1 {
		t.Fatalf("files: got %d, want 1", len(paths))
	}

	want := filepath.Join(home, ".helios", "uploads", "sess-1", "diagram.png")
	if paths[0] != want {
		t.Errorf("path: got %q, want %q", paths[0], want)
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
	s, shared, home := newUploadTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	_, payload := upload(t, s, "sess-1", part{name: "../../../etc/passwd", content: "x"})
	paths := uploadedPaths(t, payload)

	dir := filepath.Join(home, ".helios", "uploads", "sess-1")
	if filepath.Dir(paths[0]) != dir {
		t.Errorf("escaped the upload directory: %q", paths[0])
	}
	if strings.Contains(paths[0], "..") {
		t.Errorf("path still traverses: %q", paths[0])
	}
}

// Two screenshots are both called Screenshot.png. Neither may overwrite the
// other: the first one's path is already in a prompt by then.
func TestUpload_SameNameTwiceKeepsBothFiles(t *testing.T) {
	s, shared, _ := newUploadTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	_, first := upload(t, s, "sess-1", part{name: "shot.png", content: "one"})
	_, second := upload(t, s, "sess-1", part{name: "shot.png", content: "two"})

	firstPath, secondPath := uploadedPaths(t, first)[0], uploadedPaths(t, second)[0]
	if firstPath == secondPath {
		t.Fatalf("second upload reused the path %q", firstPath)
	}
	if got := filepath.Base(secondPath); got != "shot-1.png" {
		t.Errorf("second name: got %q, want shot-1.png", got)
	}

	content, err := os.ReadFile(firstPath)
	if err != nil || string(content) != "one" {
		t.Errorf("first file was overwritten: %q, %v", content, err)
	}
}

func TestUpload_SeveralFilesInOneRequest(t *testing.T) {
	s, shared, _ := newUploadTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	_, payload := upload(t, s, "sess-1",
		part{name: "a.txt", content: "a"},
		part{name: "b.txt", content: "b"},
	)
	if paths := uploadedPaths(t, payload); len(paths) != 2 {
		t.Fatalf("files: got %d, want 2", len(paths))
	}
}

func TestUpload_UnknownSessionIsRejected(t *testing.T) {
	s, _, home := newUploadTest(t)

	rec, _ := upload(t, s, "nope", part{name: "a.txt", content: "a"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(home, ".helios", "uploads", "nope")); !os.IsNotExist(err) {
		t.Errorf("created a directory for a session that does not exist")
	}
}

func TestUpload_RequestWithoutFilesIsRejected(t *testing.T) {
	s, shared, _ := newUploadTest(t)
	seedSessionWithStatus(t, shared.DB, "sess-1", "idle")

	rec, _ := upload(t, s, "sess-1")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}
