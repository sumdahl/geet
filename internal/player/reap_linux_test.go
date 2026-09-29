//go:build linux

package player

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestStartTiedToParent(t *testing.T) {
	cmd := exec.Command("true")
	release, err := startTiedToParent(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Error("no parent-death signal set")
	}
	_ = cmd.Wait()
	release()
	release() // Close and a failed start may both release

	if _, err := startTiedToParent(exec.Command("/no/such/binary")); err == nil {
		t.Error("a missing binary started")
	}
}
