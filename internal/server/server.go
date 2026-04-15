package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/reporter"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

// Shared holds shared dependencies between internal and public servers.
type Shared struct {
	DB           *store.Store
	Mgr          *notifications.Manager
	SSE          *SSEBroadcaster
	Tmux         *tmux.Client
	PendingPanes *PendingPaneMap
	PaneMap      *tmux.PaneMap
	Reporter     *reporter.Reporter
}

// InternalServer handles hooks (Claude) and admin API (CLI).
// Binds to 127.0.0.1 only. No auth required.
type InternalServer struct {
	httpServer *http.Server
	shared     *Shared
}

// PublicServer handles the frontend, push API, and notification actions.
// Binds to 0.0.0.0, exposed via tunnel. Bearer JWT auth.
type PublicServer struct {
	httpServer *http.Server
	shared     *Shared
}

// injectPane enriches a session with its current pane ID from the live PaneMap.
func (sh *Shared) injectPane(sess *store.Session) {
	if paneID, ok := sh.PaneMap.Get(sess.SessionID); ok {
		sess.TmuxPane = &paneID
	}
}

func NewShared(db *store.Store, mgr *notifications.Manager) *Shared {
	return &Shared{
		DB:           db,
		Mgr:          mgr,
		SSE:          NewSSEBroadcaster(),
		Tmux:         tmux.NewClient(),
		PendingPanes: NewPendingPaneMap(),
		PaneMap:      tmux.NewPaneMap(),
		Reporter:     reporter.New("claude", db),
	}
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

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	return s
}

// NewPublicServer creates the tunnel-exposed server for API.
func NewPublicServer(port int, shared *Shared) *PublicServer {
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
	protectedMux.HandleFunc("GET /api/files", s.handleListFiles)
	protectedMux.HandleFunc("GET /api/file", s.handleReadFile)
	protectedMux.HandleFunc("GET /api/git/status", s.handleGitStatus)
	protectedMux.HandleFunc("GET /api/git/diff", s.handleGitDiff)
	protectedMux.HandleFunc("GET /api/git/worktrees", s.handleGitWorktrees)
	protectedMux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	protectedMux.HandleFunc("POST /api/notifications/batch", s.handleBatchNotifications)
	protectedMux.Handle("GET /api/events", shared.SSE)
	protectedMux.HandleFunc("GET /api/auth/devices", s.handleListDevices)
	protectedMux.HandleFunc("POST /api/device/logs", s.handleDeviceLogs)
	protectedMux.HandleFunc("GET /api/commands", s.handleListCommands)
	protectedMux.HandleFunc("GET /api/providers", s.handleListProviders)
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
		case r.Method == "POST" && strings.HasSuffix(path, "/send"):
			s.handleSessionSend(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/stop"):
			s.handleSessionStop(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/terminate"):
			s.handleSessionTerminate(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/resume"):
			s.handleSessionResume(w, r)
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

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),
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
