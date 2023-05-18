// Copyright 2023 tangtao. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux && !poll_opt
// +build linux,!poll_opt

package netpoll

import (
	"os"
	"runtime"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type Poller struct {
	wklog.Log
	fd       int
	efd      int
	shutdown bool
	name     string
}

func NewPoller(name string) *Poller {
	var (
		err    error
		poller = new(Poller)
	)
	poller.name = name
	poller.fd, err = unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		panic(err)
	}
	poller.efd, err = unix.Eventfd(0, unix.EFD_NONBLOCK|unix.EFD_CLOEXEC)
	if err != nil {
		unix.Close(poller.fd)
		panic(err)
	}
	poller.Log = wklog.NewWKLog("epollPoller")

	err = poller.AddRead(poller.efd)
	if err != nil {
		panic(err)
	}

	return poller
}

// Polling blocks the current goroutine, waiting for network-events.
func (p *Poller) Polling(callback func(fd int, event PollEvent) error) error {
	// el := newEventList(InitPollEventsCap)
	msec := -1
	p.shutdown = false
	events := make([]unix.EpollEvent, 100)
	for !p.shutdown {
		n, err := unix.EpollWait(p.fd, events, msec)
		if n == 0 || (n < 0 && err == unix.EINTR) {
			msec = -1
			runtime.Gosched()
			continue
		} else if err != nil {
			p.Error("error occurs in epoll", zap.Error(err))
			return err
		}
		msec = 0

		var triggerRead, triggerWrite, triggerHup, triggerError bool
		var pollEvent PollEvent

		// test := make([]byte, 10000)
		for i := 0; i < n; i++ {
			event := events[i]
			evt := event.Events
			fd := event.Fd
			pollEvent = PollEventUnknown
			triggerRead = evt&readEvents != 0
			triggerWrite = evt&unix.EPOLLOUT != 0
			triggerHup = evt&(unix.EPOLLHUP|unix.EPOLLRDHUP) != 0
			triggerError = evt&unix.EPOLLERR != 0

			if triggerHup || triggerError {
				pollEvent = PollEventClose
			} else if triggerRead {
				pollEvent = PollEventRead

			}
			if pollEvent != PollEventUnknown {
				switch err = callback(int(fd), pollEvent); err {
				case nil:
				default:
					p.Error("error occurs in event-loop", zap.Error(err))
				}
			}
			if triggerWrite && !(triggerHup || triggerError) {
				switch err = callback(int(fd), PollEventWrite); err {
				case nil:
				default:
					p.Error("error occurs in event-loop", zap.Error(err))
				}
			}
		}
		// if n == el.size {
		// 	el.expand()
		// } else if n < el.size>>1 {
		// 	el.shrink()
		// }
	}
	return nil
}

const (
	readEvents      = unix.EPOLLPRI | unix.EPOLLIN
	writeEvents     = unix.EPOLLOUT
	readWriteEvents = readEvents | writeEvents
	errorEvents     = unix.EPOLLERR | unix.EPOLLHUP | unix.EPOLLRDHUP
)

// AddRead registers the given file-descriptor with readable event to the poller.
func (p *Poller) AddRead(fd int) error {
	return os.NewSyscallError("epoll_ctl add",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Fd: int32(fd), Events: readEvents}))
}

func (p *Poller) AddWrite(fd int) error {
	return os.NewSyscallError("epoll_ctl add",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_MOD, fd, &unix.EpollEvent{Fd: int32(fd), Events: readWriteEvents}))
}

// DeleteRead deletes the given file-descriptor from the poller.
func (p *Poller) DeleteRead(fd int) error {
	return os.NewSyscallError("epoll_ctl delete",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_MOD, fd, &unix.EpollEvent{Fd: int32(fd), Events: writeEvents}))
}

// DeleteWrite deletes the given file-descriptor from the poller.
func (p *Poller) DeleteWrite(fd int) error {
	return os.NewSyscallError("epoll_ctl delete",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_MOD, fd, &unix.EpollEvent{Fd: int32(fd), Events: readEvents}))
}

func (p *Poller) DeleteReadAndWrite(fd int) error {
	return os.NewSyscallError("epoll_ctl delete",
		unix.EpollCtl(p.fd, unix.EPOLL_CTL_DEL, fd, &unix.EpollEvent{Fd: int32(fd), Events: readWriteEvents}))
}

func (p *Poller) Delete(fd int) error {
	err := unix.EpollCtl(p.fd, unix.EPOLL_CTL_DEL, fd, nil)
	return err
}

// Close closes the poller.
func (p *Poller) Close() error {
	p.shutdown = true
	return os.NewSyscallError("close", unix.Close(p.efd))
}
