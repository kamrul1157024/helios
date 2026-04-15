package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/tmux"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Color Schemes ──
//
// Multiple schemes cycled with 'c'. Stored in daemon settings as "tui_color_scheme".

const schemeSettKey = "tui_color_scheme"

// schemeOrder defines the cycle order for pressing 'c'.
var schemeOrder = []string{"dark", "surface", "ocean", "forest", "dracula", "solarized"}

// schemeColors holds the palette for a given color scheme.
type schemeColors struct {
	surface         lipgloss.Color // card bg (empty string = transparent)
	surfaceSelected lipgloss.Color
	onSurface       lipgloss.Color // primary text
	onSurfaceDim    lipgloss.Color // secondary text
	onSurfaceMuted  lipgloss.Color // tertiary text
	outline         lipgloss.Color // borders
	primary         lipgloss.Color
	hasBG           bool // whether to apply Background()
}

var schemes = map[string]schemeColors{
	"dark": {
		onSurface:      lipgloss.Color("252"),
		onSurfaceDim:   lipgloss.Color("245"),
		onSurfaceMuted: lipgloss.Color("240"),
		outline:        lipgloss.Color("238"),
		primary:        lipgloss.Color("70"),
		hasBG:          false,
	},
	"surface": {
		surface:         lipgloss.Color("235"),
		surfaceSelected: lipgloss.Color("237"),
		onSurface:       lipgloss.Color("252"),
		onSurfaceDim:    lipgloss.Color("245"),
		onSurfaceMuted:  lipgloss.Color("240"),
		outline:         lipgloss.Color("238"),
		primary:         lipgloss.Color("70"),
		hasBG:           true,
	},
	"ocean": {
		surface:         lipgloss.Color("17"),  // deep navy
		surfaceSelected: lipgloss.Color("18"),  // brighter navy
		onSurface:       lipgloss.Color("159"), // light cyan
		onSurfaceDim:    lipgloss.Color("110"), // steel blue
		onSurfaceMuted:  lipgloss.Color("67"),  // muted blue
		outline:         lipgloss.Color("24"),
		primary:         lipgloss.Color("81"), // bright cyan
		hasBG:           true,
	},
	"forest": {
		surface:         lipgloss.Color("22"),  // deep green
		surfaceSelected: lipgloss.Color("23"),  // teal
		onSurface:       lipgloss.Color("194"), // light mint
		onSurfaceDim:    lipgloss.Color("108"), // sage
		onSurfaceMuted:  lipgloss.Color("65"),  // olive
		outline:         lipgloss.Color("28"),
		primary:         lipgloss.Color("114"), // bright green
		hasBG:           true,
	},
	"dracula": {
		surface:         lipgloss.Color("236"), // bg
		surfaceSelected: lipgloss.Color("238"), // current line
		onSurface:       lipgloss.Color("231"), // foreground
		onSurfaceDim:    lipgloss.Color("183"), // purple-ish
		onSurfaceMuted:  lipgloss.Color("61"),  // comment
		outline:         lipgloss.Color("60"),
		primary:         lipgloss.Color("212"), // pink
		hasBG:           true,
	},
	"solarized": {
		surface:         lipgloss.Color("0"),   // base03
		surfaceSelected: lipgloss.Color("234"), // base02
		onSurface:       lipgloss.Color("187"), // base1
		onSurfaceDim:    lipgloss.Color("246"), // base0
		onSurfaceMuted:  lipgloss.Color("242"), // base01
		outline:         lipgloss.Color("240"),
		primary:         lipgloss.Color("136"), // yellow
		hasBG:           true,
	},
}

// nextScheme cycles to the next color scheme.
func nextScheme(current string) string {
	for i, name := range schemeOrder {
		if name == current {
			return schemeOrder[(i+1)%len(schemeOrder)]
		}
	}
	return schemeOrder[0]
}

