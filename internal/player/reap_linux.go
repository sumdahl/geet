//go:build linux

package player

import (
	"os/exec"
	"syscall"
)

// dieWithParent asks the kernel to signal the child when geet dies, however
// geet dies. Without it, a killed geet leaves its mpv running.
func dieWithParent(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}
