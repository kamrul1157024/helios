package tailscale

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// multiPeerStatus mirrors the shape of a real `tailscale status --json`: Self
// is not the first DNSName in the document. The provider this package replaced
// scraped the first regex match and would pick a peer instead of this node.
const multiPeerStatus = `{
  "Version": "1.102.2",
  "BackendState": "Running",
  "MagicDNSSuffix": "tail20015d.ts.net",
  "CertDomains": null,
  "Peer": {
    "nodekey:aaa": {"DNSName": "other-laptop.tail20015d.ts.net.", "Online": true},
    "nodekey:bbb": {"DNSName": "phone.tail20015d.ts.net.", "Online": false}
  },
  "Self": {
    "DNSName": "mds-macbook-air.tail20015d.ts.net.",
    "TailscaleIPs": ["100.98.93.19", "fd7a:115c:a1e0::5934:5d14"]
  }
}`

func TestApplyStatusSelectsSelfNotAPeer(t *testing.T) {
	var status statusJSON
	if err := json.Unmarshal([]byte(multiPeerStatus), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var state State
	applyStatus(&state, status)

	if want := "mds-macbook-air.tail20015d.ts.net"; state.DNSName != want {
		t.Errorf("DNSName = %q, want %q (a peer name means Self was not read by key)", state.DNSName, want)
	}
	if strings.HasSuffix(state.DNSName, ".") {
		t.Error("DNSName retains its trailing dot; it must be trimmed")
	}
	if !state.LoggedIn {
		t.Error("LoggedIn = false for BackendState=Running")
	}
	if !state.MagicDNS {
		t.Error("MagicDNS = false despite a MagicDNSSuffix")
	}
	if state.CertsReady {
		t.Error("CertsReady = true for CertDomains: null")
	}
}

func TestApplyStatusCertDomains(t *testing.T) {
	var status statusJSON
	const withCerts = `{"BackendState":"Running","MagicDNSSuffix":"x.ts.net",
	  "CertDomains":["host.x.ts.net"],"Self":{"DNSName":"host.x.ts.net."}}`
	if err := json.Unmarshal([]byte(withCerts), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var state State
	applyStatus(&state, status)

	if !state.CertsReady {
		t.Error("CertsReady = false despite a populated CertDomains")
	}
}

// TestStateReadinessIsModeSpecific pins the central decision of this design:
// missing certificates block Funnel but must not block Serve.
func TestStateReadinessIsModeSpecific(t *testing.T) {
	state := State{
		Installed: true, Running: true, LoggedIn: true, MagicDNS: true,
		DNSName: "host.x.ts.net", CertsReady: false,
	}

	if !state.Ready() {
		t.Error("Ready() = false without certificates; Serve does not need them")
	}
	if state.FunnelReady() {
		t.Error("FunnelReady() = true without certificates")
	}
	if state.Problem() != "" {
		t.Errorf("Problem() = %q, want empty for a Serve-ready node", state.Problem())
	}
	if !strings.Contains(state.FunnelProblem(), "HTTPS certificates") {
		t.Errorf("FunnelProblem() = %q, want it to name the certificate requirement", state.FunnelProblem())
	}
}

// TestStateProblemDistinguishesFailures guards against collapsing the failure
// modes into one unhelpful message: each has a different remedy.
func TestStateProblemDistinguishesFailures(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  string
	}{
		{"not installed", State{}, "not installed"},
		{"daemon down", State{Installed: true}, "daemon is not running"},
		{"logged out", State{Installed: true, Running: true, BackendState: "NeedsLogin"}, "tailscale up"},
		{"magicdns off", State{Installed: true, Running: true, LoggedIn: true}, "MagicDNS"},
		{"no dns name", State{Installed: true, Running: true, LoggedIn: true, MagicDNS: true}, "DNS name"},
	}

	seen := make(map[string]string)
	for _, tt := range tests {
		got := tt.state.Problem()
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s: Problem() = %q, want it to mention %q", tt.name, got, tt.want)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%s and %s produce the identical message %q", tt.name, prev, got)
		}
		seen[got] = tt.name
	}
}

