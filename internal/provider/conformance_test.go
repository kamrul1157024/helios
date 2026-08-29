package provider_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
	claude "github.com/kamrul1157024/helios/internal/provider/claude"
	codex "github.com/kamrul1157024/helios/internal/provider/codex"
)

// Every provider the daemon registers, registered the same way here.
//
// A registry cannot be type-checked, so this is what keeps it honest. The
// table is deliberately not built from provider.All(): a provider that fails
// to register at all should fail this test rather than silently not be
// covered by it.
func registered(t *testing.T) []provider.Provider {
	t.Helper()
	return []provider.Provider{claude.New(0), codex.New(0)}
}

func TestConformance(t *testing.T) {
	seen := map[string]bool{}

	for _, p := range registered(t) {
		info := p.Info()
		t.Run(info.ID, func(t *testing.T) {
			if info.ID == "" {
				t.Fatal("empty ID")
			}
			if info.ID != strings.ToLower(info.ID) {
				t.Errorf("ID %q is not lower-case", info.ID)
			}
			if seen[info.ID] {
				t.Fatalf("duplicate ID %q", info.ID)
			}
			seen[info.ID] = true
			if info.Name == "" {
				t.Error("empty Name; clients have nothing to show")
			}
			if info.Kind == "" {
				t.Error("empty Kind")
			}

			// A provider must start something from an empty spec, because that
			// is what "new session, no options" sends.
			launch, err := p.Launch(provider.SessionSpec{})
			if err != nil {
				t.Fatalf("Launch on an empty spec: %v", err)
			}
			if len(launch.Argv) == 0 {
				t.Fatal("Launch returned no argv")
			}
			if _, err := exec.LookPath(launch.Argv[0]); err != nil &&
				strings.ContainsRune(launch.Argv[0], '/') {
				t.Errorf("argv[0] %q is an absolute path that does not resolve", launch.Argv[0])
			}

			checkHookRoutes(t, p, info.ID)
			checkActionRoutes(t, p, info.ID)
			checkModes(t, p)
			checkResume(t, p)
		})
	}
}

// A hook route becomes a URL under the provider's own prefix, so it must be a
// clean relative path. A leading slash or a ".." would let one provider serve
// another's namespace.
func checkHookRoutes(t *testing.T, p provider.Provider, id string) {
	t.Helper()
	h, ok := p.(provider.Hooker)
	if !ok {
		return
	}
	for route, handler := range h.HookRoutes() {
		if handler == nil {
			t.Errorf("hook route %q has a nil handler", route)
		}
		if route == "" || strings.HasPrefix(route, "/") || strings.Contains(route, "..") {
			t.Errorf("hook route %q is not a clean relative path", route)
		}
	}
}

// An action's key is the notification type it answers, and the type must be in
// the provider's namespace or two providers will collide on it.
func checkActionRoutes(t *testing.T, p provider.Provider, id string) {
	t.Helper()
	a, ok := p.(provider.Actor)
	if !ok {
		return
	}
	for notifType, route := range a.ActionRoutes() {
		if !strings.HasPrefix(notifType, id+".") {
			t.Errorf("action type %q is not prefixed with %q", notifType, id)
		}
		if route.Handler == nil {
			t.Errorf("action %q has a nil handler", notifType)
		}
		// The clients render this catalogue instead of hardcoding one, so a
		// blank label ships as a blank row in a settings screen.
		if route.Label == "" {
			t.Errorf("action %q has no label", notifType)
		}
		if route.Group != "action_required" && route.Group != "info" {
			t.Errorf("action %q has group %q, want action_required or info", notifType, route.Group)
		}
	}
}

func checkModes(t *testing.T, p provider.Provider) {
	t.Helper()
	m, ok := p.(provider.Moder)
	if !ok {
		return
	}
	modes := m.PermissionModes()
	if len(modes) == 0 {
		t.Error("implements Moder but offers no modes")
	}
	seen := map[string]bool{}
	for _, mode := range modes {
		if mode == "" {
			t.Error("empty permission mode")
		}
		if seen[mode] {
			t.Errorf("duplicate permission mode %q", mode)
		}
		seen[mode] = true
		if !m.ValidMode(mode) {
			t.Errorf("mode %q is offered but ValidMode rejects it", mode)
		}
	}
	if m.ValidMode("definitely-not-a-mode") {
		t.Error("ValidMode accepts an unknown mode")
	}
}

func checkResume(t *testing.T, p provider.Provider) {
	t.Helper()
	r, ok := p.(provider.Resumer)
	if !ok {
		return
	}
	// A resume with an id must produce a command. Without one it may return
	// nothing — that is how a provider says "this session cannot be woken",
	// which is the honest answer for an agent that mints its own id before
	// helios has heard it.
	launch, err := r.Resume("sess-1", "sess-1", "")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(launch.Argv) == 0 {
		t.Error("Resume with a resume id returned no argv")
	}
}

// Capabilities are read off the interfaces a provider implements, so a
// provider cannot claim one it has not got. This pins that they are derived
// rather than declared.
func TestCapabilitiesAreDerived(t *testing.T) {
	provider.Deregister("conformance-minimal")
	if err := provider.Register(minimalProvider{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { provider.Deregister("conformance-minimal") })

	caps := provider.CapabilitiesOf("conformance-minimal")
	if caps.Resume || caps.Hooks || caps.Transcript || caps.Titles ||
		caps.Discovery || caps.PromptQueue || caps.PermissionCards {
		t.Errorf("a two-method provider claims capabilities: %+v", caps)
	}

	// And every accessor degrades to nil rather than panicking, which is what
	// lets the daemon call them unconditionally.
	if provider.ResumerFor("conformance-minimal") != nil {
		t.Error("ResumerFor returned non-nil for a provider without Resume")
	}
	if provider.HookerFor("conformance-minimal") != nil {
		t.Error("HookerFor returned non-nil for a provider without hooks")
	}
	if provider.ResumerFor("no-such-provider") != nil {
		t.Error("ResumerFor returned non-nil for an unregistered provider")
	}
}

// minimalProvider implements the two required methods and nothing else.
type minimalProvider struct{}

func (minimalProvider) Info() provider.Info {
	return provider.Info{ID: "conformance-minimal", Name: "Minimal", Kind: provider.KindNative}
}

func (minimalProvider) Launch(provider.SessionSpec) (provider.Launch, error) {
	return provider.Launch{Argv: []string{"true"}}, nil
}

// The catalogue is what the clients render instead of holding their own copy,
// so it has to be complete and stably ordered.
func TestNotificationTypesAreServedAndSorted(t *testing.T) {
	for _, p := range registered(t) {
		provider.Deregister(p.Info().ID)
		if err := provider.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Info().ID, err)
		}
		t.Cleanup(func() { provider.Deregister(p.Info().ID) })
	}

	types := provider.NotificationTypes()
	if len(types) == 0 {
		t.Fatal("no notification types served")
	}

	var last string
	byProvider := map[string]int{}
	for _, nt := range types {
		key := nt.Provider + "\x00" + nt.Type
		if last != "" && key < last {
			t.Errorf("catalogue is not sorted: %q after %q", key, last)
		}
		last = key
		byProvider[nt.Provider]++
		if !strings.HasPrefix(nt.Type, nt.Provider+".") {
			t.Errorf("type %q does not belong to provider %q", nt.Type, nt.Provider)
		}
	}
	for _, want := range []string{"claude", "codex"} {
		if byProvider[want] == 0 {
			t.Errorf("no notification types for %q", want)
		}
	}
}
