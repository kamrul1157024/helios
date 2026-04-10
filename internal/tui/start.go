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

// Screens in the start flow
type screen int

const (
	screenLoading        screen = iota // checking daemon, starting if needed
	screenHooksInstall                 // prompt to install Claude hooks
	screenTunnelSelect                 // first time only: pick tunnel provider
	screenBinaryMissing                // tunnel binary not found
	screenTunnelStarting               // starting tunnel...
	screenCustomURL                    // custom URL input
	screenMain                         // main dashboard: status + devices + QRs
	screenConfirmDevice                // "Allow this device? y/n"
	screenError                        // error
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

// Messages
type statusCheckDone struct {
	daemonOK    bool
	hooksOK     bool
	tunnelOK    bool
	tunnelURL   string
	tunnelProv  string
	deviceCount int
	devices     []deviceInfo
	err         error
}

type tunnelStarted struct {
	url string
	err error
}

type deviceCreated struct {
	token     string
	expiresIn int
	setupURL  string
	err       error
}

type devicePollResult struct {
	pendingDevice *deviceInfo
	devices       []deviceInfo
}

type deviceActionDone struct {
	err error
}

type hooksInstallDone struct {
	err error
}

type tickMsg time.Time
type tokenTickMsg time.Time

// Model
type StartModel struct {
	screen     screen
	client     *client
	spinner    spinner.Model
	textInput  textinput.Model
	publicPort int

	// Status check results
	daemonOK    bool
	hooksOK     bool
	tunnelOK    bool
	tunnelURL   string
	tunnelProv  string
	deviceCount int
	devices     []deviceInfo

	// Tunnel selection
	tunnelCursor int

	// Binary missing info
	missingBinary string
	installHint   string

	// Pairing QR state
	pairingToken   string
	tokenExpiresAt time.Time
	pairingQR      string
	setupURL       string

	// Download QR (tunnel URL)
	downloadQR string

	// Device confirmation
	pendingDevice *deviceInfo

	// Custom URL input
	customURL string

	// Error
	errMsg string

	// Dimensions
	width  int
	height int
}

func NewStartModel(internalPort, publicPort int) StartModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "https://my-domain.com"
	ti.CharLimit = 200
	ti.Width = 50

	return StartModel{
		screen:     screenLoading,
		client:     newClient(internalPort),
		spinner:    s,
		textInput:  ti,
		publicPort: publicPort,
	}
}

func (m StartModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, checkStatus(m.client, m.publicPort))
}

func (m StartModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case hooksInstallDone:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("Failed to install hooks: %v", msg.err)
			m.screen = screenError
			return m, nil
		}
		m.hooksOK = true
		return m.proceedAfterHooks()

	case deviceActionDone:
		return m.handleDeviceAction(msg)

	case tickMsg:
		if m.screen == screenMain && m.pendingDevice == nil {
			return m, pollDevices(m.client)
		}
		return m, nil

	case tokenTickMsg:
		if m.screen == screenMain {
			if time.Now().After(m.tokenExpiresAt) {
				// Token expired — generate a new one
				return m, createDevice(m.client)
			}
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tokenTickMsg(t)
		})
	}

	// Handle text input updates
	if m.screen == screenCustomURL {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m StartModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		switch m.screen {
		case screenMain:
			return m, tea.Quit
		case screenHooksInstall, screenTunnelSelect, screenBinaryMissing, screenError:
			return m, tea.Quit
		}

	case "up", "k":
		if m.screen == screenTunnelSelect {
			if m.tunnelCursor > 0 {
				m.tunnelCursor--
			}
		}

	case "down", "j":
		if m.screen == screenTunnelSelect {
			if m.tunnelCursor < len(tunnelProviders)-1 {
				m.tunnelCursor++
			}
		}

	case "enter":
		return m.handleEnter()

	case "y":
		if m.screen == screenConfirmDevice && m.pendingDevice != nil {
			kid := m.pendingDevice.KID
			m.pendingDevice = nil
			return m, activateDevice(m.client, kid)
		}

	case "n":
		if m.screen == screenConfirmDevice && m.pendingDevice != nil {
			kid := m.pendingDevice.KID
			m.pendingDevice = nil
			return m, rejectDevice(m.client, kid)
		}

	case "s":
		if m.screen == screenHooksInstall {
			return m.proceedAfterHooks()
		}
	}

	return m, nil
}

func (m StartModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenLoading:
		if !m.daemonOK {
			m.errMsg = "Could not start daemon"
			m.screen = screenError
			return m, nil
		}
		if !m.hooksOK {
			m.screen = screenHooksInstall
			return m, nil
		}
		if !m.tunnelOK {
			m.screen = screenTunnelSelect
			return m, nil
		}
		// Tunnel OK — go to main
		m.screen = screenMain
		return m, tea.Batch(m.spinner.Tick, createDevice(m.client))

	case screenHooksInstall:
		return m, installHooksCmd()

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

	case screenError:
		return m, tea.Quit
	}

	return m, nil
}

func (m StartModel) proceedAfterHooks() (tea.Model, tea.Cmd) {
	if !m.tunnelOK {
		m.screen = screenTunnelSelect
		return m, nil
	}
	m.screen = screenMain
	return m, tea.Batch(m.spinner.Tick, createDevice(m.client))
}

