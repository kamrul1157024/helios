package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"

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
	screenHooksUpdate                  // prompt to update outdated hooks
	screenShellSetup                   // prompt to install shell wrapper
	screenTmuxRestart                  // prompt to restart tmux server
	screenEditorSetup                  // prompt to configure editor terminals
	screenTunnelSelect                 // first time only: pick tunnel provider
	screenBinaryMissing                // tunnel binary not found
	screenTunnelStarting               // starting tunnel...
	screenCustomURL                    // custom URL input
	screenMain                         // main dashboard: status + devices + QRs
	screenConfirmDevice                // "Allow this device? y/n"
	screenNotificationSettings         // desktop notification alert settings
	screenError                        // error
)

// Tunnel provider options
var tunnelProviders = []struct {
	id    string
	label string
}{
	{"cloudflare", "Cloudflare Tunnel (recommended)"},
	{"zrok", "zrok (open-source, stable URLs)"},
	{"ngrok", "ngrok"},
	{"localtunnel", "localtunnel (zero signup)"},
	{"localhostrun", "localhost.run (no install — uses SSH)"},
	{"localxpose", "localxpose (regional, reserved domains)"},
	{"tailscale", "Tailscale"},
	{"local", "Local Network (no HTTPS)"},
	{"custom", "Custom URL"},
}

// Messages
type tmuxStatus struct {
	Installed       bool
	Version         string
	ServerRunning   bool
	ResurrectPlugin bool
	ContinuumPlugin bool
}

