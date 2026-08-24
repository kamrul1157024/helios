package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/store"
)

// MCP is unauthenticated, so where it is mounted is the only thing keeping it
// off the network. The public listener binds 0.0.0.0 when the tunnel provider
// is "local"; the internal one is always loopback. Moving /mcp across would
// hand every tool on it to anyone who can reach the tunnel.
func TestMCPIsInternalOnly(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	shared := NewShared(db, notifications.NewManager(db), newStubBackend())

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`

	public := NewPublicServer("", 0, shared)
	rec := httptest.NewRecorder()
	public.httpServer.Handler.ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize)))
	if strings.Contains(rec.Body.String(), "protocolVersion") {
		t.Fatal("MCP answered on the public server")
	}

	internal := NewInternalServer(0, shared)
	rec = httptest.NewRecorder()
	internal.httpServer.Handler.ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initialize)))
	if !strings.Contains(rec.Body.String(), "protocolVersion") {
		t.Fatalf("MCP not served internally: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(internal.httpServer.Addr, "127.0.0.1:") {
		t.Fatalf("internal listener binds %q, not loopback", internal.httpServer.Addr)
	}
}
