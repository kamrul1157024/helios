package tui

import (
	"fmt"
	"strings"

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
)

func (m SetupModel) View() string {
	switch m.screen {
	case screenStatusCheck:
		return m.viewStatusCheck()
	case screenAlreadySetup:
		return m.viewAlreadySetup()
	case screenTunnelSelect:
		return m.viewTunnelSelect()
	case screenBinaryMissing:
		return m.viewBinaryMissing()
	case screenTunnelStarting:
		return m.viewTunnelStarting()
	case screenCustomURL:
		return m.viewCustomURL()
	case screenQRCode:
		return m.viewQRCode()
	case screenSuccess:
		return m.viewSuccess()
	case screenError:
		return m.viewError()
	}
	return ""
}

func (m SetupModel) viewStatusCheck() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Setup"))
	b.WriteString("\n\n")

	if !m.daemonOK && m.errMsg == "" {
		b.WriteString(fmt.Sprintf("  Checking environment... %s\n", m.spinner.View()))
	} else {
		if m.daemonOK {
			b.WriteString(check("Daemon running (7654/7655)"))
		} else {
			b.WriteString(cross("Daemon not running"))
		}

		if m.hooksOK {
			b.WriteString(check("Claude hooks installed"))
		} else {
			b.WriteString(cross("Claude hooks not installed"))
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
			b.WriteString(cross("No devices registered"))
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

func (m SetupModel) viewAlreadySetup() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Setup"))
	b.WriteString("\n\n")

	b.WriteString(check("Daemon running (7654/7655)"))
	if m.hooksOK {
		b.WriteString(check("Claude hooks installed"))
	}
	if m.tunnelOK {
		b.WriteString(check(fmt.Sprintf("Tunnel active (%s)", m.tunnelProv)))
	}
	b.WriteString(check(fmt.Sprintf("%d device(s) connected", m.deviceCount)))

	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  What would you like to do?"))
	b.WriteString("\n\n")

	for i, opt := range setupMenuOptions {
		cursor := "  "
		style := dimStyle
		if i == m.menuCursor {
			cursor = "> "
			style = selectedStyle
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, style.Render(opt)))
	}

	b.WriteString(helpStyle.Render("  ↑/↓ navigate  enter select"))

	return b.String()
}

func (m SetupModel) viewTunnelSelect() string {
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

	b.WriteString(helpStyle.Render("  ↑/↓ navigate  enter select  q back"))

	return b.String()
}

func (m SetupModel) viewBinaryMissing() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")
	b.WriteString(cross(fmt.Sprintf("%s not found", m.missingBinary)))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Install it:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("    %s\n", urlStyle.Render(m.installHint)))
	b.WriteString(helpStyle.Render("  enter retry  q back"))

	return b.String()
}

func (m SetupModel) viewTunnelStarting() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Tunnel Setup"))
	b.WriteString("\n\n")
	provider := tunnelProviders[m.tunnelCursor]
	b.WriteString(fmt.Sprintf("  Starting %s tunnel... %s\n", provider.id, m.spinner.View()))

	return b.String()
}

func (m SetupModel) viewCustomURL() string {
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

func (m SetupModel) viewQRCode() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Scan with your phone"))
	b.WriteString("\n\n")

	if m.qrString == "" {
		b.WriteString(fmt.Sprintf("  Creating device... %s\n", m.spinner.View()))
	} else {
		// Indent QR code
		for _, line := range strings.Split(m.qrString, "\n") {
			if line != "" {
				b.WriteString("    " + line + "\n")
			}
		}
		b.WriteString("\n")
		b.WriteString("  " + urlStyle.Render(m.setupURL))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("  Waiting for device to connect... %s\n", m.spinner.View()))
	}

	b.WriteString(helpStyle.Render("  q quit"))

	return b.String()
}

func (m SetupModel) viewSuccess() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Setup Complete!"))
	b.WriteString("\n\n")

	b.WriteString(check("Daemon running"))
	if m.hooksOK {
		b.WriteString(check("Claude hooks installed"))
	}
	if m.tunnelOK {
		b.WriteString(check(fmt.Sprintf("Tunnel active (%s)", m.tunnelProv)))
	}
	b.WriteString(check(fmt.Sprintf("Device connected (%s)", m.deviceName)))

	b.WriteString("\n")
	b.WriteString("  Your phone will now receive push\n")
	b.WriteString("  notifications when Claude needs\n")
	b.WriteString("  permission.\n")

	b.WriteString(helpStyle.Render("  enter exit"))

	return b.String()
}

func (m SetupModel) viewError() string {
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
