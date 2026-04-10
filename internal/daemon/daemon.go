package daemon

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/server"
	"github.com/kamrul1157024/helios/internal/store"
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
	srv := server.New(cfg.Server.Bind, cfg.Server.Port, db, mgr, cfg.Auth.Enabled, cfg.Auth.SkipLocal, FrontendFS)

	// Write PID file
	pidPath := filepath.Join(HeliosDir(), "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	defer os.Remove(pidPath)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Bind, cfg.Server.Port)
	fmt.Printf("helios daemon starting on %s\n", addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		fmt.Println("\nShutting down...")
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

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
