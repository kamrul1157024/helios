// Package desktop owns the host's remote desktop: xrdp with an XFCE session,
// reached through a guacd that the daemon runs as its child. It installs what
// is missing, keeps guacd alive, finds the session's display, and hands the
// server a ready guacd connection for each viewer.
//
// See docs/specs/66-remote-desktop.md.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// State is the desktop's lifecycle state, as GET /api/desktop reports it.
type State string

const (
	StateMissing     State = "missing"
	StateInstalling  State = "installing"
	StateStarting    State = "starting"
	StateReady       State = "ready"
	StateFailed      State = "failed"
	StateUnsupported State = "unsupported"
)

// Setting keys, in the daemon's settings table.
const (
	SettingEnabled      = "desktop.enabled"
	SettingAgentDisplay = "desktop.agent_display"
	SettingDPI          = "desktop.dpi"
)

// MaxViewers is the cap on concurrent tunnels. Each viewer is one guacd
// connection, and with it one RDP connection, to the same session.
const MaxViewers = 4

// Status is the body of GET /api/desktop and of the desktop_state event.
type Status struct {
	State State `json:"state"`
	// Enabled mirrors desktop.enabled. A client hides the desktop entirely
	// while it is false, rather than offering an install that will refuse.
	Enabled        bool     `json:"enabled"`
	Missing        []string `json:"missing"`
	CanInstall     bool     `json:"can_install"`
	InstallCommand string   `json:"install_command"`
	Display        string   `json:"display"`
	DPI            int      `json:"dpi"`
	Viewers        int      `json:"viewers"`
	Error          string   `json:"error"`
	Steps          []Step   `json:"steps"`
}

// Errors the server maps to status codes.
var (
	ErrDisabled       = errors.New("Remote desktop is turned off on this host.")
	ErrBusy           = errors.New("the desktop install is already running")
	ErrNeedsRoot      = errors.New("Helios needs root once to install the desktop. Run: " + InstallCommand)
	ErrNotReady       = errors.New("desktop not ready")
	ErrTooManyViewers = errors.New("too many viewers")
	ErrBadDPI         = fmt.Errorf("dpi must be between %d and %d", MinDPI, MaxDPI)
)

// Settings is the slice of the daemon's settings store the desktop reads.
type Settings interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

// Config wires a Manager to the daemon.
type Config struct {
	HeliosDir string
	Settings  Settings
	// OnChange receives the status after every change, for the SSE event.
	OnChange func(Status)
	// Sys defaults to this machine with sudo -n.
	Sys System
	// ProcRoot defaults to /proc.
	ProcRoot string
}

// Manager is the desktop runtime of one daemon.
type Manager struct {
	cfg     Config
	inst    *Installer
	sup     *supervisor
	Tickets *Tickets
	uid     int

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	state      State
	errMsg     string
	missing    []string
	canInstall bool
	display    Display
	dpi        int
	viewers    int
	busy       bool
	steps      []Step
	lastCheck  time.Time
	// checkedEnabled is desktop.enabled as the last check saw it.
	checkedEnabled bool
	// sudoOK caches the last sudo check, taken at sudoCheckedAt.
	sudoOK        bool
	sudoCheckedAt time.Time
}

// NewManager builds the runtime. It does nothing to the machine until Start.
func NewManager(cfg Config) *Manager {
	if cfg.Sys == nil {
		cfg.Sys = NewHostSystem(false)
	}
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = "/proc"
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		cfg:     cfg,
		Tickets: NewTickets(),
		ctx:     ctx,
		cancel:  cancel,
		state:   StateMissing,
		uid:     os.Getuid(),
	}
	m.inst = NewInstaller(cfg.Sys, cfg.HeliosDir)
	m.inst.Start = m.startRuntime
	m.inst.Progress = m.onProgress
	m.steps = NewSteps(m.inst)
	m.sup = newSupervisor(m.inst.bundleDir(), filepath.Join(cfg.HeliosDir, "logs", "guacd.log"))
	if runtime.GOOS != "linux" {
		m.state = StateUnsupported
		m.errMsg = "Remote desktop needs a Linux host."
	}
	return m
}

