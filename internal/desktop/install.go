package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// StepState is where one install step stands.
type StepState string

const (
	StepPending StepState = "pending"
	StepRunning StepState = "running"
	StepDone    StepState = "done"
	StepSkipped StepState = "skipped"
	StepFailed  StepState = "failed"
)

// Step is one install step as clients see it.
type Step struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	State StepState `json:"state"`
	// Line is the last line the step printed, for a progress view that wants
	// to show something moving.
	Line string `json:"line"`
}

// InstallCommand is what a person runs when the daemon cannot get root.
const InstallCommand = "helios setup desktop"

// Paths the install edits. Each is a constant: no request names a file.
const (
	xrdpINI     = "/etc/xrdp/xrdp.ini"
	sesmanINI   = "/etc/xrdp/sesman.ini"
	polkitDir   = "/etc/polkit-1/rules.d"
	backupExt   = ".helios-orig"
	xsessionTag = "# Written by Helios"
)

// iniEdit is one key Helios owns in an xrdp config file.
type iniEdit struct {
	path, section, key, value string
}

// xrdpEdits are the keys of step 2. xrdp listens on loopback only, so the
// daemon is the one way in; sesman keeps a session when its viewer leaves
// (KillDisconnected=false) and gives it back to a viewer of any size
// (Policy=UB matches on user and colour depth, not on screen size).
var xrdpEdits = []iniEdit{
	{xrdpINI, "Globals", "port", "tcp://127.0.0.1:3389"},
	{sesmanINI, "Sessions", "KillDisconnected", "false"},
	{sesmanINI, "Sessions", "Policy", "UB"},
}

// basePackages are what xrdp needs whatever the session; desktopPackages are
// the XFCE session itself, installed only when the user has none (D4).
var (
	basePackages    = []string{"xrdp", "xorgxrdp", "dbus-x11"}
	desktopPackages = []string{"xfce4", "xfce4-terminal"}
)

// errAptLocked is the failure for an apt that another install holds.
var errAptLocked = errors.New("another install is running. Try again when it ends")

// validUser keeps the user name safe to put in a file name and in the polkit
// rule. It comes from the OS, not from a request, but the rule is code.
var validUser = regexp.MustCompile(`^[a-z_][a-z0-9_.-]*$`)

// Installer runs the six steps of D4. The daemon and `helios setup desktop`
// use the same one; they differ only in their System (sudo -n or a sudo that
// may ask) and in Start, which only the daemon has.
type Installer struct {
	Sys       System
	User      string
	Home      string
	HeliosDir string
	Version   string
	// Start runs the part of step 6 that belongs to the daemon: guacd and the
	// warm-up. Nil in the CLI, which leaves both to the daemon.
	Start func(ctx context.Context) error
	// Progress receives the steps after every change.
	Progress func([]Step)

	mu            sync.Mutex
	steps         []Step
	current       int
	configChanged bool
	lastReport    time.Time
}

type stepDef struct {
	id, label string
	// check reports whether the step's work is already done.
	check func(ctx context.Context) (bool, error)
	// skip reports whether the step does not apply to this machine.
	skip  func() bool
	apply func(ctx context.Context) error
}

func (in *Installer) defs() []stepDef {
	return []stepDef{
		{id: "packages", label: "Install xrdp and XFCE", check: in.packagesDone, apply: in.installPackages},
		{id: "xrdp-config", label: "Configure xrdp", check: in.xrdpConfigDone, apply: in.configureXRDP},
		{id: "pam", label: "Set up Helios login", check: in.pamDone, apply: in.installPAM},
		{id: "session", label: "Choose the desktop session", check: in.sessionDone, skip: in.otherSession, apply: in.writeSession},
		{id: "guacd", label: "Get the desktop service", check: in.guacdDone, apply: in.installGuacd},
		{id: "start", label: "Start the desktop", check: in.startDone, apply: in.start},
	}
}

// NewSteps returns the step list with every step pending.
func NewSteps(in *Installer) []Step {
	var steps []Step
	for _, d := range in.defs() {
		steps = append(steps, Step{ID: d.id, Label: d.label, State: StepPending})
	}
	return steps
}

