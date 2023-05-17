// Copyright 2023 tangtao. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux && !poll_opt
// +build linux,!poll_opt

package netpoll

import (
	"os"
	"runtime"
	"syscall"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type Poller struct {
	wklog.Log
	fd       int
	efd      int
	efdBuf   []byte // efd buffer to read an 8-byte integer
	shutdown bool
}

func NewPoller() *Poller {
	var (
		err    error
		poller = new(Poller)
	)
	if poller.fd, err = unix.EpollCreate1(unix.EPOLL_CLOEXEC); err != nil {
		panic(err)
	}
	if poller.efd, err = unix.Eventfd(0, unix.EFD_NONBLOCK|unix.EFD_CLOEXEC); err != nil {
		panic(err)
	}
	poller.efdBuf = make([]byte, 8)
	if err = poller.AddRead(poller.efd); err != nil {
		panic(err)
	}
	poller.Log = wklog.NewWKLog("epollPoller")
	return poller
}

// Polling blocks the current goroutine, waiting for network-events.
func (p *Poller) Polling(callback func(fd int, event PollEvent) error) error {
	el := newEventList(InitPollEventsCap)
	msec := -1
	p.shutdown = false
	events := make([]syscall.EpollEvent, 1024)
	for !p.shutdown {
		n, err := syscall.EpollWait(p.fd, events, msec)
		if n == 0 || (n < 0 && err == unix.EINTR) {
			msec = -1
			runtime.Gosched()
			continue
		} else if err != nil {
			p.Error("error occurs in epoll", zap.Error(err))
			return err
		}
		msec = 0

		var triggerRead, triggerWrite, triggerHup bool
		var pollEvent PollEvent
		for i := 0; i < n; i++ {
			evt := &el.events[i]
			if fd := int(evt.Fd); fd != p.efd {
				pollEvent = PollEventUnknown
				triggerRead = evt.Events&readEvents != 0
				triggerWrite = evt.Events&writeEvents != 0
				triggerHup = evt.Events&errorEvents != 0

				if triggerHup {
					pollEvent = PollEventClose
				} else if triggerRead {
					pollEvent = PollEventRead

				}
				if pollEvent != PollEventUnknown {
					switch err = callback(fd, pollEvent); err {
					case nil:
					default:
						p.Error("error occurs in event-loop", zap.Error(err))
					}
				}
				if triggerWrite && !triggerHup {
					switch err = callback(fd, PollEventWrite); err {
					case nil:
					default:
						p.Error("error occurs in event-loop", zap.Error(err))
					}
				}
			}
		}
		if n == el.size {
			el.expand()
		} else if n < el.size>>1 {
			el.shrink()
		}
	}
	return nil
}

const (
	readEvents      = unix.EPOLLPRI | unix.EPOLLIN
	writeEvents     = unix.EPOLLOUT
	readWriteEvents = readEvents | writeEvents
	errorEvents     = syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP
)

// AddRead registers the given file-descriptor with readable event to the poller.
func (p *Poller) AddRead(fd int) error {
	return os.NewSyscallError("epoll_ctl add",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Fd: int32(fd), Events: readEvents}))
}

func (p *Poller) AddWrite(fd int) error {
	return os.NewSyscallError("epoll_ctl add",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Fd: int32(fd), Events: writeEvents}))
}

// DeleteRead deletes the given file-descriptor from the poller.
func (p *Poller) DeleteRead(fd int) error {
	return os.NewSyscallError("epoll_ctl delete",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_DEL, fd, &unix.EpollEvent{Fd: int32(fd), Events: readEvents}))
}

// DeleteWrite deletes the given file-descriptor from the poller.
func (p *Poller) DeleteWrite(fd int) error {
	return os.NewSyscallError("epoll_ctl delete",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_DEL, fd, &unix.EpollEvent{Fd: int32(fd), Events: writeEvents}))
}

func (p *Poller) DeleteReadAndWrite(fd int) error {
	return os.NewSyscallError("epoll_ctl delete",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_DEL, fd, &unix.EpollEvent{Fd: int32(fd), Events: readWriteEvents}))
}

// Close closes the poller.
func (p *Poller) Close() error {
	if err := os.NewSyscallError("close", unix.Close(p.fd)); err != nil {
		return err
	}
	return os.NewSyscallError("close", unix.Close(p.efd))
}
