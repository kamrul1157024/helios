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

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/discovery"
	"github.com/kamrul1157024/helios/internal/notifications"
	claude "github.com/kamrul1157024/helios/internal/provider/claude"
	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/terminal"
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

// TunnelProviderConfig projects the daemon configuration onto the tunnel
// package's view of it. The CLI needs the same projection to rebuild a running
// tunnel from its state file, which is why it is not inlined into startDaemon.
func TunnelProviderConfig(cfg *Config) tunnel.ProviderConfig {
	return tunnel.ProviderConfig{
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
		Tailscale: tunnel.TailscaleProviderConfig{
			Mode: cfg.Tunnel.Tailscale.Mode,
			Port: cfg.Tunnel.Tailscale.Port,
		},
	}
}

// resumeArgs builds the command that brings a cold session back.
//
// Empty argv means "resume" to the registry, and ptyhost has a hardcoded
// fallback for it. The daemon overrides that here because only the daemon can
// see the session's stored permission mode; without this, every wake would
// reset the mode to the default and undo the user's last switch.
//
// Returning nil is the safe answer for anything we cannot look up — ptyhost's
// fallback then applies, which is the behaviour that predates this.
func resumeArgs(db *store.Store, sessionID string) []string {
	sess, err := db.GetSession(sessionID)
	if err != nil || sess == nil || sess.Source != "claude" {
		return nil
	}
	mode := ""
	if sess.PermissionMode != nil {
		mode = *sess.PermissionMode
	}
	return claude.ResumeArgs(sessionID, mode)
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

	log.Printf("PATH: %s", importLoginPATH())

	db, err := store.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	mgr := notifications.NewManager(db)
	mgr.StartCleanup()

	// Register providers
	claude.Register(cfg.Server.InternalPort)

	// Terminal hosts are separate processes, so any that survived the last
	// daemon are still serving; adopt them before anything else looks at
	// session state.
	registry := terminal.NewRegistry(HeliosDir(), func(sessionID, cwd string, argv []string) error {
		if len(argv) == 0 {
			argv = resumeArgs(db, sessionID)
		}
		return terminal.SpawnHost(HeliosDir(), sessionID, cwd, argv)
	})
	if alive, cleaned, err := registry.Recover(); err != nil {
		log.Printf("terminal: recover: %v", err)
	} else {
		log.Printf("terminal: adopted %d live hosts, cleaned %d stale", alive, cleaned)
	}
	term := backend.NewHost(registry)
	defer term.Close()

	// Shared state between both servers
	shared := server.NewShared(db, mgr, term)

	// Give the claude action handlers access to session terminals
	claude.SetBackend(term)

	// Discover existing Claude sessions from transcript files
	go discovery.DiscoverClaudeSessions(db)

	// Create tunnel manager
	tunnelMgr := tunnel.NewManager(HeliosDir())
	tunnelMgr.SetProviderConfig(TunnelProviderConfig(cfg))

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
	server.OnTunnelConfigChanged = func(provider, customURL, tailscaleMode string) {
		cfg.Tunnel.Provider = provider
		cfg.Tunnel.CustomURL = customURL
		if provider == "tailscale" && tailscaleMode != "" {
			// The port is mode-specific, so a stale one must not survive a
			// switch between serve and funnel.
			cfg.Tunnel.Tailscale.Mode = tailscaleMode
			cfg.Tunnel.Tailscale.Port = 0
		}
		SaveConfig(cfg)
	}

	// Create both servers
	publicBind := ResolvePublicBind(cfg)
	server.PublicBind = publicBind
	internalSrv := server.NewInternalServer(cfg.Server.InternalPort, shared)
	publicSrv := server.NewPublicServer(publicBind, cfg.Server.PublicPort, shared)

	// Watch newly launched sessions for the workspace-trust dialog
	server.StartTrustWatcher(shared)

	// Write PID file
	pidPath := filepath.Join(HeliosDir(), "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	// Only if it is still ours. A daemon that failed to bind used to remove the
	// file on its way out, taking the running daemon's pid with it — which is
	// why the machine that logged this had a live daemon and no pid file.
	defer func() {
		if pidFromFile() == os.Getpid() {
			os.Remove(pidPath)
		}
	}()

	log.Printf("helios daemon starting")
	log.Printf("  internal: 127.0.0.1:%d (hooks + admin)", cfg.Server.InternalPort)
	log.Printf("  public:   %s:%d (frontend + API)", publicBind, cfg.Server.PublicPort)
	fmt.Printf("helios daemon starting\n")
	fmt.Printf("  internal: 127.0.0.1:%d (hooks + admin)\n", cfg.Server.InternalPort)
	fmt.Printf("  public:   %s:%d (frontend + API)\n", publicBind, cfg.Server.PublicPort)

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

	// Periodic stale terminal reaper. Twenty minutes, not ten seconds: probing
	// sockets and re-reading transcripts costs more the more sessions exist.
	go func() {
		ticker := time.NewTicker(20 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reapStaleSessions(db, term, shared.SSE)
			case <-ctx.Done():
				return
			}
		}
	}()

	// The memory budget is checked on its own, faster clock. It reads a map the
	// host already keeps and one query, so it costs nothing next to the reaper
	// above — and sharing the reaper's tick meant the machine could sit nineteen
	// minutes over budget while the user watched it swap.
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				evictOverBudget(db, term, shared.SSE)
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
	var srvErr error
	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down...")
	case srvErr = <-errCh:
		fmt.Printf("Server error: %v\n", srvErr)
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
	return srvErr
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
