//go:build !linux

package desktop

import "syscall"

// childProcAttr has nothing to set off Linux, where the desktop is
// unsupported and guacd never starts.
func childProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
