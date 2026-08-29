package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/kamrul1157024/helios/internal/featureflag"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/tailscale"
)

// The setup screens paint no background of their own, so this text lands on
// whatever the terminal's is. Every colour is stated for both, because the
// single values these used to be were picked against a dark terminal and left
// the light one with a pale amber warning and a pale blue URL on white — the
// two lines on the screen a user most needs to read. Each side clears 4.5:1.
var (
	accentColor = lipgloss.AdaptiveColor{Light: "28", Dark: "70"}   // green
	dimColor    = lipgloss.AdaptiveColor{Light: "241", Dark: "245"} // grey
	linkColor   = lipgloss.AdaptiveColor{Light: "25", Dark: "75"}   // blue
	dangerColor = lipgloss.AdaptiveColor{Light: "124", Dark: "203"} // red
	warnColor   = lipgloss.AdaptiveColor{Light: "130", Dark: "214"} // amber
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	checkStyle = lipgloss.NewStyle().
			Foreground(accentColor)

	crossStyle = lipgloss.NewStyle().
			Foreground(dangerColor)

	dimStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	selectedStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	urlStyle = lipgloss.NewStyle().
			Foreground(linkColor).
			Underline(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(dangerColor)

	helpStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			MarginTop(1)

	warnStyle = lipgloss.NewStyle().
			Foreground(warnColor)
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
	case screenAgentMenu:
		return m.viewAgentMenu()
	case screenAgentSetup:
		return m.viewAgentSetup()
	case screenHooksInstall:
		return m.viewHooksInstall()
	case screenHooksUpdate:
		return m.viewHooksUpdate()
	case screenShellSetup:
		return m.viewShellSetup()
	case screenMCPSetup:
		return m.viewMCPSetup()
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

		b.WriteString(renderHookLines(m.hookLines))

		if featureflag.MCP() {
			if m.mcpRegistered {
				b.WriteString(check("Agent tools registered with Claude Code"))
			} else if m.mcpDeclined {
				// Opted out during setup. Still reachable, but it stops asking:
				// a suggestion that survives being declined is a nag.
				b.WriteString(dimStyle.Render("  · Agent tools are off. Press m to turn them on."))
				b.WriteString("\n")
			} else {
				b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("~"), "Agent tools not registered with Claude Code"))
				b.WriteString(dimStyle.Render("  · Lets an agent open a file or a diff in Helios. Press m to register."))
				b.WriteString("\n")
			}
			if m.mcpMsg != "" {
				b.WriteString(dimStyle.Render("  · " + m.mcpMsg))
				b.WriteString("\n")
			}
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

		keys := "  enter continue  a agents  t change tunnel  N notifications  s settings  q quit"
		if featureflag.MCP() && !m.mcpRegistered {
			keys = "  m agent tools" + keys
		}
		b.WriteString(helpStyle.Render(keys))
	}

	return b.String()
}

func (m StartModel) viewHooksInstall() string {
	var b strings.Builder

	// Named for the agents actually on this machine, because installing is
	// per agent and the screen used to promise only Claude while writing both.
	b.WriteString(titleStyle.Render("helios — Agent Hooks"))
	b.WriteString("\n\n")
	b.WriteString(renderHookLines(m.hookLines))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Hooks let helios intercept an agent's permission prompts"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  and forward them to your phone for approval."))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter install  tab skip  q quit"))

	return b.String()
}

func (m StartModel) viewHooksUpdate() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Agent Hooks"))
	b.WriteString("\n\n")
	b.WriteString(renderHookLines(m.hookLines))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  A newer hook configuration is available."))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Update to ensure all hooks work correctly."))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  enter update  tab skip  q quit"))

	return b.String()
}