// Status accent colors (left border)
var (
	colorActive    = lipgloss.Color("70")  // green
	colorWaiting   = lipgloss.Color("214") // amber
	colorCompact   = lipgloss.Color("75")  // blue
	colorError     = lipgloss.Color("196") // red
	colorIdle      = lipgloss.Color("241") // grey
	colorTerminated = lipgloss.Color("245") // muted grey
)

// ── Styles (scheme-independent) ──

var (
	sessBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	sessSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("236"))

	sessSearchLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75"))
)

func sessStatusIcon(status string) string {
	switch status {
	case "starting":
		return "◌"
	case "active":
		return "●"
	case "waiting_permission":
		return "◆"
	case "idle":
		return "○"
	case "compacting":
		return "↻"
	case "error":
		return "✕"
	case "terminated":
		return "✕"
	default:
		return "○"
	}
}

func sessCanResume(status string) bool {
	return status == "terminated"
}

func sessStatusColor(status string) lipgloss.Color {
	switch status {
	case "active":
		return colorActive
	case "waiting_permission":
		return colorWaiting
	case "compacting":
		return colorCompact
	case "error":
		return colorError
	case "terminated":
		return colorTerminated
	default:
		return colorIdle
	}
}

// ── Messages ──

type sessionsLoaded struct {
	sessions []sessionInfo
	err      error
}

type sessSSEUpdate struct {
	SessionID       string `json:"session_id"`
	Status          string `json:"status,omitempty"`
	LastUserMessage string `json:"last_user_message,omitempty"`
	CWD             string `json:"cwd,omitempty"`
}

type sessSSEMsg sessSSEUpdate

type sessRefreshTick time.Time

type sessCreated struct {
	sess *sessionInfo
	err  error
}

// ── Model ──

const maxTabs = 2

type SessionsModel struct {
	client  *client
	tmux    *tmux.Client
	spinner spinner.Model
	search  textinput.Model

	sessions []sessionInfo
	filtered []int
	cursor   int
	offset   int
	loading  bool

	searching bool

	openTabs [maxTabs]string
	tabCount int
	myPaneID string

	scheme string // "dark" or "surface"

	errMsg string
	width  int
	height int
}

// colors returns the active color scheme palette.
func (m *SessionsModel) colors() schemeColors {
	if c, ok := schemes[m.scheme]; ok {
		return c
	}
	return schemes[schemeOrder[0]]
}

func NewSessionsModel(internalPort int) SessionsModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "filter sessions..."
	ti.CharLimit = 100

	c := newClient(internalPort)

	// Load persisted color scheme from daemon settings.
	scheme := schemeOrder[0]
	if settings, err := c.getSettings(); err == nil {
		if v, ok := settings[schemeSettKey]; ok && v != "" {
			scheme = v
		}
	}

	return SessionsModel{
		client:   c,
		tmux:     tmux.NewClient(),
		spinner:  s,
		search:   ti,
		loading:  true,
		myPaneID: os.Getenv("TMUX_PANE"),
		scheme:   scheme,
	}
}

func (m SessionsModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchSessions(m.client),
		tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return sessRefreshTick(t)
		}),
	)
}

func (m SessionsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.search.Width = m.innerWidth() - 6
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case sessionsLoaded:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.sessions = m.filterVisibleSessions(msg.sessions)
		m.refilter()
		return m, nil

	case sessSSEMsg:
		return m, fetchSessions(m.client)

	case sessRefreshTick:
		return m, tea.Batch(
			fetchSessions(m.client),
			tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return sessRefreshTick(t)
			}),
		)

	case sessCreated:
		if msg.err != nil || msg.sess == nil {
			return m, nil
		}
		m.openTab(*msg.sess)
		return m, fetchSessions(m.client)
	}

	if m.searching {
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.refilter()
		return m, cmd
	}

	return m, nil
}

func (m *SessionsModel) innerWidth() int {
	// Border takes 2 chars each side + 1 padding each side = 6
	w := m.width - 6
	if w < 20 {
		w = 20
	}
	return w
}

