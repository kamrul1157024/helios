package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/tailscale"

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
	screenMCPSetup                     // prompt to register the Helios MCP server
	screenTunnelSelect                 // first time only: pick tunnel provider
	screenBinaryMissing                // tunnel binary not found
	screenTunnelStarting               // starting tunnel...
	screenCustomURL                    // custom URL input
	screenMain                         // main dashboard: status + devices + QRs
	screenConfirmDevice                // "Allow this device? y/n"
	screenSettings                     // general settings (auto title, etc.)
	screenError                        // error
)

// Tunnel provider options.
//
// Tailscale appears twice because its two exposure modes are genuinely
// different products from the user's point of view: Serve is private to the
// tailnet and needs the VPN switched on at the other end, Funnel is public and
// does not. Presenting them as one entry plus a hidden setting would hide the
// only question that actually matters here.
//
// Serve leads the list and carries the recommendation. This screen is the
// first-run question: it is shown when no provider is configured, so putting
// Serve first with its caveat spelled out is the prompt, not a silent default.
var tunnelProviders = []struct {
	id     string
	mode   string // tailscale exposure mode; empty for every other provider
	label  string
	detail string
}{
	{
		id: "tailscale", mode: "serve",
		label:  "Tailscale Serve (recommended)",
		detail: "Private to your tailnet. Needs the Tailscale VPN switched ON on your phone.",
	},
	{
		id: "tailscale", mode: "funnel",
		label:  "Tailscale Funnel",
		detail: "Public, but with a stable hostname. No Tailscale needed on the phone.",
	},
	{id: "cloudflare", label: "Cloudflare Tunnel", detail: "Public. URL changes on every restart."},
	{id: "zrok", label: "zrok (open-source, stable URLs)"},
	{id: "ngrok", label: "ngrok"},
	{id: "localtunnel", label: "localtunnel (zero signup)"},
	{id: "localhostrun", label: "localhost.run (no install — uses SSH)"},
	{id: "localxpose", label: "localxpose (regional, reserved domains)"},
	{id: "local", label: "Local Network (no HTTPS)"},
	{id: "custom", label: "Custom URL"},
}

// Messages
type statusCheckDone struct {
	daemonOK       bool
	hooksOK        bool
	hooksOutdated  bool
	mcpRegistered  bool
	mcpDeclined    bool
	shellInfo      daemon.ShellInfo
	shellInstalled bool
	tunnelOK       bool
	tunnelURL      string
	tunnelProv     string
	deviceCount    int
	devices        []deviceInfo
	err            error
}

type mcpRegisterDone struct {
	err error
}

type tunnelStarted struct {
	url  string
	warn string
	err  error
}

