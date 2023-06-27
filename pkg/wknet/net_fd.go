//go:build linux || freebsd || dragonfly || darwin
// +build linux freebsd dragonfly darwin

package wknet

import (
	"golang.org/x/sys/unix"
)

type NetFd struct {
	fd int
}

func newNetFd(fd int) NetFd {

	return NetFd{
		fd: fd,
	}
}

func (n NetFd) Read(b []byte) (int, error) {
	return unix.Read(n.fd, b)
}

func (n NetFd) Write(b []byte) (int, error) {
	return unix.Write(n.fd, b)
}

func (n NetFd) Close() error {
	return unix.Close(n.fd)
}

func (n NetFd) Fd() int {
	return n.fd
}
