package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

func claudeToolLine(t *testing.T, at, tool string, input map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":      "assistant",
		"timestamp": at,
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "tool_use", "id": "t", "name": tool, "input": input}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func codexPatchLine(t *testing.T, at string, ordinal int, patch string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"timestamp": at,
		"ordinal":   ordinal,
		"type":      "response_item",
		"payload":   map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": patch, "call_id": "c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	write(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// strip drops the fields a test does not assert on.
func strip(files []touchedFile) []touchedFile {
	out := make([]touchedFile, len(files))
	for i, f := range files {
		out[i] = touchedFile{Path: f.Path, LastAt: f.LastAt, Tool: f.Tool, Count: f.Count, Line: f.Line, Deleted: f.Deleted}
	}
	return out
}

func TestTouchedFromAClaudeTranscript(t *testing.T) {
	cwd := t.TempDir()
	write(t, filepath.Join(cwd, "main.go"), "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	write(t, filepath.Join(cwd, "util.go"), "package main\n\n// helper\nfunc helper() {}\n")
	write(t, filepath.Join(cwd, "new.txt"), "fresh\n")

	path := writeTranscript(t,
		claudeToolLine(t, "2026-10-20T04:00:00.000Z", "Read", map[string]any{"file_path": "main.go"}),
		claudeToolLine(t, "2026-10-20T04:01:00.000Z", "Edit", map[string]any{
			"file_path": filepath.Join(cwd, "main.go"), "old_string": "x", "new_string": "\tprintln(\"hi\")"}),
		claudeToolLine(t, "2026-10-20T04:02:00.000Z", "Write", map[string]any{"file_path": "new.txt", "content": "fresh\n"}),
		claudeToolLine(t, "2026-10-20T04:03:00.000Z", "MultiEdit", map[string]any{
			"file_path": "util.go",
			"edits":     []any{map[string]any{"old_string": "a", "new_string": "// helper"}, map[string]any{"new_string": "zzz"}}}),
		claudeToolLine(t, "2026-10-20T04:04:00.000Z", "Edit", map[string]any{
			"file_path": "main.go", "new_string": "no longer in the file"}),
	)
	files, err := touchedFiles(provider.TranscriberFor("claude").ParseLine, path, provider.ToucherFor("claude"), cwd)
	if err != nil {
		t.Fatal(err)
	}
	want := []touchedFile{
		// The last edit's text is gone from the file, so its line is unknown.
		{Path: filepath.Join(cwd, "main.go"), LastAt: "2026-10-20T04:04:00Z", Tool: "Edit", Count: 2},
		{Path: filepath.Join(cwd, "util.go"), LastAt: "2026-10-20T04:03:00Z", Tool: "MultiEdit", Count: 1, Line: 3},
		{Path: filepath.Join(cwd, "new.txt"), LastAt: "2026-10-20T04:02:00Z", Tool: "Write", Count: 1, Line: 1},
	}
	if got := strip(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestTouchedFromACodexRollout(t *testing.T) {
	cwd := t.TempDir()
	write(t, filepath.Join(cwd, "src", "app.go"), "package src\n\nfunc run() {\n\treturn\n}\n")
	write(t, filepath.Join(cwd, "added.md"), "# added\n")
	write(t, filepath.Join(cwd, "moved.go"), "package x\n")

	path := writeTranscript(t,
		codexPatchLine(t, "2026-10-20T05:00:00.000Z", 1, "*** Begin Patch\n*** Add File: added.md\n+# added\n*** End Patch"),
		codexPatchLine(t, "2026-10-20T05:01:00.000Z", 2,
			"*** Begin Patch\n*** Update File: src/app.go\n@@ func run() {\n-\tpanic(1)\n+\treturn\n*** Delete File: gone.go\n*** End Patch"),
		codexPatchLine(t, "2026-10-20T05:02:00.000Z", 3,
			"*** Begin Patch\n*** Update File: old.go\n*** Move to: moved.go\n@@\n+package x\n*** End Patch"),
	)
	files, err := touchedFiles(provider.TranscriberFor("codex").ParseLine, path, provider.ToucherFor("codex"), cwd)
	if err != nil {
		t.Fatal(err)
	}
	want := []touchedFile{
		{Path: filepath.Join(cwd, "moved.go"), LastAt: "2026-10-20T05:02:00Z", Tool: "apply_patch", Count: 1, Line: 1},
		// Two files from one call share its time; transcript order breaks the tie.
		{Path: filepath.Join(cwd, "gone.go"), LastAt: "2026-10-20T05:01:00Z", Tool: "apply_patch", Count: 1, Deleted: true},
		{Path: filepath.Join(cwd, "src", "app.go"), LastAt: "2026-10-20T05:01:00Z", Tool: "apply_patch", Count: 1, Line: 4},
		{Path: filepath.Join(cwd, "added.md"), LastAt: "2026-10-20T05:00:00Z", Tool: "apply_patch", Count: 1, Line: 1},
	}
	if got := strip(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestTouchedRoute(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cwd := t.TempDir()
	path := writeTranscript(t, claudeToolLine(t, "2026-10-20T04:00:00Z", "Write", map[string]any{"file_path": "a.txt"}))
	if err := db.UpsertSession(&store.Session{
		SessionID: "s1", Source: "claude", CWD: cwd, Status: "idle", TranscriptPath: &path,
	}); err != nil {
		t.Fatal(err)
	}
	srv := &PublicServer{shared: NewShared(db, notifications.NewManager(db), newStubBackend())}

	rec := serve(t, srv.handleSessionTouched, http.MethodGet, "/api/sessions/s1/touched", nil, nil)
	var body struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s (%v)", rec.Code, rec.Body.String(), err)
	}
	if len(body.Files) != 1 || body.Files[0]["path"] != filepath.Join(cwd, "a.txt") || body.Files[0]["line"] != float64(1) {
		t.Fatalf("files %v", body.Files)
	}

	missing := httptest.NewRecorder()
	srv.handleSessionTouched(missing, httptest.NewRequest(http.MethodGet, "/api/sessions/nope/touched", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown session: %d", missing.Code)
	}
}
