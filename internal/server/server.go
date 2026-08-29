package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/featureflag"
	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/mcp"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/reporter"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/sysinfo"
)

// Shared holds shared dependencies between internal and public servers.
type Shared struct {
	DB      *store.Store
	Mgr     *notifications.Manager
	SSE     *SSEBroadcaster
	Backend backend.Backend
	// Pending tracks sessions whose terminal has started but whose agent has
	// not reported in yet, which is the window the trust dialog appears in.
	Pending *PendingSessionMap
	// Signals lets a request wait for something an agent only reports later,
	// through a hook.
	Signals  *SessionSignals
	Reporter *reporter.Reporter
	// HITL renders helios's own prompts over session terminals. See
	// docs/specs/36-helios-owned-hitl.md.
	HITL *hitl.Controller
}

// overlayNotifier is implemented by backends that can report the keystrokes an
// overlay captured. Checked at runtime for the same reason screenNotifier is:
// only the host backend can paint over a session at all.
type overlayNotifier interface {
	OnOverlayInput(fn func(sessionID string, keys []byte))
}

// InternalServer handles hooks (Claude) and admin API (CLI).
// Binds to 127.0.0.1 only. No auth required.
type InternalServer struct {
	httpServer *http.Server
	shared     *Shared
}

// PublicServer handles the frontend, push API, and notification actions.
// The bind address comes from config: loopback by default, since tunnel
// providers proxy from localhost. Exposed via tunnel. Bearer JWT auth.
type PublicServer struct {
	httpServer *http.Server
	shared     *Shared
}

// injectTerminal enriches a session with the handle of its live terminal and
// what that terminal costs, so clients can tell a running session from a cold
// one and see which of them is worth closing.
func (sh *Shared) injectTerminal(sess *store.Session) {
	handle, ok := sh.Backend.Handle(sess.SessionID)
	if !ok {
		return
	}
	sess.Terminal = &handle
	if usage, ok := sh.Backend.(backend.Usager); ok {
		if rss, found := usage.Usage()[sess.SessionID]; found {
			sess.MemoryBytes = &rss
		}
	}
}

// hostStats reports what the warm pool costs and what the machine has left,
// so a client can show a session's price next to the room it is eating into.
//
// Nothing enforces the budget — the daemon evicts nobody — so it is only there
// for a client that wants to say when the pool has grown large.
func (sh *Shared) hostStats() map[string]interface{} {
	status := sh.Backend.Status()
	machine := sysinfo.Read()
	return map[string]interface{}{
		"warm":     status.Warm,
		"warm_rss": status.WarmRSS,
		// The configured share, not a fixed quarter: the same number now
		// decides what gets evicted, so what clients display has to be it.
		"budget":       uint64(float64(machine.MemoryTotal) * sh.DB.MemoryBudgetFraction()),
		"load":         machine.Load,
		"memory_used":  machine.MemoryUsed,
		"memory_total": machine.MemoryTotal,
	}
}

func NewShared(db *store.Store, mgr *notifications.Manager, be backend.Backend) *Shared {
	sh := &Shared{
		DB:      db,
		Mgr:     mgr,
		SSE:     NewSSEBroadcaster(),
		Backend: be,
		Pending: NewPendingSessionMap(),
		Signals: NewSessionSignals(),
		// "claude" is the fallback narrator only; each session is narrated by
		// its own provider when that provider has a cheap model.
		Reporter: reporter.New("claude", db),
	}
	// The manager owns notification state, so it announces its own writes. No
	// caller has to remember to, and none can resolve one silently.
	mgr.SetBroadcaster(func(eventType string, data interface{}) {
		sh.SSE.Broadcast(SSEEvent{Type: eventType, Data: data})
	})

	// A backend that cannot paint over a session gets a controller with nothing
	// to paint on, and every prompt falls back to the phone.
	overlays, _ := be.(hitl.Overlays)
	sh.HITL = hitl.NewController(overlays)
	if n, ok := be.(overlayNotifier); ok {
		n.OnOverlayInput(sh.HITL.HandleInput)
	}
	return sh
}

