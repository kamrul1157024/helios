// The schedules view: a list, what one is, and a confirm before deleting.
//
// Built on DevicesModel's shape — a screen enum, one tea.Cmd per read, a detail
// under the list — because the two views answer the same kind of question and a
// second shape would be a second thing to learn.
//
// See docs/specs/55-scheduled-runs.md.

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type schedScreen int

const (
	schedScreenList schedScreen = iota
	schedScreenConfirmDelete
)

type schedulesLoaded struct {
	schedules []scheduleInfo
	err       error
}

type scheduleActed struct {
	err error
}

type SchedulesModel struct {
	screen  schedScreen
	client  *client
	spinner spinner.Model

	schedules []scheduleInfo
	cursor    int
	loading   bool

	errMsg string
	width  int
	height int
}

func NewSchedulesModel(internalPort int) SchedulesModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(accentColor)

	return SchedulesModel{
		screen:  schedScreenList,
		client:  newClient(internalPort),
		spinner: s,
		loading: true,
	}
}

func (m SchedulesModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadSchedules(m.client), scheduleTick())
}

// The list is a clock as much as a list — "in 4m" is only true for a minute —
// so it redraws on its own.
func scheduleTick() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return scheduleRefresh{} })
}

type scheduleRefresh struct{}

func (m SchedulesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scheduleRefresh:
		return m, tea.Batch(loadSchedules(m.client), scheduleTick())

	case schedulesLoaded:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.errMsg = ""
			m.schedules = msg.schedules
			if m.cursor >= len(m.schedules) {
				m.cursor = max(0, len(m.schedules)-1)
			}
		}

	case scheduleActed:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		return m, loadSchedules(m.client)
	}

	return m, nil
}

func (m SchedulesModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	current := func() *scheduleInfo {
		if len(m.schedules) == 0 {
			return nil
		}
		return &m.schedules[m.cursor]
	}

	switch msg.String() {
	case "ctrl+c", "q":
		if m.screen == schedScreenList {
			return m, tea.Quit
		}
		m.screen = schedScreenList

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.schedules)-1 {
			m.cursor++
		}

	case "enter", "r":
		if sc := current(); sc != nil && m.screen == schedScreenList {
			return m, runSchedule(m.client, sc.ID)
		}

	case "c":
		// Run the check without firing, which is the question a monitor is
		// opened to answer.
		if sc := current(); sc != nil && sc.Kind == "monitor" && m.screen == schedScreenList {
			return m, checkSchedule(m.client, sc.ID)
		}

	case " ":
		if sc := current(); sc != nil && m.screen == schedScreenList {
			return m, setScheduleEnabled(m.client, sc.ID, !sc.Enabled)
		}

	case "d":
		if sc := current(); sc != nil && m.screen == schedScreenList {
			m.screen = schedScreenConfirmDelete
		}

	case "y":
		if sc := current(); sc != nil && m.screen == schedScreenConfirmDelete {
			m.screen = schedScreenList
			return m, deleteSchedule(m.client, sc.ID)
		}

	case "n":
		if m.screen == schedScreenConfirmDelete {
			m.screen = schedScreenList
		}
	}

	return m, nil
}

func (m SchedulesModel) View() string {
	if m.screen == schedScreenConfirmDelete {
		return m.viewConfirmDelete()
	}
	return m.viewList()
}

func (m SchedulesModel) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("helios — schedules"))
	b.WriteString("\n\n")

	switch {
	case m.loading && len(m.schedules) == 0:
		b.WriteString(fmt.Sprintf("  %s loading…\n", m.spinner.View()))
	case m.errMsg != "":
		b.WriteString("  " + errorStyle.Render(m.errMsg) + "\n")
	case len(m.schedules) == 0:
		b.WriteString(dimStyle.Render(
			"  Nothing scheduled yet.\n\n" +
				"  A schedule is a saved prompt with something that decides when it runs:\n" +
				"  a clock, a moment, a check, or another job finishing.\n\n" +
				"  helios schedule add \"…\" --name nightly --cron \"0 2 * * *\"\n"))
	}

	// Each schedule is two lines: what it is called and where it stands, then
	// what it does. The tree is drawn by indent, as it is everywhere else.
	depth := scheduleDepths(m.schedules)
	for i, sc := range m.schedules {
		pointer := "  "
		name := sc.Name
		if i == m.cursor {
			pointer = selectedStyle.Render("▸ ")
			name = selectedStyle.Render(sc.Name)
		}
		indent := strings.Repeat("  ", depth[sc.ID])

		state := sc.state()
		styled := dimStyle.Render(state)
		switch {
		case sc.LastStatus == "failed" || sc.LastStatus == "missed":
			styled = crossStyle.Render(state)
		case sc.LastStatus == "running":
			styled = checkStyle.Render(state)
		}

		b.WriteString(fmt.Sprintf("%s%s%s %s  %s\n",
			pointer, indent, scheduleGlyph(sc.Kind), name, styled))
		b.WriteString(fmt.Sprintf("    %s%s\n", indent, dimStyle.Render(sc.summary())))
		if sc.LastError != "" {
			b.WriteString(fmt.Sprintf("    %s%s\n", indent, crossStyle.Render(sc.LastError)))
		}
	}

	b.WriteString(helpStyle.Render(
		"\n  ↑↓ move   enter run now   c check   space pause   d delete   q quit"))
	return b.String()
}

func (m SchedulesModel) viewConfirmDelete() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("helios — schedules"))
	b.WriteString("\n\n")

	name := ""
	if len(m.schedules) > 0 {
		name = m.schedules[m.cursor].Name
	}
	b.WriteString(fmt.Sprintf("  Delete %s?\n\n", selectedStyle.Render(name)))
	b.WriteString(dimStyle.Render("  Anything that followed it will be paused.\n"))
	b.WriteString(helpStyle.Render("\n  y delete   n keep"))
	return b.String()
}

func scheduleGlyph(kind string) string {
	switch kind {
	case "monitor":
		return "◉"
	case "once":
		return "⧗"
	case "after":
		return "⇢"
	default:
		return "⏰"
	}
}

// scheduleDepths is how deep in the after-chain each one sits, so a grandchild
// indents twice.
func scheduleDepths(list []scheduleInfo) map[string]int {
	parent := make(map[string]string, len(list))
	for _, sc := range list {
		parent[sc.ID] = sc.AfterID
	}
	depth := make(map[string]int, len(list))
	for _, sc := range list {
		n := 0
		for at := sc.AfterID; at != "" && n < 16; at = parent[at] {
			n++
		}
		depth[sc.ID] = n
	}
	return depth
}

// RunSchedules launches the schedules TUI.
func RunSchedules(internalPort int) error {
	p := tea.NewProgram(NewSchedulesModel(internalPort), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
