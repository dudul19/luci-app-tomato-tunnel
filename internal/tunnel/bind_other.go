//go:build !linux

package tunnel

func bindToDevice(fd int, iface string) error { return nil }
