package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/terminal"
)

// handlePtyHost runs the per-session terminal host. It is an internal
// subcommand of the same binary rather than a second artifact, so `make
// install` and codesigning are unchanged.
//
// Usage: helios ptyhost <sessionID> [--cwd dir] [--cols n] [--rows n]
//
//	[--cmd path] [--arg value ...]
func handlePtyHost(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: helios ptyhost <sessionID> [--cwd dir] [--cmd path] [--arg v]...")
		os.Exit(1)
	}
	sessionID := args[0]

	cwd, _ := os.Getwd()
	cols, rows := terminal.DefaultCols, terminal.DefaultRows
	command := ""
	loginCmd := ""
	var cmdArgs []string

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--login-cmd":
			if i+1 < len(args) {
				i++
				loginCmd = args[i]
			}
		case "--cwd":
			if i+1 < len(args) {
				i++
				cwd = args[i]
			}
		case "--cols":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					cols = n
				}
			}
		case "--rows":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					rows = n
				}
			}
		case "--cmd":
			if i+1 < len(args) {
				i++
				command = args[i]
			}
		case "--arg":
			if i+1 < len(args) {
				i++
				cmdArgs = append(cmdArgs, args[i])
			}
		}
	}

	switch {
	case command != "":
		// Explicit binary; args come from --arg.
	case loginCmd != "":
		//nolint:staticcheck // shell resolution lives with the spawner
		// Run the user's login shell and type the command into it. This keeps
		// tmux's useful property that the shell's profile loads first (PATH,
		// nvm, homebrew) and that the user still has a shell after the agent
		// exits, without tmux's send-keys quoting hazards.
		command = terminal.UserShell()
		cmdArgs = []string{"-l", "-i"}
	default:
		resolved, err := exec.LookPath("claude")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ptyhost: cannot find claude: %v\n", err)
			os.Exit(1)
		}
		command = resolved
		// Interactive resume, never `-p`: one-shot spawns cost 6-9s per
		// message and cannot be handed off between mobile and terminal.
		cmdArgs = []string{"--resume", sessionID}
	}

	heliosDir := daemon.HeliosDir()
	runDir := terminal.RunDir(heliosDir)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "ptyhost: create run dir: %v\n", err)
		os.Exit(1)
	}

	sock := terminal.SocketPath(heliosDir, sessionID)
	// A stale socket from a dead host would block Listen.
	if !terminal.Probe(sock) {
		os.Remove(sock)
	} else {
		fmt.Fprintf(os.Stderr, "ptyhost: session %s already hosted\n", sessionID)
		os.Exit(1)
	}

	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ptyhost: listen %s: %v\n", sock, err)
		os.Exit(1)
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "ptyhost: chmod socket: %v\n", err)
	}

	host, err := terminal.NewHost(terminal.HostConfig{
		SessionID: sessionID,
		Command:   command,
		Args:      cmdArgs,
		Dir:       cwd,
		Cols:      cols,
		Rows:      rows,
	})
	if err != nil {
		ln.Close()
		os.Remove(sock)
		fmt.Fprintf(os.Stderr, "ptyhost: %v\n", err)
		os.Exit(1)
	}

	if err := terminal.WriteSidecar(heliosDir, terminal.Sidecar{
		SessionID: sessionID,
		PID:       os.Getpid(),
		ChildPID:  host.Pid(),
		Cwd:       cwd,
		Socket:    sock,
		StartedAt: time.Now(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ptyhost: write sidecar: %v\n", err)
	}

	// Typed after the sidecar exists, so a crash between spawn and type leaves
	// no half-registered session. Bytes written before the shell reads them
	// stay in the PTY buffer, so there is no race with shell startup.
	if loginCmd != "" {
		if err := host.Write([]byte(loginCmd+"\r"), "ptyhost"); err != nil {
			fmt.Fprintf(os.Stderr, "ptyhost: type login command: %v\n", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
		}
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- host.Serve(ctx, ln) }()

	select {
	case <-host.Exited():
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			fmt.Fprintf(os.Stderr, "ptyhost: serve: %v\n", err)
		}
	}

	host.Close()
	ln.Close()
	terminal.RemoveHostFiles(heliosDir, sessionID)
}
