//go:build windows

package main

import "syscall"

// detachSysProcAttr Windows 侧等价物：新建进程组，避免控制台 Ctrl+C 信号
// 传递给后台看板服务。
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