// Steps returns a copy of the current step list.
func (in *Installer) Steps() []Step {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]Step(nil), in.steps...)
}

// lineInterval spaces out progress reports that only change a step's line.
// apt prints hundreds of lines, and each report is an SSE event to every
// client; a state change is always reported at once.
const lineInterval = 500 * time.Millisecond

func (in *Installer) setStep(i int, state StepState, line string) {
	in.mu.Lock()
	changed := in.steps[i].State != state
	in.steps[i].State = state
	if line != "" || state == StepRunning {
		in.steps[i].Line = line
	}
	if !changed && time.Since(in.lastReport) < lineInterval {
		in.mu.Unlock()
		return
	}
	in.lastReport = time.Now()
	steps := append([]Step(nil), in.steps...)
	in.mu.Unlock()
	if in.Progress != nil {
		in.Progress(steps)
	}
}

// line shows a line of output on the running step.
func (in *Installer) line(text string) {
	in.mu.Lock()
	i := in.current
	in.mu.Unlock()
	in.setStep(i, StepRunning, text)
}

// Run walks the steps in order. A step whose work is done passes at once, so
// an install that failed halfway can simply run again.
func (in *Installer) Run(ctx context.Context) error {
	if !validUser.MatchString(in.User) {
		return fmt.Errorf("user name %q is not one Helios can configure", in.User)
	}
	defs := in.defs()
	in.mu.Lock()
	in.steps = NewSteps(in)
	in.configChanged = false
	in.mu.Unlock()

	for i, d := range defs {
		in.setStep(i, StepRunning, "")
		if d.skip != nil && d.skip() {
			in.setStep(i, StepSkipped, "")
			continue
		}
		done, err := d.check(ctx)
		if err != nil {
			in.setStep(i, StepFailed, err.Error())
			return fmt.Errorf("%s: %w", d.label, err)
		}
		if done && d.id != "start" {
			in.setStep(i, StepDone, "")
			continue
		}
		in.mu.Lock()
		in.current = i
		in.mu.Unlock()
		in.Sys.SetOutput(in.line)
		err = d.apply(ctx)
		in.Sys.SetOutput(nil)
		if err != nil {
			in.setStep(i, StepFailed, err.Error())
			return fmt.Errorf("%s: %w", d.label, err)
		}
		in.setStep(i, StepDone, "")
	}
	return nil
}

// NeedsRoot reports whether any step that needs root still has work to do.
// When it does and sudo -n fails, the daemon stops before step 1 (D4).
func (in *Installer) NeedsRoot(ctx context.Context) bool {
	for _, check := range []func(context.Context) (bool, error){in.packagesDone, in.xrdpConfigDone, in.pamDone, in.xrdpRunning} {
		if done, err := check(ctx); err != nil || !done {
			return true
		}
	}
	return false
}

// Missing names what the machine lacks, in the words GET /api/desktop uses.
func (in *Installer) Missing(ctx context.Context) []string {
	missing := []string{}
	pkgs := in.missingPackages(ctx, basePackages)
	cfg, _ := in.xrdpConfigDone(ctx)
	pam, _ := in.pamDone(ctx)
	if len(pkgs) > 0 || !cfg || !pam {
		missing = append(missing, "xrdp")
	}
	if !in.otherSession() && len(in.missingPackages(ctx, desktopPackages)) > 0 {
		missing = append(missing, "xfce")
	}
	if ok, _ := in.guacdDone(ctx); !ok {
		missing = append(missing, "guacd")
	}
	return missing
}

// ── Step 1: packages ──

func (in *Installer) wantedPackages() []string {
	if in.otherSession() {
		return basePackages
	}
	return append(append([]string(nil), basePackages...), desktopPackages...)
}

func (in *Installer) packagesDone(ctx context.Context) (bool, error) {
	return len(in.missingPackages(ctx, in.wantedPackages())) == 0, nil
}