// NewInstaller returns an Installer for the current user.
func NewInstaller(sys System, heliosDir string) *Installer {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	home, _ := os.UserHomeDir()
	return &Installer{Sys: sys, User: name, Home: home, HeliosDir: heliosDir}
}

// Start brings an installed desktop up in the background. Called once when
// the daemon starts.
func (m *Manager) Start() {
	go m.Refresh()
}

// Close stops guacd. The xrdp session stays: it belongs to the system.
func (m *Manager) Close() {
	m.cancel()
	m.sup.Stop()
}

// Enabled reports desktop.enabled. It defaults to on: nothing installs until
// someone asks for it, and agents get a DISPLAY only once the desktop is
// ready, so on costs nothing. An explicit false is the host's off switch.
func (m *Manager) Enabled() bool {
	return m.boolSetting(SettingEnabled, true)
}

// SetEnabled flips the off switch, looks at the machine again, and reports
// the new status to every client.
func (m *Manager) SetEnabled(enabled bool) error {
	if m.cfg.Settings == nil {
		return errors.New("no settings store")
	}
	if err := m.cfg.Settings.SetSetting(SettingEnabled, strconv.FormatBool(enabled)); err != nil {
		return fmt.Errorf("save %s: %w", SettingEnabled, err)
	}
	m.Refresh()
	m.notify()
	return nil
}

func (m *Manager) boolSetting(key string, def bool) bool {
	if m.cfg.Settings == nil {
		return def
	}
	raw, err := m.cfg.Settings.GetSetting(key)
	if err != nil || raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

// storedDPI is the DPI the host keeps for its session (D9), or 0 when none
// was ever set.
func (m *Manager) storedDPI() int {
	if m.cfg.Settings == nil {
		return 0
	}
	raw, err := m.cfg.Settings.GetSetting(SettingDPI)
	if err != nil || raw == "" {
		return 0
	}
	dpi, err := strconv.Atoi(raw)
	if err != nil || dpi < MinDPI || dpi > MaxDPI {
		return 0
	}
	return dpi
}

// Status returns the current status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	missing := m.missing
	if missing == nil {
		missing = []string{}
	}
	return Status{
		State:          m.state,
		Enabled:        m.Enabled(),
		Missing:        missing,
		CanInstall:     m.canInstall,
		InstallCommand: InstallCommand,
		Display:        m.display.Name,
		DPI:            m.dpi,
		Viewers:        m.viewers,
		Error:          m.errMsg,
		Steps:          append([]Step(nil), m.steps...),
	}
}

func (m *Manager) notify() {
	if m.cfg.OnChange != nil {
		m.cfg.OnChange(m.Status())
	}
}

func (m *Manager) onProgress(steps []Step) {
	m.mu.Lock()
	m.steps = steps
	m.mu.Unlock()
	m.notify()
}

// The checks behind Refresh ask dpkg, which is cheap once and wasteful on
// every poll, so they are spaced out — more widely while the desktop is
// turned off and nobody is waiting on it.
const (
	refreshInterval         = 2 * time.Second
	disabledRefreshInterval = time.Minute
	// sudoCheckInterval spaces out `sudo -n true`. Each failed attempt can
	// land in the auth log, and a client polling the status must not fill it.
	sudoCheckInterval = time.Minute
)

