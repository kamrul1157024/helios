package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// SpawnHost starts a detached `helios ptyhost` for a session.
//
// It mirrors the daemon's own self-daemonize pattern — Setsid, Start, Release
// — so the host becomes its own session leader and outlives whatever spawned
// it. That is the whole point of a separate process: restarting the daemon
// must not SIGHUP live agent sessions, which is a failure mode tmux avoided
// only by being a separate server.
//
// Empty argv resumes the session's agent; otherwise argv is executed as given.
// The host runs it directly rather than through a shell, so the caller's
// environment is what the agent gets and nothing re-reads the user's rc file.
func SpawnHost(heliosDir, sessionID, cwd string, argv []string) error {
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

	cmd := exec.Command(exe, hostArgs(sessionID, cwd, argv)...)
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

// hostArgs renders the ptyhost command line for a session. Each element of
// argv gets its own flag, so no part of the command is ever parsed as text.
func hostArgs(sessionID, cwd string, argv []string) []string {
	args := []string{"ptyhost", sessionID}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if len(argv) > 0 {
		args = append(args, "--cmd", argv[0])
		for _, a := range argv[1:] {
			args = append(args, "--arg", a)
		}
	}
	return args
}