func (m StartModel) handleStatusCheck(msg statusCheckDone) (tea.Model, tea.Cmd) {
	m.daemonOK = msg.daemonOK
	m.hooksOK = msg.hooksOK
	m.tunnelOK = msg.tunnelOK
	m.tunnelURL = msg.tunnelURL
	m.tunnelProv = msg.tunnelProv
	m.deviceCount = msg.deviceCount
	m.devices = msg.devices

	if msg.err != nil {
		m.errMsg = msg.err.Error()
	}

	// Generate download QR if tunnel is active
	if m.tunnelURL != "" {
		qr, err := qrcode.New(m.tunnelURL, qrcode.Medium)
		if err == nil {
			m.downloadQR = qr.ToSmallString(false)
		}
	}

	return m, nil
}

func (m StartModel) handleTunnelStarted(msg tunnelStarted) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errMsg = fmt.Sprintf("Tunnel failed: %v", msg.err)
		m.screen = screenError
		return m, nil
	}
	m.tunnelOK = true
	m.tunnelURL = msg.url
	m.tunnelProv = tunnelProviders[m.tunnelCursor].id

	// Generate download QR
	qr, err := qrcode.New(m.tunnelURL, qrcode.Medium)
	if err == nil {
		m.downloadQR = qr.ToSmallString(false)
	}

	// Now go to main and create a pairing token
	m.screen = screenMain
	return m, tea.Batch(m.spinner.Tick, createDevice(m.client))
}

func (m StartModel) handleDeviceCreated(msg deviceCreated) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errMsg = fmt.Sprintf("Pairing token generation failed: %v", msg.err)
		m.screen = screenError
		return m, nil
	}

	m.pairingToken = msg.token
	m.tokenExpiresAt = time.Now().Add(time.Duration(msg.expiresIn) * time.Second)
	m.setupURL = msg.setupURL

	if m.setupURL == "" && m.tunnelURL != "" {
		m.setupURL = fmt.Sprintf("helios://pair?url=%s&token=%s", m.tunnelURL, msg.token)
	}

	// Generate pairing QR
	if m.setupURL != "" {
		qr, err := qrcode.New(m.setupURL, qrcode.Medium)
		if err == nil {
			m.pairingQR = qr.ToSmallString(false)
		}
	}

	// Start polling for pending devices + token countdown
	return m, tea.Batch(
		pollDevices(m.client),
		tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tokenTickMsg(t)
		}),
	)
}

func (m StartModel) handleDevicePoll(msg devicePollResult) (tea.Model, tea.Cmd) {
	m.devices = msg.devices

	// Count active devices
	m.deviceCount = 0
	for _, d := range m.devices {
		if d.Status == "active" {
			m.deviceCount++
		}
	}

	if msg.pendingDevice != nil {
		m.pendingDevice = msg.pendingDevice
		m.screen = screenConfirmDevice
		return m, nil
	}

	// Keep polling
	return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m StartModel) handleDeviceAction(msg deviceActionDone) (tea.Model, tea.Cmd) {
	// After approve/reject, go back to main, refresh devices, new pairing token
	m.screen = screenMain
	return m, tea.Batch(
		m.spinner.Tick,
		createDevice(m.client),
		pollDevices(m.client),
	)
}

// Commands

func checkStatus(c *client, publicPort int) tea.Cmd {
	return func() tea.Msg {
		result := statusCheckDone{}

		// Check daemon — if not running, try to start it
		h, err := c.health()
		if err != nil {
			// Auto-start daemon in background
			exe, exeErr := os.Executable()
			if exeErr != nil {
				exe = "helios"
			}
			proc, startErr := os.StartProcess(exe, []string{exe, "daemon", "start"}, &os.ProcAttr{
				Dir:   "/",
				Env:   os.Environ(),
				Files: []*os.File{os.Stdin, nil, nil},
			})
			if startErr == nil {
				proc.Release()
				// Wait for daemon to be ready
				for i := 0; i < 20; i++ {
					time.Sleep(250 * time.Millisecond)
					h, err = c.health()
					if err == nil {
						break
					}
				}
			}
			if err != nil {
				result.err = fmt.Errorf("could not start daemon")
				return result
			}
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
			result.devices = dl.Devices
			for _, d := range dl.Devices {
				if d.Status == "active" {
					result.deviceCount++
				}
			}
		}

		return result
	}
}

func installHooksCmd() tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			return hooksInstallDone{err: err}
		}
		cmd := exec.Command(exe, "hooks", "install")
		if err := cmd.Run(); err != nil {
			return hooksInstallDone{err: err}
		}
		return hooksInstallDone{}
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
		return deviceCreated{
			token:     resp.Token,
			expiresIn: resp.ExpiresIn,
			setupURL:  resp.SetupURL,
		}
	}
}

func pollDevices(c *client) tea.Cmd {
	return func() tea.Msg {
		dl, err := c.deviceList()
		if err != nil {
			return devicePollResult{}
		}

		// Look for any pending device
		var pending *deviceInfo
		for _, d := range dl.Devices {
			if d.Status == "pending" {
				dd := d
				pending = &dd
				break
			}
		}

		return devicePollResult{
			pendingDevice: pending,
			devices:       dl.Devices,
		}
	}
}

func activateDevice(c *client, kid string) tea.Cmd {
	return func() tea.Msg {
		err := c.deviceActivate(kid)
		return deviceActionDone{err: err}
	}
}

func rejectDevice(c *client, kid string) tea.Cmd {
	return func() tea.Msg {
		err := c.deviceRevoke(kid)
		return deviceActionDone{err: err}
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

func installHooksQuietly() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "hooks", "install")
	cmd.Run()
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

// RunStart launches the bubbletea start TUI.
func RunStart(internalPort, publicPort int) error {
	m := NewStartModel(internalPort, publicPort)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