// tailscaleDetected carries the result of a readiness probe. Detection failures
// are not reported separately: a failed probe is indistinguishable from "not
// ready" as far as the picker is concerned, and State.Problem already says
// which prerequisite is missing.
type tailscaleDetected struct {
	state tailscale.State
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

type tickMsg time.Time
type tokenTickMsg time.Time

type generalSettingsLoaded struct {
	values map[string]bool
	err    error
}

type generalSettingSaved struct {
	err error
}

// Model
type StartModel struct {
	screen       screen
	client       *client
	spinner      spinner.Model
	textInput    textinput.Model
	publicPort   int
	internalPort int

	// Status check results
	daemonOK      bool
	hooksOK       bool
	hooksOutdated bool
	// mcpRegistered reports whether Claude Code knows about the Helios MCP
	// server. Unregistered is a suggestion, never a blocker: the explain panel
	// is one feature, and a user who does not want it should not be nagged past
	// a single line.
	mcpRegistered bool
	// mcpDeclined records that the user said no during setup. Asking again on
	// every launch would make an optional feature feel mandatory.
	mcpDeclined bool
	mcpMsg      string
	tunnelOK    bool
	tunnelURL   string
	tunnelProv  string
	tunnelMode  string // tailscale exposure mode of the running tunnel; empty otherwise
	tunnelWarn  string // daemon-reported caveat about the tunnel just started
	deviceCount int
	devices     []deviceInfo

	// General settings screen
	settingsCursor int
	settingsValues map[string]bool

	// Shell setup
	shellInfo      daemon.ShellInfo
	shellInstalled bool
	shellManual    string // non-empty if auto-install failed

	// Tunnel selection
	tunnelCursor int

	// Tailscale readiness, refreshed each time the picker is opened. Detection
	// shells out, so the picker renders immediately and fills the status column
	// when the result arrives.
	tsState   tailscale.State
	tsChecked bool

	// Provider-unavailable info. For most providers this is a missing binary;
	// for Tailscale it is whichever prerequisite is unmet, which is why the
	// message is carried rather than derived from the binary name.
	missingBinary   string
	installHint     string
	providerProblem string

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
		screen:       screenLoading,
		client:       newClient(internalPort),
		spinner:      s,
		textInput:    ti,
		publicPort:   publicPort,
		internalPort: internalPort,
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

	case tailscaleDetected:
		m.tsState = msg.state
		m.tsChecked = true
		return m, nil

	case deviceCreated:
		return m.handleDeviceCreated(msg)

	case devicePollResult:
		return m.handleDevicePoll(msg)

	case mcpRegisterDone:
		if msg.err != nil {
			m.mcpMsg = fmt.Sprintf("Could not register: %v", msg.err)
			// Setup must not dead-end on a failure it cannot fix; the dashboard
			// still offers the key.
			if m.screen == screenMCPSetup {
				return m.proceedAfterMCP()
			}
			return m, nil
		}
		m.mcpRegistered = true
		m.mcpMsg = ""
		if m.screen == screenMCPSetup {
			return m.proceedAfterMCP()
		}
		return m, nil

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
		}
		return m.proceedAfterShell()

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

	case generalSettingsLoaded:
		if msg.err == nil && msg.values != nil {
			m.settingsValues = msg.values
		}
		return m, nil

	case generalSettingSaved:
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
		case screenLoading, screenMain:
			return m, tea.Quit
		case screenTunnelSelect, screenBinaryMissing, screenCustomURL:
			// If tunnel is already active, go back to main instead of quitting
			if m.tunnelOK {
				m.screen = screenMain
				return m, nil
			}
			return m, tea.Quit
		case screenHooksInstall, screenHooksUpdate, screenShellSetup, screenMCPSetup, screenError:
			return m, tea.Quit
		case screenSettings:
			m.screen = screenMain
			return m, nil
		}

	case "up", "k":
		if m.screen == screenTunnelSelect {
			if m.tunnelCursor > 0 {
				m.tunnelCursor--
			}
		}
		if m.screen == screenSettings {
			if m.settingsCursor > 0 {
				m.settingsCursor--
			}
		}

	case "down", "j":
		if m.screen == screenTunnelSelect {
			if m.tunnelCursor < len(tunnelProviders)-1 {
				m.tunnelCursor++
			}
		}
		if m.screen == screenSettings {
			if m.settingsCursor < len(generalSettingsKeys)-1 {
				m.settingsCursor++
			}
		}

	case " ":
		if m.screen == screenSettings {
			return m.toggleGeneralSetting()
		}

	case "enter":
		if m.screen == screenSettings {
			return m.toggleGeneralSetting()
		}
		return m.handleEnter()

	case "r":
		if m.screen == screenSettings {
			return m.resetGeneralSettings()
		}

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

	case "t":
		if m.screen == screenMain || (m.screen == screenLoading && m.daemonOK) {
			return m.enterTunnelSelect()
		}

	case "s":
		if m.screen == screenMain {
			m.screen = screenSettings
			m.settingsCursor = 0
			return m, loadGeneralSettings(m.client)
		}

	case "m":
		// The summary screen is where this is usually read: a set-up install
		// sits there waiting on enter, and never reaches the dashboard.
		if (m.screen == screenMain || (m.screen == screenLoading && m.daemonOK)) && !m.mcpRegistered {
			m.mcpDeclined = false
			m.mcpMsg = "Registering..."
			return m, registerMCPCmd(m.internalPort)
		}

	case "tab":
		switch m.screen {
		case screenHooksInstall, screenHooksUpdate:
			return m.proceedAfterHooks()
		case screenShellSetup:
			return m.proceedAfterShell()
		case screenMCPSetup:
			return m.declineMCP()
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

	case screenMCPSetup:
		m.mcpMsg = "Registering..."
		return m, registerMCPCmd(m.internalPort)

	case screenTunnelSelect:
		provider := tunnelProviders[m.tunnelCursor]
		if provider.id == "custom" {
			m.screen = screenCustomURL
			m.textInput.Focus()
			return m, textinput.Blink
		}
		if provider.id == "tailscale" && !m.tsChecked {
			// The probe is still in flight; the picker shows "checking…" next
			// to the entry, so ignoring the keypress is the honest response.
			return m, nil
		}
		if problem := m.providerUnavailable(provider.id, provider.mode); problem != "" {
			m.missingBinary = providerBinary(provider.id)
			m.installHint = providerInstallHint(provider.id)
			m.providerProblem = problem
			m.screen = screenBinaryMissing
			return m, nil
		}
		m.screen = screenTunnelStarting
		return m, tea.Batch(m.spinner.Tick,
			startTunnel(m.client, provider.id, "", m.publicPort, provider.mode))

	case screenCustomURL:
		url := m.textInput.Value()
		if url == "" {
			return m, nil
		}
		m.screen = screenTunnelStarting
		return m, tea.Batch(m.spinner.Tick, startTunnel(m.client, "custom", url, m.publicPort, ""))

	case screenBinaryMissing:
		// Retry — the user has had a chance to install or fix the prerequisite.
		// Tailscale's remedies are not detectable synchronously, so the retry
		// re-probes and returns to the picker rather than blocking here.
		provider := tunnelProviders[m.tunnelCursor]
		if provider.id == "tailscale" {
			return m.enterTunnelSelect()
		}
		if problem := m.providerUnavailable(provider.id, provider.mode); problem != "" {
			return m, nil // Still missing
		}
		m.screen = screenTunnelStarting
		return m, tea.Batch(m.spinner.Tick,
			startTunnel(m.client, provider.id, "", m.publicPort, provider.mode))

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
	// Asked once, and only once. Registering gives agents tools they did not
	// have, which is the user's call rather than a step to get past.
	if !m.mcpRegistered && !m.mcpDeclined {
		m.screen = screenMCPSetup
		return m, nil
	}
	return m.proceedAfterMCP()
}

