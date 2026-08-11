package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClientIPTrustsForwardedOnlyFromLoopback is the security property of the
// fix: a local reverse proxy may name the real client, a direct remote peer may
// not. Without the loopback scope, any caller could pick its own rate-limit
// bucket and defeat the pairing limiter.
func TestClientIPTrustsForwardedOnlyFromLoopback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{
			name:       "loopback peer with forwarded header",
			remoteAddr: "127.0.0.1:54321",
			forwarded:  "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "loopback peer, forwarded chain uses left-most",
			remoteAddr: "127.0.0.1:54321",
			forwarded:  "203.0.113.7, 70.41.3.18, 150.172.238.178",
			want:       "203.0.113.7",
		},
		{
			name:       "IPv6 loopback peer",
			remoteAddr: "[::1]:54321",
			forwarded:  "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "remote peer cannot spoof",
			remoteAddr: "198.51.100.9:44444",
			forwarded:  "203.0.113.7",
			want:       "198.51.100.9",
		},
		{
			name:       "remote IPv6 peer cannot spoof",
			remoteAddr: "[2001:db8::1]:44444",
			forwarded:  "203.0.113.7",
			want:       "2001:db8::1",
		},
		{
			name:       "loopback peer, no header",
			remoteAddr: "127.0.0.1:54321",
			want:       "127.0.0.1",
		},
		{
			name:       "loopback peer, garbage header falls back to peer",
			remoteAddr: "127.0.0.1:54321",
			forwarded:  "not-an-ip",
			want:       "127.0.0.1",
		},
		{
			name:       "loopback peer, empty first element falls back",
			remoteAddr: "127.0.0.1:54321",
			forwarded:  ", 203.0.113.7",
			want:       "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/auth/pair", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := clientIP(r); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRateLimiterSeparatesProxiedClients is the behavioural consequence: two
// phones pairing through the same tunnel must not share one bucket. Before the
// fix both presented as 127.0.0.1 and the second was throttled by the first.
func TestRateLimiterSeparatesProxiedClients(t *testing.T) {
	limiter := newIPRateLimiter(2, time.Minute)
	handler := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	send := func(forwarded string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/pair", nil)
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	// Exhaust the first client's budget.
	for i := 0; i < 2; i++ {
		if code := send("203.0.113.7"); code != http.StatusOK {
			t.Fatalf("request %d from first client = %d, want 200", i+1, code)
		}
	}
	if code := send("203.0.113.7"); code != http.StatusTooManyRequests {
		t.Errorf("third request from first client = %d, want 429", code)
	}

	// A different client behind the same proxy must still be served.
	if code := send("198.51.100.9"); code != http.StatusOK {
		t.Errorf("second client = %d, want 200 (buckets are shared)", code)
	}
}