// NewInternalServer creates the localhost-only server for hooks and admin API.
func NewInternalServer(port int, shared *Shared) *InternalServer {
	s := &InternalServer{shared: shared}

	mux := http.NewServeMux()

	// Hook endpoint (generic — dispatches by type, e.g. POST /hooks/claude/permission)
	mux.HandleFunc("POST /hooks/{hookType...}", s.handleHook)

	// Internal admin API (CLI — no auth, localhost only)
	mux.HandleFunc("GET /internal/sessions", s.handleInternalListSessions)
	mux.HandleFunc("POST /internal/sessions", s.handleInternalCreateSession)
	mux.HandleFunc("GET /internal/health", s.handleInternalHealth)
	mux.HandleFunc("GET /internal/tunnel/status", s.handleTunnelStatus)
	mux.HandleFunc("POST /internal/tunnel/start", s.handleTunnelStart)
	mux.HandleFunc("POST /internal/tunnel/stop", s.handleTunnelStop)
	mux.HandleFunc("POST /internal/device/create", s.handleDeviceCreate)
	mux.HandleFunc("POST /internal/device/activate", s.handleDeviceActivate)
	mux.HandleFunc("POST /internal/device/rekey", s.handleDeviceRekey)
	mux.HandleFunc("GET /internal/device/list", s.handleDeviceList)
	mux.HandleFunc("POST /internal/device/revoke", s.handleDeviceRevoke)
	mux.HandleFunc("POST /internal/wrap", s.handleWrap)
	mux.HandleFunc("PATCH /internal/sessions/{id}", s.handleInternalPatchSession)
	mux.HandleFunc("POST /internal/sessions/{id}/stop", s.handleInternalSessionStop)
	mux.HandleFunc("POST /internal/sessions/{id}/terminate", s.handleInternalSessionTerminate)
	mux.HandleFunc("POST /internal/sessions/{id}/resume", s.handleInternalSessionResume)
	mux.HandleFunc("GET /internal/settings", s.handleInternalGetSettings)
	mux.HandleFunc("PUT /internal/settings", s.handleInternalUpdateSettings)
	mux.Handle("GET /internal/events", shared.SSE)
	mux.HandleFunc("GET /internal/logs", s.handleInternalLogs)

	// MCP lives here rather than on the public server because this listener is
	// already what it needs to be: loopback only, no auth. Agents reach it with
	// a session id from their prompt; see docs/specs/39-agent-driven-explain-ui.md.
	mux.Handle("/mcp", mcp.New(shared.DB, shared, shared, featureflag.MCP()))

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	return s
}