func (m StartModel) proceedAfterMCP() (tea.Model, tea.Cmd) {
	if !m.tunnelOK {
		return m.enterTunnelSelect()
	}
	m.screen = screenMain
	return m, tea.Batch(m.spinner.Tick, createDevice(m.client))
}

// enterTunnelSelect opens the provider picker and re-probes Tailscale. The
// probe is re-run on every entry rather than cached for the process lifetime,
// because the common remedies — logging in, starting the app — happen while
// this screen is on screen.
func (m StartModel) enterTunnelSelect() (tea.Model, tea.Cmd) {
	m.screen = screenTunnelSelect
	m.tsChecked = false
	m.tsState = tailscale.State{}
	return m, detectTailscale()
}

func (m StartModel) handleStatusCheck(msg statusCheckDone) (tea.Model, tea.Cmd) {
	m.daemonOK = msg.daemonOK
	m.hooksOK = msg.hooksOK
	m.hooksOutdated = msg.hooksOutdated
	m.shellInfo = msg.shellInfo
	m.shellInstalled = msg.shellInstalled
	m.mcpRegistered = msg.mcpRegistered
	m.mcpDeclined = msg.mcpDeclined
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
	m.tunnelWarn = msg.warn
	m.tunnelProv = tunnelProviders[m.tunnelCursor].id
	m.tunnelMode = tunnelProviders[m.tunnelCursor].mode

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

		result.mcpRegistered = mcpRegistered()
		if settings, err := c.getSettings(); err == nil {
			result.mcpDeclined = settings[mcpDeclinedSetting] == "true"
		}

		// Check shell wrapper
		result.shellInfo = daemon.DetectShell()
		result.shellInstalled = daemon.ShellWrapperInstalled(result.shellInfo)

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

func startTunnel(c *client, provider, customURL string, localPort int, tailscaleMode string) tea.Cmd {
	return func() tea.Msg {
		resp, err := c.tunnelStart(provider, customURL, localPort, tailscaleMode)
		if err != nil {
			return tunnelStarted{err: err}
		}
		warn := ""
		if resp.RestartRequired {
			warn = resp.Message
			if warn == "" {
				warn = "restart the daemon for this tunnel to be reachable"
			}
		}
		return tunnelStarted{url: resp.PublicURL, warn: warn}
	}
}

// detectTailscale probes Tailscale readiness off the UI thread. It shells out
// to the CLI, which can block on a slow or unresponsive tailscaled, so the
// picker never waits on it.
func detectTailscale() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		state, _ := tailscale.Detect(ctx)
		return tailscaleDetected{state: state}
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

// mcpDeclinedSetting remembers that the user said no during setup, so the
// question is asked once rather than at every launch.
const mcpDeclinedSetting = "mcp.setup_declined"

// declineMCP records the refusal and moves on. Persisted through the daemon so
// every client agrees, and so a reinstall does not start asking again.
func (m StartModel) declineMCP() (tea.Model, tea.Cmd) {
	m.mcpDeclined = true
	client := m.client
	model, cmd := m.proceedAfterMCP()
	return model, tea.Batch(cmd, func() tea.Msg {
		client.updateSettings(map[string]string{mcpDeclinedSetting: "true"})
		return nil
	})
}

// mcpRegistered reports whether Claude Code has the Helios MCP server in its
// user-scope config. `claude mcp add --scope user` writes it to the top-level
// mcpServers map in ~/.claude.json.
func mcpRegistered() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}
	var config struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return false
	}
	_, ok := config.MCPServers["helios"]
	return ok
}

