package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	qrcode "github.com/skip2/go-qrcode"
)

// Screens in the setup flow
type screen int

const (
	screenStatusCheck screen = iota
	screenAlreadySetup
	screenTunnelSelect
	screenBinaryMissing
	screenTunnelStarting
	screenCustomURL
	screenQRCode
	screenSuccess
	screenError
)

// Tunnel provider options
var tunnelProviders = []struct {
	id    string
	label string
}{
	{"cloudflare", "Cloudflare Tunnel (recommended)"},
	{"ngrok", "ngrok"},
	{"tailscale", "Tailscale"},
	{"local", "Local Network (no HTTPS)"},
	{"custom", "Custom URL"},
}

// Already-setup menu options
var setupMenuOptions = []string{
	"Add another device",
	"Change tunnel provider",
	"Exit",
}

// Messages
type statusCheckDone struct {
	daemonOK   bool
	hooksOK    bool
	tunnelOK   bool
	tunnelURL  string
	tunnelProv string
	deviceCount int
	err        error
}

type tunnelStarted struct {
	url string
	err error
}

type deviceCreated struct {
	kid      string
	key      string
	setupURL string
	err      error
}

type devicePollResult struct {
	connected  bool
	deviceName string
}

type tickMsg time.Time

// Model
type SetupModel struct {
	screen       screen
	client       *client
	spinner      spinner.Model
	textInput    textinput.Model
	publicPort   int

	// Status check results
	daemonOK    bool
	hooksOK     bool
	tunnelOK    bool
	tunnelURL   string
	tunnelProv  string
	deviceCount int

	// Tunnel selection
	tunnelCursor int

	// Already-setup menu
	menuCursor int

	// Binary missing info
	missingBinary string
	installHint   string

	// QR code state
	qrString     string
	setupURL     string
	deviceKID    string
	waitingDevice bool
	deviceName   string

	// Custom URL input
	customURL string

	// Error
	errMsg string

	// Dimensions
	width  int
	height int
}

func NewSetupModel(internalPort, publicPort int) SetupModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "https://my-domain.com"
	ti.CharLimit = 200
	ti.Width = 50

	return SetupModel{
		screen:     screenStatusCheck,
		client:     newClient(internalPort),
		spinner:    s,
		textInput:  ti,
		publicPort: publicPort,
	}
}

func (m SetupModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, checkStatus(m.client, m.publicPort))
}

func (m SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case statusCheckDone:
		return m.handleStatusCheck(msg)

	case tunnelStarted:
		return m.handleTunnelStarted(msg)

	case deviceCreated:
		return m.handleDeviceCreated(msg)

	case devicePollResult:
		return m.handleDevicePoll(msg)

	case tickMsg:
		if m.waitingDevice {
			return m, tea.Batch(pollDevice(m.client, m.deviceKID), m.spinner.Tick)
		}
		return m, nil
	}

	// Handle text input updates
	if m.screen == screenCustomURL {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m SetupModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "q":
		switch m.screen {
		case screenQRCode, screenBinaryMissing, screenError:
			return m, tea.Quit
		case screenTunnelSelect:
			return m, tea.Quit
		}

	case "up", "k":
		switch m.screen {
		case screenTunnelSelect:
			if m.tunnelCursor > 0 {
				m.tunnelCursor--
			}
		case screenAlreadySetup:
			if m.menuCursor > 0 {
				m.menuCursor--
			}
		}

	case "down", "j":
		switch m.screen {
		case screenTunnelSelect:
			if m.tunnelCursor < len(tunnelProviders)-1 {
				m.tunnelCursor++
			}
		case screenAlreadySetup:
			if m.menuCursor < len(setupMenuOptions)-1 {
				m.menuCursor++
			}
		}

	case "enter":
		return m.handleEnter()
	}

	return m, nil
}

func (m SetupModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenStatusCheck:
		if !m.daemonOK {
			// Try to start daemon
			m.errMsg = "Daemon not running. Start it with: helios daemon start -d"
			m.screen = screenError
			return m, nil
		}
		if m.tunnelOK && m.deviceCount > 0 {
			// Already fully set up
			m.screen = screenAlreadySetup
			return m, nil
		}
		if !m.tunnelOK {
			m.screen = screenTunnelSelect
		} else {
			// Tunnel OK but no devices — go to QR
			m.screen = screenQRCode
			return m, tea.Batch(m.spinner.Tick, createDevice(m.client))
		}
		return m, nil

	case screenAlreadySetup:
		switch m.menuCursor {
		case 0: // Add another device
			m.screen = screenQRCode
			return m, tea.Batch(m.spinner.Tick, createDevice(m.client))
		case 1: // Change tunnel provider
			m.screen = screenTunnelSelect
			return m, nil
		case 2: // Exit
			return m, tea.Quit
		}

	case screenTunnelSelect:
		provider := tunnelProviders[m.tunnelCursor]
		if provider.id == "custom" {
			m.screen = screenCustomURL
			m.textInput.Focus()
			return m, textinput.Blink
		}
		// Check if binary exists for non-custom/non-local
		if provider.id != "local" {
			binary := providerBinary(provider.id)
			if _, err := exec.LookPath(binary); err != nil {
				m.missingBinary = binary
				m.installHint = providerInstallHint(provider.id)
				m.screen = screenBinaryMissing
				return m, nil
			}
		}
		m.screen = screenTunnelStarting
		return m, tea.Batch(m.spinner.Tick, startTunnel(m.client, provider.id, "", m.publicPort))

	case screenCustomURL:
		url := m.textInput.Value()
		if url == "" {
			return m, nil
		}
		m.screen = screenTunnelStarting
		return m, tea.Batch(m.spinner.Tick, startTunnel(m.client, "custom", url, m.publicPort))

	case screenBinaryMissing:
		// Retry — check if binary was installed
		provider := tunnelProviders[m.tunnelCursor]
		binary := providerBinary(provider.id)
		if _, err := exec.LookPath(binary); err != nil {
			return m, nil // Still missing
		}
		m.screen = screenTunnelStarting
		return m, tea.Batch(m.spinner.Tick, startTunnel(m.client, provider.id, "", m.publicPort))

	case screenSuccess:
		return m, tea.Quit

	case screenError:
		return m, tea.Quit
	}

	return m, nil
}

