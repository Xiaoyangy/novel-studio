//go:build !windows

package main

import "syscall"

// detachSysProcAttr 让看板子进程脱离当前进程组，父进程退出或收到 Ctrl+C 时
// 不会连带杀掉后台服务。
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
