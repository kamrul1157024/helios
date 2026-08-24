package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
)

// Sessions is the slice of the store this package reads.
type Sessions interface {
	ListSessions() ([]store.Session, error)
	GetSession(sessionID string) (*store.Session, error)
}

// toolOrder fixes the order tools/list reports, so the agent meets the tool it
// needs most first rather than in map order.
var toolOrder = []string{
	"helios_show",
	"helios_sessions",
}

// showViews are the views helios_show can switch to, and what each one needs.
// "diff" takes an optional path: without one it shows the whole change, which
// is why there is no separate "git" view.
var showViews = map[string]struct {
	needsPath bool
	takesPath bool
}{
	"file":     {needsPath: true, takesPath: true},
	"diff":     {needsPath: false, takesPath: true},
	"terminal": {},
	"agent":    {},
}

func obj(props map[string]interface{}, required ...string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

func (s *Server) registry() map[string]tool {
	return map[string]tool{
		"helios_show": {
			description: "Show the human something in the Helios app: open a file, " +
				"open a diff, or switch to the terminal or the transcript. " +
				"Paths are absolute, the same form your own file tools use. " +
				"view=file needs path. view=diff takes an optional path; omit it " +
				"for the whole change. view=terminal and view=agent take neither. " +
				"A diff is the uncommitted change by default; pass base for a branch " +
				"against it, or commit for a single commit. " +
				"Use this rarely, when the human must actually look at something.",
			schema: obj(map[string]interface{}{
				"view":   str("file | diff | terminal | agent"),
				"path":   str("absolute file path, for view=file and optionally view=diff"),
				"line":   map[string]interface{}{"type": "integer", "description": "line to scroll to, view=file only"},
				"base":   str("branch to diff against, e.g. main. view=diff only, and not with commit"),
				"commit": str("one commit to show. view=diff only, and not with base"),
				"layout": str("split (default, side by side) or unified. view=diff only"),
				"note":   str("one line saying why the human should look at this"),
			}, "view"),
			call: s.callShow,
		},

		"helios_sessions": {
			description: "List Helios sessions. Dead sessions are omitted unless all=true.",
			schema: obj(map[string]interface{}{
				"project": str("optional substring match on project or cwd"),
				"all":     map[string]interface{}{"type": "boolean", "description": "include terminated and archived sessions"},
			}),
			call: s.callSessions,
		},
	}
}

// isAbsolutePath accepts "~/..." as well, because the daemon expands it the
// same way it expands an absolute path.
func isAbsolutePath(path string) bool {
	return filepath.IsAbs(path) || strings.HasPrefix(path, "~/")
}

func argString(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func argInt(args map[string]interface{}, key string) int {
	if f, ok := args[key].(float64); ok {
		return int(f)
	}
	return 0
}

// callShow validates the request and broadcasts it. Validation answers with a
// correction rather than a bare failure, so the agent can fix the call itself
// without the human being involved.
func (s *Server) callShow(sessionID string, args map[string]interface{}) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("this session was not started by Helios, so it has no panel to show anything in")
	}

	view := argString(args, "view")
	rules, known := showViews[view]
	if !known {
		return "", fmt.Errorf("unknown view %q; use file, diff, terminal or agent", view)
	}

	path := argString(args, "path")
	if rules.needsPath && path == "" {
		return "", fmt.Errorf("view=%s needs a path", view)
	}
	if !rules.takesPath && path != "" {
		return "", fmt.Errorf("view=%s does not take a path", view)
	}
	// The daemon resolves a relative path against its own working directory,
	// which is "/" — so a repo-relative path silently becomes a path that does
	// not exist. Absolute is also the form the agent's own file tools use.
	if path != "" && !isAbsolutePath(path) {
		return "", fmt.Errorf("path must be absolute, not %q", path)
	}

	base, commit := argString(args, "base"), argString(args, "commit")
	layout := argString(args, "layout")
	if view != "diff" && (base != "" || commit != "" || layout != "") {
		return "", fmt.Errorf("base, commit and layout are for view=diff, not view=%s", view)
	}
	if layout != "" && layout != "split" && layout != "unified" {
		return "", fmt.Errorf("layout must be split or unified, not %q", layout)
	}
	// A branch range and a single commit are different questions, and honouring
	// one while ignoring the other would be worse than refusing.
	if base != "" && commit != "" {
		return "", fmt.Errorf("pass base or commit, not both")
	}

	payload := map[string]interface{}{
		"session_id": sessionID,
		"view":       view,
	}
	for key, value := range map[string]string{
		"path":   path,
		"base":   base,
		"commit": commit,
		"layout": layout,
		"note":   argString(args, "note"),
	} {
		if value != "" {
			payload[key] = value
		}
	}
	if line := argInt(args, "line"); line > 0 && view == "file" {
		payload["line"] = line
	}

	clients := s.notify.Show(payload)

	// Reported so an agent nobody is watching writes prose instead of pointing
	// at a screen. This counts connected clients, not people looking at this
	// particular session — the event stream has no per-session affinity.
	if clients == 0 {
		return encode(map[string]interface{}{
			"shown":  false,
			"reason": "no client attached",
		})
	}
	return encode(map[string]interface{}{"shown": true, "clients": clients})
}

func (s *Server) callSessions(_ string, args map[string]interface{}) (string, error) {
	sessions, err := s.sessions.ListSessions()
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}

	match := strings.ToLower(argString(args, "project"))
	all, _ := args["all"].(bool)

	out := make([]map[string]interface{}, 0, len(sessions))
	for _, sess := range sessions {
		// A long-lived install accumulates hundreds of dead sessions, nearly
		// all untitled. Listing them buries the handful worth addressing.
		if !all && (sess.Status == "terminated" || sess.Archived) {
			continue
		}
		if match != "" &&
			!strings.Contains(strings.ToLower(sess.Project), match) &&
			!strings.Contains(strings.ToLower(sess.CWD), match) {
			continue
		}
		entry := map[string]interface{}{
			"session": sess.SessionID,
			"project": sess.Project,
			"cwd":     sess.CWD,
			"status":  sess.Status,
		}
		if sess.Title != nil {
			entry["title"] = *sess.Title
		}
		out = append(out, entry)
	}
	return encode(map[string]interface{}{"sessions": out})
}

func encode(v interface{}) (string, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