// missingPackages asks dpkg which of pkgs are not installed. dpkg-query exits
// non-zero when it has never heard of a package, which here just means
// "missing", so its exit status is not an error.
func (in *Installer) missingPackages(ctx context.Context, pkgs []string) []string {
	args := append([]string{"dpkg-query", "-W", "-f", "${Package} ${db:Status-Status}\n"}, pkgs...)
	out, _ := in.Sys.Run(ctx, args...)
	installed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == "installed" {
			installed[f[0]] = true
		}
	}
	var missing []string
	for _, p := range pkgs {
		if !installed[p] {
			missing = append(missing, p)
		}
	}
	return missing
}

func (in *Installer) installPackages(ctx context.Context) error {
	// The lock timeout is the "waits up to 2 minutes" of the failure table;
	// DEBIAN_FRONTEND goes in through env because sudo drops the caller's.
	apt := []string{"env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-q", "-o", "DPkg::Lock::Timeout=120"}
	if out, err := in.Sys.Root(ctx, append(apt, "update")...); err != nil {
		return aptError(out, err)
	}
	args := append(append(apt, "install", "-y", "--no-install-recommends"), in.wantedPackages()...)
	if out, err := in.Sys.Root(ctx, args...); err != nil {
		return aptError(out, err)
	}
	return nil
}

func aptError(out string, err error) error {
	if strings.Contains(out, "Could not get lock") || strings.Contains(out, "Unable to acquire the dpkg frontend lock") {
		return errAptLocked
	}
	return fmt.Errorf("%w: %s", err, lastLine(out))
}

// ── Step 2: xrdp config ──

func (in *Installer) polkitPath() string {
	return filepath.Join(polkitDir, "45-helios-desktop-"+in.User+".rules")
}

// polkitRule stops the "Authentication is required to create a color
// profile" prompts colord raises in every new xrdp session, for this user only.
func (in *Installer) polkitRule() string {
	return `// Installed by Helios (helios setup desktop) for the remote desktop of ` + in.User + `.
// colord otherwise asks for an admin password each time an xrdp session starts.
polkit.addRule(function(action, subject) {
    if (action.id.indexOf("org.freedesktop.color-manager.") === 0 &&
        subject.user === "` + in.User + `") {
        return polkit.Result.YES;
    }
});
`
}

func (in *Installer) xrdpConfigDone(ctx context.Context) (bool, error) {
	for _, e := range xrdpEdits {
		raw, err := in.Sys.ReadFile(e.path)
		if err != nil {
			return false, nil
		}
		if v, ok := iniValue(string(raw), e.section, e.key); !ok || v != e.value {
			return false, nil
		}
	}
	raw, err := in.Sys.ReadFile(in.polkitPath())
	return err == nil && string(raw) == in.polkitRule(), nil
}

func (in *Installer) configureXRDP(ctx context.Context) error {
	edited := map[string]string{}
	original := map[string]string{}
	for _, e := range xrdpEdits {
		if _, ok := edited[e.path]; !ok {
			raw, err := in.Sys.ReadFile(e.path)
			if err != nil {
				return fmt.Errorf("xrdp is not installed: %w", err)
			}
			original[e.path] = string(raw)
			edited[e.path] = string(raw)
		}
		edited[e.path] = setINIKey(edited[e.path], e.section, e.key, e.value)
	}
	for _, path := range []string{xrdpINI, sesmanINI} {
		if edited[path] == original[path] {
			continue
		}
		// The first install keeps the file as the package left it, which is
		// what --uninstall puts the keys back to.
		if !fileExists(in.Sys, path+backupExt) {
			if err := in.Sys.WriteRootFile(ctx, path+backupExt, []byte(original[path]), 0o644); err != nil {
				return err
			}
		}
		if err := in.Sys.WriteRootFile(ctx, path, []byte(edited[path]), 0o644); err != nil {
			return err
		}
	}
	if err := in.Sys.WriteRootFile(ctx, in.polkitPath(), []byte(in.polkitRule()), 0o644); err != nil {
		return err
	}
	in.mu.Lock()
	in.configChanged = true
	in.mu.Unlock()
	return nil
}

