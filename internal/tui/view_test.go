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
