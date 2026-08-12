//go:build !linux

package download

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {}
