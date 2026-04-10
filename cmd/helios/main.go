package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/daemon"
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
	case "show":
		handleShow()
	case "setup":
		handleSetup()
	case "devices":
		handleDevices()
	case "daemon":
		handleDaemon(os.Args[2:])
	case "auth":
		handleAuth(os.Args[2:])
	case "hooks":
		handleHooks(os.Args[2:])
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
		// Generate a new pairing QR via internal API
		client := &http.Client{Timeout: 3 * time.Second}
		tunnelURL := getTunnelURL(client, internalURL)
		if tunnelURL == "" {
			fmt.Println()
			fmt.Println("  No tunnel active. Start a tunnel to connect devices:")
			fmt.Println("    helios tunnel start --provider ngrok")
			fmt.Println()
			return
		}
		fmt.Println()
		fmt.Println("  Helios Device Pairing")
		fmt.Println("  ---------------------")
		fmt.Println()
		showPairingQR(client, internalURL, tunnelURL)
		fmt.Println()

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

	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)

	// Check if already running
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(internalURL + "/internal/health")
	if err == nil {
		resp.Body.Close()
		fmt.Println("helios is already running")
		fmt.Printf("  internal: 127.0.0.1:%d\n", cfg.Server.InternalPort)
		fmt.Printf("  public:   0.0.0.0:%d\n", cfg.Server.PublicPort)

		// Show tunnel status
		resp, err = client.Get(internalURL + "/internal/tunnel/status")
		if err == nil {
			defer resp.Body.Close()
			var ts struct {
				Active    bool   `json:"active"`
				PublicURL string `json:"public_url"`
				Provider  string `json:"provider"`
			}
			json.NewDecoder(resp.Body).Decode(&ts)
			if ts.Active {
				fmt.Printf("  tunnel:   %s (%s)\n", ts.PublicURL, ts.Provider)
			} else {
				fmt.Println("  tunnel:   not active")
			}
		}

		// Show devices + QR
		fmt.Println()
		printDevices(client, internalURL)
		return
	}

	// Start daemon in background
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	proc, err := os.StartProcess(exe, []string{exe, "daemon", "start"}, &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, nil, nil},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
		os.Exit(1)
	}
	proc.Release()

	// Wait for daemon to be ready
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		resp, err := client.Get(internalURL + "/internal/health")
		if err == nil {
			resp.Body.Close()
			break
		}
	}

	// Install hooks if missing
	daemon.InstallHooksIfMissing()

	fmt.Println("helios started")
	fmt.Printf("  internal: 127.0.0.1:%d\n", cfg.Server.InternalPort)
	fmt.Printf("  public:   0.0.0.0:%d\n", cfg.Server.PublicPort)

	// Check tunnel status
	if cfg.Tunnel.Provider != "" {
		// Give tunnel a moment to start
		time.Sleep(2 * time.Second)
		resp, err := client.Get(internalURL + "/internal/tunnel/status")
		if err == nil {
			defer resp.Body.Close()
			var ts struct {
				Active    bool   `json:"active"`
				PublicURL string `json:"public_url"`
				Provider  string `json:"provider"`
			}
			json.NewDecoder(resp.Body).Decode(&ts)
			if ts.Active {
				fmt.Printf("  tunnel:   %s (%s)\n", ts.PublicURL, ts.Provider)
			} else {
				fmt.Printf("  tunnel:   starting (%s)...\n", cfg.Tunnel.Provider)
			}
		}
	}

	// Show devices + QR
	fmt.Println()
	printDevices(client, internalURL)
}

func handleShow() {
	cfg, _ := daemon.LoadConfig()
	internalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.InternalPort)
	client := &http.Client{Timeout: 3 * time.Second}

	// Daemon status
	resp, err := client.Get(internalURL + "/internal/health")
	if err != nil {
		fmt.Println("helios is not running. Run: helios start")
		return
	}
	resp.Body.Close()
	fmt.Println("helios is running")
	fmt.Printf("  internal: 127.0.0.1:%d\n", cfg.Server.InternalPort)
	fmt.Printf("  public:   0.0.0.0:%d\n", cfg.Server.PublicPort)

	// Tunnel status
	resp, err = client.Get(internalURL + "/internal/tunnel/status")
	if err == nil {
		defer resp.Body.Close()
		var ts struct {
			Active    bool   `json:"active"`
			PublicURL string `json:"public_url"`
			Provider  string `json:"provider"`
		}
		json.NewDecoder(resp.Body).Decode(&ts)
		if ts.Active {
			fmt.Printf("  tunnel:   %s (%s)\n", ts.PublicURL, ts.Provider)
		} else {
			fmt.Println("  tunnel:   not active")
		}
	}

	// Devices
	fmt.Println()
	printDevices(client, internalURL)
}