// The setup step for the MCP server. Optional and explicitly skippable: it
// gives agents tools they did not have, which is a decision rather than a
// formality, and skipping is remembered so it is asked once.
func (m StartModel) viewMCPSetup() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Agent Tools"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("~"), "Helios MCP is not registered with Claude Code"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  Lets an agent open a file, a diff or a tab in Helios,"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  so it can show you what it is talking about."))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  Everything else in Helios works without it."))
	b.WriteString("\n")
	if m.mcpMsg != "" {
		b.WriteString(dimStyle.Render("  · " + m.mcpMsg))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("  enter register  tab skip  q quit"))

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
	b.WriteString(renderHookLines(m.hookLines))
	if featureflag.MCP() {
		if m.mcpRegistered {
			b.WriteString(check("Agent tools registered with Claude Code"))
		} else if m.mcpDeclined {
			// Opted out during setup. Still reachable, but it stops asking:
			// a suggestion that survives being declined is a nag.
			b.WriteString(dimStyle.Render("  · Agent tools are off. Press m to turn them on."))
			b.WriteString("\n")
		} else {
			// A suggestion, not a fault: everything else works without it.
			b.WriteString(fmt.Sprintf("  %s %s\n", warnStyle.Render("~"), "Agent tools not registered with Claude Code"))
			b.WriteString(dimStyle.Render("  · Lets an agent open a file or a diff in Helios. Press m to register."))
			b.WriteString("\n")
		}
		if m.mcpMsg != "" {
			b.WriteString(dimStyle.Render("  · " + m.mcpMsg))
			b.WriteString("\n")
		}
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
		if m.tunnelWarn != "" {
			b.WriteString(cross(m.tunnelWarn))
		}
	} else {
		b.WriteString(cross("No tunnel configured"))
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

	// The MCP key is advertised only while there is something to do with it,
	// so the bar does not carry a permanently dead binding.
	keys := "  a agents  t change tunnel  N notifications  s settings  q quit"
	if featureflag.MCP() && !m.mcpRegistered {
		keys = "  m agent tools" + keys
	}
	b.WriteString(helpStyle.Render(keys))

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

// renderHookLines reports each installed agent's hooks on its own line.
//
// One line per provider rather than one tick for all of them, because the
// states differ and the difference is the whole point: a machine can have
// Claude working and Codex installed-but-untrusted, and Codex says nothing
// about the latter itself. The detail comes from the provider, so it can name
// the command that fixes it.
// renderHookLines reports each agent on its own line.
//
// One line per agent rather than a single tick for all of them, because the
// states differ and the difference is the point: a machine can have Claude
// working and Codex not.
//
// The row itself comes from agentMenuRow — literally the same function the
// agent menu uses. It was a second implementation, and the two drifted within
// a day: the dashboard called an agent "~ not run them yet" while the menu one
// keypress away called the same agent "✓ ready". Two screens of one program
// disagreeing about the same fact is worse than either wording.
func renderHookLines(lines []hookLine) string {
	if len(lines) == 0 {
		return cross("No agents registered")
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  " + agentMenuRow(l) + "\n")
	}
	return b.String()
}

// agentFirstRunNote explains a caveat helios cannot clear itself.
//
// The line goes away on its own once the agent runs a hook. Until then it is
// indistinguishable from an agent that has read the hooks and declined to run
// them, so say what to check without asserting which it is.
func agentFirstRunNote(providerID string) string {
	if providerID == "codex" {
		return "Codex asks to approve these at the start of its next session — " +
			"choose \"Trust all and continue\". Helios will also offer it as a " +
			"notification. Inside a running session, /hooks does the same."
	}
	return "This clears after the agent's next session."
}

// agentInstallHint tells the user how to get an agent they do not have.
func agentInstallHint(providerID string) string {
	switch providerID {
	case "claude":
		return "npm i -g @anthropic-ai/claude-code"
	case "codex":
		return "npm i -g @openai/codex"
	default:
		return "see the provider's own docs"
	}
}

func agentDisplayName(providerID string) string {
	if p, ok := provider.Get(providerID); ok {
		return p.Info().Name
	}
	return providerID
}

// viewAgentMenu lists the agents and lets the user set one up at a time.
//
// A menu rather than a single prompt because the decisions are separate: a
// machine with two agents was being asked one yes/no for both, on a screen
// that could only describe one of them.
func (m StartModel) viewAgentMenu() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Agents"))
	b.WriteString("\n\n")
	b.WriteString(subtitleStyle.Render("  An agent is offered when you start a session only once it is set up."))
	b.WriteString("\n\n")

	for i, l := range m.hookLines {
		cursor := "  "
		if i == m.agentCursor {
			cursor = selectedStyle.Render("▸ ")
		}
		b.WriteString(cursor + agentMenuRow(l) + "\n")
	}

	if m.agentMsg != "" {
		b.WriteString("\n")
		b.WriteString(check(m.agentMsg))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑↓ choose  enter set up  s skip this agent  tab skip all  q quit"))
	return b.String()
}

// agentMenuRow is one line: the agent, its state, and what is missing.
func agentMenuRow(l hookLine) string {
	name := agentDisplayName(l.Provider)
	switch {
	case l.Skipped:
		// Dim, and stated as the user's choice rather than a fault, because
		// it is one. Reversible with the same key that set it.
		return subtitleStyle.Render(fmt.Sprintf("· %s — skipped", name))
	case !l.CLIPresent:
		return subtitleStyle.Render(fmt.Sprintf("· %s — not installed", name))
	case !l.Health.Installed:
		return fmt.Sprintf("%s %s — hooks not installed", warnStyle.Render("✗"), name)
	case !l.Health.Current:
		return fmt.Sprintf("%s %s — hooks out of date", warnStyle.Render("~"), name)
	case !l.Health.Effective:
		// A tick, not a warning: everything helios can do is done, and
		// readiness already counts this agent as startable. The note is a
		// caveat, and it clears itself after the agent's first session.
		return fmt.Sprintf("%s %s — ready %s", checkStyle.Render("✓"), name,
			subtitleStyle.Render("(awaiting its first session)"))
	default:
		return fmt.Sprintf("%s %s — ready", checkStyle.Render("✓"), name)
	}
}

// viewAgentSetup explains one agent and what setting it up will do.
func (m StartModel) viewAgentSetup() string {
	var b strings.Builder
	line, ok := m.agentAt(m.agentCursor)
	name := agentDisplayName(m.agentSetup)

	b.WriteString(titleStyle.Render("helios — " + name))
	b.WriteString("\n\n")

	if !ok || !line.CLIPresent {
		// Helios cannot install someone else's agent, and should not pretend
		// to. Say what to run and get out of the way.
		b.WriteString(cross(name + " is not installed"))
		b.WriteString("\n")
		b.WriteString(subtitleStyle.Render("  Install it, then run helios start again:"))
		b.WriteString("\n")
		b.WriteString(subtitleStyle.Render("    " + agentInstallHint(m.agentSetup)))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  s skip this agent  enter back  tab back  q quit"))
		return b.String()
	}

	b.WriteString("  " + agentMenuRow(line) + "\n\n")
	b.WriteString(subtitleStyle.Render("  Hooks let helios see this agent's sessions and answer its"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render("  permission prompts from your phone."))
	b.WriteString("\n")

	done := line.Health.Installed && line.Health.Current
	action := "enter install hooks"

	if done {
		// Nothing left for helios to do. Saying "install hooks" here offered
		// an action that rewrote the same file and returned to an identical
		// screen, which reads as the key being broken.
		action = "enter reinstall hooks"
		b.WriteString("\n")
		b.WriteString(check("Hooks are installed and up to date"))
		if !line.Health.Effective {
			b.WriteString("\n")
			b.WriteString(subtitleStyle.Render("  " + agentFirstRunNote(m.agentSetup)))
		}
	}

	if m.agentMsg != "" {
		b.WriteString("\n")
		b.WriteString(check(m.agentMsg))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  " + action + "  s skip this agent  tab back  q quit"))
	return b.String()
}
