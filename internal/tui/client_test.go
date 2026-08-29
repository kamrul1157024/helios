package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The setup screen shows "Generating pairing code..." exactly while the token
// is empty, so a failure that returns an empty token with no error spins for
// ever and says nothing. Every one of these must be an error.
func TestDeviceCreateReportsFailures(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want string
	}{
		{"server error", 500, `{"error":"failed to store pairing token"}`, "500"},
		{"not json", 200, `<html>gateway</html>`, "unreadable"},
		{"empty token", 200, `{"expires_in":120}`, "no pairing token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := testClient(srv.URL).deviceCreate()
			if err == nil {
				t.Fatal("reported success")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestDeviceCreateAcceptsAGoodAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"abc","expires_in":120,"setup_url":""}`))
	}))
	defer srv.Close()

	got, err := testClient(srv.URL).deviceCreate()
	if err != nil {
		t.Fatalf("deviceCreate: %v", err)
	}
	if got.Token != "abc" {
		t.Errorf("token = %q, want abc", got.Token)
	}
}

// testClient points a client at an arbitrary base URL. newClient builds one
// from a port on localhost, which a test server does not have.
func testClient(baseURL string) *client {
	return &client{
		baseURL:        baseURL,
		httpClient:     &http.Client{Timeout: 3 * time.Second},
		longHTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}
