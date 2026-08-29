package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SpawnDetached starts helios in its own session, so it outlives the terminal
// it was launched from.
//
// Setsid is the whole point. Without it the daemon stays in the launcher's
// process group with the launcher's controlling terminal, and every signal the
// terminal delivers to the foreground group reaches it: Ctrl-C in the setup TUI
// killed the daemon, and closing the window SIGHUPed it. The daemon runs its
// supervisor in-process, so nothing survived to restart it, and the first sign
// of it was an agent's hooks failing with curl's exit 7.
//
// Nothing is inherited from the launcher's terminal either. A daemon holding
// the tty's stdin is the same tie in a quieter form.
func SpawnDetached(exe string, args []string) (int, error) {
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devnull.Close()

	// Output goes to a file, not to /dev/null. The daemon logs through log.
	// SetOutput, but a panic is written to stderr by the runtime and never
	// reaches that file — so a daemon that panicked looked exactly like one
	// that was killed: a log ending mid-sentence and nothing else anywhere.
	crash := crashLog()
	if crash != nil {
		defer crash.Close()
	} else {
		crash = devnull
	}

	proc, err := os.StartProcess(exe, append([]string{exe}, args...), &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{devnull, crash, crash},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("start %s: %w", exe, err)
	}
	// Released, not waited on: this process is about to exit or move on, and a
	// daemon whose parent is gone is reparented rather than orphaned.
	pid := proc.Pid
	return pid, proc.Release()
}

// crashLog opens the file the daemon's stderr is pointed at, or nil.
//
// Appended to, never truncated: the whole point is the last thing written
// before a process disappeared, and a restart must not erase it.
func crashLog() *os.File {
	dir := filepath.Join(HeliosDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "daemon-stderr.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}
