package tunnel

import "syscall"

func bindToDevice(fd int, iface string) error {
	return syscall.SetsockoptString(fd, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
}
