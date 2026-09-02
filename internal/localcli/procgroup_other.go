//go:build !unix

package localcli

import "os/exec"

// ownProcessGroup has no portable equivalent here; the child alone is killed.
func ownProcessGroup(*exec.Cmd) {}
