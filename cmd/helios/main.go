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
	case "auth":
		handleAuth(os.Args[2:])
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
		if err := daemon.Start(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := daemon.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "status":
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
	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	// Stop tunnel if running
	resp, err := http.Post(internalURL+"/internal/tunnel/stop", "application/json", bytes.NewBufferString("{}"))
	if err == nil {
		resp.Body.Close()
		fmt.Println("Tunnel stopped")
	}

	// Stop daemon
	if err := daemon.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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

	// If already inside tmux, just exec the command directly
	if os.Getenv("TMUX") != "" {
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
		// Stop daemon first if running
		_ = daemon.Stop()

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
  stop                  Stop daemon and tunnel
  devices               Device management (TUI)
  new "prompt" [flags]  Launch Claude in a managed tmux pane
                        --cwd PATH  Working directory (default: current)
  wrap -- <cmd> [args]  Run a command in a helios-managed tmux pane
                        Example: helios wrap -- claude
  sessions              List all tracked sessions

  daemon start [flags]  Start the helios daemon directly
                        -d                Run in background (daemonize)
                        --internal-port P Internal port (default: 7654)
                        --public-port P   Public port (default: 7655)
  daemon stop           Stop the running daemon
  daemon status         Show daemon status

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
