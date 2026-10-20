package desktop

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// fakeSystem is a machine in memory: files, installed packages, and whether
// xrdp runs. It records every root command and every root write, which is how
// the tests see that a finished step does nothing the second time.
type fakeSystem struct {
	mu         sync.Mutex
	files      map[string]string
	installed  map[string]bool
	active     bool
	rootCalls  []string
	rootWrites []string
	aptOutput  string // when set, apt-get install fails with this output
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{files: map[string]string{}, installed: map[string]bool{}}
}

func (f *fakeSystem) Run(ctx context.Context, argv ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch argv[0] {
	case "dpkg-query":
		var out strings.Builder
		for _, p := range argv[4:] {
			if f.installed[p] {
				fmt.Fprintf(&out, "%s installed\n", p)
			}
		}
		return out.String(), nil
	case "systemctl":
		if f.active {
			return "", nil
		}
		return "", errors.New("inactive")
	}
	return "", nil
}

func (f *fakeSystem) Root(ctx context.Context, argv ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rootCalls = append(f.rootCalls, strings.Join(argv, " "))
	switch {
	case len(argv) > 3 && argv[2] == "apt-get" && contains(argv, "install"):
		if f.aptOutput != "" {
			return f.aptOutput, errors.New("exit status 100")
		}
		for _, p := range argv[len(argv)-len(wantedIn(argv)):] {
			f.installed[p] = true
		}
		// What the xrdp package itself puts on disk.
		f.files[xrdpINI] = xrdpINISample
		f.files[sesmanINI] = "[Sessions]\nKillDisconnected=true\nPolicy=Default\n"
		f.files[pamPath] = sesmanPAM
	case argv[0] == "systemctl" && argv[1] == "restart":
		f.active = true
	case argv[0] == "rm":
		delete(f.files, argv[len(argv)-1])
	}
	return "", nil
}

