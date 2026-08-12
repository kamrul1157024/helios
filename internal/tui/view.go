package tui

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/kamrul1157024/helios/internal/tailscale"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("70"))

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	checkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("70"))

	crossStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("70")).
			Bold(true)

	urlStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75")).
			Underline(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))
)

// copyableURL renders a URL on a line of its own, flush left and with nothing
// else on it. The indentation used everywhere else in these views is dropped
// deliberately: a triple-click or shift-click line selection then yields
// exactly the URL, with no leading spaces to strip out by hand. Styling is
// colour and underline only, which terminals do not include in a copy.
func copyableURL(url string) string {
	if url == "" {
		return ""
	}
	return urlStyle.Render(url) + "\n"
}

func (m StartModel) View() string {
	switch m.screen {
	case screenLoading:
		return m.viewLoading()
	case screenHooksInstall:
		return m.viewHooksInstall()
	case screenHooksUpdate:
		return m.viewHooksUpdate()
	case screenShellSetup:
		return m.viewShellSetup()
	case screenTunnelSelect:
		return m.viewTunnelSelect()
	case screenBinaryMissing:
		return m.viewBinaryMissing()
	case screenTunnelStarting:
		return m.viewTunnelStarting()
	case screenCustomURL:
		return m.viewCustomURL()
	case screenMain:
		return m.viewMain()
	case screenConfirmDevice:
		return m.viewConfirmDevice()
	case screenNotificationSettings:
		return m.viewNotificationSettings()
	case screenSettings:
		return m.viewGeneralSettings()
	case screenError:
		return m.viewError()
	}
	return ""
}

func (m StartModel) viewLoading() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios"))
	b.WriteString("\n\n")

	if !m.daemonOK && m.errMsg == "" {
		b.WriteString(fmt.Sprintf("  Checking environment... %s\n", m.spinner.View()))
	} else {
		if m.daemonOK {
			b.WriteString(check("Daemon running"))
		} else {
			b.WriteString(cross("Daemon not running"))
		}

		if m.hooksOK && !m.hooksOutdated {
			b.WriteString(check("Claude hooks installed"))
		} else if m.hooksOK && m.hooksOutdated {
			b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("~"), "Claude hooks outdated"))
		} else {
			b.WriteString(cross("Claude hooks not installed"))
		}

		if m.shellInstalled {
			b.WriteString(check(fmt.Sprintf("Shell wrapper (%s)", m.shellInfo.Name)))
		} else if m.shellInfo.RCPath != "" {
			b.WriteString(cross(fmt.Sprintf("Shell wrapper not installed (%s)", m.shellInfo.Name)))
		}

		if m.tunnelOK {
			b.WriteString(check(fmt.Sprintf("Tunnel active (%s)", m.tunnelProv)))
		} else {
			b.WriteString(cross("No tunnel configured"))
		}

		if m.notifyBin != "" {
			b.WriteString(check(fmt.Sprintf("Desktop notifications (%s)", filepath.Base(m.notifyBin))))
			if runtime.GOOS == "darwin" {
				b.WriteString(dimStyle.Render("  · If notifications don't appear, enable in System Settings → Notifications → terminal-notifier"))
				b.WriteString("\n")
			}
		} else {
			b.WriteString(cross(desktopNotifyInstallHint()))
			if runtime.GOOS == "darwin" {
				b.WriteString(dimStyle.Render("  · After installing, enable in System Settings → Notifications → terminal-notifier"))
				b.WriteString("\n")
			}
		}

		if m.deviceCount > 0 {
			label := "device connected"
			if m.deviceCount > 1 {
				label = "devices connected"
			}
			b.WriteString(check(fmt.Sprintf("%d %s", m.deviceCount, label)))
		} else {
			b.WriteString(dimStyle.Render("  · No devices registered"))
			b.WriteString("\n")
		}

		if m.errMsg != "" {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render("  " + m.errMsg))
			b.WriteString("\n")
		}

		b.WriteString(helpStyle.Render("  enter continue  t change tunnel  N notifications  s settings  q quit"))
	}

	return b.String()
}