// ── Step 3: PAM ──

func (in *Installer) secretPath() string {
	return filepath.Join(in.HeliosDir, "desktop", "secret")
}

func (in *Installer) hashPath() string {
	return filepath.Join(hashDir, in.User+".sha256")
}

// readSecret returns the daemon's xrdp secret, or "" when there is none yet.
func (in *Installer) readSecret() string {
	raw, err := in.Sys.ReadFile(in.secretPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (in *Installer) pamDone(ctx context.Context) (bool, error) {
	secret := in.readSecret()
	if secret == "" {
		return false, nil
	}
	helper, err := in.Sys.ReadFile(helperPath)
	if err != nil || string(helper) != helperScript {
		return false, nil
	}
	hash, err := in.Sys.ReadFile(in.hashPath())
	if err != nil || string(hash) != hashFileContent(secret) {
		return false, nil
	}
	pam, err := in.Sys.ReadFile(pamPath)
	return err == nil && hasPAMLine(string(pam)), nil
}

func (in *Installer) installPAM(ctx context.Context) error {
	secret := in.readSecret()
	if secret == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("make secret: %w", err)
		}
		secret = hex.EncodeToString(raw)
		if err := in.Sys.WriteFile(in.secretPath(), []byte(secret+"\n"), 0o600); err != nil {
			return err
		}
	}
	if err := in.Sys.WriteRootFile(ctx, helperPath, []byte(helperScript), 0o755); err != nil {
		return err
	}
	if err := in.Sys.WriteRootFile(ctx, in.hashPath(), []byte(hashFileContent(secret)), 0o644); err != nil {
		return err
	}
	pam, err := in.Sys.ReadFile(pamPath)
	if err != nil {
		return fmt.Errorf("xrdp's PAM file is missing: %w", err)
	}
	if updated := insertPAMLine(string(pam)); updated != string(pam) {
		return in.Sys.WriteRootFile(ctx, pamPath, []byte(updated), 0o644)
	}
	return nil
}

// ── Step 4: session ──

func (in *Installer) xsessionPath() string {
	return filepath.Join(in.Home, ".xsession")
}

const xsessionContent = xsessionTag + ` (helios setup desktop): the remote desktop runs XFCE.
# Replace this file to run another session; Helios never overwrites it.
exec startxfce4
`

// otherSession reports a ~/.xsession that Helios did not write: the user
// already chose a session for xrdp, and the install leaves it alone.
func (in *Installer) otherSession() bool {
	raw, err := in.Sys.ReadFile(in.xsessionPath())
	if err != nil {
		return fileExists(in.Sys, in.xsessionPath())
	}
	return !strings.HasPrefix(string(raw), xsessionTag)
}

func (in *Installer) sessionDone(ctx context.Context) (bool, error) {
	return fileExists(in.Sys, in.xsessionPath()), nil
}

func (in *Installer) writeSession(ctx context.Context) error {
	return in.Sys.WriteFile(in.xsessionPath(), []byte(xsessionContent), 0o755)
}

// ── Step 5: guacd ──

func (in *Installer) bundleDir() string {
	return filepath.Join(in.HeliosDir, "desktop", "guacd")
}

func (in *Installer) guacdDone(ctx context.Context) (bool, error) {
	src, err := resolveBundle(in.Version)
	if err != nil {
		return false, err
	}
	want, err := expectedBundleHash(src)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errBundleUnavailable, err)
	}
	return installedBundleHash(in.bundleDir()) == want, nil
}

func (in *Installer) installGuacd(ctx context.Context) error {
	src, err := resolveBundle(in.Version)
	if err != nil {
		return err
	}
	return installBundle(ctx, src, in.bundleDir(), in.line)
}

// ── Step 6: start ──

func (in *Installer) xrdpRunning(ctx context.Context) (bool, error) {
	if _, err := in.Sys.Run(ctx, "systemctl", "is-active", "--quiet", "xrdp"); err != nil {
		return false, nil
	}
	_, err := in.Sys.Run(ctx, "systemctl", "is-enabled", "--quiet", "xrdp")
	return err == nil, nil
}

