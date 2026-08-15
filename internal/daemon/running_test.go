package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// freePort takes a port and hands it back, so a test can name one nothing is
// listening on.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func configOn(internal, public int) *Config {
	cfg := DefaultConfig()
	cfg.Server.InternalPort = internal
	cfg.Server.PublicPort = public
	return cfg
}

func TestAlreadyRunning_NothingListening(t *testing.T) {
	cfg := configOn(freePort(t), freePort(t))

	if _, running := AlreadyRunning(cfg); running {
		t.Error("reported a daemon with both ports free")
	}
}

// The conflict this exists to catch: the internal port taken by a daemon that
// is already up.
func TestAlreadyRunning_InternalPortTaken(t *testing.T) {
	cfg := configOn(freePort(t), freePort(t))

	holder, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Server.InternalPort))
	if err != nil {
		t.Fatalf("hold the port: %v", err)
	}
	defer holder.Close()

	if _, running := AlreadyRunning(cfg); !running {
		t.Error("did not notice the internal port was taken")
	}
}

// Either port is enough. The daemon needs both, so binding one of them is
// already a failed start.
func TestAlreadyRunning_PublicPortTaken(t *testing.T) {
	cfg := configOn(freePort(t), freePort(t))

	holder, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Server.PublicPort))
	if err != nil {
		t.Fatalf("hold the port: %v", err)
	}
	defer holder.Close()

	if _, running := AlreadyRunning(cfg); !running {
		t.Error("did not notice the public port was taken")
	}
}

func TestAlreadyRunning_NamesThePidWhenItCan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".helios"), 0o755); err != nil {
		t.Fatalf("make helios dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".helios", "daemon.pid"), []byte("4321\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	cfg := configOn(freePort(t), freePort(t))
	holder, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Server.InternalPort))
	if err != nil {
		t.Fatalf("hold the port: %v", err)
	}
	defer holder.Close()

	pid, running := AlreadyRunning(cfg)
	if !running {
		t.Fatal("did not notice the port was taken")
	}
	if pid != 4321 {
		t.Errorf("pid: got %d, want 4321", pid)
	}
	if msg := RunningError(pid, cfg).Error(); !strings.Contains(msg, "4321") || !strings.Contains(msg, "helios daemon stop") {
		t.Errorf("message does not say which daemon or how to stop it: %q", msg)
	}
}

// Something else on the port is still a reason not to start, and the message
// has to say so without inventing a pid.
func TestRunningError_WithoutAPid(t *testing.T) {
	cfg := configOn(7654, 7655)

	msg := RunningError(0, cfg).Error()
	if !strings.Contains(msg, strconv.Itoa(cfg.Server.InternalPort)) {
		t.Errorf("message does not name the port: %q", msg)
	}
	if strings.Contains(msg, "pid") {
		t.Errorf("message claims a pid it does not have: %q", msg)
	}
}
