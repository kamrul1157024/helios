package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
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
	case screenTmuxRestart:
		return m.viewTmuxRestart()
	case screenEditorSetup:
		return m.viewEditorSetup()
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

		if m.tmux.Installed {
			b.WriteString(check(fmt.Sprintf("tmux installed (%s)", m.tmux.Version)))
		} else {
			b.WriteString(cross("tmux not installed — session management unavailable"))
		}

		if m.shellInstalled {
			b.WriteString(check(fmt.Sprintf("Shell wrapper (%s)", m.shellInfo.Name)))
		} else if m.shellInfo.RCPath != "" {
			b.WriteString(cross(fmt.Sprintf("Shell wrapper not installed (%s)", m.shellInfo.Name)))
		}

		editorCount := len(m.editors)
		if editorCount > 0 {
			configured := 0
			for _, e := range m.editors {
				if e.Configured {
					configured++
				}
			}
			if configured == editorCount {
				b.WriteString(check(fmt.Sprintf("%d editor(s) configured", configured)))
			} else {
				b.WriteString(cross(fmt.Sprintf("%d of %d editor(s) configured", configured, editorCount)))
			}
		}

		if m.tunnelOK {
			b.WriteString(check(fmt.Sprintf("Tunnel active (%s)", m.tunnelProv)))
		} else {
			b.WriteString(cross("No tunnel configured"))
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

		b.WriteString(helpStyle.Render("  enter continue  q quit"))
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
	b.WriteString(helpStyle.Render("  enter install  s skip  q quit"))

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
	b.WriteString(helpStyle.Render("  enter update  s skip  q quit"))

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
	b.WriteString(subtitleStyle.Render("  When you type 'claude' in your terminal, helios will"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  automatically wrap it in a managed tmux session."))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  This lets you send prompts and control sessions from your phone."))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  Will add wrapper to: %s", m.shellInfo.RCPath)))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter install  s skip  q quit"))

	return b.String()
}

func (m StartModel) viewTmuxRestart() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — tmux Restart Required"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("!"), "Shell wrapper was installed/updated."))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Existing tmux sessions use the old shell environment."))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  A tmux server restart is needed for the wrapper to take effect."))
	b.WriteString("\n\n")
	b.WriteString(warnStyle.Render("  Warning: this will kill all running tmux sessions."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Any Claude sessions in tmux will be terminated."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  The tmux server will start fresh on next use."))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  y restart tmux  n skip  q quit"))

	return b.String()
}

func (m StartModel) viewEditorSetup() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Editor Terminal Setup"))
	b.WriteString("\n\n")

	if m.editorManual != "" {
		// Some editors failed — show results + manual instructions
		for _, r := range m.editorResults {
			if r.Success {
				b.WriteString(check(fmt.Sprintf("%s — configured", r.Editor.Name)))
			} else {
				b.WriteString(cross(fmt.Sprintf("%s — failed", r.Editor.Name)))
			}
		}
		b.WriteString("\n")
		b.WriteString(m.editorManual)
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  enter continue  q quit"))
		return b.String()
	}

	b.WriteString(subtitleStyle.Render("  Configure editor terminals to use tmux?"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  This ensures Claude sessions in your editor are managed by helios."))
	b.WriteString("\n\n")

	for _, e := range m.editors {
		if e.Configured {
			b.WriteString(check(fmt.Sprintf("%s — already configured", e.Name)))
		} else {
			b.WriteString(cross(fmt.Sprintf("%s — needs configuration", e.Name)))
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter configure  s skip  q quit"))

	return b.String()
}

func (m StartModel) viewTunnelSelect() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")
	b.WriteString(subtitleStyle.Render("  How will your phone connect?"))
	b.WriteString("\n\n")

	for i, p := range tunnelProviders {
		cursor := "  "
		style := dimStyle
		if i == m.tunnelCursor {
			cursor = "> "
			style = selectedStyle
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, style.Render(p.label)))
	}

	b.WriteString(helpStyle.Render("  ↑/↓ navigate  enter select  q quit"))

	return b.String()
}

func (m StartModel) viewBinaryMissing() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")
	b.WriteString(cross(fmt.Sprintf("%s not found", m.missingBinary)))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Install it:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("    %s\n", urlStyle.Render(m.installHint)))
	b.WriteString(helpStyle.Render("  enter retry  q quit"))

	return b.String()
}

func (m StartModel) viewTunnelStarting() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")
	provider := tunnelProviders[m.tunnelCursor]
	b.WriteString(fmt.Sprintf("  Starting %s tunnel... %s\n", provider.id, m.spinner.View()))

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
	if m.tmux.Installed {
		b.WriteString(check(fmt.Sprintf("tmux (%s)", m.tmux.Version)))
	} else {
		b.WriteString(cross("tmux not installed — session management disabled"))
	}
	if m.shellInstalled {
		b.WriteString(check(fmt.Sprintf("Shell wrapper (%s)", m.shellInfo.Name)))
	}
	for _, e := range m.editors {
		if e.Configured {
			b.WriteString(check(fmt.Sprintf("%s terminal", e.Name)))
		}
	}
	if m.tunnelOK {
		b.WriteString(check(fmt.Sprintf("Tunnel: %s (%s)", m.tunnelURL, m.tunnelProv)))
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

	// tmux plugin recommendations
	if m.tmux.Installed && (!m.tmux.ResurrectPlugin || !m.tmux.ContinuumPlugin) {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("  Recommended tmux plugins for crash recovery:"))
		b.WriteString("\n")
		if !m.tmux.ResurrectPlugin {
			b.WriteString(dimStyle.Render("    tmux-resurrect  — saves/restores tmux sessions"))
			b.WriteString("\n")
		}
		if !m.tmux.ContinuumPlugin {
			b.WriteString(dimStyle.Render("    tmux-continuum  — auto-saves every 5 minutes"))
			b.WriteString("\n")
		}
		b.WriteString(dimStyle.Render("    Install: git clone https://github.com/tmux-plugins/tpm ~/.tmux/plugins/tpm"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("    See: helios docs for setup instructions"))
		b.WriteString("\n")
	}

	if !m.tmux.Installed {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("  tmux is required for session management."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("    Install: brew install tmux"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("    Session features (send, stop, resume) will not work without tmux."))
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
		b.WriteString("  " + urlStyle.Render(m.tunnelURL))
		b.WriteString("\n")
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
	} else if m.pairingToken == "" {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  Generating pairing code... %s\n", m.spinner.View()))
	}

	b.WriteString(helpStyle.Render("  q quit"))

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
