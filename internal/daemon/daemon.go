package daemon

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/push"
	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tunnel"
)

// FrontendFS is set by main.go via go:embed
var FrontendFS fs.FS

func Start(cfg *Config) error {
	if err := os.MkdirAll(HeliosDir(), 0755); err != nil {
		return fmt.Errorf("create helios dir: %w", err)
	}

	db, err := store.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	mgr := notifications.NewManager(db)

	// Initialize Web Push
	vapidKeys, err := push.LoadOrGenerateVAPID(HeliosDir())
	if err != nil {
		return fmt.Errorf("init VAPID keys: %w", err)
	}
	pusher := push.NewSender(db, vapidKeys)

	// Shared state between both servers
	shared := server.NewShared(db, mgr, pusher)

	// Create tunnel manager
	tunnelMgr := tunnel.NewManager()
	server.TunnelManager = tunnelMgr

	// Create both servers
	internalSrv := server.NewInternalServer(cfg.Server.InternalPort, shared)
	publicSrv := server.NewPublicServer(cfg.Server.PublicPort, shared, FrontendFS)

	// Write PID file
	pidPath := filepath.Join(HeliosDir(), "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	defer os.Remove(pidPath)

	fmt.Printf("helios daemon starting\n")
	fmt.Printf("  internal: 127.0.0.1:%d (hooks + admin)\n", cfg.Server.InternalPort)
	fmt.Printf("  public:   0.0.0.0:%d (frontend + API)\n", cfg.Server.PublicPort)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Auto-start tunnel if configured
	if cfg.Tunnel.Provider != "" {
		go func() {
			url, err := tunnelMgr.Start(cfg.Tunnel.Provider, cfg.Tunnel.CustomURL, cfg.Server.PublicPort)
			if err != nil {
				log.Printf("tunnel auto-start failed: %v", err)
			} else {
				fmt.Printf("  tunnel:   %s (%s)\n", url, cfg.Tunnel.Provider)
			}
		}()
	}

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

	// Graceful shutdown
	shutdownCtx := context.Background()
	tunnelMgr.Stop()
	internalSrv.Shutdown(shutdownCtx)
	publicSrv.Shutdown(shutdownCtx)

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

	fmt.Printf("Sent stop signal to daemon (pid %d)\n", pid)
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