// Refresh looks at the machine again. An installed desktop that is not
// running is started; a ready one whose session has gone is started again.
func (m *Manager) Refresh() {
	m.mu.Lock()
	enabled := m.Enabled()
	interval := refreshInterval
	if !enabled {
		interval = disabledRefreshInterval
	}
	// Turning the setting on is a reason to look at once, whatever the clock.
	flipped := enabled != m.checkedEnabled
	if m.state == StateUnsupported || m.busy || (!flipped && time.Since(m.lastCheck) < interval) {
		m.mu.Unlock()
		return
	}
	m.lastCheck, m.checkedEnabled = time.Now(), enabled
	state := m.state
	m.mu.Unlock()

	if state == StateReady {
		if _, ok := findDisplay(m.cfg.ProcRoot, m.uid); ok && m.sup.Addr() != "" {
			return
		}
		log.Printf("desktop: session or guacd went away; starting again")
	}

	ctx := m.ctx
	missing := m.inst.Missing(ctx)
	canInstall := !m.inst.NeedsRoot(ctx) || m.canSudo(ctx)

	m.mu.Lock()
	m.missing, m.canInstall = missing, canInstall
	start := len(missing) == 0 && m.Enabled() && !m.busy && m.state != StateFailed
	if len(missing) > 0 && m.state != StateFailed {
		m.state = StateMissing
	}
	if start {
		m.busy = true
		m.state = StateStarting
	}
	m.mu.Unlock()
	m.notify()

	if start {
		go m.run(func(ctx context.Context) error { return m.startRuntime(ctx) })
	}
}

// canSudo is Sys.CanSudo, remembered for sudoCheckInterval.
func (m *Manager) canSudo(ctx context.Context) bool {
	m.mu.Lock()
	if time.Since(m.sudoCheckedAt) < sudoCheckInterval {
		ok := m.sudoOK
		m.mu.Unlock()
		return ok
	}
	m.mu.Unlock()
	ok := m.cfg.Sys.CanSudo(ctx)
	m.mu.Lock()
	m.sudoOK, m.sudoCheckedAt = ok, time.Now()
	m.mu.Unlock()
	return ok
}

// Install starts the install in the background (D4).
func (m *Manager) Install() error {
	if m.Status().State == StateUnsupported {
		return ErrNotReady
	}
	if !m.Enabled() {
		return ErrDisabled
	}
	ctx := m.ctx
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return ErrBusy
	}
	m.busy = true
	m.mu.Unlock()

	if m.inst.NeedsRoot(ctx) && !m.cfg.Sys.CanSudo(ctx) {
		m.mu.Lock()
		m.busy = false
		m.canInstall = false
		m.mu.Unlock()
		m.notify()
		return ErrNeedsRoot
	}

	m.mu.Lock()
	m.state = StateInstalling
	m.errMsg = ""
	m.canInstall = true
	m.mu.Unlock()
	m.notify()
	go m.run(m.inst.Run)
	return nil
}

// run executes fn with the busy flag held and records how it ended.
func (m *Manager) run(fn func(ctx context.Context) error) {
	err := fn(m.ctx)
	missing := m.inst.Missing(m.ctx)
	m.mu.Lock()
	m.busy = false
	m.missing = missing
	if err != nil {
		plain, detail := explain(err, "The desktop did not start.")
		m.state = StateFailed
		m.errMsg = plain
		log.Printf("desktop: %s %s", plain, detail)
	}
	m.mu.Unlock()
	m.notify()
}

// startRuntime is the daemon's half of step 6, and the whole of a start once
// the desktop is installed: a current guacd bundle, xrdp running, guacd
// running, a session with a display, and the host's DPI applied to it.
func (m *Manager) startRuntime(ctx context.Context) error {
	m.setState(StateStarting, "")

	// A new daemon may carry a new bundle hash; the replacement needs no
	// root, so it happens here rather than waiting for an install (D5).
	if done, err := m.inst.guacdDone(ctx); err != nil {
		return err
	} else if !done {
		m.sup.Stop()
		if err := m.inst.installGuacd(ctx); err != nil {
			return err
		}
	}
	if running, _ := m.inst.xrdpRunning(ctx); !running {
		if !m.cfg.Sys.CanSudo(ctx) {
			return fail("xrdp is not running, and starting it needs root. Run: "+InstallCommand, nil)
		}
		if _, err := m.cfg.Sys.Root(ctx, "systemctl", "start", "xrdp"); err != nil {
			journal, _ := m.cfg.Sys.Run(ctx, "journalctl", "-u", "xrdp", "-n", "5", "--no-pager")
			return fail("xrdp did not start.", fmt.Errorf("%w\n%s", err, journal))
		}
	}
	if err := m.sup.Start(ctx); err != nil {
		return fail("The desktop service did not start.", err)
	}

	d, ok := findDisplay(m.cfg.ProcRoot, m.uid)
	if !ok {
		var err error
		if d, err = warmSession(ctx, m.sup.Addr(), m.credentials(), m.cfg.ProcRoot, m.uid); err != nil {
			return fail(fmt.Sprintf("The desktop session did not start. A broken %s is the usual cause.",
				filepath.Join(m.inst.Home, ".xsession")), err)
		}
	}
	if !waitSessionBus(ctx, m.cfg.ProcRoot, m.uid, d, sessionBusTimeout) {
		log.Printf("desktop: no session bus on %s yet; dpi unknown", d.Name)
	}

	dpi := readDPI(ctx, m.cfg.ProcRoot, m.uid, d)
	if want := m.storedDPI(); want != 0 && want != dpi {
		if err := applyDPI(ctx, m.cfg.ProcRoot, m.uid, d, want); err != nil {
			log.Printf("desktop: apply stored dpi %d: %v", want, err)
		} else {
			dpi = want
		}
	}

	m.mu.Lock()
	m.display = d
	m.dpi = dpi
	m.state = StateReady
	m.errMsg = ""
	m.missing = []string{}
	m.mu.Unlock()
	log.Printf("desktop: ready on display %s (guacd %s)", d.Name, m.sup.Addr())
	m.notify()
	return nil
}

