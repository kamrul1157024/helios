package desktop

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// System is everything the installer does to the machine. The steps go
// through it rather than through os/exec, so a test can run every step against
// a fake and check that a finished step does nothing the second time.
//
// Every command is an argv. Nothing here goes through a shell, and nothing
// from a request is ever part of an argv: the steps build their commands from
// constants and from the user's own name.
type System interface {
	// Run runs a command as the user and returns its combined output.
	Run(ctx context.Context, argv ...string) (string, error)
	// Root runs a command as root, through sudo.
	Root(ctx context.Context, argv ...string) (string, error)
	// ReadFile reads a file the user can read. Everything Helios writes as
	// root is world-readable, so checking a step needs no root.
	ReadFile(path string) ([]byte, error)
	// WriteFile writes a file as the user, creating its directory.
	WriteFile(path string, data []byte, mode os.FileMode) error
	// WriteRootFile writes a file owned by root with the given mode.
	WriteRootFile(ctx context.Context, path string, data []byte, mode os.FileMode) error
	// CanSudo reports whether Root would work. The daemon has no terminal, so
	// for it that means passwordless sudo.
	CanSudo(ctx context.Context) bool
	// SetOutput sends each line a command prints to fn, so the install can
	// show the last one as it goes. Nil stops it.
	SetOutput(fn func(line string))
}

// HostSystem is the real machine.
type HostSystem struct {
	// Interactive lets sudo ask for a password on the terminal. The daemon
	// leaves it false and gets `sudo -n`, which fails rather than waits.
	Interactive bool

	mu     sync.Mutex
	output func(string)
}

// NewHostSystem returns the System for this machine.
func NewHostSystem(interactive bool) *HostSystem {
	return &HostSystem{Interactive: interactive}
}

// SetOutput implements System.
func (h *HostSystem) SetOutput(fn func(string)) {
	h.mu.Lock()
	h.output = fn
	h.mu.Unlock()
}

func (h *HostSystem) emit(line string) {
	h.mu.Lock()
	fn := h.output
	h.mu.Unlock()
	if fn != nil {
		fn(line)
	}
}

// Run implements System.
func (h *HostSystem) Run(ctx context.Context, argv ...string) (string, error) {
	return h.exec(ctx, argv)
}

// Root implements System.
func (h *HostSystem) Root(ctx context.Context, argv ...string) (string, error) {
	return h.exec(ctx, append(h.sudo(), argv...))
}

func (h *HostSystem) sudo() []string {
	if h.Interactive {
		return []string{"sudo", "--"}
	}
	return []string{"sudo", "-n", "--"}
}

// CanSudo implements System.
func (h *HostSystem) CanSudo(ctx context.Context) bool {
	if h.Interactive {
		return true
	}
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}

func (h *HostSystem) exec(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if h.Interactive {
		// sudo asks on the terminal itself, but a command it runs may still
		// want stdin; the CLI hands over its own.
		cmd.Stdin = os.Stdin
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			out.WriteString(line)
			out.WriteByte('\n')
			if strings.TrimSpace(line) != "" {
				h.emit(line)
			}
		}
		// Drain whatever is left so the command never blocks on a full pipe.
		io.Copy(io.Discard, pr)
	}()
	err := cmd.Run()
	pw.Close()
	<-done
	if err != nil {
		return out.String(), fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return out.String(), nil
}

// ReadFile implements System.
func (h *HostSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile implements System.
func (h *HostSystem) WriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// WriteRootFile implements System. The content goes to a private temp file
// first and `install` copies it into place, which sets owner and mode in the
// same step, so the file is never briefly writable by the user.
func (h *HostSystem) WriteRootFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	dir, err := os.MkdirTemp("", "helios-desktop-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("stage %s: %w", path, err)
	}
	if _, err := h.Root(ctx, "install", "-D", "-o", "root", "-g", "root",
		"-m", strconv.FormatUint(uint64(mode.Perm()), 8), tmp, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}

// fileExists reports whether a path exists for sys. A read error other than
// "not there" counts as present, so a check never decides to overwrite a file
// it merely could not read.
func fileExists(sys System, path string) bool {
	_, err := sys.ReadFile(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
