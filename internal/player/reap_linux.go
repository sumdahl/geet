//go:build linux

package player

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startTiedToParent starts cmd so the kernel signals it when geet dies,
// however geet dies. Without it, a killed geet leaves its mpv running.
//
// Linux sends the parent-death signal when the *thread* that forked the
// child exits, not the process, and Go may retire an idle thread at any
// time. So the start runs on a goroutine locked to its thread, which stays
// parked there until release is called once mpv has been reaped.
func startTiedToParent(cmd *exec.Cmd) (release func(), err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM

	started := make(chan error)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		// Unlocking before returning keeps the thread alive; a locked
		// goroutine that exits takes its thread with it.
		defer runtime.UnlockOSThread()
		started <- cmd.Start()
		<-done
	}()
	release = sync.OnceFunc(func() { close(done) })
	if err := <-started; err != nil {
		release()
		return nil, err
	}
	return release, nil
}
