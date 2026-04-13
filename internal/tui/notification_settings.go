package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// notifSettingsKeys defines the ordered list of toggles for the notification
// settings screen.
var notifSettingsKeys = []struct {
	key   string
	label string
}{
	{"desktop.notify.enabled", "Desktop notifications"},
	{"desktop.notify.sound", "Sound"},
	{"desktop.notify.suppress_focused", "Suppress when pane is focused"},
	{"desktop.notify.alert.permission", "Permission requests"},
	{"desktop.notify.alert.question", "Questions"},
	{"desktop.notify.alert.elicitation", "Elicitation"},
	{"desktop.notify.alert.done", "Session completed"},
	{"desktop.notify.alert.error", "Session error"},
}

var notifSettingDefaults = map[string]bool{
	"desktop.notify.enabled":             true,
	"desktop.notify.sound":               true,
	"desktop.notify.suppress_focused":    true,
	"desktop.notify.alert.permission":    true,
	"desktop.notify.alert.question":      true,
	"desktop.notify.alert.elicitation":   true,
	"desktop.notify.alert.done":          true,
	"desktop.notify.alert.error":         true,
}

var toggleOnStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("42"))

var toggleOffStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("196"))

// loadNotifSettings loads notification settings from the daemon and returns
// a tea.Cmd that delivers a notifSettingsLoaded message.
func loadNotifSettings(c *client) tea.Cmd {
	return func() tea.Msg {
		settings, err := c.getSettings()
		if err != nil {
			return notifSettingsLoaded{err: err}
		}
		values := make(map[string]bool, len(notifSettingsKeys))
		for _, item := range notifSettingsKeys {
			if raw, ok := settings[item.key]; ok {
				b, err := strconv.ParseBool(raw)
				if err == nil {
					values[item.key] = b
					continue
				}
			}
			values[item.key] = notifSettingDefaults[item.key]
		}
		return notifSettingsLoaded{values: values}
	}
}

// toggleNotifSetting flips the focused setting and saves it to the daemon.
func (m StartModel) toggleNotifSetting() (tea.Model, tea.Cmd) {
	if m.notifSettingsValues == nil {
		m.notifSettingsValues = defaultNotifSettings()
	}
	key := notifSettingsKeys[m.notifSettingsCursor].key
	current := m.notifSettingsValues[key]
	m.notifSettingsValues[key] = !current

	// Persist immediately.
	val := "false"
	if m.notifSettingsValues[key] {
		val = "true"
	}
	patch := map[string]string{key: val}
	c := m.client
	return m, func() tea.Msg {
		c.updateSettings(patch) //nolint:errcheck — best-effort
		return notifSettingSaved{}
	}
}

// resetNotifSettings resets all toggles to defaults and saves them.
func (m StartModel) resetNotifSettings() (tea.Model, tea.Cmd) {
	m.notifSettingsValues = defaultNotifSettings()
	patch := make(map[string]string, len(notifSettingsKeys))
	for _, item := range notifSettingsKeys {
		if notifSettingDefaults[item.key] {
			patch[item.key] = "true"
		} else {
			patch[item.key] = "false"
		}
	}
	c := m.client
	return m, func() tea.Msg {
		c.updateSettings(patch) //nolint:errcheck — best-effort
		return notifSettingSaved{}
	}
}

func defaultNotifSettings() map[string]bool {
	values := make(map[string]bool, len(notifSettingDefaults))
	for k, v := range notifSettingDefaults {
		values[k] = v
	}
	return values
}

// viewNotificationSettings renders the notification settings screen.
func (m StartModel) viewNotificationSettings() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Notification Settings"))
	b.WriteString("\n\n")

	values := m.notifSettingsValues
	if values == nil {
		values = defaultNotifSettings()
	}

	// Section 1: global toggles
	b.WriteString(dimStyle.Render("  ─── Desktop Notifications ──────────────────"))
	b.WriteString("\n\n")

	for i, item := range notifSettingsKeys {
		if i == 3 {
			// Section divider before alert types
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  ─── Alert types ─────────────────────────────"))
			b.WriteString("\n\n")
		}

		label := item.label
		enabled := values[item.key]

		toggle := toggleOffStyle.Render("[OFF]")
		if enabled {
			toggle = toggleOnStyle.Render("[ON ]")
		}

		row := fmt.Sprintf("  %-30s %s", label, toggle)

		if i == m.notifSettingsCursor {
			b.WriteString(selectedStyle.Render(row))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑/↓ navigate  space/enter toggle  r reset defaults  q back"))

	return b.String()
}
