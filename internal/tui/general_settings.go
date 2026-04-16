package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var generalSettingsKeys = []struct {
	key     string
	label   string
	section string
}{
	{key: "autotitle.enabled", label: "Auto title generation", section: "Auto Title"},
	{key: "autotitle.emoji", label: "Title emoji prefix", section: ""},
}

var generalSettingDefaults = map[string]bool{
	"autotitle.enabled": false,
	"autotitle.emoji":   true,
}

func loadGeneralSettings(c *client) tea.Cmd {
	return func() tea.Msg {
		settings, err := c.getSettings()
		if err != nil {
			return generalSettingsLoaded{err: err}
		}
		values := make(map[string]bool, len(generalSettingsKeys))
		for _, item := range generalSettingsKeys {
			if raw, ok := settings[item.key]; ok {
				values[item.key] = raw == "true"
				continue
			}
			values[item.key] = generalSettingDefaults[item.key]
		}
		return generalSettingsLoaded{values: values}
	}
}

func (m StartModel) toggleGeneralSetting() (tea.Model, tea.Cmd) {
	if m.settingsValues == nil {
		m.settingsValues = defaultGeneralSettings()
	}
	key := generalSettingsKeys[m.settingsCursor].key
	m.settingsValues[key] = !m.settingsValues[key]

	val := "false"
	if m.settingsValues[key] {
		val = "true"
	}
	patch := map[string]string{key: val}
	c := m.client
	return m, func() tea.Msg {
		c.updateSettings(patch) //nolint:errcheck — best-effort
		return generalSettingSaved{}
	}
}

func (m StartModel) resetGeneralSettings() (tea.Model, tea.Cmd) {
	m.settingsValues = defaultGeneralSettings()
	patch := make(map[string]string, len(generalSettingsKeys))
	for _, item := range generalSettingsKeys {
		if generalSettingDefaults[item.key] {
			patch[item.key] = "true"
		} else {
			patch[item.key] = "false"
		}
	}
	c := m.client
	return m, func() tea.Msg {
		c.updateSettings(patch) //nolint:errcheck — best-effort
		return generalSettingSaved{}
	}
}

func defaultGeneralSettings() map[string]bool {
	values := make(map[string]bool, len(generalSettingDefaults))
	for k, v := range generalSettingDefaults {
		values[k] = v
	}
	return values
}

func (m StartModel) viewGeneralSettings() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("helios — Settings"))
	b.WriteString("\n\n")

	values := m.settingsValues
	if values == nil {
		values = defaultGeneralSettings()
	}

	lastSection := ""
	for i, item := range generalSettingsKeys {
		if item.section != "" && item.section != lastSection {
			lastSection = item.section
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ─── %s %s", item.section, strings.Repeat("─", 40-len(item.section)))))
			b.WriteString("\n\n")
		}

		enabled := values[item.key]
		toggle := toggleOffStyle.Render("[OFF]")
		if enabled {
			toggle = toggleOnStyle.Render("[ON ]")
		}

		row := fmt.Sprintf("  %-30s %s", item.label, toggle)
		if i == m.settingsCursor {
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
