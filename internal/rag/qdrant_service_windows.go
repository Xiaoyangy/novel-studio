//go:build windows

package rag

import "os/exec"

func detachQdrantCommand(cmd *exec.Cmd) {}