// TestPermissionHintRewritesAccessDenied pins the remedy surfaced when
// tailscaled refuses a serve/funnel mutation for lacking permission — the raw
// CLI message is correct but two lines of prose that a cramped error panel
// truncates.
func TestPermissionHintRewritesAccessDenied(t *testing.T) {
	raw := fmt.Errorf("tailscale serve --bg --yes --http=7655 http://127.0.0.1:7655: " +
		"sending serve config: Access denied: serve config denied")

	got := permissionHint(raw)
	if !strings.Contains(got.Error(), "sudo tailscale set --operator=") {
		t.Errorf("permissionHint(%v) = %q, want it to name the operator remedy", raw, got)
	}

	other := fmt.Errorf("tailscale status: connection refused")
	if got := permissionHint(other); got != other {
		t.Errorf("permissionHint on an unrelated error = %q, want it unchanged", got)
	}

	if got := permissionHint(nil); got != nil {
		t.Errorf("permissionHint(nil) = %v, want nil", got)
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeServe, false},
		{"serve", ModeServe, false},
		{"funnel", ModeFunnel, false},
		{"  Funnel  ", ModeFunnel, false},
		{"SERVE", ModeServe, false},
		{"public", "", true},
	}

	for _, tt := range tests {
		got, err := ParseMode(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q): expected an error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestValidatePort covers the asymmetry between the modes: Funnel terminates
// on three fixed ports, Serve on any.
func TestValidatePort(t *testing.T) {
	tests := []struct {
		mode    Mode
		port    int
		wantErr bool
	}{
		{ModeFunnel, 443, false},
		{ModeFunnel, 8443, false},
		{ModeFunnel, 10000, false},
		{ModeFunnel, 7655, true},
		{ModeFunnel, 8080, true},
		{ModeServe, 7655, false},
		{ModeServe, 8080, false},
		{ModeServe, 443, false},
		{ModeServe, 0, true},
		{ModeServe, 70000, true},
	}

	for _, tt := range tests {
		err := ValidatePort(tt.mode, tt.port)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePort(%q, %d) error = %v, wantErr = %v", tt.mode, tt.port, err, tt.wantErr)
		}
	}
}

func TestDefaultPort(t *testing.T) {
	if got := DefaultPort(ModeServe); got != DefaultServePort {
		t.Errorf("DefaultPort(serve) = %d, want %d", got, DefaultServePort)
	}
	if got := DefaultPort(ModeFunnel); got != DefaultFunnelPort {
		t.Errorf("DefaultPort(funnel) = %d, want %d", got, DefaultFunnelPort)
	}
}

// TestBuildURL pins the scheme split: Serve is http (WireGuard already
// encrypts), Funnel is https (public clients have no tunnel).
func TestBuildURL(t *testing.T) {
	tests := []struct {
		mode Mode
		port int
		want string
	}{
		{ModeServe, 7655, "http://host.x.ts.net:7655"},
		{ModeServe, 8080, "http://host.x.ts.net:8080"},
		{ModeServe, 80, "http://host.x.ts.net"},
		{ModeFunnel, 443, "https://host.x.ts.net"},
		{ModeFunnel, 8443, "https://host.x.ts.net:8443"},
		{ModeFunnel, 10000, "https://host.x.ts.net:10000"},
	}

	for _, tt := range tests {
		tun := New(tt.mode, tt.port)
		tun.dnsName = "host.x.ts.net"
		if got := tun.buildURL(); got != tt.want {
			t.Errorf("buildURL(%q, %d) = %q, want %q", tt.mode, tt.port, got, tt.want)
		}
	}
}

func TestNewAppliesDefaultPort(t *testing.T) {
	if got := New(ModeServe, 0).Port(); got != DefaultServePort {
		t.Errorf("New(serve, 0).Port() = %d, want %d", got, DefaultServePort)
	}
	if got := New(ModeFunnel, 0).Port(); got != DefaultFunnelPort {
		t.Errorf("New(funnel, 0).Port() = %d, want %d", got, DefaultFunnelPort)
	}
}

// TestTunnelOwnsNoProcess documents why this provider needs Reconcile: a PID
// cannot answer whether the mapping is still published.
func TestTunnelOwnsNoProcess(t *testing.T) {
	tun := New(ModeServe, 0)
	if tun.PID() != 0 {
		t.Errorf("PID() = %d, want 0", tun.PID())
	}
	if tun.URL() != "" {
		t.Errorf("URL() before Start = %q, want empty", tun.URL())
	}
	if tun.Provider() != "tailscale" {
		t.Errorf("Provider() = %q, want tailscale", tun.Provider())
	}
}

