//go:build !windows

package hostexec

import "os/exec"

func configure(cmd *exec.Cmd) {}