func (m *SessionsModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.searching {
		switch key {
		case "esc":
			m.searching = false
			m.search.Blur()
			m.search.SetValue("")
			m.refilter()
			return m, nil
		case "enter":
			m.searching = false
			m.search.Blur()
			return m, nil
		case "up", "ctrl+k":
			if m.cursor > 0 {
				m.cursor--
				m.scrollIntoView()
			}
			return m, nil
		case "down", "ctrl+j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.scrollIntoView()
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.refilter()
			return m, cmd
		}
	}

	switch key {
	case "ctrl+c", "q":
		m.closeAllTabs()
		return m, tea.Quit

	case "/":
		m.searching = true
		m.search.Focus()
		return m, textinput.Blink

	case "esc":
		if m.tabCount > 0 {
			m.closeAllTabs()
			return m, nil
		}
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.scrollIntoView()
		}

	case "down", "j":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.scrollIntoView()
		}

	case "enter", "t":
		if m.cursor < len(m.filtered) {
			m.openTab(m.sessions[m.filtered[m.cursor]])
		}

	case "r":
		m.loading = true
		return m, fetchSessions(m.client)

	case "n":
		cwd := ""
		if m.cursor < len(m.filtered) {
			cwd = m.sessions[m.filtered[m.cursor]].CWD
		}
		cl := m.client
		return m, func() tea.Msg {
			sess, err := cl.sessionCreate(cwd)
			return sessCreated{sess: sess, err: err}
		}

	case "c":
		m.scheme = nextScheme(m.scheme)
		go m.client.updateSettings(map[string]string{schemeSettKey: m.scheme}) //nolint:errcheck
		return m, nil
	}

	return m, nil
}

func (m *SessionsModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.cursor > 0 {
			m.cursor--
			m.scrollIntoView()
		}
	case tea.MouseButtonWheelDown:
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.scrollIntoView()
		}
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionRelease {
			return m, nil
		}
		// Layout: title(1) + search(1) + blank(1) = 3 header lines
		// Each card is cardLines(3) + 1 gap line = 4 lines.
		row := msg.Y - 3
		cardHeight := cardLines + 1
		if row >= 0 {
			idx := m.offset + row/cardHeight
			if idx >= 0 && idx < len(m.filtered) {
				if m.cursor == idx {
					m.openTab(m.sessions[m.filtered[m.cursor]])
				} else {
					m.cursor = idx
				}
			}
		}
	}
	return m, nil
}

// ── Search / filter ──

func (m *SessionsModel) refilter() {
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	m.filtered = m.filtered[:0]

	for i, s := range m.sessions {
		if query == "" {
			m.filtered = append(m.filtered, i)
			continue
		}
		project := strings.ToLower(filepath.Base(s.CWD))
		label := strings.ToLower(s.label())
		status := strings.ToLower(s.Status)
		if strings.Contains(project, query) ||
			strings.Contains(label, query) ||
			strings.Contains(status, query) {
			m.filtered = append(m.filtered, i)
		}
	}

	m.clampCursor()
}

// ── Tab management ──

// listPanePercent is the percentage of the window width to give the list pane.
const listPanePercent = 40

func (m *SessionsModel) openTab(sess sessionInfo) {
	if sess.TmuxPane == nil || *sess.TmuxPane == "" {
		return
	}
	paneID := *sess.TmuxPane

	// Already open — just focus it.
	for i := 0; i < m.tabCount; i++ {
		if m.openTabs[i] == paneID {
			m.tmux.SelectPane(paneID)
			return
		}
	}

	// Break all existing tabs first — enter always replaces.
	for m.tabCount > 0 {
		m.tabCount--
		m.tmux.BreakPane(m.openTabs[m.tabCount])
		m.openTabs[m.tabCount] = ""
	}

	// Get full window width before join (it won't change).
	winWidth := m.tmux.WindowWidth(m.myPaneID)

	if err := m.tmux.JoinPaneHorizontal(paneID, m.myPaneID, 70); err != nil {
		return
	}
	m.openTabs[0] = paneID
	m.tabCount = 1

	if winWidth > 0 {
		cols := winWidth * listPanePercent / 100
		if cols < 30 {
			cols = 30
		}
		m.tmux.ResizePane(m.myPaneID, cols)
	}
	m.tmux.SelectPane(m.myPaneID)
}