// NewPublicServer creates the tunnel-exposed server for API. bind is the
// interface to listen on; an empty value means loopback only.
func NewPublicServer(bind string, port int, shared *Shared) *PublicServer {
	s := &PublicServer{shared: shared}

	globalLimiter := newIPRateLimiter(1000, time.Minute)
	pairLimiter := newIPRateLimiter(5, time.Minute)

	mux := http.NewServeMux()

	// Landing page (no auth — download links, exact root path only)
	mux.HandleFunc("GET /{$}", handleLanding)

	// Public endpoints (no auth)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.Handle("POST /api/auth/pair", pairLimiter.middleware(http.HandlerFunc(s.handlePair)))

	// Auth-protected API endpoints
	bearerAuth := bearerAuthMiddleware(shared.DB)

	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("GET /api/sessions", s.handleListSessions)
	protectedMux.HandleFunc("GET /api/sessions/directories", s.handleListDirectories)
	// Registered before the /api/sessions/ catch-all below, which would take
	// "order" for a session id.
	protectedMux.HandleFunc("POST /api/sessions/order", s.handleSessionOrder)
	protectedMux.HandleFunc("GET /api/groups", s.handleListGroups)
	protectedMux.HandleFunc("POST /api/groups", s.handleCreateGroup)
	// Before the /api/groups/ catch-all, which would take "order" for a key.
	protectedMux.HandleFunc("POST /api/groups/order", s.handleSetGroupOrder)
	protectedMux.HandleFunc("/api/groups/{key}", s.handleGroup)
	protectedMux.HandleFunc("GET /api/files", s.handleListFiles)
	protectedMux.HandleFunc("GET /api/files/search", s.handleSearchFiles)
	protectedMux.HandleFunc("GET /api/files/grep", s.handleGrepFiles)
	protectedMux.HandleFunc("GET /api/file", s.handleReadFile)
	protectedMux.HandleFunc("PUT /api/file", s.handleWriteFile)
	protectedMux.HandleFunc("GET /api/git/status", s.handleGitStatus)
	protectedMux.HandleFunc("GET /api/git/diff", s.handleGitDiff)
	protectedMux.HandleFunc("GET /api/git/log", s.handleGitLog)
	protectedMux.HandleFunc("GET /api/git/changes", s.handleGitChanges)
	protectedMux.HandleFunc("GET /api/git/worktrees", s.handleGitWorktrees)
	protectedMux.HandleFunc("GET /api/git/reviewed", s.handleGetReviewed)
	protectedMux.HandleFunc("POST /api/git/reviewed", s.handleSetReviewed)
	protectedMux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	protectedMux.HandleFunc("POST /api/notifications/batch", s.handleBatchNotifications)
	protectedMux.Handle("GET /api/events", shared.SSE)
	protectedMux.HandleFunc("GET /api/auth/devices", s.handleListDevices)
	protectedMux.HandleFunc("POST /api/device/logs", s.handleDeviceLogs)
	protectedMux.HandleFunc("GET /api/commands", s.handleListCommands)
	protectedMux.HandleFunc("GET /api/providers", s.handleListProviders)
	protectedMux.HandleFunc("GET /api/notification-types", s.handleNotificationTypes)
	protectedMux.HandleFunc("GET /api/hooks/health", s.handleHooksHealth)
	protectedMux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	protectedMux.HandleFunc("GET /api/reporter", s.handleReporter)
	protectedMux.HandleFunc("GET /api/settings", s.handleGetSettings)
	protectedMux.HandleFunc("POST /api/settings", s.handleUpdateSettings)

	// Dynamic path handlers for providers
	protectedMux.HandleFunc("/api/providers/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "GET" && strings.HasSuffix(path, "/models"):
			s.handleListModels(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/models/refresh"):
			s.handleRefreshModels(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// Dynamic path handlers
	protectedMux.HandleFunc("/api/notifications/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/action"):
			s.handleNotificationAction(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/dismiss"):
			s.handleDismissNotification(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	protectedMux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "GET" && strings.HasSuffix(path, "/subagents"):
			s.handleListSubagents(w, r)
		case r.Method == "GET" && strings.HasSuffix(path, "/transcript"):
			s.handleSessionTranscript(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/touch"):
			s.handleSessionTouch(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/send"):
			s.handleSessionSend(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/files"):
			s.handleSessionUpload(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/stop"):
			s.handleSessionStop(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/terminate"):
			s.handleSessionTerminate(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/resume"):
			s.handleSessionResume(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/permission-mode"):
			s.handleSessionPermissionMode(w, r)
		case (r.Method == "POST" || r.Method == "GET") && strings.HasSuffix(path, "/terminals"):
			s.handleSessionTerminals(w, r)
		case r.Method == "GET" && strings.HasSuffix(path, "/terminal"):
			s.handleSessionTerminal(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/wake"):
			s.handleSessionWake(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/title/generate"):
			s.handleGenerateSessionTitle(w, r)
		case r.Method == "PATCH":
			s.handlePatchSession(w, r)
		case r.Method == "DELETE":
			s.handleDeleteSession(w, r)
		case r.Method == "GET":
			s.handleGetSession(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// One terminal by id, which is how a session's shells are addressed: the
	// per-session path above names the agent's and nothing else.
	protectedMux.HandleFunc("/api/terminals/", s.handleTerminal)

	protectedMux.HandleFunc("/api/auth/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			s.handleRevokeDevice(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	// Pending-ok routes (pending devices can poll their own status)
	pendingAuth := pendingOrActiveBearerMiddleware(shared.DB)
	pendingMux := http.NewServeMux()
	pendingMux.HandleFunc("GET /api/auth/device/me", s.handleDeviceMe)
	pendingMux.HandleFunc("POST /api/auth/device/me", s.handleUpdateDeviceMe)
	mux.Handle("GET /api/auth/device/me", pendingAuth(pendingMux))
	mux.Handle("POST /api/auth/device/me", pendingAuth(pendingMux))

	// Wire protected routes through Bearer auth middleware
	mux.Handle("/api/", bearerAuth(protectedMux))

	if bind == "" {
		bind = "127.0.0.1"
	}
	s.httpServer = &http.Server{
		Addr:    net.JoinHostPort(bind, strconv.Itoa(port)),
		Handler: globalLimiter.middleware(mux),
	}

	return s
}

func (s *InternalServer) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *InternalServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *PublicServer) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *PublicServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
