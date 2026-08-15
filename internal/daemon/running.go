package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AlreadyRunning reports whether a daemon already holds the ports, and the pid
// of the one that does when it can be named.
//
// Nothing used to check. A second daemon would start, fail to bind, and exit
// with "address already in use" — which is fine once, from a terminal, and
// ruinous under a launchd agent with KeepAlive: the agent restarts what it
// thinks crashed, the new copy hits the same bound port, and the pair spin at
// about a restart a second, filling daemon.log with thousands of lines while
// the daemon that actually works sits there untouched. One machine here had
// logged 264 restarts.
//
// The port is the thing worth testing rather than the pid file: the file is
// left behind by a daemon that was killed, and a pid can be reused, but a
// socket in use is the conflict itself.
func AlreadyRunning(cfg *Config) (int, bool) {
	if !portInUse(cfg.Server.InternalPort) && !portInUse(cfg.Server.PublicPort) {
		return 0, false
	}
	// Something holds a port. The pid file names it when the holder is ours,
	// and a bare "in use" is still the right answer when it is not.
	return pidFromFile(), true
}

// portInUse reports whether anything is listening on the loopback port.
func portInUse(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	listener.Close()
	return false
}

// pidFromFile reads the running daemon's pid, or 0 when there is no usable one.
func pidFromFile() int {
	data, err := os.ReadFile(filepath.Join(HeliosDir(), "daemon.pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// RunningError describes the daemon that is in the way, in the words the CLI
// should print.
func RunningError(pid int, cfg *Config) error {
	if pid > 0 {
		return fmt.Errorf("helios daemon is already running (pid %d) — stop it with `helios daemon stop`", pid)
	}
	return fmt.Errorf(
		"port %d or %d is already in use — another helios daemon, or something else holding it",
		cfg.Server.InternalPort, cfg.Server.PublicPort,
	)
}
