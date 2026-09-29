//go:build !linux

package player

import "os/exec"

// dieWithParent has no equivalent outside Linux: macOS has no parent-death
// signal, so mpv is reaped by Close alone there.
func dieWithParent(cmd *exec.Cmd) {}