// registerMCPCmd adds the Helios MCP server to Claude Code. Registration is
// offered, never performed on the user's behalf: an agent gaining a new set of
// tools is their call to make.
func registerMCPCmd(internalPort int) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://127.0.0.1:%d/mcp", internalPort)
		cmd := exec.Command("claude", "mcp", "add",
			"--transport", "http", "--scope", "user", "helios", url)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return mcpRegisterDone{err: fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))}
		}
		return mcpRegisterDone{}
	}
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

// providerUnavailable reports why a provider cannot be started right now, or ""
// when it can. Most providers need nothing but a binary on $PATH; Tailscale
// needs a running, logged-in daemon as well, and Funnel needs certificates on
// top of that — so the answer depends on the mode, not just the provider id.
//
// A Tailscale answer is only meaningful once tsChecked is set; callers must not
// treat "" from an unprobed state as readiness.
func (m StartModel) providerUnavailable(provider, mode string) string {
	if provider == "tailscale" {
		if mode == string(tailscale.ModeFunnel) {
			return m.tsState.FunnelProblem()
		}
		return m.tsState.Problem()
	}

	// local and localhostrun use tools that are always present.
	if provider == "local" || provider == "localhostrun" {
		return ""
	}

	binary := providerBinary(provider)
	if binary == "" {
		return ""
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Sprintf("%s not found on your PATH", binary)
	}
	return ""
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

// RunStart launches the bubbletea start TUI.
func RunStart(internalPort, publicPort int) error {
	m := NewStartModel(internalPort, publicPort)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
