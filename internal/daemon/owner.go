package daemon

import (
	"os/exec"
	"strconv"
	"strings"
)

// isHeliosProcess reports whether pid is a helios daemon or supervisor.
//
// Both pid files are removed by a deferred call, which is exactly the call that
// does not run when the process is killed. What is left behind is a number that
// belonged to helios once, and pids are recycled — fast enough on a busy
// machine to turn the whole space over in an hour. `helios stop` would then
// send SIGTERM, and shortly SIGKILL, to whatever holds that number now.
//
// Checked against the command line rather than trusting the file. An
// unreadable process answers no: refusing to stop a daemon that is already
// gone costs a stale pid file, and the alternative costs someone else's work.
func isHeliosProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "helios")
}
