package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var toggleOnStyle = lipgloss.NewStyle().
	Foreground(accentColor)

var toggleOffStyle = lipgloss.NewStyle().
	Foreground(dangerColor)

var generalSettingsKeys = []struct {
	key     string
	label   string
	section string
	// slider makes a row a value dragged with ←/→ rather than a toggle. The
	// value is a fraction stored as a string.
	slider bool
}{
	{key: "autotitle.enabled", label: "Auto title generation", section: "Sessions"},
	{key: "autotitle.emoji", label: "Title icon prefix (needs a Nerd Font)", section: ""},
	{key: "memory.evict", label: "Save memory", section: ""},
	{key: "memory.budget_fraction", label: "Memory limit", section: "", slider: true},
}

// The slider's travel, as a share of machine memory. Past the budget, sessions
// nobody has opened for a while go cold. See docs/specs/42-cold-sessions.md.
//
// It stops short of the whole machine at both ends: below a twentieth the
// budget cannot hold one agent, and at 100% eviction would only begin once the
// machine was already swapping.
const (
	budgetMin     = 0.05
	budgetMax     = 0.9
	budgetStep    = 0.05
	budgetDefault = 0.25
	// budgetCells is how wide the bar is drawn.
	budgetCells = 17
)

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
			if item.slider {
				choices[item.key] = formatFraction(clampBudget(parseFraction(settings[item.key])))
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

// parseFraction reads a stored fraction. Anything unparseable lands on the
// default, so a hand-edited setting cannot leave the row showing nothing.
func parseFraction(stored string) float64 {
	f, err := strconv.ParseFloat(stored, 64)
	if err != nil {
		return budgetDefault
	}
	return f
}

// clampBudget holds a value inside the slider's travel. The setting predates
// the slider, so a stored one can sit outside it.
func clampBudget(f float64) float64 {
	return math.Min(budgetMax, math.Max(budgetMin, f))
}

// formatFraction is the stored form: two decimals, so stepping by twentieths
// cannot accumulate float noise in the database.
func formatFraction(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// budgetBar draws the slider. A filled run, an empty one, and the percentage,
// because a bar alone cannot be read back as a number.
func budgetBar(f float64) string {
	filled := int(math.Round((f - budgetMin) / (budgetMax - budgetMin) * float64(budgetCells)))
	if filled < 0 {
		filled = 0
	}
	if filled > budgetCells {
		filled = budgetCells
	}
	return fmt.Sprintf("%s%s %3d%%",
		strings.Repeat("━", filled),
		strings.Repeat("─", budgetCells-filled),
		int(math.Round(f*100)))
}

// adjustGeneralSetting moves a slider row by one step. Rows that are not
// sliders ignore ←/→.
func (m StartModel) adjustGeneralSetting(delta float64) (tea.Model, tea.Cmd) {
	item := generalSettingsKeys[m.settingsCursor]
	if !item.slider {
		return m, nil
	}
	if m.settingsChoices == nil {
		m.settingsChoices = map[string]string{}
	}
	next := formatFraction(clampBudget(parseFraction(m.settingsChoices[item.key]) + delta))
	if next == m.settingsChoices[item.key] {
		return m, nil
	}
	m.settingsChoices[item.key] = next
	return m, saveSetting(m.client, item.key, next)
}

func (m StartModel) toggleGeneralSetting() (tea.Model, tea.Cmd) {
	if m.settingsValues == nil {
		m.settingsValues = defaultGeneralSettings()
	}
	item := generalSettingsKeys[m.settingsCursor]
	// A slider has no state to flip; enter on one would either do nothing or
	// jump it somewhere the user did not aim for.
	if item.slider {
		return m, nil
	}

	m.settingsValues[item.key] = !m.settingsValues[item.key]
	val := "false"
	if m.settingsValues[item.key] {
		val = "true"
	}
	return m, saveSetting(m.client, item.key, val)
}

func saveSetting(c *client, key, val string) tea.Cmd {
	patch := map[string]string{key: val}
	return func() tea.Msg {
		c.updateSettings(patch) //nolint:errcheck — best-effort
		return generalSettingSaved{}
	}
}

func (m StartModel) resetGeneralSettings() (tea.Model, tea.Cmd) {
	m.settingsValues = defaultGeneralSettings()
	patch := make(map[string]string, len(generalSettingsKeys))
	m.settingsChoices = map[string]string{}
	for _, item := range generalSettingsKeys {
		if item.slider {
			patch[item.key] = formatFraction(budgetDefault)
			m.settingsChoices[item.key] = formatFraction(budgetDefault)
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
		if item.slider {
			bar := budgetBar(clampBudget(parseFraction(m.settingsChoices[item.key])))
			// A limit nothing enforces should not read as a live setting.
			if values["memory.evict"] {
				state = toggleOnStyle.Render(bar)
			} else {
				state = dimStyle.Render(bar)
			}
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
	b.WriteString(helpStyle.Render("  ↑/↓ navigate  space/enter toggle  ←/→ adjust  r reset defaults  q back"))

	return b.String()
}