func (in *Installer) startDone(ctx context.Context) (bool, error) {
	in.mu.Lock()
	changed := in.configChanged
	in.mu.Unlock()
	running, err := in.xrdpRunning(ctx)
	return running && !changed, err
}

func (in *Installer) start(ctx context.Context) error {
	if done, _ := in.startDone(ctx); !done {
		if _, err := in.Sys.Root(ctx, "systemctl", "enable", "xrdp", "xrdp-sesman"); err != nil {
			return err
		}
		// A restart, not a start: step 2 may have just changed the port, and
		// a running xrdp only reads it at start.
		if out, err := in.Sys.Root(ctx, "systemctl", "restart", "xrdp-sesman", "xrdp"); err != nil {
			journal, _ := in.Sys.Run(ctx, "journalctl", "-u", "xrdp", "-n", "5", "--no-pager")
			return fmt.Errorf("xrdp did not start: %w\n%s%s", err, out, journal)
		}
		in.mu.Lock()
		in.configChanged = false
		in.mu.Unlock()
	}
	if in.Start == nil {
		return nil
	}
	return in.Start(ctx)
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Uninstall reverts what the install added — the PAM line, the helper, the
// hash file, the xrdp config keys, the polkit rule, the secret and the guacd
// bundle — and leaves the apt packages and ~/.xsession in place (D4). It
// returns the packages it left, so the caller can say so.
func (in *Installer) Uninstall(ctx context.Context) ([]string, error) {
	if !validUser.MatchString(in.User) {
		return nil, fmt.Errorf("user name %q is not one Helios can configure", in.User)
	}
	if pam, err := in.Sys.ReadFile(pamPath); err == nil {
		if updated := removePAMLine(string(pam)); updated != string(pam) {
			if err := in.Sys.WriteRootFile(ctx, pamPath, []byte(updated), 0o644); err != nil {
				return nil, err
			}
		}
	}
	if err := in.restoreXRDPConfig(ctx); err != nil {
		return nil, err
	}
	for _, path := range []string{helperPath, in.hashPath(), in.polkitPath()} {
		if _, err := in.Sys.Root(ctx, "rm", "-f", path); err != nil {
			return nil, err
		}
	}
	if _, err := in.Sys.Root(ctx, "rmdir", "--ignore-fail-on-non-empty", hashDir, filepath.Dir(helperPath)); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(in.bundleDir()); err != nil {
		return nil, fmt.Errorf("remove desktop service: %w", err)
	}
	if err := os.Remove(in.secretPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove secret: %w", err)
	}
	if _, err := in.Sys.Root(ctx, "systemctl", "try-restart", "xrdp-sesman", "xrdp"); err != nil {
		return nil, err
	}
	return in.wantedPackages(), nil
}

// restoreXRDPConfig puts each Helios key back to its value in the backup the
// first install made, or removes it when the package had no such key. The
// rest of each file keeps any edits made since.
func (in *Installer) restoreXRDPConfig(ctx context.Context) error {
	for _, path := range []string{xrdpINI, sesmanINI} {
		backup, err := in.Sys.ReadFile(path + backupExt)
		if err != nil {
			continue
		}
		current, err := in.Sys.ReadFile(path)
		if err != nil {
			continue
		}
		restored := string(current)
		for _, e := range xrdpEdits {
			if e.path != path {
				continue
			}
			if v, ok := iniValue(string(backup), e.section, e.key); ok {
				restored = setINIKey(restored, e.section, e.key, v)
			} else {
				restored = removeINIKey(restored, e.section, e.key)
			}
		}
		if restored != string(current) {
			if err := in.Sys.WriteRootFile(ctx, path, []byte(restored), 0o644); err != nil {
				return err
			}
		}
		if _, err := in.Sys.Root(ctx, "rm", "-f", path+backupExt); err != nil {
			return err
		}
	}
	return nil
}
