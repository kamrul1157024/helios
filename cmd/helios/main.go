package main

import (
	"bytes"
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

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/tmux"
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
	case "notify":
		handleNotify()
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
				KID        string  `json:"kid"`
				Name       string  `json:"name"`
				Status     string  `json:"status"`
				Platform   string  `json:"platform"`
				Browser    string  `json:"browser"`
				PushEnabled bool   `json:"push_enabled"`
				LastSeenAt *string `json:"last_seen_at"`
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

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Spawn desktop notifier as a detached background process.
	spawnNotifier(exe, cfg.Server.InternalPort)

	// If already inside tmux, run the TUI directly in the current pane.
	if os.Getenv("TMUX") != "" {
		if err := tui.RunStart(cfg.Server.InternalPort, cfg.Server.PublicPort); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Outside tmux: open the TUI in a dedicated helios tmux window, then
	// attach — so the user lands inside tmux without any manual step.
	tc := tmux.NewClient()

	if _, err := tc.OpenWindow("helios", exe, "start"); err != nil {
		// tmux not available — fall back to running TUI directly.
		if err := tui.RunStart(cfg.Server.InternalPort, cfg.Server.PublicPort); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Replace this process with tmux attach — user is now inside tmux.
	if err := tc.AttachSession(); err != nil {
		fmt.Fprintf(os.Stderr, "Error attaching to tmux: %v\n", err)
		os.Exit(1)
	}
}

// spawnNotifier starts helios notify as a detached background process if one
// is not already running. The PID is written to ~/.helios/notify.pid.
func spawnNotifier(exe string, internalPort int) {
	pidPath := filepath.Join(daemon.HeliosDir(), "notify.pid")

	// Check if already running.
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				if proc.Signal(syscall.Signal(0)) == nil {
					return // already running
				}
			}
		}
		os.Remove(pidPath)
	}

	logPath := filepath.Join(daemon.HeliosDir(), "logs", "desktop-notif.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logFile = nil
	}

	portStr := strconv.Itoa(internalPort)
	cmd := exec.Command(exe, "notify", "--port", portStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return
	}
	if logFile != nil {
		logFile.Close()
	}

	os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	cmd.Process.Release()
}

// handleNotify runs the desktop notification subscriber (internal command).
func handleNotify() {
	port := 7654 // default
	for i, arg := range os.Args[2:] {
		if arg == "--port" && i+1 < len(os.Args[2:]) {
			if p, err := strconv.Atoi(os.Args[i+3]); err == nil {
				port = p
			}
		}
	}
	tui.RunNotifier(port)
}

