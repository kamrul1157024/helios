package daemon

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// loginPATHTimeout bounds the shell probe. A user's profile can do arbitrary
// work — version managers, network calls — and none of it is worth delaying
// the daemon for.
const loginPATHTimeout = 5 * time.Second

// importLoginPATH widens the process PATH with the one a login shell reports.
//
// launchd starts agents with PATH=/usr/bin:/bin:/usr/sbin:/sbin, so a daemon
// launched at boot cannot see /opt/homebrew/bin and every agent it spawns
// fails with "executable file not found in $PATH". The same daemon started
// from a terminal works, which makes the failure look like anything but what
// it is. Ask the login shell once at startup and inherit what it knows.
//
// Failures are silent by design: the PATH already in the environment is a
// working default for anyone who started the daemon by hand.
func importLoginPATH() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return os.Getenv("PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginPATHTimeout)
	defer cancel()

	merged := mergePATH(os.Getenv("PATH"), loginShellPATH(ctx, shell))
	os.Setenv("PATH", merged)
	return merged
}

// loginShellPATH asks a login shell for its PATH. It is deliberately not an
// interactive shell: -i sources rc files that expect a terminal, and some of
// them block forever without one.
func loginShellPATH(ctx context.Context, shell string) string {
	out, err := exec.CommandContext(ctx, shell, "-lc", "printf %s \"$PATH\"").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// mergePATH appends entries of extra that current does not already list,
// preserving the order of both. current keeps priority: an operator who set
// PATH explicitly outranks whatever a profile happens to say.
func mergePATH(current, extra string) string {
	if extra == "" {
		return current
	}

	seen := make(map[string]bool)
	var out []string
	for _, dir := range strings.Split(current, string(os.PathListSeparator)) {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	for _, dir := range strings.Split(extra, string(os.PathListSeparator)) {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return strings.Join(out, string(os.PathListSeparator))
}
