package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	maxRestarts    = 5
	restartWindow  = 5 * time.Minute
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// Supervisor runs the daemon and restarts it on failure.
type Supervisor struct {
	cfg *Config
}

func NewSupervisor(cfg *Config) *Supervisor {
	return &Supervisor{cfg: cfg}
}

// Run starts the daemon in a supervised loop.
// It restarts the daemon on panic/error with exponential backoff.
// It exits when receiving SIGTERM/SIGINT or when max restarts are exceeded.
func (s *Supervisor) Run() error {
	// Write supervisor PID file
	pidPath := filepath.Join(HeliosDir(), "supervisor.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("write supervisor pid: %w", err)
	}
	defer os.Remove(pidPath)

	// Set up log file for supervisor messages
	logsDir := filepath.Join(HeliosDir(), "logs")
	os.MkdirAll(logsDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logsDir, "supervisor.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open supervisor log: %w", err)
	}
	defer logFile.Close()
	slog := log.New(logFile, "", log.LstdFlags)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var restartTimes []time.Time
	backoff := initialBackoff

	slog.Printf("supervisor started (pid %d)", os.Getpid())

	for {
		select {
		case <-ctx.Done():
			slog.Printf("supervisor received shutdown signal")
			return nil
		default:
		}

		slog.Printf("starting daemon")
		errCh := make(chan error, 1)

		go func() {
			errCh <- Start(s.cfg)
		}()

		// Wait for daemon to exit or shutdown signal
		select {
		case <-ctx.Done():
			// Signal also reaches the daemon (same process), so it shuts down on its own.
			// Wait for Start() to return.
			slog.Printf("supervisor received shutdown signal, waiting for daemon to exit")
			<-errCh
			slog.Printf("daemon exited after signal")
			return nil
		case err := <-errCh:
			if err == nil {
				slog.Printf("daemon exited cleanly")
				return nil
			}
			slog.Printf("daemon exited with error: %v", err)
		}

		// Track restart times within the window
		now := time.Now()
		var recent []time.Time
		for _, t := range restartTimes {
			if now.Sub(t) < restartWindow {
				recent = append(recent, t)
			}
		}
		recent = append(recent, now)
		restartTimes = recent

		if len(restartTimes) >= maxRestarts {
			slog.Printf("daemon crashed %d times in %v, giving up", maxRestarts, restartWindow)
			return fmt.Errorf("daemon exceeded max restarts (%d in %v)", maxRestarts, restartWindow)
		}

		slog.Printf("restarting daemon in %v (restart %d/%d)", backoff, len(restartTimes), maxRestarts)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			slog.Printf("supervisor received shutdown signal during backoff")
			return nil
		}

		// Exponential backoff, capped
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// StopSupervisor sends SIGTERM to the supervisor process.
func StopSupervisor() error {
	pidPath := filepath.Join(HeliosDir(), "supervisor.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		// No supervisor running, fall back to direct daemon stop
		return Stop()
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		os.Remove(pidPath)
		return Stop()
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return Stop()
	}

	// Check if supervisor is actually alive
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidPath)
		return Stop()
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send signal to supervisor: %w", err)
	}

	// Wait for the supervisor to die (up to 10 seconds — it needs to stop daemon too)
	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			os.Remove(pidPath)
			fmt.Printf("helios stopped (supervisor pid %d)\n", pid)
			return nil
		}
	}

	// Force kill
	proc.Signal(syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	os.Remove(pidPath)
	fmt.Printf("helios killed (supervisor pid %d)\n", pid)
	return nil
}

// SupervisorStatus reports whether the supervisor is running.
func SupervisorStatus() (running bool, pid int) {
	pidPath := filepath.Join(HeliosDir(), "supervisor.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, 0
	}

	pid, err = strconv.Atoi(string(data))
	if err != nil {
		os.Remove(pidPath)
		return false, 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidPath)
		return false, 0
	}

	return true, pid
}

