//go:build !linux

package daemon

import "syscall"

func hevProcAttr() *syscall.SysProcAttr { return nil }
