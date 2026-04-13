package daemon

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/kamrul1157024/helios/internal/discovery"
	"github.com/kamrul1157024/helios/internal/notifications"
	claude "github.com/kamrul1157024/helios/internal/provider/claude"
	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
	"github.com/kamrul1157024/helios/internal/tunnel"
)

func Start(cfg *Config) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
			err = fmt.Errorf("daemon panicked: %v", r)
		}
	}()

	return startDaemon(cfg)
}

func startDaemon(cfg *Config) error {
	if err := os.MkdirAll(HeliosDir(), 0755); err != nil {
		return fmt.Errorf("create helios dir: %w", err)
	}

	// Set up logs directory and daemon log file
	logsDir := filepath.Join(HeliosDir(), "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}
	server.LogsDir = logsDir

	daemonLogPath := filepath.Join(logsDir, "daemon.log")
	logFile, err := os.OpenFile(daemonLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	db, err := store.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	mgr := notifications.NewManager(db)
	mgr.StartCleanup()

	// Register providers
	claude.Register()

	// Shared state between both servers
	shared := server.NewShared(db, mgr)

	// Give the claude action handlers access to the tmux client
	claude.SetTmux(shared.Tmux)

	// Discover existing Claude sessions from transcript files + tmux
	go discovery.DiscoverClaudeSessions(db, tmux.NewClient())

	// Create tunnel manager
	tunnelMgr := tunnel.NewManager(HeliosDir())
	tunnelMgr.SetProviderConfig(tunnel.ProviderConfig{
		Zrok: tunnel.ZrokProviderConfig{
			ShareMode:  cfg.Tunnel.Zrok.ShareMode,
			ShareToken: cfg.Tunnel.Zrok.ShareToken,
		},
		Localtunnel: tunnel.LocaltunnelProviderConfig{
			Subdomain: cfg.Tunnel.Localtunnel.Subdomain,
			Host:      cfg.Tunnel.Localtunnel.Host,
		},
		LocalhostRun: tunnel.LocalhostRunProviderConfig{
			SSHUser:           cfg.Tunnel.LocalhostRun.SSHUser,
			CustomDomain:      cfg.Tunnel.LocalhostRun.CustomDomain,
			KeepaliveInterval: cfg.Tunnel.LocalhostRun.KeepaliveInterval,
			UseAutossh:        cfg.Tunnel.LocalhostRun.UseAutossh,
		},
		Localxpose: tunnel.LocalxposeProviderConfig{
			Subdomain:      cfg.Tunnel.Localxpose.Subdomain,
			ReservedDomain: cfg.Tunnel.Localxpose.ReservedDomain,
			Region:         cfg.Tunnel.Localxpose.Region,
			BasicAuth:      cfg.Tunnel.Localxpose.BasicAuth,
			AccessToken:    cfg.Tunnel.Localxpose.AccessToken,
		},
	})

	// Persist zrok reserved share tokens to config.yaml
	tunnelMgr.OnZrokTokenCreated = func(token string) {
		cfg.Tunnel.Zrok.ShareToken = token
		SaveConfig(cfg)
	}

	// Persist localtunnel subdomain assignments to config.yaml
	tunnelMgr.OnLocaltunnelSubdomainAssigned = func(subdomain string) {
		cfg.Tunnel.Localtunnel.Subdomain = subdomain
		SaveConfig(cfg)
	}

	server.TunnelManager = tunnelMgr

	// Persist tunnel config changes to config.yaml
	server.OnTunnelConfigChanged = func(provider, customURL string) {
		cfg.Tunnel.Provider = provider
		cfg.Tunnel.CustomURL = customURL
		SaveConfig(cfg)
	}

	// Create both servers
	internalSrv := server.NewInternalServer(cfg.Server.InternalPort, shared)
	publicSrv := server.NewPublicServer(cfg.Server.PublicPort, shared)

	// Start pane watcher for trust prompt detection
	server.StartPaneWatcher(shared)

	// Write PID file
	pidPath := filepath.Join(HeliosDir(), "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	defer os.Remove(pidPath)

	log.Printf("helios daemon starting")
	log.Printf("  internal: 127.0.0.1:%d (hooks + admin)", cfg.Server.InternalPort)
	log.Printf("  public:   0.0.0.0:%d (frontend + API)", cfg.Server.PublicPort)
	fmt.Printf("helios daemon starting\n")
	fmt.Printf("  internal: 127.0.0.1:%d (hooks + admin)\n", cfg.Server.InternalPort)
	fmt.Printf("  public:   0.0.0.0:%d (frontend + API)\n", cfg.Server.PublicPort)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Try to adopt an existing tunnel from a previous daemon run
	// If none found and a provider is configured, start a new one
	go func() {
		if url, err := tunnelMgr.Adopt(); err != nil {
			log.Printf("tunnel adopt failed: %v", err)
		} else if url != "" {
			log.Printf("tunnel adopted: %s", url)
			fmt.Printf("  tunnel:   %s (adopted)\n", url)
			return
		}

		if cfg.Tunnel.Provider != "" {
			url, err := tunnelMgr.Start(cfg.Tunnel.Provider, cfg.Tunnel.CustomURL, cfg.Server.PublicPort)
			if err != nil {
				log.Printf("tunnel auto-start failed: %v", err)
			} else {
				log.Printf("tunnel started: %s (%s)", url, cfg.Tunnel.Provider)
				fmt.Printf("  tunnel:   %s (%s)\n", url, cfg.Tunnel.Provider)
			}
		}
	}()

	// Periodic cleanup of expired pairing tokens
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				db.CleanExpiredPairingTokens()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Periodic stale session reaper
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		tmuxClient := tmux.NewClient()
		for {
			select {
			case <-ticker.C:
				reapStaleSessions(db, tmuxClient, shared.SSE)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start both servers
	errCh := make(chan error, 2)

	go func() {
		if err := internalSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("internal server: %w", err)
		}
	}()

	go func() {
		if err := publicSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("public server: %w", err)
		}
	}()

	// Wait for shutdown or error
	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down...")
	case err := <-errCh:
		fmt.Printf("Server error: %v\n", err)
	}

	// Graceful shutdown (3 second timeout to avoid hanging on open SSE connections)
	// Tunnel is NOT stopped — it keeps running independently
	log.Printf("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	internalSrv.Shutdown(shutdownCtx)
	publicSrv.Shutdown(shutdownCtx)

	log.Printf("helios daemon stopped")
	fmt.Println("helios daemon stopped")
	return nil
}

func Stop() error {
	pidPath := filepath.Join(HeliosDir(), "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("daemon not running (no pid file)")
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return fmt.Errorf("invalid pid file")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send signal: %w", err)
	}

	// Wait for the process to actually die (up to 5 seconds)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process is gone
			os.Remove(pidPath)
			fmt.Printf("helios daemon stopped (pid %d)\n", pid)
			return nil
		}
	}

	// Force kill if still alive
	proc.Signal(syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	os.Remove(pidPath)
	fmt.Printf("helios daemon killed (pid %d)\n", pid)
	return nil
}

func Status() error {
	pidPath := filepath.Join(HeliosDir(), "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("helios daemon is not running")
		return nil
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		fmt.Println("helios daemon is not running (invalid pid)")
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("helios daemon is not running")
		return nil
	}

	// Signal 0 checks if process exists
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		fmt.Println("helios daemon is not running (stale pid)")
		os.Remove(pidPath)
		return nil
	}

	fmt.Printf("helios daemon is running (pid %d)\n", pid)
	return nil
}
