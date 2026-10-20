package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The desktop's display number is not configured anywhere Helios can read:
// sesman picks the next free one when the session starts. So the daemon finds
// the session's Xorg among the user's processes and reads it from there (D3).

// Display is a running xrdp session's X server.
type Display struct {
	Name       string // ":10"
	XAuthority string // the cookie file that X server accepts
	PID        int
}

// findDisplay scans procRoot (normally /proc) for an xorgxrdp X server owned
// by uid. With several, the lowest display wins, which is the one sesman made
// first and the one Policy=UB keeps reusing.
func findDisplay(procRoot string, uid int) (Display, bool) {
	var best Display
	found := false
	for _, pid := range listPIDs(procRoot) {
		dir := filepath.Join(procRoot, strconv.Itoa(pid))
		if procUID(dir) != uid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			continue
		}
		name, auth, ok := parseXorgCmdline(splitNUL(raw))
		if !ok {
			continue
		}
		if auth != "" && !filepath.IsAbs(auth) {
			// sesman passes the cookie path relative to the session's working
			// directory, which is the user's home.
			if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil {
				auth = filepath.Join(cwd, auth)
			}
		}
		d := Display{Name: name, XAuthority: auth, PID: pid}
		if !found || displayNumber(d.Name) < displayNumber(best.Name) {
			best, found = d, true
		}
	}
	return best, found
}

// parseXorgCmdline recognises the X server sesman starts for an xrdp session:
// Xorg with xorgxrdp's config (xrdp/xorg.conf) or the -xrdp flag some builds
// use. It returns the display and the -auth file.
func parseXorgCmdline(args []string) (string, string, bool) {
	if len(args) == 0 || !strings.HasPrefix(filepath.Base(args[0]), "Xorg") {
		return "", "", false
	}
	display, auth := "", ""
	xrdp := false
	for i, a := range args[1:] {
		switch {
		case a == "-xrdp" || strings.Contains(a, "xrdp/"):
			xrdp = true
		case a == "-auth" && i+2 < len(args):
			auth = args[i+2]
		case display == "" && isDisplayArg(a):
			display = a
		}
	}
	if !xrdp || display == "" {
		return "", "", false
	}
	return display, auth, true
}

func isDisplayArg(a string) bool {
	if len(a) < 2 || a[0] != ':' {
		return false
	}
	_, err := strconv.Atoi(a[1:])
	return err == nil
}

func displayNumber(name string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(name, ":"))
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return n
}

// sessionBus finds the D-Bus address of the desktop session on display, from
// the environment of the session's own processes. xfce4-session is preferred:
// it is the session leader and always has the bus. Any other process of the
// user on the same display with a bus address will do when it is missing.
func sessionBus(procRoot string, uid int, display string) string {
	fallback := ""
	for _, pid := range listPIDs(procRoot) {
		dir := filepath.Join(procRoot, strconv.Itoa(pid))
		if procUID(dir) != uid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "environ"))
		if err != nil {
			continue
		}
		env := parseEnviron(raw)
		if !sameDisplay(env["DISPLAY"], display) || env["DBUS_SESSION_BUS_ADDRESS"] == "" {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
		if strings.TrimSpace(string(comm)) == "xfce4-session" {
			return env["DBUS_SESSION_BUS_ADDRESS"]
		}
		if fallback == "" {
			fallback = env["DBUS_SESSION_BUS_ADDRESS"]
		}
	}
	return fallback
}

// sameDisplay compares display names without their screen number: the
// session's processes carry ":10.0" for the server sesman started as ":10".
func sameDisplay(a, b string) bool {
	trim := func(d string) string {
		if i := strings.LastIndex(d, "."); i > strings.LastIndex(d, ":") {
			return d[:i]
		}
		return d
	}
	return a != "" && trim(a) == trim(b)
}

func listPIDs(procRoot string) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// procUID reads the real uid from /proc/<pid>/status, or -1.
func procUID(dir string) int {
	raw, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return -1
		}
		uid, err := strconv.Atoi(f[0])
		if err != nil {
			return -1
		}
		return uid
	}
	return -1
}

func splitNUL(raw []byte) []string {
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(string(raw), "\x00")
}

func parseEnviron(raw []byte) map[string]string {
	env := make(map[string]string)
	for _, kv := range splitNUL(raw) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}