// TestStopBeforeStartIsNoop ensures a never-started tunnel does not shell out
// and tear down a mapping it does not own.
func TestStopBeforeStartIsNoop(t *testing.T) {
	if err := New(ModeServe, 0).Stop(); err != nil {
		t.Errorf("Stop before Start: %v", err)
	}
}

func TestPublishAndOffArgs(t *testing.T) {
	serve := New(ModeServe, 7655)
	serve.target = "http://127.0.0.1:7655"
	got := strings.Join(serve.publishArgs(), " ")
	if want := "serve --bg --yes --http=7655 http://127.0.0.1:7655"; got != want {
		t.Errorf("serve publishArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(serve.offArgs(), " "), "serve --http=7655 off"; got != want {
		t.Errorf("serve offArgs = %q, want %q", got, want)
	}

	funnel := New(ModeFunnel, 443)
	funnel.target = "http://127.0.0.1:7655"
	got = strings.Join(funnel.publishArgs(), " ")
	if want := "funnel --bg --yes --https=443 http://127.0.0.1:7655"; got != want {
		t.Errorf("funnel publishArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(funnel.offArgs(), " "), "funnel --https=443 off"; got != want {
		t.Errorf("funnel offArgs = %q, want %q", got, want)
	}
}

// TestServeConfigParsing works against the real shape tailscaled emits.
func TestServeConfigParsing(t *testing.T) {
	const raw = `{
	  "TCP": {"7655": {"HTTP": true}},
	  "Web": {
	    "host.x.ts.net:7655": {
	      "Handlers": {"/": {"Proxy": "http://127.0.0.1:7655"}}
	    }
	  }
	}`

	var cfg serveConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.empty() {
		t.Error("empty() = true for a populated config")
	}
	if got := cfg.proxyTarget("host.x.ts.net:7655"); got != "http://127.0.0.1:7655" {
		t.Errorf("proxyTarget = %q, want the configured proxy", got)
	}
	if got := cfg.proxyTarget("host.x.ts.net:8443"); got != "" {
		t.Errorf("proxyTarget for an unpublished port = %q, want empty", got)
	}
}

// TestServeConfigLiveDocument uses output captured verbatim from
// `tailscale serve status --json` during the §8 validation run, so the shape
// the rest of the package assumes has a provenance beyond someone's memory of
// the docs. proxyTarget's answer here is exactly what waitPublished and
// Reconcile compare against.
func TestServeConfigLiveDocument(t *testing.T) {
	const raw = `{
  "TCP": {
    "8080": {
      "HTTP": true
    }
  },
  "Web": {
    "mds-macbook-air.tail20015d.ts.net:8080": {
      "Handlers": {
        "/": {
          "Proxy": "http://127.0.0.1:9999"
        }
      }
    }
  }
}`

	var cfg serveConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.empty() {
		t.Error("empty() = true for a live published config")
	}
	if got := cfg.proxyTarget("mds-macbook-air.tail20015d.ts.net:8080"); got != "http://127.0.0.1:9999" {
		t.Errorf("proxyTarget = %q, want http://127.0.0.1:9999", got)
	}
	// Serve does not set AllowFunnel, which is what keeps a serve mapping from
	// ever being mistaken for a public one.
	if cfg.AllowFunnel["mds-macbook-air.tail20015d.ts.net:8080"] {
		t.Error("a serve-only config reports AllowFunnel")
	}
}

// TestServeConfigEmptyDocument covers the unconfigured node, which prints "{}".
func TestServeConfigEmptyDocument(t *testing.T) {
	var cfg serveConfig
	if err := json.Unmarshal([]byte(`{}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.empty() {
		t.Error("empty() = false for {}")
	}
	if got := cfg.proxyTarget("host.x.ts.net:7655"); got != "" {
		t.Errorf("proxyTarget on an empty config = %q, want empty", got)
	}
}

func TestServeConfigFunnelFlag(t *testing.T) {
	const raw = `{
	  "TCP": {"443": {"HTTPS": true}},
	  "Web": {"host.x.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:7655"}}}},
	  "AllowFunnel": {"host.x.ts.net:443": true}
	}`

	var cfg serveConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.AllowFunnel["host.x.ts.net:443"] {
		t.Error("AllowFunnel not parsed")
	}
	if cfg.AllowFunnel["host.x.ts.net:8443"] {
		t.Error("AllowFunnel reports true for an unconfigured host:port")
	}
}
