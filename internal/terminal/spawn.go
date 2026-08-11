package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// UserShell returns the user's login shell, defaulting to /bin/sh.
func UserShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// SpawnHost starts a detached `helios ptyhost` for a session.
//
// It mirrors the daemon's own self-daemonize pattern — Setsid, Start, Release
// — so the host becomes its own session leader and outlives whatever spawned
// it. That is the whole point of a separate process: restarting the daemon
// must not SIGHUP live agent sessions, which is a failure mode tmux avoided
// only by being a separate server.
//
// An empty command resumes the session's agent; a non-empty one is typed into
// a fresh login shell.
func SpawnHost(heliosDir, sessionID, cwd, command string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve helios binary: %w", err)
	}

	logDir := filepath.Join(heliosDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "ptyhost.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logFile = nil
	}
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	args := []string{"ptyhost", sessionID}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if command != "" {
		args = append(args, "--login-cmd", command)
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ptyhost: %w", err)
	}
	return cmd.Process.Release()
}