// wantedIn returns the package names at the end of an apt-get install argv.
func wantedIn(argv []string) []string {
	for i, a := range argv {
		if a == "--no-install-recommends" {
			return argv[i+1:]
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (f *fakeSystem) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(body), nil
}

func (f *fakeSystem) WriteFile(path string, data []byte, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = string(data)
	return nil
}

func (f *fakeSystem) WriteRootFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rootWrites = append(f.rootWrites, path)
	f.files[path] = string(data)
	return nil
}

func (f *fakeSystem) CanSudo(ctx context.Context) bool { return true }
func (f *fakeSystem) SetOutput(fn func(string))        {}

func (f *fakeSystem) resetLog() {
	f.mu.Lock()
	f.rootCalls, f.rootWrites = nil, nil
	f.mu.Unlock()
}

// writeBundle makes a minimal bundle archive in the published layout.
func writeBundle(t *testing.T, path string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(out)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	entries := []struct {
		name, body string
		mode       int64
	}{
		{"helios-guacd/sbin/guacd", "#!/bin/sh\n", 0o755},
		{"helios-guacd/lib/libguac.so.25", "", 0o644},
	}
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "helios-guacd/lib/libguac.so", Linkname: "libguac.so.25", Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []interface{ Close() error }{tw, zw, out} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestInstaller(t *testing.T, sys *fakeSystem) (*Installer, *int) {
	t.Helper()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle.tar.zst")
	writeBundle(t, bundle)
	t.Setenv(BundleEnv, bundle)
	starts := 0
	in := &Installer{
		Sys:       sys,
		User:      "dev",
		Home:      "/home/dev",
		HeliosDir: filepath.Join(dir, "helios"),
		Version:   "dev",
		Start:     func(ctx context.Context) error { starts++; return nil },
	}
	return in, &starts
}

func TestInstallFromNothingThenAgainDoesNothing(t *testing.T) {
	sys := newFakeSystem()
	in, starts := newTestInstaller(t, sys)
	ctx := context.Background()

	if got := in.Missing(ctx); strings.Join(got, ",") != "xrdp,xfce,guacd" {
		t.Fatalf("Missing before install = %v", got)
	}
	if err := in.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	for _, s := range in.Steps() {
		if s.State != StepDone {
			t.Errorf("step %s = %s after install", s.ID, s.State)
		}
	}
	if len(sys.rootCalls) == 0 || *starts != 1 {
		t.Fatalf("first run made %d root calls and %d starts", len(sys.rootCalls), *starts)
	}
	if got := in.Missing(ctx); len(got) != 0 {
		t.Fatalf("Missing after install = %v", got)
	}
	if in.NeedsRoot(ctx) {
		t.Fatal("NeedsRoot after a full install")
	}

	sys.resetLog()
	if err := in.Run(ctx); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(sys.rootCalls) != 0 || len(sys.rootWrites) != 0 {
		t.Fatalf("second run did root work: calls %v, writes %v", sys.rootCalls, sys.rootWrites)
	}
	if *starts != 2 {
		t.Fatalf("starts = %d; the daemon's half of step 6 runs every time", *starts)
	}
}

func TestEachStepCheckPassesWhenDone(t *testing.T) {
	sys := newFakeSystem()
	in, _ := newTestInstaller(t, sys)
	ctx := context.Background()
	if err := in.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, d := range in.defs() {
		done, err := d.check(ctx)
		if err != nil || !done {
			t.Errorf("check %s after install = %v, %v", d.id, done, err)
		}
	}
}

func TestInstallWritesWhatD1AndD4Ask(t *testing.T) {
	sys := newFakeSystem()
	in, _ := newTestInstaller(t, sys)
	if err := in.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(sys.files[in.secretPath()])
	if len(secret) != 64 {
		t.Fatalf("secret %q is not 32 random bytes in hex", secret)
	}
	if sys.files["/etc/helios/desktop/dev.sha256"] != hashFileContent(secret) {
		t.Error("hash file does not match the secret")
	}
	if sys.files[helperPath] != helperScript {
		t.Error("helper not installed")
	}
	if !hasPAMLine(sys.files[pamPath]) {
		t.Error("PAM line missing")
	}
	if v, _ := iniValue(sys.files[xrdpINI], "Globals", "port"); v != "tcp://127.0.0.1:3389" {
		t.Errorf("xrdp port = %q", v)
	}
	if v, _ := iniValue(sys.files[sesmanINI], "Sessions", "KillDisconnected"); v != "false" {
		t.Errorf("KillDisconnected = %q", v)
	}
	if v, _ := iniValue(sys.files[sesmanINI], "Sessions", "Policy"); v != "UB" {
		t.Errorf("Policy = %q", v)
	}
	if sys.files[xrdpINI+backupExt] != xrdpINISample {
		t.Error("the package's xrdp.ini was not kept for the uninstall")
	}
	if !strings.Contains(sys.files["/home/dev/.xsession"], "startxfce4") {
		t.Error("~/.xsession not written")
	}
	for _, call := range sys.rootCalls {
		if strings.Contains(call, "apt-get") && strings.Contains(call, "install") &&
			!strings.HasSuffix(call, "--no-install-recommends xrdp xorgxrdp dbus-x11 xfce4 xfce4-terminal") {
			t.Errorf("apt call = %q", call)
		}
	}
}

func TestOtherSessionKeepsItsXsessionAndSkipsXFCE(t *testing.T) {
	sys := newFakeSystem()
	sys.files["/home/dev/.xsession"] = "exec gnome-session\n"
	in, _ := newTestInstaller(t, sys)
	if err := in.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sys.files["/home/dev/.xsession"] != "exec gnome-session\n" {
		t.Fatal("the user's ~/.xsession was changed")
	}
	for _, s := range in.Steps() {
		if s.ID == "session" && s.State != StepSkipped {
			t.Errorf("session step = %s, want skipped", s.State)
		}
	}
	if sys.installed["xfce4"] {
		t.Error("XFCE installed beside an existing session")
	}
}

func TestAptLockIsReportedInWords(t *testing.T) {
	sys := newFakeSystem()
	sys.aptOutput = "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 42 (apt)\n"
	in, _ := newTestInstaller(t, sys)
	err := in.Run(context.Background())
	if !errors.Is(err, errAptLocked) {
		t.Fatalf("Run = %v, want the apt lock error", err)
	}
	if s := in.Steps()[0]; s.State != StepFailed {
		t.Fatalf("packages step = %s", s.State)
	}
}

func TestUninstallRevertsOnlyWhatHeliosAdded(t *testing.T) {
	sys := newFakeSystem()
	in, _ := newTestInstaller(t, sys)
	ctx := context.Background()
	if err := in.Run(ctx); err != nil {
		t.Fatal(err)
	}
	left, err := in.Uninstall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sys.files[pamPath] != sesmanPAM {
		t.Errorf("PAM file after uninstall:\n%s", sys.files[pamPath])
	}
	if sys.files[xrdpINI] != setINIKey(xrdpINISample, "Globals", "port", "3389") {
		t.Errorf("xrdp.ini after uninstall:\n%s", sys.files[xrdpINI])
	}
	for _, path := range []string{helperPath, "/etc/helios/desktop/dev.sha256", in.polkitPath(), xrdpINI + backupExt} {
		if _, ok := sys.files[path]; ok {
			t.Errorf("%s still present", path)
		}
	}
	if _, err := os.Stat(in.bundleDir()); !os.IsNotExist(err) {
		t.Error("guacd bundle still present")
	}
	if !sys.installed["xrdp"] || len(left) == 0 {
		t.Error("uninstall must leave the packages and say which")
	}
	if _, ok := sys.files["/home/dev/.xsession"]; !ok {
		t.Error("uninstall removed ~/.xsession")
	}
}

func TestBundleRejectsHashMismatch(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "b.tar.zst")
	writeBundle(t, bundle)
	err := installBundle(context.Background(), bundleSource{path: bundle, want: strings.Repeat("0", 64)},
		filepath.Join(dir, "guacd"), func(string) {})
	if !errors.Is(err, errBundleUnavailable) {
		t.Fatalf("installBundle = %v, want a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "guacd")); !os.IsNotExist(err) {
		t.Fatal("a bundle that failed the check was unpacked")
	}
}

func TestManifestAndURL(t *testing.T) {
	manifest := "abc123  helios-guacd-1.6.0-linux-amd64.tar.zst\n"
	if h, ok := manifestHash(manifest, "helios-guacd-1.6.0-linux-amd64.tar.zst"); !ok || h != "abc123" {
		t.Fatalf("manifestHash = %q, %v", h, ok)
	}
	if _, ok := manifestHash(manifest, "helios-guacd-1.6.0-linux-arm64.tar.zst"); ok {
		t.Fatal("found an arch the manifest does not list")
	}
	if got := bundleURL("3.12.0", "amd64"); got != releaseBase+"/download/v3.12.0/helios-guacd-1.6.0-linux-amd64.tar.zst" {
		t.Errorf("release URL = %s", got)
	}
	if got := bundleURL("3.12.0-4-gabc", "arm64"); got != releaseBase+"/latest/download/helios-guacd-1.6.0-linux-arm64.tar.zst" {
		t.Errorf("dev URL = %s", got)
	}
}
