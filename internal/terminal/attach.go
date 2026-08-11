package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// DefaultDetachKey is the prefix that returns control to the shell. C-b is
// deliberately avoided: users often run Helios inside their own tmux, where
// C-b is already spoken for.
const DefaultDetachKey byte = 0x1c // Ctrl-\

// AttachConfig configures an interactive viewer on the local machine.
type AttachConfig struct {
	// Socket is the host's unix socket path.
	Socket string
	// Name is advisory and drives the writer indicator other viewers see.
	Name string
	// In and Out default to stdin and stdout.
	In  *os.File
	Out *os.File
	// DetachKey overrides DefaultDetachKey.
	DetachKey byte
}

// AttachResult reports how an attach session ended.
type AttachResult struct {
	// Detached is true when the user left the session running.
	Detached bool
	// ExitCode is the hosted process's status, meaningful only when the
	// session ended on its own.
	ExitCode int
}

// Attach runs a full-screen interactive viewer against a terminal host.
//
// Detaching leaves the host running, which is the whole point: the session
// survives, and the same terminal can be picked up again from here, from
// another machine, or from the desktop app.
func Attach(ctx context.Context, cfg AttachConfig) (res AttachResult, err error) {
	in, out := cfg.In, cfg.Out
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	detachKey := cfg.DetachKey
	if detachKey == 0 {
		detachKey = DefaultDetachKey
	}

	inFd, outFd := int(in.Fd()), int(out.Fd())
	if !term.IsTerminal(inFd) || !term.IsTerminal(outFd) {
		return AttachResult{}, errors.New("terminal: attach needs a terminal on stdin and stdout")
	}
	cols, rows, err := term.GetSize(outFd)
	if err != nil {
		return AttachResult{}, fmt.Errorf("read terminal size: %w", err)
	}

	client, err := Dial(cfg.Socket, Hello{
		Role: RoleInteractive, Cols: cols, Rows: rows, Name: cfg.Name,
	})
	if err != nil {
		return AttachResult{}, err
	}
	defer client.Close()

	// Raw mode: every keystroke belongs to the hosted process, including the
	// ones the local shell would otherwise act on.
	state, err := term.MakeRaw(inFd)
	if err != nil {
		return AttachResult{}, fmt.Errorf("enter raw mode: %w", err)
	}
	defer func() {
		// Leave the terminal usable even on the error paths, and report a
		// failed restore rather than dropping the user into a broken shell.
		if _, werr := out.Write([]byte("\x1b[m\x1b[?25h\r\n")); werr != nil && err == nil {
			err = fmt.Errorf("reset terminal: %w", werr)
		}
		if rerr := term.Restore(inFd, state); rerr != nil && err == nil {
			err = fmt.Errorf("restore terminal: %w", rerr)
		}
	}()

	done := make(chan struct{})
	defer close(done)
	go watchResize(client, outFd, done)

	detached := make(chan struct{})
	type outcome struct {
		code int
		err  error
	}
	finished := make(chan outcome, 1)
	go func() {
		code, err := pumpOutput(client, out)
		finished <- outcome{code, err}
	}()
	go pumpInput(client, in, detachKey, detached)

	select {
	case o := <-finished:
		return AttachResult{ExitCode: o.code}, o.err
	case <-detached:
		return AttachResult{Detached: true}, nil
	case <-ctx.Done():
		return AttachResult{Detached: true}, ctx.Err()
	}
}

// watchResize forwards SIGWINCH so the PTY tracks the window. Only interactive
// viewers get a vote, so this is what makes the local terminal authoritative
// while it is attached.
func watchResize(client *Client, outFd int, done <-chan struct{}) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	for {
		select {
		case <-winch:
			cols, rows, err := term.GetSize(outFd)
			if err != nil {
				continue
			}
			if err := client.Resize(cols, rows); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// pumpOutput renders frames to the local terminal until the host goes away.
func pumpOutput(client *Client, out io.Writer) (int, error) {
	code, sawExit := 0, false
	for {
		f, err := client.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if sawExit {
					return code, nil
				}
				return 0, errors.New("terminal: host closed the connection")
			}
			return 0, err
		}
		switch f.Type {
		case FrameSnapshot:
			_, ansi, err := DecodeSnapshot(f.Payload)
			if err != nil {
				return 0, err
			}
			if _, err := out.Write(ansi); err != nil {
				return 0, fmt.Errorf("write to terminal: %w", err)
			}
		case FrameOutput:
			if _, err := out.Write(f.Payload); err != nil {
				return 0, fmt.Errorf("write to terminal: %w", err)
			}
		case FrameExit:
			// Recorded rather than returned: the host may still have buffered
			// output to flush before it closes.
			code, sawExit = ParseExit(f.Payload), true
		}
	}
}

// pumpInput forwards keystrokes, watching for the detach sequence.
//
// It stays blocked on a read after detaching. That is deliberate: the caller
// is a CLI that exits immediately afterwards, and interrupting a terminal read
// portably would cost more than it saves.
func pumpInput(client *Client, in io.Reader, detachKey byte, detached chan<- struct{}) {
	buf := make([]byte, 4096)
	armed := false
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			send, detach := filterDetach(buf[:n], detachKey, &armed)
			if len(send) > 0 {
				if err := client.Send(send); err != nil {
					return
				}
			}
			if detach {
				close(detached)
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

// filterDetach splits input into bytes bound for the PTY and the decision to
// detach. armed carries the "prefix seen" state across reads, since a prefix
// and its follow-up key routinely land in separate reads.
//
// After the prefix: `d` detaches, the prefix itself sends one literal prefix
// byte, and anything else passes through unchanged behind the prefix.
func filterDetach(p []byte, detachKey byte, armed *bool) (send []byte, detach bool) {
	send = make([]byte, 0, len(p))
	for _, b := range p {
		switch {
		case *armed && (b == 'd' || b == 'D'):
			*armed = false
			return send, true
		case *armed && b == detachKey:
			*armed = false
			send = append(send, detachKey)
		case *armed:
			*armed = false
			send = append(send, detachKey, b)
		case b == detachKey:
			*armed = true
		default:
			send = append(send, b)
		}
	}
	return send, false
}