func (m SetupModel) handleStatusCheck(msg statusCheckDone) (tea.Model, tea.Cmd) {
	m.daemonOK = msg.daemonOK
	m.hooksOK = msg.hooksOK
	m.tunnelOK = msg.tunnelOK
	m.tunnelURL = msg.tunnelURL
	m.tunnelProv = msg.tunnelProv
	m.deviceCount = msg.deviceCount

	if msg.err != nil {
		m.errMsg = msg.err.Error()
	}

	return m, nil
}

func (m SetupModel) handleTunnelStarted(msg tunnelStarted) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errMsg = fmt.Sprintf("Tunnel failed: %v", msg.err)
		m.screen = screenError
		return m, nil
	}
	m.tunnelOK = true
	m.tunnelURL = msg.url
	m.tunnelProv = tunnelProviders[m.tunnelCursor].id

	// Now create a device and show QR
	m.screen = screenQRCode
	return m, tea.Batch(m.spinner.Tick, createDevice(m.client))
}

func (m SetupModel) handleDeviceCreated(msg deviceCreated) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errMsg = fmt.Sprintf("Device creation failed: %v", msg.err)
		m.screen = screenError
		return m, nil
	}
	m.deviceKID = msg.kid
	m.setupURL = msg.setupURL
	if m.setupURL == "" {
		m.setupURL = fmt.Sprintf("%s/#/setup?key=%s&kid=%s", m.tunnelURL, msg.key, msg.kid)
	}

	// Generate QR
	qr, err := qrcode.New(m.setupURL, qrcode.Medium)
	if err == nil {
		m.qrString = qr.ToSmallString(false)
	}

	// Start polling for device connection
	m.waitingDevice = true
	return m, tea.Batch(m.spinner.Tick, pollDevice(m.client, m.deviceKID))
}

func (m SetupModel) handleDevicePoll(msg devicePollResult) (tea.Model, tea.Cmd) {
	if msg.connected {
		m.waitingDevice = false
		m.deviceName = msg.deviceName
		m.deviceCount++
		m.screen = screenSuccess
		return m, nil
	}
	// Keep polling
	return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Commands

func checkStatus(c *client, publicPort int) tea.Cmd {
	return func() tea.Msg {
		result := statusCheckDone{}

		// Check daemon
		h, err := c.health()
		if err != nil {
			result.err = fmt.Errorf("daemon not running on port %d", publicPort)
			return result
		}
		result.daemonOK = h.Status == "ok"

		// Check hooks
		result.hooksOK = hooksInstalled()

		// Check tunnel
		ts, err := c.tunnelStatus()
		if err == nil && ts.Active {
			result.tunnelOK = true
			result.tunnelURL = ts.PublicURL
			result.tunnelProv = ts.Provider
		}

		// Check devices
		dl, err := c.deviceList()
		if err == nil {
			for _, d := range dl.Devices {
				if d.Status == "active" {
					result.deviceCount++
				}
			}
		}

		return result
	}
}

func startTunnel(c *client, provider, customURL string, localPort int) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.tunnelStart(provider, customURL, localPort)
		if err != nil {
			return tunnelStarted{err: err}
		}
		return tunnelStarted{url: resp.PublicURL}
	}
}

func createDevice(c *client) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.deviceCreate()
		if err != nil {
			return deviceCreated{err: err}
		}
		return deviceCreated{kid: resp.KID, key: resp.Key, setupURL: resp.SetupURL}
	}
}

func pollDevice(c *client, kid string) tea.Cmd {
	return func() tea.Msg {
		dl, err := c.deviceList()
		if err != nil {
			return devicePollResult{connected: false}
		}
		for _, d := range dl.Devices {
			if d.KID == kid && d.Status == "active" {
				name := d.Name
				if name == "" {
					name = d.KID
				}
				return devicePollResult{connected: true, deviceName: name}
			}
		}
		return devicePollResult{connected: false}
	}
}

func hooksInstalled() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.claude/settings.json")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "/hooks/permission")
}

func providerBinary(provider string) string {
	switch provider {
	case "cloudflare":
		return "cloudflared"
	case "ngrok":
		return "ngrok"
	case "tailscale":
		return "tailscale"
	default:
		return ""
	}
}

func providerInstallHint(provider string) string {
	switch provider {
	case "cloudflare":
		return "brew install cloudflared"
	case "ngrok":
		return "brew install ngrok  (or https://ngrok.com/download)"
	case "tailscale":
		return "brew install tailscale  (or https://tailscale.com/download)"
	default:
		return ""
	}
}

// RunSetup launches the bubbletea setup TUI.
func RunSetup(internalPort, publicPort int) error {
	m := NewSetupModel(internalPort, publicPort)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
