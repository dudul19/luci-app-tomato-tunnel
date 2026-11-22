//go:build !linux

package tunnel

import "syscall"

func xrayProcAttr() *syscall.SysProcAttr { return nil }
