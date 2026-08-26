package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var toggleOnStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("42"))

var toggleOffStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("196"))

var generalSettingsKeys = []struct {
	key     string
	label   string
	section string
	// choices makes a row a cycle rather than a toggle. Enter steps to the next
	// one and wraps. Nil means the row is a plain on/off.
	choices []settingChoice
}{
	{key: "autotitle.enabled", label: "Auto title generation", section: "Auto Title"},
	{key: "autotitle.emoji", label: "Title icon prefix (needs a Nerd Font)", section: ""},
	{key: "memory.evict", label: "Let idle sessions go cold", section: "Memory"},
	{key: "memory.budget_fraction", label: "Warm session memory", section: "", choices: budgetChoices},
}

// settingChoice is one step of a cycling row.
type settingChoice struct {
	value string
	label string
}

// budgetChoices are the shares of machine memory the warm pool may hold. Past
// it, sessions nobody has opened for a while go cold. See
// docs/specs/42-cold-sessions.md.
var budgetChoices = []settingChoice{
	{value: "0.25", label: "quarter of RAM"},
	{value: "0.5", label: "half of RAM"},
	{value: "0.75", label: "three quarters"},
	{value: "0", label: "no limit"},
}

var generalSettingDefaults = map[string]bool{
	"autotitle.enabled": false,
	"autotitle.emoji":   false,
	// Opt-in: eviction kills a running agent, so upgrading must not start
	// doing it. See docs/specs/42-cold-sessions.md.
	"memory.evict": false,
}

func loadGeneralSettings(c *client) tea.Cmd {
	return func() tea.Msg {
		settings, err := c.getSettings()
		if err != nil {
			return generalSettingsLoaded{err: err}
		}
		values := make(map[string]bool, len(generalSettingsKeys))
		choices := make(map[string]string, len(generalSettingsKeys))
		for _, item := range generalSettingsKeys {
			if item.choices != nil {
				choices[item.key] = pickChoice(item.choices, settings[item.key])
				continue
			}
			if raw, ok := settings[item.key]; ok {
				values[item.key] = raw == "true"
				continue
			}
			values[item.key] = generalSettingDefaults[item.key]
		}
		return generalSettingsLoaded{values: values, choices: choices}
	}
}

// pickChoice resolves a stored value to one of the offered steps. Anything
// unrecognised lands on the first, so a hand-edited setting cannot leave the
// row showing nothing.
func pickChoice(choices []settingChoice, stored string) string {
	for _, c := range choices {
		if c.value == stored {
			return c.value
		}
	}
	return choices[0].value
}

// labelFor is what a cycling row displays.
func labelFor(choices []settingChoice, value string) string {
	for _, c := range choices {
		if c.value == value {
			return c.label
		}
	}
	return choices[0].label
}

func (m StartModel) toggleGeneralSetting() (tea.Model, tea.Cmd) {
	if m.settingsValues == nil {
		m.settingsValues = defaultGeneralSettings()
	}
	item := generalSettingsKeys[m.settingsCursor]
	key := item.key

	var val string
	if item.choices != nil {
		// A cycle, not a flip: step to the next choice and wrap.
		if m.settingsChoices == nil {
			m.settingsChoices = map[string]string{}
		}
		current := pickChoice(item.choices, m.settingsChoices[key])
		next := item.choices[0].value
		for i, c := range item.choices {
			if c.value == current {
				next = item.choices[(i+1)%len(item.choices)].value
				break
			}
		}
		m.settingsChoices[key] = next
		val = next
	} else {
		m.settingsValues[key] = !m.settingsValues[key]
		val = "false"
		if m.settingsValues[key] {
			val = "true"
		}
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
	m.settingsChoices = map[string]string{}
	for _, item := range generalSettingsKeys {
		if item.choices != nil {
			patch[item.key] = item.choices[0].value
			m.settingsChoices[item.key] = item.choices[0].value
			continue
		}
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

		var state string
		if item.choices != nil {
			state = toggleOnStyle.Render(labelFor(item.choices, m.settingsChoices[item.key]))
		} else if values[item.key] {
			state = toggleOnStyle.Render("[ON ]")
		} else {
			state = toggleOffStyle.Render("[OFF]")
		}

		row := fmt.Sprintf("  %-30s %s", item.label, state)
		if i == m.settingsCursor {
			b.WriteString(selectedStyle.Render(row))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑/↓ navigate  space/enter change  r reset defaults  q back"))

	return b.String()
}
