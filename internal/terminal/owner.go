package terminal

import (
	"os/exec"
	"strconv"
	"strings"
)

// IsHostProcess reports whether pid is the ptyhost serving sessionID.
//
// A pid on record is a claim about the past, not the present. Hosts are
// recorded in a sidecar that outlives them, pids are recycled — on a busy
// machine the whole space can turn over in an hour — and the signal sent to a
// recycled one lands on whatever holds it now. Everything helios runs is a
// candidate, the daemon included, and the kill is silent from both ends: the
// registry believes it stopped a host, and the process that died leaves no
// note.
//
// So the pid is checked against the command line it is supposed to have. It
// costs one ps per eviction, which happens when a session is closed, not in
// any loop.
//
// An unreadable process answers no. A host that outlives its eviction is found
// by the next sweep and costs some memory until then; a stranger killed by
// mistake costs whatever it was doing.
func IsHostProcess(pid int, sessionID string) bool {
	if pid <= 0 || sessionID == "" {
		return false
	}
	// -ww: the session id sits early in the arguments, but a truncated line is
	// still a line this has to read correctly.
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	cmd := string(out)
	return strings.Contains(cmd, "ptyhost") && strings.Contains(cmd, sessionID)
}