func (m *SessionsModel) closeNewestTab() {
	if m.tabCount == 0 {
		return
	}
	m.tabCount--
	m.tmux.BreakPane(m.openTabs[m.tabCount])
	m.openTabs[m.tabCount] = ""

	if m.tabCount == 1 {
		if winWidth := m.tmux.WindowWidth(m.myPaneID); winWidth > 0 {
			cols := winWidth * listPanePercent / 100
			if cols < 30 {
				cols = 30
			}
			m.tmux.ResizePane(m.myPaneID, cols)
		}
	}
}

func (m *SessionsModel) closeAllTabs() {
	for m.tabCount > 0 {
		m.tabCount--
		m.tmux.BreakPane(m.openTabs[m.tabCount])
		m.openTabs[m.tabCount] = ""
	}
}

func (m *SessionsModel) isTabOpen(paneID string) bool {
	for i := 0; i < m.tabCount; i++ {
		if m.openTabs[i] == paneID {
			return true
		}
	}
	return false
}

func (m *SessionsModel) closeTabByPane(paneID string) {
	for i := 0; i < m.tabCount; i++ {
		if m.openTabs[i] == paneID {
			m.tmux.BreakPane(paneID)
			for j := i; j < m.tabCount-1; j++ {
				m.openTabs[j] = m.openTabs[j+1]
			}
			m.tabCount--
			m.openTabs[m.tabCount] = ""
			return
		}
	}
}

// ── Card rendering ──

// cardLines is the number of visible lines per session card.
const cardLines = 2

func (m SessionsModel) renderCard(sess sessionInfo, selected bool, width int) string {
	c := m.colors()
	accent := sessStatusColor(sess.Status)

	// Card uses lipgloss border on left side only for the accent stripe.
	// Inner width: total - border(1) - padding(2)
	inner := width - 5
	if inner < 10 {
		inner = 10
	}

	// Helper: optionally apply background.
	withBG := func(s lipgloss.Style) lipgloss.Style {
		if !c.hasBG {
			return s
		}
		bg := c.surface
		if selected {
			bg = c.surfaceSelected
		}
		return s.Background(bg)
	}

	// ── Line 1: status icon + project + time ──
	icon := sessStatusIcon(sess.Status)
	project := filepath.Base(sess.CWD)
	if len(project) > 18 {
		project = project[:17] + "…"
	}

	timeStr := ""
	if sess.LastEventAt != nil {
		t, err := time.Parse(time.RFC3339, *sess.LastEventAt)
		if err == nil {
			timeStr = humanDuration(time.Since(t))
		}
	}

	iconSty := withBG(lipgloss.NewStyle().Foreground(accent))
	projSty := withBG(lipgloss.NewStyle().Bold(true).Foreground(c.onSurface))
	if selected {
		projSty = withBG(lipgloss.NewStyle().Bold(true).Foreground(c.primary))
	}
	timeSty := withBG(lipgloss.NewStyle().Foreground(c.onSurfaceMuted))
	fillSty := withBG(lipgloss.NewStyle())

	left1 := iconSty.Render(icon) + " " + projSty.Render(project)
	right1 := timeSty.Render(timeStr)
	gap1 := inner - lipgloss.Width(left1) - lipgloss.Width(right1)
	if gap1 < 1 {
		gap1 = 1
	}
	line1 := left1 + fillSty.Render(strings.Repeat(" ", gap1)) + right1

	// ── Line 2: truncated prompt/title ──
	label := sess.label()
	maxLabel := inner - 2
	if maxLabel < 0 {
		maxLabel = 0
	}
	if len(label) > maxLabel {
		if maxLabel > 3 {
			label = label[:maxLabel-3] + "…"
		} else {
			label = ""
		}
	}
	if label == "" {
		label = "—"
	}
	labelSty := withBG(lipgloss.NewStyle().Foreground(c.onSurfaceDim))
	line2 := labelSty.Render(label)
	pad2 := inner - lipgloss.Width(line2)
	if pad2 > 0 {
		line2 += fillSty.Render(strings.Repeat(" ", pad2))
	}

	body := line1 + "\n" + line2

	// Left border for accent stripe.
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(accent).
		PaddingLeft(1)
	if c.hasBG {
		bg := c.surface
		if selected {
			bg = c.surfaceSelected
		}
		cardStyle = cardStyle.Background(bg)
	}

	return cardStyle.Render(body)
}

