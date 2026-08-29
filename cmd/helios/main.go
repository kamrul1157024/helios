package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/tailscale"
	"github.com/kamrul1157024/helios/internal/terminal"
	"github.com/kamrul1157024/helios/internal/tui"
	"github.com/kamrul1157024/helios/internal/tunnel"
)

const version = "0.2.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		handleStart()
	case "stop":
		handleStop()
	case "devices":
		handleDevices()
	case "new":
		handleNew(os.Args[2:])
	case "sessions":
		handleSessions(os.Args[2:])
	case "daemon":
		handleDaemon(os.Args[2:])
	case "tunnel":
		handleTunnel(os.Args[2:])
	case "auth":
		handleAuth(os.Args[2:])
	case "attach":
		handleAttach(os.Args[2:])
	case "ptyhost":
		handlePtyHost(os.Args[2:])
	case "wrap":
		handleWrap(os.Args[2:])
	case "hooks":
		handleHooks(os.Args[2:])
	case "setup":
		handleSetup(os.Args[2:])
	case "cleanup":
		handleCleanup(os.Args[2:])
	case "logs":
		handleLogs(os.Args[2:])
	case "version":
		fmt.Printf("helios v%s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func handleDaemon(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios daemon <start|stop|status>")
		os.Exit(1)
	}

	switch args[0] {
	case "start":
		cfg, err := daemon.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		// Parse optional flags
		background := false
		for i, a := range args[1:] {
			switch a {
			case "-d", "--daemonize":
				background = true
			case "--bind":
				if i+1 < len(args[1:]) {
					cfg.Server.Bind = args[i+2]
				}
			case "--internal-port":
				if i+1 < len(args[1:]) {
					p, err := strconv.Atoi(args[i+2])
					if err == nil {
						cfg.Server.InternalPort = p
					}
				}
			case "--public-port":
				if i+1 < len(args[1:]) {
					p, err := strconv.Atoi(args[i+2])
					if err == nil {
						cfg.Server.PublicPort = p
					}
				}
			}
		}
		// Before forking, not after: a background start would otherwise report a
		// pid that is already on its way out, and the failure would only appear
		// in a log the user has no reason to open.
		if pid, running := daemon.AlreadyRunning(cfg); running {
			fmt.Fprintln(os.Stderr, daemon.RunningError(pid, cfg))
			os.Exit(1)
		}

		if background {
			exe, _ := os.Executable()
			// Rebuild args without -d/--daemonize
			var newArgs []string
			for _, a := range os.Args[1:] {
				if a != "-d" && a != "--daemonize" {
					newArgs = append(newArgs, a)
				}
			}
			proc, err := os.StartProcess(exe, append([]string{exe}, newArgs...), &os.ProcAttr{
				Dir:   "/",
				Env:   os.Environ(),
				Files: []*os.File{os.Stdin, nil, nil},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error starting background daemon: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("helios daemon started in background (pid %d)\n", proc.Pid)
			proc.Release()
			return
		}
		// Run under supervisor so panics/crashes get auto-restarted
		sv := daemon.NewSupervisor(cfg)
		if err := sv.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := daemon.StopSupervisor(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "status":
		if running, pid := daemon.SupervisorStatus(); running {
			fmt.Printf("helios supervisor is running (pid %d)\n", pid)
		}
		if err := daemon.Status(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown daemon command: %s\n", args[0])
		os.Exit(1)
	}
}

func handleAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios auth <init|devices|revoke>")
		os.Exit(1)
	}

	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	switch args[0] {
	case "init":
		if err := auth.InitDevice(""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "devices":
		resp, err := http.Get(internalURL + "/internal/device/list")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: daemon not running? %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result struct {
			Devices []struct {
				KID         string  `json:"kid"`
				Name        string  `json:"name"`
				Status      string  `json:"status"`
				Platform    string  `json:"platform"`
				Browser     string  `json:"browser"`
				PushEnabled bool    `json:"push_enabled"`
				LastSeenAt  *string `json:"last_seen_at"`
			} `json:"devices"`
		}
		json.NewDecoder(resp.Body).Decode(&result)

		if len(result.Devices) == 0 {
			fmt.Println("No devices registered. Run: helios auth init")
			return
		}

		fmt.Printf("%-14s %-25s %-10s %-10s %-10s %s\n", "Key ID", "Name", "Status", "Platform", "Push", "Last Seen")
		fmt.Println("--------------------------------------------------------------------------------------------")

		for _, d := range result.Devices {
			lastSeen := "never"
			if d.LastSeenAt != nil {
				t, err := time.Parse(time.RFC3339, *d.LastSeenAt)
				if err == nil {
					lastSeen = humanDuration(time.Since(t))
				}
			}
			pushStr := "off"
			if d.PushEnabled {
				pushStr = "on"
			}
			name := d.Name
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Printf("%-14s %-25s %-10s %-10s %-10s %s\n", d.KID, name, d.Status, d.Platform, pushStr, lastSeen)
		}

	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: helios auth revoke <kid>")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{"kid": args[1]})
		resp, err := http.Post(internalURL+"/internal/device/revoke", "application/json", bytes.NewBuffer(body))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: daemon not running? %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		var result struct {
			Revoked bool   `json:"revoked"`
			Message string `json:"message"`
		}
		json.Unmarshal(data, &result)
		if result.Revoked {
			fmt.Printf("Device %q revoked\n", args[1])
		} else {
			fmt.Fprintf(os.Stderr, "Failed to revoke device: %s\n", result.Message)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown auth command: %s\n", args[0])
		os.Exit(1)
	}
}

func handleStart() {
	cfg, err := daemon.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Notifications belong to the desktop app now; a notifier left running by
	// an older install would fire alongside it.
	reapLegacyNotifier()

	// The TUI runs in this terminal. Sessions live in their own terminal hosts,
	// so there is no multiplexer to open a window in or attach to.
	if err := tui.RunStart(cfg.Server.InternalPort, cfg.Server.PublicPort); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleDevices() {
	cfg, _ := daemon.LoadConfig()
	if err := tui.RunDevices(cfg.Server.InternalPort); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleStop() {
	// Stop daemon (or supervisor if running). Tunnel is left alive.
	if err := daemon.StopSupervisor(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// reapLegacyNotifier stops the `helios notify` process older installs spawned.
//
// The desktop app raises notifications now, and nothing left running from a
// previous version will ever be stopped otherwise — it would keep firing
// duplicates forever. Best-effort: a missing or stale pid file is the normal
// case after the first run.
func reapLegacyNotifier() {
	pidPath := filepath.Join(daemon.HeliosDir(), "notify.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(pidPath)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return
	}
	proc.Signal(syscall.SIGTERM)

	// Wait for the process to actually exit (up to 5 seconds).
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if proc.Signal(syscall.Signal(0)) != nil {
			break
		}
	}

	os.Remove(pidPath)
}

// tunnelProviderConfig loads provider settings for a command that runs without
// the daemon. A missing or unreadable config is not fatal here: the defaults
// still describe every provider well enough to check and stop a tunnel.
func tunnelProviderConfig() tunnel.ProviderConfig {
	cfg, err := daemon.LoadConfig()
	if err != nil || cfg == nil {
		cfg = daemon.DefaultConfig()
	}
	return daemon.TunnelProviderConfig(cfg)
}

// liveTunnel resolves the persisted tunnel state and confirms the tunnel is
// still up, printing the reason and cleaning up when it is not. The returned
// URL is the live one, which can differ from the persisted one.
func liveTunnel() (tunnel.TunnelState, string, bool) {
	state, err := tunnel.LoadState(daemon.HeliosDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if state == nil {
		fmt.Println("No tunnel running.")
		return tunnel.TunnelState{}, "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url, active := tunnel.StateLiveness(ctx, *state, tunnelProviderConfig())
	if !active {
		fmt.Println("No tunnel running (stale state, cleaning up).")
		tunnel.RemoveState(daemon.HeliosDir())
		return tunnel.TunnelState{}, "", false
	}
	return *state, url, true
}

// tunnelDescription names the provider, adding the PID only for providers that
// own one. Printing "PID 0" for the rest reads as a bug.
func tunnelDescription(state tunnel.TunnelState, url string) string {
	name := state.Provider
	if state.Provider == "tailscale" {
		name += " " + tailscaleMode(url)
	}
	if state.PID > 0 {
		return fmt.Sprintf("%s, PID %d", name, state.PID)
	}
	return name
}

// tailscaleMode recovers the exposure mode from the published URL. Serve
// publishes plain HTTP inside the tailnet; Funnel always terminates TLS.
func tailscaleMode(url string) string {
	if strings.HasPrefix(url, "http://") {
		return string(tailscale.ModeServe)
	}
	return string(tailscale.ModeFunnel)
}

// tunnelExposure states who can reach the tunnel — the one thing the URL does
// not say, and the thing that differs between the two Tailscale modes.
func tunnelExposure(provider, url string) string {
	switch provider {
	case "tailscale":
		if tailscaleMode(url) == string(tailscale.ModeServe) {
			return "Reachable only from your tailnet (Tailscale must be on at the other end)"
		}
		return "Reachable from the public internet"
	case "local":
		return "Reachable only from your local network"
	}
	return ""
}

func handleTunnel(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios tunnel <stop|status>")
		os.Exit(1)
	}

	switch args[0] {
	case "status":
		state, url, ok := liveTunnel()
		if !ok {
			return
		}
		uptime := time.Since(state.StartedAt).Truncate(time.Second)
		fmt.Printf("Tunnel active: %s (%s, up %s)\n", url, tunnelDescription(state, url), uptime)
		if exposure := tunnelExposure(state.Provider, url); exposure != "" {
			fmt.Printf("  %s\n", exposure)
		}

	case "stop":
		state, url, ok := liveTunnel()
		if !ok {
			return
		}

		fmt.Printf("Tunnel is running: %s (%s)\n\n", url, tunnelDescription(state, url))
		fmt.Println("WARNING: Killing the tunnel will disconnect all mobile devices.")
		fmt.Println("         They will need to rescan and reconnect.")
		fmt.Print("\nKill tunnel? [y/N]: ")

		var answer string
		fmt.Scanln(&answer)
		answer = strings.TrimSpace(strings.ToLower(answer))

		if answer != "y" {
			fmt.Println("Tunnel kept alive.")
			return
		}

		if err := tunnel.KillTunnel(daemon.HeliosDir(), tunnelProviderConfig()); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping tunnel: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Tunnel stopped.")

	default:
		fmt.Fprintf(os.Stderr, "Unknown tunnel command: %s\n", args[0])
		os.Exit(1)
	}
}

func handleNew(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios new \"prompt\" [--model model] [--cwd /path/to/dir]")
		os.Exit(1)
	}

	prompt := args[0]
	cwd, _ := os.Getwd()
	model := ""

	for i, a := range args {
		if a == "--cwd" && i+1 < len(args) {
			cwd = args[i+1]
		}
		if a == "--model" && i+1 < len(args) {
			model = args[i+1]
		}
	}

	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	reqBody := map[string]string{
		"prompt": prompt,
		"cwd":    cwd,
	}
	if model != "" {
		reqBody["model"] = model
	}
	body, _ := json.Marshal(reqBody)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(internalURL+"/internal/sessions", "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "helios is not running. Start it with: helios start")
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result struct {
		Success   bool   `json:"success"`
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read response: %v\n", err)
		os.Exit(1)
	}

	if !result.Success {
		fmt.Fprintf(os.Stderr, "Failed to create session: %s\n", result.Message)
		os.Exit(1)
	}

	fmt.Printf("Session %s started\n", result.SessionID)
	fmt.Printf("  cwd: %s\n", result.CWD)
	fmt.Printf("  Attach with: helios attach %s\n", result.SessionID)
}

// handleWrap runs a command inside a helios terminal host and attaches to it.
//
// To the user this looks exactly like running the command directly, but the
// process now lives in a host of its own: closing the terminal, or detaching
// with the detach key, leaves it running, and mobile or desktop clients can
// watch and drive the same session.
func handleWrap(args []string) {
	cmdStart := -1
	for i, a := range args {
		if a == "--" {
			cmdStart = i + 1
			break
		}
	}
	if cmdStart < 0 || cmdStart >= len(args) {
		fmt.Fprintln(os.Stderr, "Usage: helios wrap -- <command> [args...]")
		fmt.Fprintln(os.Stderr, "Example: helios wrap -- claude")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	sessionID, parts, providerID := wrapCommand(args[cmdStart:])

	heliosDir := daemon.HeliosDir()
	socket := terminal.SocketPath(heliosDir, sessionID)
	if !terminal.Probe(socket) {
		// A provider whose agent mints its own session id identifies itself
		// through the environment; harmless for one that does not.
		env := map[string]string{"HELIOS_SESSION": sessionID}
		if err := terminal.SpawnHost(heliosDir, sessionID, cwd, resolveBinary(parts), env); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start terminal host: %v\n", err)
			os.Exit(1)
		}
		if !terminal.WaitForSocket(socket, 15*time.Second) {
			fmt.Fprintln(os.Stderr, "Terminal host did not come up; see ~/.helios/logs/ptyhost.log")
			os.Exit(1)
		}
	}

	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)
	// Any known provider is registered, not just Claude. A wrapped codex used
	// to get a helios-managed terminal the daemon never heard about.
	if providerID != "" {
		registerWrappedSession(internalURL, socket, cwd, sessionID, providerID, explicitPermissionMode(parts))
	}

	res, err := terminal.Attach(context.Background(), terminal.AttachConfig{
		Socket: socket,
		Name:   attachViewerName(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}
	if res.Detached {
		fmt.Printf("[detached — still running; reattach with: helios attach %s]\n", sessionID)
		return
	}

	// The process ended for real. Hooks do not fire on Ctrl-C or a crash, so
	// the daemon is told directly rather than left showing a live session.
	if providerID != "" {
		body, err := json.Marshal(map[string]interface{}{
			"session_id": sessionID,
			"cwd":        cwd,
		})
		if err == nil {
			postAndClose(internalURL+"/hooks/"+providerID+"/session.end", body)
		}
	}
	os.Exit(res.ExitCode)
}

// resolveBinary returns the command with its binary resolved against this
// process's PATH.
//
// The host executes the command directly, and it is spawned detached, so it
// cannot be relied on to have inherited the PATH the user typed the command
// under. Resolution can fail, and the bare name is then the best guess.
func resolveBinary(parts []string) []string {
	out := make([]string, len(parts))
	copy(out, parts)
	if bin, err := exec.LookPath(out[0]); err == nil {
		out[0] = bin
	}
	return out
}

// wrapCommand decides the session ID a wrapped command runs under and returns
// the command line to launch.
//
// Claude sessions are tracked, so they need an ID the daemon and the agent
// agree on. --resume/--continue already carry one; otherwise we mint it and
// pass it down, which is what makes the session addressable from the phone
// before its first hook arrives.
// wrapProvider maps a wrapped binary to the provider that speaks for it.
//
// A name match, because the CLI cannot ask the registry: providers are
// registered in the daemon process, not this one. The daemon validates what it
// is sent, so a wrong guess here is rejected rather than believed.
func wrapProvider(bin string) string {
	switch filepath.Base(bin) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	default:
		return ""
	}
}

func wrapCommand(parts []string) (sessionID string, cmd []string, providerID string) {
	providerID = wrapProvider(parts[0])
	if providerID != "claude" {
		// Only Claude takes an id from us. Codex mints its own and reports it
		// through its session-start hook, so helios keeps this one as the row
		// key and learns the other later.
		return uuid.New().String(), parts, providerID
	}

	for i, a := range parts {
		if (a == "--resume" || a == "--continue" || a == "--session-id") && i+1 < len(parts) {
			return parts[i+1], parts, providerID
		}
	}

	sessionID = uuid.New().String()
	cmd = append([]string{parts[0], "--session-id", sessionID}, parts[1:]...)
	return sessionID, cmd, providerID
}

// explicitPermissionMode returns the permission mode the user asked a wrapped
// command to run under, or "" if they left it to the CLI.
//
// Wrap does not add a mode of its own — the point of it is that the command
// behaves as if it had been typed directly — so what the user typed is the only
// thing worth recording. Recording it is what stops a wake from replacing it
// with the Helios default, since the mode is a per-invocation flag the resume
// has to repeat.
//
// --dangerously-skip-permissions has no resume equivalent and is recorded as
// the mode that comes closest, matching claude.LaunchPermissionMode.
func explicitPermissionMode(parts []string) string {
	for i, a := range parts {
		switch {
		case a == "--dangerously-skip-permissions":
			return "bypassPermissions"
		case a == "--permission-mode" && i+1 < len(parts):
			return parts[i+1]
		case strings.HasPrefix(a, "--permission-mode="):
			return strings.TrimPrefix(a, "--permission-mode=")
		}
	}
	return ""
}

// registerWrappedSession binds a hand-started terminal to a session record.
// The daemon being down is not fatal: the command still runs, it is simply not
// tracked until the daemon comes back and recovers the host from its sidecar.
func registerWrappedSession(internalURL, socket, cwd, sessionID, providerID, permissionMode string) {
	body, err := json.Marshal(map[string]string{
		"handle":          socket,
		"cwd":             cwd,
		"session_id":      sessionID,
		"provider":        providerID,
		"permission_mode": permissionMode,
	})
	if err != nil {
		return
	}
	if err := postAndClose(internalURL+"/internal/wrap", body); err != nil {
		fmt.Fprintf(os.Stderr, "helios: session not registered (%v)\n", err)
	}
}

func postAndClose(url string, body []byte) error {
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func handleAttach(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios attach <session-id|socket-path>")
		os.Exit(1)
	}

	target := args[0]
	socket := target
	// A path is attached to as-is, which is what makes a host reachable even
	// when the daemon is down.
	if !strings.HasPrefix(target, "/") {
		cfg, _ := daemon.LoadConfig()
		var err error
		socket, err = resolveTerminalSocket(
			fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort), target)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	res, err := terminal.Attach(context.Background(), terminal.AttachConfig{
		Socket: socket,
		Name:   attachViewerName(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}
	if res.Detached {
		fmt.Printf("[detached — the session keeps running; reattach with: helios attach %s]\n", target)
		return
	}
	os.Exit(res.ExitCode)
}

// resolveTerminalSocket maps a session ID, or a unique prefix of one, to its
// terminal socket, waking a cold session on the way.
func resolveTerminalSocket(internalURL, target string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(internalURL + "/internal/sessions")
	if err != nil {
		return "", fmt.Errorf("helios is not running. Start it with: helios start")
	}
	defer resp.Body.Close()

	var result struct {
		Sessions []struct {
			SessionID string  `json:"session_id"`
			Terminal  *string `json:"terminal"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("read session list: %w", err)
	}

	var match string
	var socket string
	var prefixed int
	for _, s := range result.Sessions {
		live := ""
		if s.Terminal != nil {
			live = *s.Terminal
		}
		// An exact ID wins outright, even when it is also a prefix of another.
		if s.SessionID == target {
			match, socket, prefixed = s.SessionID, live, 1
			break
		}
		if strings.HasPrefix(s.SessionID, target) {
			match, socket = s.SessionID, live
			prefixed++
		}
	}
	switch {
	case prefixed == 0:
		return "", fmt.Errorf("session not found: %s", target)
	case prefixed > 1:
		return "", fmt.Errorf("%q matches %d sessions; use a longer prefix", target, prefixed)
	case socket != "":
		return socket, nil
	}

	// Cold: resume it. Claude reloads the transcript, so the conversation is
	// still there even though the screen was lost.
	fmt.Fprintf(os.Stderr, "Resuming %s...\n", match)
	wake, err := client.Post(
		internalURL+"/internal/sessions/"+match+"/resume", "application/json", nil)
	if err != nil {
		return "", fmt.Errorf("resume session: %w", err)
	}
	defer wake.Body.Close()

	var woke struct {
		Terminal string `json:"terminal"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(wake.Body).Decode(&woke); err != nil {
		return "", fmt.Errorf("read resume response: %w", err)
	}
	if woke.Terminal == "" {
		if woke.Error != "" {
			return "", fmt.Errorf("resume session: %s", woke.Error)
		}
		return "", fmt.Errorf("resume session: daemon returned %s", wake.Status)
	}
	return woke.Terminal, nil
}

// attachViewerName labels this viewer in the writer indicator other clients
// see, so a phone can tell someone is typing at a desk.
func attachViewerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "terminal"
	}
	return host + " terminal"
}

func handleSessions(args []string) {
	// --list flag: plain table output (old behavior)
	for _, a := range args {
		if a == "--list" || a == "-l" {
			handleSessionsList()
			return
		}
	}

	// Default: interactive TUI
	cfg, _ := daemon.LoadConfig()
	if err := tui.RunSessions(cfg.Server.InternalPort); err != nil {
		fmt.Fprintf(os.Stderr, "sessions TUI error: %v\n", err)
		os.Exit(1)
	}
}

func handleSessionsList() {
	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(internalURL + "/internal/sessions")
	if err != nil {
		fmt.Fprintln(os.Stderr, "helios is not running. Start it with: helios start")
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result struct {
		Sessions []struct {
			SessionID   string  `json:"session_id"`
			CWD         string  `json:"cwd"`
			Status      string  `json:"status"`
			Model       *string `json:"model"`
			Terminal    *string `json:"terminal"`
			LastEvent   *string `json:"last_event"`
			LastEventAt *string `json:"last_event_at"`
			CreatedAt   string  `json:"created_at"`
		} `json:"sessions"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Sessions) == 0 {
		fmt.Println("No sessions. Start one with: helios new \"your prompt\"")
		return
	}

	fmt.Printf("%-10s %-12s %-40s %-8s %s\n", "Session", "Status", "CWD", "Terminal", "Last Activity")
	fmt.Println(strings.Repeat("-", 100))

	for _, s := range result.Sessions {
		sid := s.SessionID
		if len(sid) > 10 {
			sid = sid[:10]
		}
		cwdShort := s.CWD
		if len(cwdShort) > 40 {
			cwdShort = "..." + cwdShort[len(cwdShort)-37:]
		}
		// The socket path itself is noise in a table; what the user needs is
		// whether the session is live or has to be resumed.
		term := "cold"
		if s.Terminal != nil && *s.Terminal != "" {
			term = "live"
		}
		lastActivity := ""
		if s.LastEventAt != nil {
			t, err := time.Parse(time.RFC3339, *s.LastEventAt)
			if err == nil {
				lastActivity = humanDuration(time.Since(t))
			}
		}

		fmt.Printf("%-10s %-12s %-40s %-8s %s\n", sid, s.Status, cwdShort, term, lastActivity)
	}
}

func handleLogs(args []string) {
	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	tail := 50
	source := "" // all
	for i, a := range args {
		switch a {
		case "--tail", "-n":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					tail = n
				}
			}
		case "--daemon":
			source = "daemon"
		case "--device", "--devices":
			source = "device"
		}
	}

	url := fmt.Sprintf("%s/internal/logs?tail=%d", internalURL, tail)
	if source != "" {
		url += "&source=" + source
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helios is not running. Start it with: helios start")
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result struct {
		Daemon  []string            `json:"daemon"`
		Devices map[string][]string `json:"devices"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &result)

	if result.Daemon != nil && len(result.Daemon) > 0 {
		fmt.Println("=== Daemon Logs ===")
		for _, line := range result.Daemon {
			fmt.Println(line)
		}
	} else if source == "" || source == "daemon" {
		fmt.Println("=== Daemon Logs ===")
		fmt.Println("(no logs)")
	}

	if result.Devices != nil {
		for kid, lines := range result.Devices {
			fmt.Println()
			name := kid
			if len(name) > 12 {
				name = name[:12] + "..."
			}
			fmt.Printf("=== Device: %s ===\n", name)
			if len(lines) == 0 {
				fmt.Println("(no logs)")
			} else {
				for _, line := range lines {
					fmt.Println(line)
				}
			}
		}
	} else if source == "" || source == "device" {
		fmt.Println()
		fmt.Println("=== Device Logs ===")
		fmt.Println("(no logs)")
	}
}

func handleCleanup(args []string) {
	target := "all"
	if len(args) > 0 {
		target = args[0]
	}

	heliosDir := daemon.HeliosDir()

	switch target {
	case "db":
		dbPath := filepath.Join(heliosDir, "helios.db")
		if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error removing database: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Database removed:", dbPath)

	case "logs":
		logsDir := filepath.Join(heliosDir, "logs")
		if err := os.RemoveAll(logsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing logs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Logs removed:", logsDir)

	case "all":
		// Stop tunnel, supervisor, and daemon
		tunnel.KillTunnel(heliosDir, tunnelProviderConfig())
		daemon.StopSupervisor()

		if err := os.RemoveAll(heliosDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing helios data: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("All helios data removed:", heliosDir)
		fmt.Println("Run 'helios start' to set up fresh.")

	default:
		fmt.Fprintf(os.Stderr, "Unknown cleanup target: %s\nUsage: helios cleanup [db|logs|all]\n", target)
		os.Exit(1)
	}
}

func handleHooks(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios hooks <install|show|remove>")
		os.Exit(1)
	}

	// The registry is per process, and this one is not the daemon: without
	// this every branch below iterates nothing and reports success.
	daemon.RegisterDefaultProviders()

	switch args[0] {
	case "install":
		local := false
		for _, a := range args[1:] {
			if a == "--local" {
				local = true
			}
		}
		if err := daemon.InstallHooks(local); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "show":
		daemon.ShowHooks()
	case "remove":
		if err := daemon.RemoveHooks(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown hooks command: %s\n", args[0])
		os.Exit(1)
	}
}

func handleSetup(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios setup <shell|tailscale>")
		os.Exit(1)
	}

	switch args[0] {
	case "tailscale":
		checkTailscaleSetup()

	// "all" is kept as an alias: setup once configured editors too, and there
	// is nothing left to configure now that sessions bring their own terminal.
	case "shell", "all":
		info := daemon.DetectShell()
		if daemon.ShellWrapperInstalled(info) {
			fmt.Printf("Shell wrapper already installed in %s\n", info.RCPath)
			return
		}
		if err := daemon.InstallShellWrapper(info); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
			fmt.Println(daemon.ManualShellInstructions(info, err))
			os.Exit(1)
		}
		fmt.Printf("Shell wrapper installed in %s\n", info.RCPath)
		fmt.Println("Restart your shell or run: source", info.RCPath)

	default:
		fmt.Fprintf(os.Stderr, "Unknown setup target: %s\nUsage: helios setup <shell|tailscale>\n", args[0])
		os.Exit(1)
	}
}

// checkTailscaleSetup reports Tailscale readiness for both exposure modes. It
// only reports: enabling HTTPS certificates publishes this machine's name to
// public Certificate Transparency logs, so that stays the user's decision.
func checkTailscaleSetup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	state, err := tailscale.Detect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking Tailscale: %v\n", err)
		os.Exit(1)
	}

	if state.DNSName != "" {
		fmt.Printf("Machine: %s\n\n", state.DNSName)
	}

	if problem := state.Problem(); problem != "" {
		fmt.Printf("Tailscale Serve:  not ready\n  %s\n", problem)
	} else {
		fmt.Printf("Tailscale Serve:  ready\n  http://%s:%d\n",
			state.DNSName, tailscale.DefaultServePort)
	}

	if problem := state.FunnelProblem(); problem != "" {
		fmt.Printf("Tailscale Funnel: not ready\n  %s\n", problem)
	} else {
		fmt.Printf("Tailscale Funnel: ready\n  https://%s\n", state.DNSName)
	}

	if state.ServeInUse {
		fmt.Println("\nNote: this machine already publishes a serve configuration.")
	}

	if state.Problem() != "" {
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`helios - orchestrates AI coding agents on your machine

Usage:
  helios <command> [options]

Commands:
  start                 Start helios (daemon + tunnel + device pairing TUI)
  stop                  Stop daemon (tunnel stays alive)
  devices               Device management (TUI)
  new "prompt" [flags]  Launch Claude in a helios-managed terminal
                        --cwd PATH  Working directory (default: current)
  attach <session>      Attach this terminal to a session (^\ d to detach)
  wrap -- <cmd> [args]  Run a command in a helios-managed terminal
                        Example: helios wrap -- claude
  sessions              Interactive session manager (TUI)
                        --list  Plain table output

  daemon start [flags]  Start the helios daemon (with supervisor)
                        -d                Run in background (daemonize)
                        --internal-port P Internal port (default: 7654)
                        --public-port P   Public port (default: 7655)
  daemon stop           Stop the daemon (tunnel stays alive)
  daemon status         Show daemon and supervisor status

  tunnel status         Show tunnel status (works without daemon)
  tunnel stop           Stop the tunnel (prompts for confirmation)

  setup shell           Install shell wrapper (claude → helios wrap)
  setup tailscale       Check Tailscale readiness for Serve and Funnel

  auth init             Generate pairing QR (non-interactive)
  auth devices          List trusted devices
  auth revoke <kid>     Revoke a device

  logs [flags]          Show daemon and device logs
                        --tail N, -n N  Show last N lines (default: 50)
                        --daemon        Show only daemon logs
                        --device        Show only device logs

  hooks install         Install Claude Code hooks (global)
  hooks install --local Install hooks for current project
  hooks show            Print hook config JSON
  hooks remove          Remove helios hooks

  cleanup [target]      Remove helios data and start fresh
                        db     Remove database only
                        logs   Remove logs only
                        all    Remove everything (default)

  version               Show version
  help                  Show this help`)
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
