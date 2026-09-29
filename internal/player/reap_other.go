//go:build !linux

package player

import "os/exec"

// startTiedToParent just starts cmd: macOS has no parent-death signal, so
// mpv is reaped by Close alone there.
func startTiedToParent(cmd *exec.Cmd) (release func(), err error) {
	return func() {}, cmd.Start()
}
