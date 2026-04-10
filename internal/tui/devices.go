package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type devScreen int

const (
	devScreenList devScreen = iota
	devScreenDetail
	devScreenConfirmRevoke
)

type devicesLoaded struct {
	devices []deviceInfo
	err     error
}

type deviceRevoked struct {
	err error
}

type DevicesModel struct {
	screen  devScreen
	client  *client
	spinner spinner.Model

	devices []deviceInfo
	cursor  int
	loading bool

	// Selected device for detail/revoke
	selected *deviceInfo

	errMsg string
	width  int
	height int
}

func NewDevicesModel(internalPort int) DevicesModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return DevicesModel{
		screen:  devScreenList,
		client:  newClient(internalPort),
		spinner: s,
		loading: true,
	}
}

func (m DevicesModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadDevices(m.client))
}

func (m DevicesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case devicesLoaded:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.devices = msg.devices
		}

	case deviceRevoked:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("Revoke failed: %v", msg.err)
		}
		m.screen = devScreenList
		m.loading = true
		return m, loadDevices(m.client)
	}

	return m, nil
}

func (m DevicesModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.screen == devScreenList {
			return m, tea.Quit
		}
		m.screen = devScreenList
		return m, nil

	case "up", "k":
		if m.screen == devScreenList && m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.screen == devScreenList && m.cursor < len(m.devices)-1 {
			m.cursor++
		}

	case "enter":
		switch m.screen {
		case devScreenList:
			if len(m.devices) > 0 {
				d := m.devices[m.cursor]
				m.selected = &d
				m.screen = devScreenDetail
			}
		case devScreenDetail:
			// Do nothing on enter in detail
		case devScreenConfirmRevoke:
			// Do nothing — handled by y/n
		}

	case "b":
		if m.screen == devScreenDetail || m.screen == devScreenConfirmRevoke {
			m.screen = devScreenList
		}

	case "r":
		if m.screen == devScreenDetail && m.selected != nil {
			m.screen = devScreenConfirmRevoke
		}

	case "y":
		if m.screen == devScreenConfirmRevoke && m.selected != nil {
			kid := m.selected.KID
			m.selected = nil
			return m, revokeDevice(m.client, kid)
		}

	case "n":
		if m.screen == devScreenConfirmRevoke {
			m.screen = devScreenDetail
		}
	}

	return m, nil
}

func (m DevicesModel) View() string {
	switch m.screen {
	case devScreenList:
		return m.viewList()
	case devScreenDetail:
		return m.viewDetail()
	case devScreenConfirmRevoke:
		return m.viewConfirmRevoke()
	}
	return ""
}

func (m DevicesModel) viewList() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Devices"))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString(fmt.Sprintf("  Loading... %s\n", m.spinner.View()))
		return b.String()
	}

	if m.errMsg != "" {
		b.WriteString(errorStyle.Render("  " + m.errMsg))
		b.WriteString("\n")
	}

	if len(m.devices) == 0 {
		b.WriteString(dimStyle.Render("  No devices registered."))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  Run: helios auth init"))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  q quit"))
		return b.String()
	}

	for i, d := range m.devices {
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}

		name := d.Name
		if name == "" {
			name = "(unnamed)"
		}

		statusIcon := "●"
		statusColor := "70" // green
		if d.Status == "pending" {
			statusColor = "214" // yellow
		} else if d.Status == "revoked" {
			statusColor = "196" // red
		}

		line1 := fmt.Sprintf("%s%s %s",
			prefix,
			lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(statusIcon),
			name,
		)

		info := d.KID
		if d.Platform != "" {
			info += " · " + d.Platform
		}
		if d.Browser != "" {
			info += " · " + d.Browser
		}

		lastSeen := "never"
		if d.LastSeenAt != nil {
			t, err := time.Parse(time.RFC3339, *d.LastSeenAt)
			if err == nil {
				lastSeen = humanDuration(time.Since(t))
			}
		}

		pushStr := "OFF"
		if d.PushEnabled {
			pushStr = "ON"
		}

		line2 := fmt.Sprintf("    %s", dimStyle.Render(info))
		line3 := fmt.Sprintf("    %s", dimStyle.Render(fmt.Sprintf("Last seen: %s · Push: %s", lastSeen, pushStr)))

		b.WriteString(line1 + "\n")
		b.WriteString(line2 + "\n")
		b.WriteString(line3 + "\n\n")
	}

	b.WriteString(helpStyle.Render("  ↑/↓ navigate  enter details  q quit"))

	return b.String()
}

func (m DevicesModel) viewDetail() string {
	var b strings.Builder
	d := m.selected

	b.WriteString(titleStyle.Render(fmt.Sprintf("helios — Device: %s", displayName(d))))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("  Key ID:     %s\n", d.KID))
	b.WriteString(fmt.Sprintf("  Platform:   %s\n", valueOrDash(d.Platform)))
	b.WriteString(fmt.Sprintf("  Browser:    %s\n", valueOrDash(d.Browser)))
	b.WriteString(fmt.Sprintf("  Status:     %s\n", d.Status))

	pushStr := "disabled"
	if d.PushEnabled {
		pushStr = "enabled"
	}
	b.WriteString(fmt.Sprintf("  Push:       %s\n", pushStr))

	lastSeen := "never"
	if d.LastSeenAt != nil {
		t, err := time.Parse(time.RFC3339, *d.LastSeenAt)
		if err == nil {
			lastSeen = humanDuration(time.Since(t))
		}
	}
	b.WriteString(fmt.Sprintf("  Last seen:  %s\n", lastSeen))

	b.WriteString(helpStyle.Render("  r revoke  b back"))

	return b.String()
}

func (m DevicesModel) viewConfirmRevoke() string {
	var b strings.Builder
	d := m.selected

	b.WriteString(titleStyle.Render("helios — Revoke Device"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("  Are you sure you want to revoke\n"))
	b.WriteString(fmt.Sprintf("  %s (%s)?\n\n", errorStyle.Render(displayName(d)), d.KID))

	b.WriteString("  This device will no longer receive\n")
	b.WriteString("  notifications or be able to approve\n")
	b.WriteString("  permissions.\n")

	b.WriteString(helpStyle.Render("  y yes, revoke  n no, go back"))

	return b.String()
}

func loadDevices(c *client) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.deviceList()
		if err != nil {
			return devicesLoaded{err: err}
		}
		return devicesLoaded{devices: resp.Devices}
	}
}

func revokeDevice(c *client, kid string) tea.Cmd {
	return func() tea.Msg {
		err := c.deviceRevoke(kid)
		return deviceRevoked{err: err}
	}
}

func displayName(d *deviceInfo) string {
	if d.Name != "" {
		return d.Name
	}
	return d.KID
}

func valueOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// RunDevices launches the bubbletea devices TUI.
func RunDevices(internalPort int) error {
	m := NewDevicesModel(internalPort)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