func printDevices(client *http.Client, internalURL string) {
	resp, err := client.Get(internalURL + "/internal/device/list")
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Could not fetch devices")
		return
	}
	defer resp.Body.Close()

	type deviceEntry struct {
		KID         string  `json:"kid"`
		Name        string  `json:"name"`
		Status      string  `json:"status"`
		Platform    string  `json:"platform"`
		Browser     string  `json:"browser"`
		PushEnabled bool    `json:"push_enabled"`
		LastSeenAt  *string `json:"last_seen_at"`
	}
	var result struct {
		Devices []deviceEntry `json:"devices"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	// Filter to non-revoked devices
	var devices []deviceEntry
	for _, d := range result.Devices {
		if d.Status != "revoked" {
			devices = append(devices, d)
		}
	}

	if len(devices) > 0 {
		fmt.Printf("  %d device(s):\n", len(devices))
		for _, d := range devices {
			name := d.Name
			if name == "" {
				name = "(unnamed)"
			}
			lastSeen := "never"
			if d.LastSeenAt != nil {
				t, parseErr := time.Parse(time.RFC3339, *d.LastSeenAt)
				if parseErr == nil {
					lastSeen = humanDuration(time.Since(t))
				}
			}
			pushStr := "off"
			if d.PushEnabled {
				pushStr = "on"
			}
			icon := "+"
			if d.Status == "active" {
				icon = "*"
			}
			fmt.Printf("    %s %-20s  %-8s  push:%s  seen:%s  [%s]\n", icon, name, d.Platform, pushStr, lastSeen, d.KID)
		}
	} else {
		fmt.Println("  No devices registered.")
	}

	// Check tunnel status — QRs require an active tunnel
	tunnelURL := getTunnelURL(client, internalURL)
	if tunnelURL == "" {
		fmt.Println()
		fmt.Println("  No tunnel active. Start a tunnel to connect devices:")
		fmt.Println("    helios tunnel start --provider ngrok")
		return
	}

	// QR 1: Download page (tunnel URL)
	fmt.Println()
	fmt.Println("  Download the app:")
	fmt.Println()
	auth.PrintQR(tunnelURL)
	fmt.Printf("  %s\n", tunnelURL)

	// QR 2: Device pairing
	fmt.Println()
	fmt.Println("  Pair a new device:")
	fmt.Println()
	showPairingQR(client, internalURL, tunnelURL)
}

func getTunnelURL(client *http.Client, internalURL string) string {
	resp, err := client.Get(internalURL + "/internal/tunnel/status")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var ts struct {
		Active    bool   `json:"active"`
		PublicURL string `json:"public_url"`
	}
	json.NewDecoder(resp.Body).Decode(&ts)
	if ts.Active && ts.PublicURL != "" {
		return ts.PublicURL
	}
	return ""
}

func showPairingQR(client *http.Client, internalURL string, tunnelURL string) {
	resp, err := client.Post(internalURL+"/internal/device/create", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Could not generate pairing QR")
		return
	}
	defer resp.Body.Close()

	var result struct {
		Key string `json:"key"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	payload := fmt.Sprintf("helios://setup?key=%s&server=%s", result.Key, tunnelURL)
	auth.PrintQR(payload)
	fmt.Printf("  %s\n", payload)
}


func handleSetup() {
	cfg, _ := daemon.LoadConfig()
	if err := tui.RunSetup(cfg.Server.InternalPort, cfg.Server.PublicPort); err != nil {
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

func printUsage() {
	fmt.Println(`helios - orchestrates AI coding agents on your machine

Usage:
  helios <command> [subcommand] [options]

Commands:
  start                 Start daemon, install hooks, show QR
  stop                  Stop everything (tunnel + daemon)
  show                  Show status, devices, and setup QR code
  setup                 Interactive setup wizard (TUI)
  devices               Device management (TUI)

  daemon start [flags]  Start the helios daemon
                        -d                Run in background (daemonize)
                        --internal-port P Internal port (default: 7654)
                        --public-port P   Public port (default: 7655)
  daemon stop           Stop the running daemon
  daemon status         Show daemon status

  auth init             Generate device keypair and show QR code
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