func (m StartModel) viewHooksInstall() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Claude Hooks"))
	b.WriteString("\n\n")
	b.WriteString(cross("Claude hooks not installed"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Hooks let helios intercept Claude Code permission prompts"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  and forward them to your phone for approval."))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter install  tab skip  q quit"))

	return b.String()
}

func (m StartModel) viewHooksUpdate() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Claude Hooks"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("~"), "Claude hooks are outdated"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  A newer hook configuration is available."))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Update to ensure all hooks work correctly."))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter update  tab skip  q quit"))

	return b.String()
}

func (m StartModel) viewShellSetup() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Shell Wrapper"))
	b.WriteString("\n\n")

	if m.shellManual != "" {
		// Auto-install failed — show manual instructions
		b.WriteString(cross("Could not auto-configure shell"))
		b.WriteString("\n")
		b.WriteString(m.shellManual)
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  enter continue  q quit"))
		return b.String()
	}

	b.WriteString(cross(fmt.Sprintf("Shell wrapper not installed (%s)", m.shellInfo.Name)))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Sending prompts from your phone will not work without the shell wrapper."))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  When you type 'claude', helios runs it in a terminal of its own"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  so it can send prompts and control sessions remotely."))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  Will add wrapper to: %s", m.shellInfo.RCPath)))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter install  tab skip  q quit"))

	return b.String()
}

func (m StartModel) viewTunnelSelect() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")

	// Reached with a tunnel already up, this screen is "change the tunnel", so
	// it has to say what the current one is — otherwise the only place the URL
	// appears is the main screen the user just left.
	if m.tunnelOK {
		name := m.tunnelProv
		if m.tunnelMode != "" {
			name = m.tunnelProv + " " + m.tunnelMode
		}
		b.WriteString(dimStyle.Render("  Current tunnel (" + name + "):"))
		b.WriteString("\n")
		b.WriteString(copyableURL(m.tunnelURL))
		b.WriteString("\n")
	}

	b.WriteString(subtitleStyle.Render("  How will your phone connect?"))
	b.WriteString("\n\n")

	// The label column is padded to a common width so the status column lines
	// up, which is what makes "ready" versus "needs setup" scannable at all.
	labelWidth := 0
	for _, p := range tunnelProviders {
		// Display width, not byte length: several labels contain em-dashes.
		if w := lipgloss.Width(p.label); w > labelWidth {
			labelWidth = w
		}
	}

	for i, p := range tunnelProviders {
		cursor := "  "
		style := dimStyle
		selected := i == m.tunnelCursor
		if selected {
			cursor = "> "
			style = selectedStyle
		}
		// Pad before styling: lipgloss wraps the text in escape sequences, and
		// a width-aware format verb would count those towards the width.
		label := style.Render(p.label + strings.Repeat(" ", labelWidth-lipgloss.Width(p.label)))
		b.WriteString(fmt.Sprintf("  %s%s  %s\n", cursor, label, m.providerStatus(p.id, p.mode)))

		// Details are only shown for the highlighted row: printing every
		// caveat at once turns the list into a wall of text.
		if selected && p.detail != "" {
			b.WriteString(subtitleStyle.Render("      " + p.detail))
			b.WriteString("\n")
		}
	}

	if m.tunnelOK {
		b.WriteString(helpStyle.Render("  ↑/↓ navigate  enter select  q back"))
	} else {
		b.WriteString(helpStyle.Render("  ↑/↓ navigate  enter select  q quit"))
	}

	return b.String()
}

// providerStatus renders the inline availability column. Tailscale reports the
// specific unmet prerequisite; everything else reports only whether its binary
// is present, because that is the only thing checkable without side effects.
func (m StartModel) providerStatus(provider, mode string) string {
	if provider == "tailscale" && !m.tsChecked {
		return dimStyle.Render("checking…")
	}
	if problem := m.providerUnavailable(provider, mode); problem != "" {
		return warnStyle.Render("needs setup")
	}
	return checkStyle.Render("ready")
}

