package server

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

type Server struct {
	httpServer *http.Server
	db         *store.Store
	mgr        *notifications.Manager
	sse        *SSEBroadcaster
}

func New(bind string, port int, db *store.Store, mgr *notifications.Manager, authEnabled, skipLocal bool, frontendFS fs.FS) *Server {
	s := &Server{
		db:  db,
		mgr: mgr,
		sse: NewSSEBroadcaster(),
	}

	mux := http.NewServeMux()

	// Hook endpoints (no auth — localhost only from Claude)
	mux.HandleFunc("POST /hooks/permission", s.handlePermissionHook)
	mux.HandleFunc("POST /hooks/stop", s.handleStopHook)
	mux.HandleFunc("POST /hooks/stop-failure", s.handleStopFailureHook)
	mux.HandleFunc("POST /hooks/notification", s.handleNotificationHook)
	mux.HandleFunc("POST /hooks/session-start", s.handleSessionStartHook)
	mux.HandleFunc("POST /hooks/session-end", s.handleSessionEndHook)

	// Public endpoints (no auth)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/auth/verify", s.handleVerifyAuth)

	// Auth-protected API endpoints
	authMw := authMiddleware(db, authEnabled, skipLocal)

	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	protectedMux.HandleFunc("POST /api/notifications/batch", s.handleBatchNotifications)
	protectedMux.Handle("GET /api/events", s.sse)
	protectedMux.HandleFunc("GET /api/auth/devices", s.handleListDevices)

	// Dynamic path handlers need manual routing
	protectedMux.HandleFunc("/api/notifications/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/approve"):
			s.handleApproveNotification(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/deny"):
			s.handleDenyNotification(w, r)
		case r.Method == "POST" && strings.HasSuffix(path, "/dismiss"):
			s.handleDismissNotification(w, r)
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

	// Wire protected routes through auth middleware
	mux.Handle("/api/", authMw(protectedMux))

	// Serve embedded frontend (SPA fallback)
	if frontendFS != nil {
		mux.Handle("/", ServeFrontend(frontendFS))
	}

	// CORS wrapper
	handler := corsMiddleware(mux)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", bind, port),
		Handler: handler,
	}

	return s
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
