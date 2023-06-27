//go:build linux || freebsd || dragonfly || netbsd || openbsd || darwin

package wknet

import "syscall"

func GetMaxOpenFiles() int {
	maxOpenFiles := 1024 * 1024 * 2
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		if n := int(limit.Max); n > 0 && n < maxOpenFiles {
			maxOpenFiles = n
		}
	}

	return maxOpenFiles
}