func handleDevices() {
	cfg, _ := daemon.LoadConfig()
	if err := tui.RunDevices(cfg.Server.InternalPort); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleStop() {
	// Kill notifier if running.
	stopNotifier()

	// Stop daemon (or supervisor if running). Tunnel is left alive.
	if err := daemon.StopSupervisor(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func stopNotifier() {
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
	os.Remove(pidPath)
}

func handleTunnel(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios tunnel <stop|status>")
		os.Exit(1)
	}

	switch args[0] {
	case "status":
		state, err := tunnel.LoadState(daemon.HeliosDir())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if state == nil {
			fmt.Println("No tunnel running.")
			return
		}
		if !tunnel.IsProcessAlive(state.PID) {
			fmt.Println("No tunnel running (stale state, cleaning up).")
			tunnel.RemoveState(daemon.HeliosDir())
			return
		}
		uptime := time.Since(state.StartedAt).Truncate(time.Second)
		fmt.Printf("Tunnel active: %s (%s, PID %d, up %s)\n", state.URL, state.Provider, state.PID, uptime)

	case "stop":
		state, err := tunnel.LoadState(daemon.HeliosDir())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if state == nil {
			fmt.Println("No tunnel running.")
			return
		}
		if !tunnel.IsProcessAlive(state.PID) {
			fmt.Println("No tunnel running (stale state, cleaning up).")
			tunnel.RemoveState(daemon.HeliosDir())
			return
		}

		fmt.Printf("Tunnel is running: %s (%s, PID %d)\n\n", state.URL, state.Provider, state.PID)
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

		// Kill the tunnel process
		if err := tunnel.KillTunnel(daemon.HeliosDir()); err != nil {
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
		Success  bool   `json:"success"`
		TmuxPane string `json:"tmux_pane"`
		CWD      string `json:"cwd"`
		Message  string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if !result.Success {
		fmt.Fprintf(os.Stderr, "Failed to create session: %s\n", result.Message)
		os.Exit(1)
	}

	fmt.Printf("Session started in tmux pane %s\n", result.TmuxPane)
	fmt.Printf("  cwd: %s\n", result.CWD)
	fmt.Println("  Attach with: tmux attach -t helios")
}

func handleWrap(args []string) {
	// Find "--" separator
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

	command := strings.Join(args[cmdStart:], " ")
	cwd, _ := os.Getwd()

	tc := tmux.NewClient()

	// If already inside tmux, register this pane with the daemon, then exec
	if os.Getenv("TMUX") != "" {
		paneID := os.Getenv("TMUX_PANE")
		if paneID != "" {
			cfg, _ := daemon.LoadConfig()
			internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)
			body, _ := json.Marshal(map[string]string{
				"pane_id": paneID,
				"cwd":     cwd,
			})
			http.Post(internalURL+"/internal/wrap", "application/json", bytes.NewBuffer(body))
		}
		parts := args[cmdStart:]
		binary, err := exec.LookPath(parts[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "command not found: %s\n", parts[0])
			os.Exit(1)
		}
		syscall.Exec(binary, parts, os.Environ())
		return
	}

	// Create a new tmux window with the command
	paneID, err := tc.CreateWindow(cwd, command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create tmux session: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is tmux installed? brew install tmux")
		os.Exit(1)
	}

	// Notify the daemon about this pane so it can track it
	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)
	body, _ := json.Marshal(map[string]string{
		"pane_id": paneID,
		"cwd":     cwd,
	})
	http.Post(internalURL+"/internal/wrap", "application/json", bytes.NewBuffer(body))

	// Attach to the tmux session on the new pane
	if err := tc.Attach(paneID); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to attach to tmux: %v\n", err)
		os.Exit(1)
	}
}

func handleAttach(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: helios attach <pane-id|session-id>")
		os.Exit(1)
	}

	target := args[0]
	tc := tmux.NewClient()

	// If the target looks like a tmux pane ID (%N), attach directly.
	if strings.HasPrefix(target, "%") {
		if err := tc.Attach(target); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to attach to pane %s: %v\n", target, err)
			os.Exit(1)
		}
		return
	}

	// Otherwise treat it as a session ID — look up the pane from the daemon.
	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(internalURL + "/internal/sessions")
	if err != nil {
		fmt.Fprintln(os.Stderr, "helios is not running. Start it with: helios start")
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result struct {
		Sessions []struct {
			SessionID string  `json:"session_id"`
			TmuxPane  *string `json:"tmux_pane"`
		} `json:"sessions"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	for _, s := range result.Sessions {
		if s.SessionID == target || strings.HasPrefix(s.SessionID, target) {
			if s.TmuxPane == nil || *s.TmuxPane == "" {
				fmt.Fprintf(os.Stderr, "Session %s has no active tmux pane\n", target)
				os.Exit(1)
			}
			if err := tc.Attach(*s.TmuxPane); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to attach to pane %s: %v\n", *s.TmuxPane, err)
				os.Exit(1)
			}
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Session not found: %s\n", target)
	os.Exit(1)
}

func handleSessions(args []string) {
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
			TmuxPane    *string `json:"tmux_pane"`
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

	fmt.Printf("%-10s %-12s %-40s %-8s %s\n", "Session", "Status", "CWD", "Pane", "Last Activity")
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
		pane := "-"
		if s.TmuxPane != nil {
			pane = *s.TmuxPane
		}
		lastActivity := ""
		if s.LastEventAt != nil {
			t, err := time.Parse(time.RFC3339, *s.LastEventAt)
			if err == nil {
				lastActivity = humanDuration(time.Since(t))
			}
		}

		fmt.Printf("%-10s %-12s %-40s %-8s %s\n", sid, s.Status, cwdShort, pane, lastActivity)
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
		tunnel.KillTunnel(heliosDir)
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
		fmt.Fprintln(os.Stderr, "Usage: helios setup <shell|editor|all>")
		os.Exit(1)
	}

	switch args[0] {
	case "shell":
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

	case "editor":
		tmuxPath := findTmuxBinary()
		editors := daemon.DetectEditors()
		if len(editors) == 0 {
			fmt.Println("No supported editors found.")
			return
		}
		for _, editor := range editors {
			if editor.Configured {
				fmt.Printf("  ✓ %s — already configured\n", editor.Name)
				continue
			}
			if err := daemon.ConfigureEditor(editor, tmuxPath); err != nil {
				fmt.Printf("  ✗ %s — %v\n", editor.Name, err)
				fmt.Println(daemon.ManualEditorInstructions(editor, tmuxPath, err))
			} else {
				fmt.Printf("  ✓ %s — configured\n", editor.Name)
			}
		}

	case "all":
		// Shell
		info := daemon.DetectShell()
		if daemon.ShellWrapperInstalled(info) {
			fmt.Printf("  ✓ Shell wrapper (%s)\n", info.Name)
		} else if err := daemon.InstallShellWrapper(info); err != nil {
			fmt.Printf("  ✗ Shell wrapper — %v\n", err)
			fmt.Println(daemon.ManualShellInstructions(info, err))
		} else {
			fmt.Printf("  ✓ Shell wrapper installed (%s)\n", info.Name)
		}

		// Editors
		tmuxPath := findTmuxBinary()
		editors := daemon.DetectEditors()
		for _, editor := range editors {
			if editor.Configured {
				fmt.Printf("  ✓ %s — already configured\n", editor.Name)
				continue
			}
			if err := daemon.ConfigureEditor(editor, tmuxPath); err != nil {
				fmt.Printf("  ✗ %s — %v\n", editor.Name, err)
				fmt.Println(daemon.ManualEditorInstructions(editor, tmuxPath, err))
			} else {
				fmt.Printf("  ✓ %s — configured\n", editor.Name)
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown setup target: %s\nUsage: helios setup <shell|editor|all>\n", args[0])
		os.Exit(1)
	}
}

func findTmuxBinary() string {
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/usr/bin/tmux"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "tmux"
}

func printUsage() {
	fmt.Println(`helios - orchestrates AI coding agents on your machine

Usage:
  helios <command> [options]

Commands:
  start                 Start helios (daemon + tunnel + device pairing TUI)
  stop                  Stop daemon (tunnel stays alive)
  devices               Device management (TUI)
  new "prompt" [flags]  Launch Claude in a managed tmux pane
                        --cwd PATH  Working directory (default: current)
  wrap -- <cmd> [args]  Run a command in a helios-managed tmux pane
                        Example: helios wrap -- claude
  sessions              List all tracked sessions

  daemon start [flags]  Start the helios daemon (with supervisor)
                        -d                Run in background (daemonize)
                        --internal-port P Internal port (default: 7654)
                        --public-port P   Public port (default: 7655)
  daemon stop           Stop the daemon (tunnel stays alive)
  daemon status         Show daemon and supervisor status

  tunnel status         Show tunnel status (works without daemon)
  tunnel stop           Stop the tunnel (prompts for confirmation)

  setup shell           Install shell wrapper (claude → helios wrap)
  setup editor          Configure editor terminals to use tmux
  setup all             Install shell wrapper + configure editors

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
