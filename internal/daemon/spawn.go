package daemon

import (
	"fmt"
	"os"
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

	proc, err := os.StartProcess(exe, append([]string{exe}, args...), &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{devnull, devnull, devnull},
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