func (m StartModel) viewBinaryMissing() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")

	problem := m.providerProblem
	if problem == "" {
		problem = fmt.Sprintf("%s not found", m.missingBinary)
	}
	b.WriteString(cross(problem))
	b.WriteString("\n")

	// Tailscale's remedy is carried in the problem text itself — the fix is
	// logging in or flipping an admin-console setting, not installing a
	// package — so an install hint would be misleading unless it is genuinely
	// missing.
	if m.installHint != "" && (tunnelProviders[m.tunnelCursor].id != "tailscale" || !m.tsState.Installed) {
		b.WriteString(subtitleStyle.Render("  Install it:"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("    %s\n", urlStyle.Render(m.installHint)))
	}
	b.WriteString(helpStyle.Render("  enter retry  q quit"))

	return b.String()
}

func (m StartModel) viewTunnelStarting() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")
	provider := tunnelProviders[m.tunnelCursor]
	name := provider.id
	if provider.mode != "" {
		name = provider.id + " " + provider.mode
	}
	b.WriteString(fmt.Sprintf("  Starting %s tunnel... %s\n", name, m.spinner.View()))

	return b.String()
}

func (m StartModel) viewCustomURL() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Custom Tunnel URL"))
	b.WriteString("\n\n")
	b.WriteString(subtitleStyle.Render("  Enter your public URL:"))
	b.WriteString("\n\n")
	b.WriteString("  " + m.textInput.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter confirm  ctrl+c cancel"))

	return b.String()
}

// tunnelExposure states who can reach the running tunnel. Who can connect is
// the one thing the URL does not say, and it differs between the two Tailscale
// modes, so it is spelled out rather than left to be inferred from the scheme.
//
// The mode is recovered from the URL when it is not known directly: a tunnel
// adopted from a previous daemon run is reported by the API as "tailscale"
// with no mode, but Serve always publishes http:// and Funnel always https://.
func (m StartModel) tunnelExposure() string {
	switch m.tunnelProv {
	case "tailscale":
		mode := m.tunnelMode
		if mode == "" {
			mode = string(tailscale.ModeFunnel)
			if strings.HasPrefix(m.tunnelURL, "http://") {
				mode = string(tailscale.ModeServe)
			}
		}
		if mode == string(tailscale.ModeServe) {
			return "Reachable only from your tailnet — switch Tailscale ON on your phone"
		}
		return "Reachable from the public internet"
	case "local":
		return "Reachable only from your local network"
	}
	return ""
}

