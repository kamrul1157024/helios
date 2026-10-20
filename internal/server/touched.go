package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/transcript"
)

// See docs/specs/65-fast-files.md, B6: what the agent changed.
//
// The transcript is the source, for attribution only. Spec 54 rejected tool
// payloads as a freshness signal because they miss writers like `sed -i`, and
// that still holds: this route says which files *this session* edited, and git
// status covers the rest.

// touchedFile is one row of the Changes view.
type touchedFile struct {
	Path   string `json:"path"`
	LastAt string `json:"last_at"`
	// Tool is the provider's own tool name for the last call on the path.
	Tool  string `json:"tool"`
	Count int    `json:"count"`
	// Line is the first changed line of the last edit, when it can be found.
	Line    int  `json:"line,omitempty"`
	Deleted bool `json:"deleted"`

	at    time.Time
	order int
	last  provider.Touch
}

// handleSessionTouched lists the files a session's tool calls wrote, newest
// first.
func (s *PublicServer) handleSessionTouched(w http.ResponseWriter, r *http.Request) {
	id := extractSessionID(r.URL.Path, "/touched")
	session, err := s.shared.DB.GetSession(id)
	if err != nil || session == nil {
		jsonErrorCode(w, "session not found", http.StatusNotFound, "not_found")
		return
	}

	files := []touchedFile{}
	path := s.resolveTranscriptPath(session)
	transcriber := provider.TranscriberFor(session.Source)
	toucher := provider.ToucherFor(session.Source)
	if path != "" && transcriber != nil && toucher != nil {
		started := time.Now()
		files, err = touchedFiles(transcriber.ParseLine, path, toucher, session.CWD)
		timingFrom(r.Context()).since("read", started)
		if err != nil {
			jsonError(w, "failed to read transcript: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"files": files})
}

// touchedFiles folds a transcript's tool calls into one row per path.
func touchedFiles(parse transcript.LineParser, path string, toucher provider.Toucher, cwd string) ([]touchedFile, error) {
	byPath := map[string]*touchedFile{}
	order := 0
	err := transcript.Each(parse, path, func(m *transcript.Message) {
		if m.Role != transcript.RoleToolUse || m.Metadata == nil {
			return
		}
		for _, t := range toucher.TouchedPaths(m.Tool, m.Metadata) {
			abs := resolveTouched(t.Path, cwd)
			if abs == "" {
				continue
			}
			row := byPath[abs]
			if row == nil {
				row = &touchedFile{Path: abs}
				byPath[abs] = row
			}
			order++
			row.Count++
			row.Tool, row.Deleted, row.last, row.order = m.Tool, t.Deleted, t, order
			if at, err := time.Parse(time.RFC3339Nano, m.Timestamp); err == nil {
				row.at = at
				row.LastAt = at.UTC().Format(time.RFC3339)
			} else {
				row.LastAt = m.Timestamp
			}
		}
	})
	if err != nil {
		return nil, err
	}

	files := make([]touchedFile, 0, len(byPath))
	for _, row := range byPath {
		if !row.Deleted {
			row.Line = touchedLine(row.Path, row.last)
		}
		files = append(files, *row)
	}
	// Newest first. Transcript order breaks ties, and orders rows whose
	// timestamps did not parse.
	sort.Slice(files, func(i, j int) bool {
		if !files[i].at.Equal(files[j].at) {
			return files[i].at.After(files[j].at)
		}
		return files[i].order > files[j].order
	})
	return files, nil
}

// resolveTouched makes a tool's path absolute against the session's working
// directory, as the tool itself would have.
func resolveTouched(path, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			return ""
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path)
}

// touchedLine finds where the last change to a file starts: the call's own
// line when it has one, otherwise the first line of its anchor text in the
// file as it is now. Zero — omitted — when the text is not there any more.
func touchedLine(path string, t provider.Touch) int {
	if t.Line > 0 {
		return t.Line
	}
	if t.Anchor == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxFileSize {
		return 0
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	at := strings.Index(string(content), t.Anchor)
	if at < 0 {
		return 0
	}
	return strings.Count(string(content[:at]), "\n") + 1
}
