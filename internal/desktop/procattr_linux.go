package desktop

import "syscall"

// childProcAttr ties guacd's life to the daemon's. Without it a daemon that is
// killed outright leaves guacd holding its port, and the next daemon starts a
// second one beside it.
func childProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
