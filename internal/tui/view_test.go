package tui

import (
	"strings"
	"testing"
	"time"
)

// stripANSI removes SGR escape sequences so assertions match what a terminal
// would put on the clipboard rather than what lipgloss wrote to the screen.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// lineWith returns the first stripped line containing want, or "" if none does.
func lineWith(rendered, want string) string {
	for _, line := range strings.Split(stripANSI(rendered), "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

func TestMainShowsPairingURLAsCopyableLine(t *testing.T) {
	setupURL := "helios://pair?url=http://host.tailnet.ts.net:7655&token=abc123"
	m := StartModel{
		screen:         screenMain,
		tunnelOK:       true,
		tunnelURL:      "http://host.tailnet.ts.net:7655",
		tunnelProv:     "tailscale",
		pairingQR:      "##\n##",
		pairingToken:   "abc123",
		setupURL:       setupURL,
		tokenExpiresAt: time.Now().Add(2 * time.Minute),
	}

	line := lineWith(m.viewMain(), setupURL)
	if line == "" {
		t.Fatalf("pairing URL %q not rendered as text", setupURL)
	}
	if line != setupURL {
		t.Errorf("pairing URL line must hold the URL and nothing else, got %q", line)
	}
}

func TestMainShowsTunnelURL(t *testing.T) {
	url := "http://host.tailnet.ts.net:7655"
	m := StartModel{
		screen:     screenMain,
		tunnelOK:   true,
		tunnelURL:  url,
		tunnelProv: "tailscale",
		downloadQR: "##\n##",
	}

	rendered := stripANSI(m.viewMain())
	if !strings.Contains(rendered, "Tunnel: "+url) {
		t.Errorf("tunnel status line missing the URL:\n%s", rendered)
	}
	if line := lineWith(m.viewMain(), url); line == "" {
		t.Fatal("download URL not rendered")
	}
}

func TestMainReportsMissingTunnel(t *testing.T) {
	m := StartModel{screen: screenMain}
	if !strings.Contains(stripANSI(m.viewMain()), "No tunnel configured") {
		t.Error("main screen says nothing when no tunnel is configured")
	}
}

// The summary screen, not the dashboard, is what a fully set-up install shows:
// it sits on "enter continue" and most people never press it. An offer only the
// dashboard carries is an offer nobody sees.
// The summary screen, not the dashboard, is what a fully set-up install shows:
// it sits on "enter continue" and most people never press it. An offer only the
// dashboard carries is an offer nobody sees.
func TestLoadingScreenAlsoOffersMCPRegistration(t *testing.T) {
	t.Setenv("HELIOS_EXPERIMENTAL_MCP", "1")
	m := StartModel{screen: screenLoading, daemonOK: true, hooksOK: true}
	rendered := stripANSI(m.viewLoading())

	if !strings.Contains(rendered, "not registered") {
		t.Fatal("summary screen says nothing about unregistered agent tools")
	}
	if !strings.Contains(rendered, "m agent tools") {
		t.Error("the m key is not advertised on the summary screen")
	}

	registered := stripANSI(StartModel{
		screen: screenLoading, daemonOK: true, hooksOK: true, mcpRegistered: true,
	}.viewLoading())
	if strings.Contains(registered, "not registered") {
		t.Error("still nagging after registration")
	}
}

// Behind the flag the tools do not exist, so neither should the offer: a key
// advertised in the bar that answers nothing is worse than no key at all.
func TestAgentToolsAreInvisibleWithoutTheFlag(t *testing.T) {
	for name, rendered := range map[string]string{
		"summary": stripANSI(StartModel{
			screen: screenLoading, daemonOK: true, hooksOK: true,
		}.viewLoading()),
		"dashboard": stripANSI(StartModel{screen: screenMain}.viewMain()),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(rendered, "Agent tools") {
				t.Error("agent tools mentioned while the flag is off")
			}
			if strings.Contains(rendered, "m agent tools") {
				t.Error("the m key is advertised while the flag is off")
			}
		})
	}
}

// Setup asks once. It must be skippable, and skipping must be an offer rather
// than a dead end.
func TestMCPSetupIsSkippable(t *testing.T) {
	rendered := stripANSI(StartModel{screen: screenMCPSetup}.viewMCPSetup())

	if !strings.Contains(rendered, "tab skip") {
		t.Error("no way offered to skip")
	}
	if !strings.Contains(rendered, "enter register") {
		t.Error("no way offered to register")
	}
	// Skipping has to look survivable, or it reads as breaking Helios.
	if !strings.Contains(rendered, "works without it") {
		t.Error("does not say the rest of Helios works without it")
	}
}

// Declining during setup must stop the nagging, and must still leave a way in.
func TestDeclinedMCPStopsNaggingButStaysReachable(t *testing.T) {
	t.Setenv("HELIOS_EXPERIMENTAL_MCP", "1")
	for name, rendered := range map[string]string{
		"summary": stripANSI(StartModel{
			screen: screenLoading, daemonOK: true, hooksOK: true, mcpDeclined: true,
		}.viewLoading()),
		"dashboard": stripANSI(StartModel{screen: screenMain, mcpDeclined: true}.viewMain()),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(rendered, "~") && strings.Contains(rendered, "Agent tools not registered") {
				t.Error("still warning after the user declined")
			}
			if !strings.Contains(rendered, "Press m to turn them on") {
				t.Error("no way back in after declining")
			}
			if !strings.Contains(rendered, "m agent tools") {
				t.Error("the key is not advertised, so opting in later is undiscoverable")
			}
		})
	}
}

func TestMainOffersMCPRegistrationWithoutTreatingItAsAFault(t *testing.T) {
	t.Setenv("HELIOS_EXPERIMENTAL_MCP", "1")
	rendered := stripANSI(StartModel{screen: screenMain}.viewMain())

	line := lineWith(rendered, "Agent tools not registered")
	if line == "" {
		t.Fatal("main screen says nothing about unregistered agent tools")
	}
	// Unregistered is a suggestion. Rendering it with the cross used for real
	// problems would read as something being broken.
	if strings.Contains(line, "✗") {
		t.Errorf("unregistered MCP rendered as a failure: %q", line)
	}
	if !strings.Contains(rendered, "Press m to register") {
		t.Error("no way offered to register")
	}

	if !strings.Contains(rendered, "m agent tools") {
		t.Error("the m key is not advertised in the key bar")
	}

	registered := stripANSI(StartModel{screen: screenMain, mcpRegistered: true}.viewMain())
	if strings.Contains(registered, "not registered") {
		t.Error("still nagging after registration")
	}
	// A binding that does nothing should not sit in the bar.
	if strings.Contains(registered, "m agent tools") {
		t.Error("the m key is still advertised after registration")
	}
}

func TestTunnelSelectShowsCurrentURL(t *testing.T) {
	url := "http://host.tailnet.ts.net:7655"
	m := StartModel{
		screen:     screenTunnelSelect,
		tunnelOK:   true,
		tunnelURL:  url,
		tunnelProv: "tailscale",
		tunnelMode: "serve",
		tsChecked:  true,
	}

	line := lineWith(m.viewTunnelSelect(), url)
	if line == "" {
		t.Fatalf("picker does not show the tunnel it would replace:\n%s", stripANSI(m.viewTunnelSelect()))
	}
	if line != url {
		t.Errorf("current URL line must hold the URL and nothing else, got %q", line)
	}
}

func TestCopyableURLEmpty(t *testing.T) {
	if got := copyableURL(""); got != "" {
		t.Errorf("copyableURL(\"\") = %q, want empty (no blank line)", got)
	}
}
