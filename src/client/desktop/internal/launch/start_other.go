//go:build !windows

package launch

import "os/exec"

func prepare(cmd *exec.Cmd) {}
