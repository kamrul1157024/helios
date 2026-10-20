package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseXorgCmdline(t *testing.T) {
	cases := []struct {
		args          []string
		display, auth string
		ok            bool
	}{
		{[]string{"/usr/lib/xorg/Xorg", ":10", "-auth", ".Xauthority", "-config", "xrdp/xorg.conf", "-noreset", "-nolisten", "tcp"},
			":10", ".Xauthority", true},
		{[]string{"Xorg", "-xrdp", ":11", "-auth", "/run/user/1000/xauth"}, ":11", "/run/user/1000/xauth", true},
		// The console's own X server is not the desktop.
		{[]string{"/usr/lib/xorg/Xorg", "vt1", "-displayfd", "3", "-auth", "/run/user/1000/gdm/Xauthority"}, "", "", false},
		{[]string{"/usr/bin/Xvfb", ":99", "xrdp/"}, "", "", false},
		{nil, "", "", false},
	}
	for _, c := range cases {
		display, auth, ok := parseXorgCmdline(c.args)
		if display != c.display || auth != c.auth || ok != c.ok {
			t.Errorf("parseXorgCmdline(%v) = %q, %q, %v; want %q, %q, %v",
				c.args, display, auth, ok, c.display, c.auth, c.ok)
		}
	}
}

// fakeProc builds a /proc-shaped tree with one process.
func fakeProc(t *testing.T, root string, pid, uid int, comm string, cmdline, environ []string, cwd string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("status", "Name:\t"+comm+"\nUid:\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\t0\t0\n")
	write("comm", comm+"\n")
	write("cmdline", strings.Join(cmdline, "\x00")+"\x00")
	write("environ", strings.Join(environ, "\x00")+"\x00")
	if cwd != "" {
		if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindDisplay(t *testing.T) {
	root := t.TempDir()
	xorg := func(display string) []string {
		return []string{"/usr/lib/xorg/Xorg", display, "-auth", ".Xauthority", "-config", "xrdp/xorg.conf"}
	}
	fakeProc(t, root, 100, 1000, "Xorg", xorg(":12"), nil, "/home/dev")
	fakeProc(t, root, 200, 1000, "Xorg", xorg(":10"), nil, "/home/dev")
	// Another user's session must never be picked.
	fakeProc(t, root, 50, 1001, "Xorg", xorg(":5"), nil, "/home/other")
	fakeProc(t, root, 300, 1000, "bash", []string{"bash"}, nil, "")

	d, ok := findDisplay(root, 1000)
	if !ok {
		t.Fatal("no display found")
	}
	if d.Name != ":10" || d.XAuthority != "/home/dev/.Xauthority" || d.PID != 200 {
		t.Fatalf("findDisplay = %+v", d)
	}
	if _, ok := findDisplay(root, 4242); ok {
		t.Fatal("found a display for a user with none")
	}
}

func TestSessionBusPrefersXfceSession(t *testing.T) {
	root := t.TempDir()
	fakeProc(t, root, 10, 1000, "xfwm4", []string{"xfwm4"},
		[]string{"DISPLAY=:10", "DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/other"}, "")
	// The session's own processes name the screen too.
	fakeProc(t, root, 11, 1000, "xfce4-session", []string{"xfce4-session"},
		[]string{"DISPLAY=:10.0", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus"}, "")
	fakeProc(t, root, 12, 1000, "xfce4-session", []string{"xfce4-session"},
		[]string{"DISPLAY=:11", "DBUS_SESSION_BUS_ADDRESS=unix:path=/wrong"}, "")
	if got := sessionBus(root, 1000, ":10"); got != "unix:path=/run/user/1000/bus" {
		t.Fatalf("sessionBus = %q", got)
	}
	if got := sessionBus(root, 1000, ":99"); got != "" {
		t.Fatalf("sessionBus for an unknown display = %q", got)
	}
}