// ── View ──

func (m SessionsModel) View() string {
	c := m.colors()
	iw := m.innerWidth()

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(c.primary)
	countStyle := lipgloss.NewStyle().Foreground(c.onSurfaceMuted)
	dimText := lipgloss.NewStyle().Foreground(c.onSurfaceDim)

	var content strings.Builder

	// ── Title bar ──
	title := titleStyle.Render("🔥 helios sessions")
	count := ""
	if len(m.sessions) > 0 {
		count = countStyle.Render(fmt.Sprintf("%d sessions", len(m.sessions)))
	}
	titleGap := iw - lipgloss.Width(title) - lipgloss.Width(count)
	if titleGap < 1 {
		titleGap = 1
	}
	content.WriteString(title + strings.Repeat(" ", titleGap) + count + "\n")

	// ── Search box ──
	searchContent := sessSearchLabel.Render("🔍 ")
	if m.searching {
		searchContent += m.search.View()
	} else if m.search.Value() != "" {
		searchContent += dimText.Render(m.search.Value())
	} else {
		searchContent += dimText.Render("type / to search")
	}
	searchSty := lipgloss.NewStyle().Foreground(c.onSurface).Padding(0, 1)
	if c.hasBG {
		searchSty = searchSty.Background(c.surface)
	}
	searchBox := searchSty.Width(iw - 4).Render(searchContent)
	content.WriteString(searchBox + "\n")

	// ── Loading / error / empty ──
	if m.loading && len(m.sessions) == 0 {
		content.WriteString(fmt.Sprintf("\n  Loading... %s\n", m.spinner.View()))
		return sessBorderStyle.Width(m.width - 4).Render(content.String())
	}

	if m.errMsg != "" {
		content.WriteString("\n" + errorStyle.Render("  "+m.errMsg) + "\n")
		content.WriteString(m.renderHelp(iw))
		return sessBorderStyle.Width(m.width - 4).Render(content.String())
	}

	if len(m.filtered) == 0 {
		content.WriteString("\n")
		if len(m.sessions) == 0 {
			content.WriteString(dimText.Render("  No sessions. Start one with: helios new \"prompt\"") + "\n")
		} else {
			content.WriteString(dimText.Render("  No matches.") + "\n")
		}
		content.WriteString(m.renderHelp(iw))
		return sessBorderStyle.Width(m.width - 4).Render(content.String())
	}

	content.WriteString("\n")

	// ── Session cards ──
	// Reserve: title(1) + search(1) + blank(1) + sep(1) + tabs(1) + help(2) + border(2) + margins = ~12
	maxVisible := (m.height - 12) / (cardLines + 1)
	if maxVisible < 1 {
		maxVisible = 1
	}

	end := m.offset + maxVisible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}

	for i := m.offset; i < end; i++ {
		sess := m.sessions[m.filtered[i]]
		content.WriteString(m.renderCard(sess, i == m.cursor, iw) + "\n")
	}

	// Scroll indicator
	if len(m.filtered) > maxVisible {
		scrollInfo := countStyle.Render(fmt.Sprintf("  %d/%d", m.cursor+1, len(m.filtered)))
		content.WriteString(scrollInfo + "\n")
	}

	// ── Separator ──
	content.WriteString(sessSepStyle.Render(strings.Repeat("┈", iw)) + "\n")

	// ── Tab bar ──
	tabActive := lipgloss.NewStyle().Foreground(c.primary).Bold(true)
	tabBar := lipgloss.NewStyle().Foreground(c.onSurfaceMuted)
	if m.tabCount > 0 {
		var tabs []string
		for i := 0; i < m.tabCount; i++ {
			name := m.openTabs[i]
			for _, s := range m.sessions {
				if s.TmuxPane != nil && *s.TmuxPane == m.openTabs[i] {
					name = filepath.Base(s.CWD)
					break
				}
			}
			tabs = append(tabs, tabActive.Render("▸ "+name))
		}
		content.WriteString("  " + strings.Join(tabs, tabBar.Render("  │  ")) + "\n")
	} else {
		content.WriteString(dimText.Render("  no tabs open") + "\n")
	}

	// ── Help ──
	content.WriteString(m.renderHelp(iw))

	return sessBorderStyle.Width(m.width - 4).Render(content.String())
}

