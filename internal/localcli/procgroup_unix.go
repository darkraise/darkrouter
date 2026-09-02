//go:build unix

package localcli

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts the child as the leader of a new process group and
// kills that whole group when the run is cancelled, so a helper the CLI
// spawned does not outlive it.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
