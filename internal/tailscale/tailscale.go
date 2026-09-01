// Package tailscale detects the local Tailscale installation and publishes the
// helios public server on the tailnet.
//
// Two exposure modes are supported. Serve keeps the server inside the tailnet
// and speaks plain HTTP: every tailnet packet is already WireGuard-encrypted
// end to end, so terminating TLS as well would buy nothing and would cost a
// Certificate Transparency disclosure. Funnel exposes the server to the public
// internet and therefore must terminate TLS, which requires HTTPS certificates
// to be enabled for the tailnet.
package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

// commandTimeout bounds every CLI invocation. The tailscale CLI talks to a
// local daemon over a unix socket, so anything slower than this is a hang.
const commandTimeout = 10 * time.Second

// darwinBundlePaths are the CLI locations inside the macOS app bundle, which
// is not on $PATH for GUI-installed Tailscale.
var darwinBundlePaths = []string{
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/Applications/Tailscale.app/Contents/MacOS/tailscale",
}

// Binary resolves the tailscale CLI: $PATH first, then the macOS app bundle.
func Binary() (string, error) {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "darwin" {
		for _, path := range darwinBundlePaths {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("tailscale CLI not found in $PATH or /Applications/Tailscale.app")
}

// State is the detection result. It drives both the recommendation surface and
// the error messages, so every failure mode is a distinct field rather than a
// single "unavailable" boolean.
type State struct {
	Installed  bool
	Running    bool   // the local tailscaled answered
	LoggedIn   bool   // BackendState == "Running"
	MagicDNS   bool   // MagicDNSSuffix present; required by both modes
	CertsReady bool   // HTTPS certificates enabled; required by Funnel only
	DNSName    string // Self.DNSName with the trailing dot trimmed
	ServeInUse bool   // a serve config already exists on this node

	BackendState string // raw value, for diagnostics
	BinaryPath   string
}

// Ready reports whether Serve can be started right now.
func (s State) Ready() bool {
	return s.Installed && s.Running && s.LoggedIn && s.MagicDNS && s.DNSName != ""
}

// FunnelReady reports whether Funnel can be started right now. Funnel needs
// everything Serve needs plus HTTPS certificates.
func (s State) FunnelReady() bool { return s.Ready() && s.CertsReady }

// Problem returns a human-readable description of the first unmet requirement
// along with its remedy, or "" when Serve is ready. Callers must not collapse
// these into a generic "Tailscale unavailable": each has a different fix.
func (s State) Problem() string {
	switch {
	case !s.Installed:
		return "Tailscale is not installed — see https://tailscale.com/download"
	case !s.Running:
		return "Tailscale is installed but its daemon is not running — start the Tailscale app"
	case !s.LoggedIn:
		return fmt.Sprintf("Tailscale is not logged in (state: %s) — run `tailscale up`", s.BackendState)
	case !s.MagicDNS:
		return "MagicDNS is disabled — enable it in the Tailscale admin console under DNS"
	case s.DNSName == "":
		return "Tailscale did not report a DNS name for this machine"
	}
	return ""
}

// FunnelProblem returns the unmet requirement for Funnel specifically.
func (s State) FunnelProblem() string {
	if p := s.Problem(); p != "" {
		return p
	}
	if !s.CertsReady {
		return "HTTPS certificates are not enabled for this tailnet — enable them in the " +
			"admin console under DNS. Note this publishes this machine's name to public " +
			"Certificate Transparency logs. Tailscale Serve does not require this."
	}
	return ""
}

// statusJSON is the subset of `tailscale status --json` that we consume. Fields
// are read by name rather than scraped, so extra peers cannot be mistaken for
// this node.
type statusJSON struct {
	BackendState   string   `json:"BackendState"`
	MagicDNSSuffix string   `json:"MagicDNSSuffix"`
	CertDomains    []string `json:"CertDomains"`
	Self           struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
}

// Detect inspects the local Tailscale installation. A missing or unreachable
// Tailscale is reported through State, not as an error; an error means the
// detection itself failed in an unexpected way.
func Detect(ctx context.Context) (State, error) {
	var state State

	binary, err := Binary()
	if err != nil {
		return state, nil
	}
	state.Installed = true
	state.BinaryPath = binary

	out, err := run(ctx, binary, "status", "--json")
	if err != nil {
		// tailscaled not running, or not reachable by this user.
		return state, nil
	}
	state.Running = true

	var status statusJSON
	if err := json.Unmarshal(out, &status); err != nil {
		return state, fmt.Errorf("parse tailscale status: %w", err)
	}

	applyStatus(&state, status)

	if cfg, err := loadServeConfig(ctx, binary); err == nil {
		state.ServeInUse = !cfg.empty()
	}

	return state, nil
}

// applyStatus maps a parsed status document onto the detection result. Split
// out from Detect so the classification can be tested without a live tailnet.
func applyStatus(state *State, status statusJSON) {
	state.BackendState = status.BackendState
	state.LoggedIn = status.BackendState == "Running"
	state.MagicDNS = status.MagicDNSSuffix != ""
	state.CertsReady = len(status.CertDomains) > 0
	state.DNSName = strings.TrimSuffix(status.Self.DNSName, ".")
}

// run executes the tailscale CLI and returns stdout. Stderr is folded into the
// error, because the CLI reports actionable failures there.
func run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("tailscale %s: %s", strings.Join(args, " "), msg)
		}
		return nil, fmt.Errorf("tailscale %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// permissionHint rewrites tailscaled's "Access denied" response into a direct
// remedy. On Linux, publishing a serve/funnel mapping requires either root or
// a designated operator; the raw CLI message states this correctly but as two
// lines of prose, which a cramped error panel (the TUI, the mobile app)
// truncates before the fix is legible.
func permissionHint(err error) error {
	if err == nil || !strings.Contains(err.Error(), "Access denied") {
		return err
	}
	username := "$USER"
	if u, uerr := user.Current(); uerr == nil && u.Username != "" {
		username = u.Username
	}
	return fmt.Errorf("tailscale needs elevated permission on this machine — "+
		"run `sudo tailscale set --operator=%s` once, then retry", username)
}