type statusCheckDone struct {
	daemonOK       bool
	hooksOK        bool
	hooksOutdated  bool
	shellInfo      daemon.ShellInfo
	shellInstalled bool
	editors        []daemon.EditorInfo
	tunnelOK       bool
	tunnelURL      string
	tunnelProv     string
	deviceCount    int
	devices        []deviceInfo
	tmux           tmuxStatus
	err error
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

type shellSetupDone struct {
	installed bool
	err       error
	manual    string // manual instructions if failed
}

type tmuxRestartDone struct {
	err error
}

type editorSetupDone struct {
	results []daemon.EditorSetupResult
	manual  string // manual instructions for failed editors
}

type tickMsg time.Time
type tokenTickMsg time.Time

type notifSettingsLoaded struct {
	values map[string]bool
	err    error
}

type notifSettingSaved struct {
	err error
}

// Model
type StartModel struct {
	screen     screen
	client     *client
	spinner    spinner.Model
	textInput  textinput.Model
	publicPort int

	// Status check results
	daemonOK      bool
	hooksOK       bool
	hooksOutdated bool
	tunnelOK      bool
	tunnelURL    string
	tunnelProv   string
	deviceCount  int
	devices      []deviceInfo
	tmux         tmuxStatus
	// Notification settings screen
	notifSettingsCursor int
	notifSettingsValues map[string]bool

	// Shell setup
	shellInfo         daemon.ShellInfo
	shellInstalled    bool
	shellManual       string // non-empty if auto-install failed
	tmuxRestartNeeded bool   // true if shell wrapper was just installed/updated

	// Editor setup
	editors       []daemon.EditorInfo
	editorResults []daemon.EditorSetupResult
	editorManual  string // non-empty if any editor failed

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
		m.hooksOutdated = false
		return m.proceedAfterHooks()

	case shellSetupDone:
		if msg.err != nil {
			m.shellManual = msg.manual
		} else {
			m.shellInstalled = true
			// If tmux server is running, we need a restart for the new wrapper to take effect
			if m.tmux.ServerRunning {
				m.tmuxRestartNeeded = true
			}
		}
		return m.proceedAfterShell()

	case tmuxRestartDone:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("Failed to restart tmux: %v", msg.err)
			m.screen = screenError
			return m, nil
		}
		m.tmuxRestartNeeded = false
		return m.proceedAfterTmuxRestart()

	case editorSetupDone:
		m.editorResults = msg.results
		if msg.manual != "" {
			m.editorManual = msg.manual
		}
		return m.proceedAfterEditor()

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

	case notifSettingsLoaded:
		if msg.err == nil && msg.values != nil {
			m.notifSettingsValues = msg.values
		}
		return m, nil

	case notifSettingSaved:
		// Ignore save errors silently — settings are best-effort.
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

func (m StartModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		switch m.screen {
		case screenMain:
			return m, tea.Quit
		case screenTunnelSelect, screenBinaryMissing, screenCustomURL:
			// If tunnel is already active, go back to main instead of quitting
			if m.tunnelOK {
				m.screen = screenMain
				return m, nil
			}
			return m, tea.Quit
		case screenHooksInstall, screenHooksUpdate, screenShellSetup, screenTmuxRestart,
			screenEditorSetup, screenError:
			return m, tea.Quit
		case screenNotificationSettings:
			m.screen = screenMain
			return m, nil
		}

	case "up", "k":
		if m.screen == screenTunnelSelect {
			if m.tunnelCursor > 0 {
				m.tunnelCursor--
			}
		}
		if m.screen == screenNotificationSettings {
			if m.notifSettingsCursor > 0 {
				m.notifSettingsCursor--
			}
		}

	case "down", "j":
		if m.screen == screenTunnelSelect {
			if m.tunnelCursor < len(tunnelProviders)-1 {
				m.tunnelCursor++
			}
		}
		if m.screen == screenNotificationSettings {
			if m.notifSettingsCursor < len(notifSettingsKeys)-1 {
				m.notifSettingsCursor++
			}
		}

	case " ":
		if m.screen == screenNotificationSettings {
			return m.toggleNotifSetting()
		}

	case "enter":
		if m.screen == screenNotificationSettings {
			return m.toggleNotifSetting()
		}
		return m.handleEnter()

	case "r":
		if m.screen == screenNotificationSettings {
			return m.resetNotifSettings()
		}

	case "y":
		if m.screen == screenConfirmDevice && m.pendingDevice != nil {
			kid := m.pendingDevice.KID
			m.pendingDevice = nil
			return m, activateDevice(m.client, kid)
		}
		if m.screen == screenTmuxRestart {
			return m, restartTmuxCmd()
		}

	case "n":
		if m.screen == screenConfirmDevice && m.pendingDevice != nil {
			kid := m.pendingDevice.KID
			m.pendingDevice = nil
			return m, rejectDevice(m.client, kid)
		}
		if m.screen == screenTmuxRestart {
			m.tmuxRestartNeeded = false
			return m.proceedAfterTmuxRestart()
		}

	case "t":
		if m.screen == screenMain || (m.screen == screenLoading && m.daemonOK) {
			m.screen = screenTunnelSelect
			return m, nil
		}

	case "N":
		if m.screen == screenMain {
			m.screen = screenNotificationSettings
			m.notifSettingsCursor = 0
			return m, loadNotifSettings(m.client)
		}

	case "s":
		switch m.screen {
		case screenHooksInstall, screenHooksUpdate:
			return m.proceedAfterHooks()
		case screenShellSetup:
			return m.proceedAfterShell()
		case screenTmuxRestart:
			m.tmuxRestartNeeded = false
			return m.proceedAfterTmuxRestart()
		case screenEditorSetup:
			return m.proceedAfterEditor()
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
		if m.hooksOutdated {
			m.screen = screenHooksUpdate
			return m, nil
		}
		return m.proceedAfterHooks()

	case screenHooksInstall:
		return m, installHooksCmd()

	case screenHooksUpdate:
		return m, installHooksCmd()

	case screenShellSetup:
		if m.shellManual != "" {
			// Manual instructions shown — just continue
			return m.proceedAfterShell()
		}
		return m, installShellWrapperCmd(m.shellInfo)

	case screenEditorSetup:
		if m.editorManual != "" {
			// Manual instructions shown — just continue
			return m.proceedAfterEditor()
		}
		return m, configureEditorsCmd(m.editors)

	case screenTunnelSelect:
		provider := tunnelProviders[m.tunnelCursor]
		if provider.id == "custom" {
			m.screen = screenCustomURL
			m.textInput.Focus()
			return m, textinput.Blink
		}
		// Check if binary exists (skip for local and localhostrun which use built-in tools)
		if provider.id != "local" && provider.id != "localhostrun" {
			binary := providerBinary(provider.id)
			if binary != "" {
				if _, err := exec.LookPath(binary); err != nil {
					m.missingBinary = binary
					m.installHint = providerInstallHint(provider.id)
					m.screen = screenBinaryMissing
					return m, nil
				}
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
	// Shell wrapper setup (skip if already installed or unsupported shell)
	if !m.shellInstalled && m.shellInfo.Syntax != "unknown" && m.shellInfo.RCPath != "" {
		m.screen = screenShellSetup
		return m, nil
	}
	return m.proceedAfterShell()
}

func (m StartModel) proceedAfterShell() (tea.Model, tea.Cmd) {
	// Tmux restart needed after shell wrapper install/update
	if m.tmuxRestartNeeded {
		m.screen = screenTmuxRestart
		return m, nil
	}
	return m.proceedAfterTmuxRestart()
}

func (m StartModel) proceedAfterTmuxRestart() (tea.Model, tea.Cmd) {
	// Editor setup (skip if no editors found or all already configured)
	unconfigured := false
	for _, e := range m.editors {
		if !e.Configured {
			unconfigured = true
			break
		}
	}
	if unconfigured {
		m.screen = screenEditorSetup
		return m, nil
	}
	return m.proceedAfterEditor()
}

func (m StartModel) proceedAfterEditor() (tea.Model, tea.Cmd) {
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
	m.hooksOutdated = msg.hooksOutdated
	m.shellInfo = msg.shellInfo
	m.shellInstalled = msg.shellInstalled
	m.editors = msg.editors
	m.tunnelOK = msg.tunnelOK
	m.tunnelURL = msg.tunnelURL
	m.tunnelProv = msg.tunnelProv
	m.deviceCount = msg.deviceCount
	m.devices = msg.devices
	m.tmux = msg.tmux

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

	// Clear stale pairing QR (new token will regenerate it)
	m.pairingQR = ""
	m.pairingToken = ""
	m.setupURL = ""

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
			dnIn, _ := os.Open(os.DevNull)
			dnOut, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			dnErr, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			proc, startErr := os.StartProcess(exe, []string{exe, "daemon", "start"}, &os.ProcAttr{
				Dir:   "/",
				Env:   os.Environ(),
				Files: []*os.File{dnIn, dnOut, dnErr},
			})
			if dnIn != nil {
				dnIn.Close()
			}
			if dnOut != nil {
				dnOut.Close()
			}
			if dnErr != nil {
				dnErr.Close()
			}
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
		if result.hooksOK {
			result.hooksOutdated = daemon.HooksOutdated()
		}

		// Check shell wrapper
		result.shellInfo = daemon.DetectShell()
		result.shellInstalled = daemon.ShellWrapperInstalled(result.shellInfo)

		// Check editors
		result.editors = daemon.DetectEditors()

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

		// Check tmux status
		if h != nil && h.Tmux != nil {
			result.tmux = tmuxStatus{
				Installed:       h.Tmux.Installed,
				Version:         h.Tmux.Version,
				ServerRunning:   h.Tmux.ServerRunning,
				ResurrectPlugin: h.Tmux.ResurrectPlugin,
				ContinuumPlugin: h.Tmux.ContinuumPlugin,
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

func installShellWrapperCmd(info daemon.ShellInfo) tea.Cmd {
	return func() tea.Msg {
		err := daemon.InstallShellWrapper(info)
		if err != nil {
			return shellSetupDone{
				err:    err,
				manual: daemon.ManualShellInstructions(info, err),
			}
		}
		return shellSetupDone{installed: true}
	}
}

func configureEditorsCmd(editors []daemon.EditorInfo) tea.Cmd {
	return func() tea.Msg {
		// Find tmux path
		tmuxPath := findTmuxPath()

		results := make([]daemon.EditorSetupResult, len(editors))
		var manualParts []string

		for i, editor := range editors {
			results[i] = daemon.EditorSetupResult{Editor: editor}
			if editor.Configured {
				results[i].Success = true
				continue
			}
			err := daemon.ConfigureEditor(editor, tmuxPath)
			results[i].Success = err == nil
			results[i].Error = err
			if err != nil {
				manualParts = append(manualParts, daemon.ManualEditorInstructions(editor, tmuxPath, err))
			}
		}

		return editorSetupDone{
			results: results,
			manual:  strings.Join(manualParts, "\n"),
		}
	}
}

func restartTmuxCmd() tea.Cmd {
	return func() tea.Msg {
		tmuxPath := findTmuxPath()
		err := exec.Command(tmuxPath, "kill-server").Run()
		return tmuxRestartDone{err: err}
	}
}

func findTmuxPath() string {
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	// Common locations
	for _, p := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/usr/bin/tmux"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "tmux"
}

func hooksInstalled() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.claude/settings.json")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "/hooks/claude/permission")
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
	case "zrok":
		// zrok v2 installs as "zrok2"
		if _, err := exec.LookPath("zrok"); err == nil {
			return "zrok"
		}
		return "zrok2"
	case "localtunnel":
		return "lt"
	case "localhostrun":
		return "ssh"
	case "localxpose":
		return "loclx"
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
	case "zrok":
		return "brew install openziti/tap/zrok  (or zrok2: https://zrok.io)"
	case "localtunnel":
		return "npm install -g localtunnel  (or brew install localtunnel)"
	case "localhostrun":
		return "ssh is built-in — this should not happen"
	case "localxpose":
		return "npm install -g loclx  (or https://localxpose.io/download)"
	default:
		return ""
	}
}

// RunStart launches the bubbletea start TUI and subscribes to internal SSE
// for desktop notifications.
func RunStart(internalPort, publicPort int) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go subscribeDesktopNotifications(ctx, internalPort)

	m := NewStartModel(internalPort, publicPort)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
