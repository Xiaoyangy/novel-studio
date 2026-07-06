//go:build !windows

package rag

import (
	"os/exec"
	"syscall"
)

func detachQdrantCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}
