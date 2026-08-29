package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"
	claude "github.com/kamrul1157024/helios/internal/provider/claude"
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
	var cmdArgs []string
	extraEnv := map[string]string{}

	for i := 1; i < len(args); i++ {
		switch args[i] {
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
		case "--env":
			// KEY=VALUE. A provider uses this to tell its own hooks which
			// helios session they belong to, when the agent has no flag for it.
			if i+1 < len(args) {
				i++
				if k, v, ok := strings.Cut(args[i], "="); ok && k != "" {
					extraEnv[k] = v
				}
			}
		}
	}

	switch {
	case command != "":
		// Explicit binary; args come from --arg.
	default:
		resolved, err := exec.LookPath("claude")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ptyhost: cannot find claude: %v\n", err)
			os.Exit(1)
		}
		command = resolved
		// Interactive resume, never `-p`: one-shot spawns cost 6-9s per
		// message and cannot be handed off between mobile and terminal.
		//
		// The permission mode is repeated here because it is a per-invocation
		// flag rather than conversation state: without it a session that went
		// cold would come back in the CLI's default mode and start asking
		// questions it had stopped asking.
		cmdArgs = []string{"--resume", sessionID, "--permission-mode", claude.DefaultPermissionMode}
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
		Env:       agentEnv(extraEnv),
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
		Protocol:  terminal.HostProtocol,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ptyhost: write sidecar: %v\n", err)
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

// agentVars are the variables Claude Code exports into a session it is running.
var agentVars = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	heliosSessionVar,
}

// agentEnv returns this process's environment without the marks of the agent
// that may have spawned it.
//
// Starting a session from a terminal that is itself an agent session is
// ordinary, and the child inherits those variables through the wrap. The agent
// then takes itself for a continuation of its parent: its hooks report the
// wrong session and the new one never gets a title.
// heliosSessionVar names the session a terminal belongs to. Listed in
// agentVars so it is scrubbed from an inherited environment: an agent started
// from inside another agent's session would otherwise report its parent's id,
// and every hook it sent would be filed against the parent's row — including
// the transcript path, which would then point at the wrong agent's file.
const heliosSessionVar = "HELIOS_SESSION"

func agentEnv(extra map[string]string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(extra))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && slices.Contains(agentVars, key) {
			continue
		}
		// A provider's own variable wins over an inherited one, so a nested
		// session cannot be mistaken for the one that launched it.
		if ok {
			if _, overridden := extra[key]; overridden {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
