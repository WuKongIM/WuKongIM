// Copyright 2023 tangtao. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build (freebsd || dragonfly || darwin) && !poll_opt
// +build freebsd dragonfly darwin
// +build !poll_opt

package netpoll

import (
	"os"
	"runtime"
	"syscall"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

var note = []unix.Kevent_t{{
	Ident:  0,
	Filter: unix.EVFILT_USER,
	Fflags: unix.NOTE_TRIGGER,
}}

type Poller struct {
	fd int
	wklog.Log
	shutdown bool
	name     string
}

// NewPoller instantiates a poller.
func NewPoller(name string) *Poller {
	poller := new(Poller)
	poller.name = name
	var err error
	if poller.fd, err = unix.Kqueue(); err != nil {
		panic(err)
	}
	if _, err = unix.Kevent(poller.fd, []unix.Kevent_t{{
		Ident:  0,
		Filter: unix.EVFILT_USER,
		Flags:  unix.EV_ADD | unix.EV_CLEAR,
	}}, nil, nil); err != nil {
		_ = poller.close()
		poller = nil
		panic(err)
	}
	poller.Log = wklog.NewWKLog("KqueuePoller")
	return poller
}

// Polling blocks the current goroutine, waiting for network-events.
func (p *Poller) Polling(callback func(fd int, event PollEvent) error) error {
	el := newEventList(InitPollEventsCap)
	var (
		ts  unix.Timespec // 超时
		tsp *unix.Timespec
	)
	p.shutdown = false
	for !p.shutdown {
		n, err := unix.Kevent(p.fd, nil, el.events, tsp)
		if n == 0 || (n < 0 && err == unix.EINTR) {
			tsp = nil
			runtime.Gosched()
			continue
		} else if err != nil {
			return err
		}
		tsp = &ts
		var triggerRead, triggerWrite, triggerHup bool
		var pollEvent PollEvent
		for i := 0; i < n; i++ {
			evt := &el.events[i]

			var fd = int(evt.Ident)
			if fd != 0 {
				pollEvent = PollEventUnknown
				triggerRead = evt.Filter&syscall.EVFILT_READ == syscall.EVFILT_READ
				triggerWrite = evt.Filter&syscall.EVFILT_WRITE == syscall.EVFILT_WRITE
				triggerHup = evt.Flags&syscall.EV_EOF != 0

				// fmt.Println("triggerRead---->", triggerRead, "triggerWrite---->", triggerWrite, "triggerHup---->", triggerHup, "fd---->", p.fd)
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

// AddRead registers the given file-descriptor with readable event to the poller.
func (p *Poller) AddRead(fd int) error {
	// fmt.Println("AddRead---->", fd)
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_ADD, Filter: unix.EVFILT_READ},
	}, nil, nil)
	return os.NewSyscallError("kevent add", err)
}

func (p *Poller) AddWrite(fd int) error {
	// fmt.Println("AddWrite---->", fd)
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_ADD, Filter: unix.EVFILT_WRITE},
	}, nil, nil)
	return os.NewSyscallError("kevent add", err)
}

// DeleteRead deletes the given file-descriptor from the poller.
func (p *Poller) DeleteRead(fd int) error {
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_DELETE, Filter: unix.EVFILT_READ},
	}, nil, nil)
	return os.NewSyscallError("kevent delete", err)
}

// DeleteWrite deletes the given file-descriptor from the poller.
func (p *Poller) DeleteWrite(fd int) error {
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_DELETE, Filter: unix.EVFILT_WRITE},
	}, nil, nil)
	return os.NewSyscallError("kevent delete", err)
}

func (p *Poller) DeleteReadAndWrite(fd int) error {
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_DELETE, Filter: unix.EVFILT_READ},
		{Ident: uint64(fd), Flags: unix.EV_DELETE, Filter: unix.EVFILT_WRITE},
	}, nil, nil)
	return os.NewSyscallError("kevent delete", err)
}

func (p *Poller) Delete(fd int) error {
	return p.DeleteReadAndWrite(fd)
}

func (p *Poller) Close() error {
	p.shutdown = true
	p.trigger()
	return p.close()
}

// close closes the poller.
func (p *Poller) close() error {
	return os.NewSyscallError("close", unix.Close(p.fd))
}

func (p *Poller) trigger() {
	syscall.Kevent(p.fd, []syscall.Kevent_t{{Ident: 0, Filter: syscall.EVFILT_USER, Fflags: syscall.NOTE_TRIGGER}}, nil, nil)
}