func (m *Manager) setState(s State, errMsg string) {
	m.mu.Lock()
	m.state = s
	m.errMsg = errMsg
	m.mu.Unlock()
	m.notify()
}

func (m *Manager) credentials() Credentials {
	return Credentials{Username: m.inst.User, Password: m.inst.readSecret()}
}

// OpenViewer reserves a viewer slot and returns a guacd connection past its
// handshake. The caller must call release when the viewer leaves.
func (m *Manager) OpenViewer(ctx context.Context, size Size) (*GuacConn, func(), error) {
	m.mu.Lock()
	if m.state != StateReady {
		m.mu.Unlock()
		return nil, nil, ErrNotReady
	}
	if m.viewers >= MaxViewers {
		m.mu.Unlock()
		return nil, nil, ErrTooManyViewers
	}
	m.viewers++
	m.mu.Unlock()
	m.notify()

	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			m.viewers--
			m.mu.Unlock()
			m.notify()
		})
	}
	conn, err := DialGuacd(ctx, m.sup.Addr(), size, m.credentials(), hostTimezone())
	if err != nil {
		release()
		return nil, nil, err
	}
	return conn, release, nil
}

// SetDPI changes the session's DPI live and keeps it as the host's choice.
func (m *Manager) SetDPI(ctx context.Context, dpi int) (int, error) {
	if dpi < MinDPI || dpi > MaxDPI {
		return 0, ErrBadDPI
	}
	m.mu.Lock()
	ready, d := m.state == StateReady, m.display
	m.mu.Unlock()
	if !ready {
		return 0, ErrNotReady
	}
	if err := applyDPI(ctx, m.cfg.ProcRoot, m.uid, d, dpi); err != nil {
		return 0, err
	}
	if m.cfg.Settings != nil {
		if err := m.cfg.Settings.SetSetting(SettingDPI, strconv.Itoa(dpi)); err != nil {
			return 0, fmt.Errorf("save dpi: %w", err)
		}
	}
	m.mu.Lock()
	m.dpi = dpi
	m.mu.Unlock()
	m.notify()
	return dpi, nil
}

// AgentEnv is what a new agent session adds to its environment so its GUI
// apps draw on the desktop (D6). Empty unless the desktop is ready, the host
// has not turned it off, and desktop.agent_display is on, so agents behave as
// before otherwise.
func (m *Manager) AgentEnv() map[string]string {
	m.mu.Lock()
	ready, d := m.state == StateReady, m.display
	m.mu.Unlock()
	if !ready || !m.Enabled() || !m.boolSetting(SettingAgentDisplay, true) {
		return nil
	}
	env := map[string]string{"DISPLAY": d.Name}
	if d.XAuthority != "" {
		env["XAUTHORITY"] = d.XAuthority
	}
	return env
}