func (m StartModel) viewMain() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios"))
	b.WriteString("\n\n")

	// Status
	b.WriteString(check("Daemon running"))
	if m.hooksOK && !m.hooksOutdated {
		b.WriteString(check("Claude hooks installed"))
	} else if m.hooksOK {
		b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("~"), "Claude hooks outdated"))
	}
	if m.shellInstalled {
		b.WriteString(check(fmt.Sprintf("Shell wrapper (%s)", m.shellInfo.Name)))
	} else if m.shellInfo.RCPath != "" {
		b.WriteString(cross(fmt.Sprintf("Shell wrapper not installed (%s)", m.shellInfo.Name)))
		b.WriteString(dimStyle.Render("  · Sending prompts from your phone requires the shell wrapper"))
		b.WriteString("\n")
	}
	if m.tunnelOK {
		name := m.tunnelProv
		if m.tunnelMode != "" {
			name = m.tunnelProv + " " + m.tunnelMode
		}
		b.WriteString(check(fmt.Sprintf("Tunnel: %s (%s)", m.tunnelURL, name)))
		if exposure := m.tunnelExposure(); exposure != "" {
			b.WriteString(dimStyle.Render("  · " + exposure))
			b.WriteString("\n")
		}
	} else {
		b.WriteString(cross("No tunnel configured"))
	}
	if m.notifyBin != "" {
		b.WriteString(check(fmt.Sprintf("Desktop notifications (%s)", filepath.Base(m.notifyBin))))
	} else {
		b.WriteString(cross(desktopNotifyInstallHint()))
	}
	// Devices
	b.WriteString("\n")
	activeDevices := 0
	for _, d := range m.devices {
		if d.Status == "active" {
			activeDevices++
			name := d.Name
			if name == "" {
				name = d.KID
			}
			lastSeen := "never"
			if d.LastSeenAt != nil {
				t, err := time.Parse(time.RFC3339, *d.LastSeenAt)
				if err == nil {
					lastSeen = humanDuration(time.Since(t))
				}
			}
			pushStr := "off"
			if d.PushEnabled {
				pushStr = "on"
			}
			b.WriteString(fmt.Sprintf("  %s %-20s  push:%s  %s\n",
				checkStyle.Render("*"), name, pushStr, dimStyle.Render(lastSeen)))
		}
	}
	if activeDevices == 0 {
		b.WriteString(dimStyle.Render("  No devices connected yet."))
		b.WriteString("\n")
	}

	// Download QR (landing page)
	if m.downloadQR != "" {
		b.WriteString("\n")
		b.WriteString(subtitleStyle.Render("  Download app:"))
		b.WriteString("\n")
		for _, line := range strings.Split(m.downloadQR, "\n") {
			if line != "" {
				b.WriteString("    " + line + "\n")
			}
		}
		b.WriteString(copyableURL(m.tunnelURL))
	}

	// Pairing QR
	if m.pairingQR != "" {
		b.WriteString("\n")
		b.WriteString(subtitleStyle.Render("  Pair a new device:"))
		b.WriteString("\n")
		for _, line := range strings.Split(m.pairingQR, "\n") {
			if line != "" {
				b.WriteString("    " + line + "\n")
			}
		}

		// Countdown
		remaining := time.Until(m.tokenExpiresAt)
		if remaining < 0 {
			remaining = 0
		}
		mins := int(remaining.Minutes())
		secs := int(remaining.Seconds()) % 60
		countdown := fmt.Sprintf("%d:%02d", mins, secs)

		if remaining < 15*time.Second {
			b.WriteString(fmt.Sprintf("  %s  %s\n", warnStyle.Render("Expires in "+countdown), dimStyle.Render("(auto-refreshes)")))
		} else {
			b.WriteString(fmt.Sprintf("  %s  %s\n", dimStyle.Render("Expires in "+countdown), dimStyle.Render("(auto-refreshes)")))
		}

		// The same link the QR encodes, in text. Scanning is not always an
		// option — the terminal may be over SSH, the QR may not fit the window
		// — and the app's manual field accepts this URL verbatim.
		b.WriteString(dimStyle.Render("  · Or paste this into the app:"))
		b.WriteString("\n")
		b.WriteString(copyableURL(m.setupURL))
	} else if m.pairingToken == "" {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  Generating pairing code... %s\n", m.spinner.View()))
	}

	b.WriteString(helpStyle.Render("  t change tunnel  N notifications  s settings  q quit"))

	return b.String()
}

func (m StartModel) viewConfirmDevice() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — New Device"))
	b.WriteString("\n\n")

	b.WriteString("  A device wants to pair:\n\n")

	if m.pendingDevice != nil {
		name := m.pendingDevice.Name
		if name == "" {
			name = "(unnamed)"
		}
		b.WriteString(fmt.Sprintf("    Name:     %s\n", name))
		if m.pendingDevice.Platform != "" {
			b.WriteString(fmt.Sprintf("    Platform: %s\n", m.pendingDevice.Platform))
		}
		b.WriteString(fmt.Sprintf("    KID:      %s\n", m.pendingDevice.KID))
	}

	b.WriteString("\n")
	b.WriteString("  Allow this device?\n")
	b.WriteString(helpStyle.Render("  y approve  n reject"))

	return b.String()
}

func (m StartModel) viewError() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Error"))
	b.WriteString("\n\n")
	b.WriteString(errorStyle.Render("  " + m.errMsg))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter exit"))

	return b.String()
}

func check(msg string) string {
	return fmt.Sprintf("  %s %s\n", checkStyle.Render("✓"), msg)
}

func cross(msg string) string {
	return fmt.Sprintf("  %s %s\n", crossStyle.Render("✗"), msg)
}

func desktopNotifyInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "Desktop notifications — brew install terminal-notifier"
	case "linux":
		return "Desktop notifications — sudo apt install libnotify-bin"
	default:
		return "Desktop notifications — not supported on this platform"
	}
}