func (m SessionsModel) renderHelp(width int) string {
	c := m.colors()
	helpSty := lipgloss.NewStyle().Foreground(c.onSurfaceMuted)
	hintSty := lipgloss.NewStyle().Foreground(c.outline).Italic(true)

	var b strings.Builder

	if m.searching {
		b.WriteString(helpSty.Render("  esc clear  enter confirm  ↑/↓ navigate"))
	} else {
		b.WriteString(helpSty.Render("  j/k ↕  ⏎/t open  / search  c theme  r refresh  q quit"))
	}

	if m.tabCount > 0 {
		b.WriteString("\n")
		b.WriteString(hintSty.Render("  tmux: ^b ←→ switch panes  ^b z zoom  ^b ; last pane"))
	}

	return b.String()
}

// ── Cursor helpers ──

func (m *SessionsModel) clampCursor() {
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.scrollIntoView()
}

func (m *SessionsModel) scrollIntoView() {
	maxVisible := (m.height - 12) / (cardLines + 1)
	if maxVisible < 1 {
		maxVisible = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+maxVisible {
		m.offset = m.cursor - maxVisible + 1
	}
}

// ── Commands ──

func fetchSessions(c *client) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.sessionsList()
		if err != nil {
			return sessionsLoaded{err: err}
		}
		return sessionsLoaded{sessions: resp.Sessions}
	}
}

func subscribeSSE(p *tea.Program, eventsURL string) {
	for {
		resp, err := http.Get(eventsURL)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			raw := strings.TrimPrefix(line, "data: ")

			var envelope struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal([]byte(raw), &envelope) != nil {
				continue
			}
			if envelope.Type != "session_status" {
				continue
			}

			var update sessSSEUpdate
			if json.Unmarshal(envelope.Data, &update) == nil {
				p.Send(sessSSEMsg(update))
			}
		}
		resp.Body.Close()
		time.Sleep(time.Second)
	}
}

func (m SessionsModel) filterVisibleSessions(sessions []sessionInfo) []sessionInfo {
	var result []sessionInfo
	for _, s := range sessions {
		if s.TmuxPane == nil || *s.TmuxPane == "" {
			continue
		}
		switch s.Status {
		case "active", "idle", "waiting_permission", "compacting", "starting", "error":
		default:
			continue
		}
		if !m.tmux.HasPane(*s.TmuxPane) {
			continue
		}
		result = append(result, s)
	}
	return result
}

// ── Entry point ──

func RunSessions(internalPort int) error {
	if os.Getenv("TMUX") == "" {
		fmt.Fprintln(os.Stderr, "helios sessions requires tmux.")
		fmt.Fprintln(os.Stderr, "Run inside a tmux session, or use: helios sessions --list")
		os.Exit(1)
	}

	m := NewSessionsModel(internalPort)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	go subscribeSSE(p, m.client.eventsURL())

	_, err := p.Run()
	return err
}
