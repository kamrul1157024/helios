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

	helios "github.com/kamrul1157024/helios"
	"github.com/kamrul1157024/helios/internal/auth"
	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/tui"
)

const version = "0.2.0"

func init() {
	daemon.FrontendFS = helios.FrontendFS()
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
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
		// Create device via internal API
		resp, err := http.Post(internalURL+"/internal/device/create", "application/json", bytes.NewBufferString("{}"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: daemon not running? %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result struct {
			KID      string `json:"kid"`
			Key      string `json:"key"`
			SetupURL string `json:"setup_url"`
		}
		json.NewDecoder(resp.Body).Decode(&result)

		payload := fmt.Sprintf("helios://setup?key=%s&kid=%s&v=1", result.Key, result.KID)

		fmt.Println()
		fmt.Println("  Helios Device Setup")
		fmt.Println("  -------------------")
		fmt.Println()
		fmt.Println("  Scan this QR code with your phone:")
		fmt.Println()

		if result.SetupURL != "" {
			// QR encodes the full HTTPS setup URL for the tunnel
			if err := auth.PrintQR(result.SetupURL); err != nil {
				fmt.Printf("  (QR generation failed: %v)\n", err)
			}
			fmt.Println()
			fmt.Printf("  Setup URL: %s\n", result.SetupURL)
		} else {
			// No tunnel — encode the helios:// payload
			if err := auth.PrintQR(payload); err != nil {
				fmt.Printf("  (QR generation failed: %v)\n", err)
			}
			fmt.Println()
			fmt.Println("  Or copy this setup string:")
			fmt.Printf("  %s\n", payload)
		}

		fmt.Println()
		fmt.Printf("  Key ID: %s\n", result.KID)
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
