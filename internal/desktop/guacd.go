package desktop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// guacd runs as a child of the daemon, as the user, bound to loopback (D3).
// Only the daemon talks to it, so its port is whatever is free from 4822 up.

const (
	guacdBasePort = 4822
	guacdPortSpan = 100
	// guacdReadyTimeout is how long a fresh guacd gets to start listening.
	guacdReadyTimeout = 10 * time.Second
	guacdMinBackoff   = time.Second
	guacdMaxBackoff   = 30 * time.Second
	// guacdStableAfter is the uptime after which a crash counts as new rather
	// than as part of a crash loop, and the back-off starts over.
	guacdStableAfter = time.Minute
)

// supervisor keeps one guacd alive.
type supervisor struct {
	dir     string // the unpacked bundle
	logPath string

	mu      sync.Mutex
	addr    string
	cancel  context.CancelFunc
	stopped chan struct{}
}

func newSupervisor(dir, logPath string) *supervisor {
	return &supervisor{dir: dir, logPath: logPath}
}

// Addr is guacd's address, or "" when it is not running.
func (s *supervisor) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Start launches guacd and waits until it accepts connections. A second call
// while it runs is a no-op.
func (s *supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	cmd, addr, err := s.launch()
	if err != nil {
		return err
	}
	if err := waitListening(ctx, addr, guacdReadyTimeout); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("guacd did not start listening on %s: %w (see %s)", addr, err, s.logPath)
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	s.mu.Lock()
	s.addr, s.cancel, s.stopped = addr, cancel, stopped
	s.mu.Unlock()
	go s.watch(loopCtx, cmd, stopped)
	return nil
}

// Stop ends guacd and its restart loop.
func (s *supervisor) Stop() {
	s.mu.Lock()
	cancel, stopped := s.cancel, s.stopped
	s.cancel, s.addr = nil, ""
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-stopped
}

// watch waits for guacd to exit and starts it again, backing off while it
// keeps crashing. Viewers already connected lose their stream either way; the
// next one gets a fresh guacd.
func (s *supervisor) watch(ctx context.Context, cmd *exec.Cmd, stopped chan struct{}) {
	defer close(stopped)
	backoff := guacdMinBackoff
	for {
		started := time.Now()
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			<-exited
			return
		case err := <-exited:
			log.Printf("desktop: guacd exited: %v", err)
		}
		if time.Since(started) > guacdStableAfter {
			backoff = guacdMinBackoff
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, guacdMaxBackoff)

		next, addr, err := s.launch()
		if err != nil {
			log.Printf("desktop: restart guacd: %v", err)
			continue
		}
		if err := waitListening(ctx, addr, guacdReadyTimeout); err != nil {
			log.Printf("desktop: restarted guacd is not listening: %v", err)
		}
		s.mu.Lock()
		s.addr = addr
		s.mu.Unlock()
		cmd = next
	}
}

// launch starts one guacd on the first free port.
func (s *supervisor) launch() (*exec.Cmd, string, error) {
	port, err := freePort(guacdBasePort, guacdPortSpan)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(s.logPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(s.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open guacd log: %w", err)
	}
	defer logFile.Close()

	bin := filepath.Join(s.dir, "sbin", "guacd")
	// -f keeps it in the foreground, so it is our child and its exit is ours
	// to see; -b pins it to loopback, which is the security boundary (D2).
	cmd := exec.Command(bin, "-f", "-b", "127.0.0.1", "-l", strconv.Itoa(port), "-L", "info")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = childProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start guacd: %w", err)
	}
	return cmd, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

// freePort returns the first port from base that nothing listens on.
func freePort(base, span int) (int, error) {
	for port := base; port < base+span; port++ {
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		l.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free port for guacd in %d-%d", base, base+span-1)
}

var errNotListening = errors.New("not listening")

// waitListening polls addr until a TCP connect succeeds.
func waitListening(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return errNotListening
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
