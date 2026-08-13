//go:build !linux

package ability_download

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {}
