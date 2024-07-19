// Copyright 2023 tangtao. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build (freebsd || dragonfly || darwin) && !poll_opt
// +build freebsd dragonfly darwin
// +build !poll_opt

package netpoll

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

type Poller struct {
	fd int
	wklog.Log
	shutdown atomic.Bool
	name     string
}

// NewPoller instantiates a poller.
func NewPoller(index int, name string) *Poller {
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
	poller.Log = wklog.NewWKLog(fmt.Sprintf("KqueuePoller-%s[%d]", name, index))
	return poller
}

// Polling blocks the current goroutine, waiting for network-events.
func (p *Poller) Polling(callback func(fd int, event PollEvent) error) error {
	el := newEventList(InitPollEventsCap)
	var (
		ts  unix.Timespec // 超时
		tsp *unix.Timespec
	)
	p.shutdown.Store(false)
	for !p.shutdown.Load() {
		n, err := unix.Kevent(p.fd, nil, el.events, tsp)

		if n == 0 || (n < 0 && err == unix.EINTR) {
			tsp = nil
			runtime.Gosched()
			continue
		} else if err != nil {
			p.Error("unix.Kevent: error occurs in event-loop", zap.Error(err))
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

				// p.Debug("poll....", zap.Int("fd", fd), zap.Bool("read", triggerRead), zap.Bool("write", triggerWrite), zap.Bool("hup", triggerHup))
				if evt.Flags&syscall.EV_DELETE > 0 {
					continue
				}
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

			} else {
				p.Debug("trigger......")
			}

		}
		if n == el.size {
			el.expand()
		} else if n < el.size>>1 {
			el.shrink()
		}
	}
	_ = p.close()
	p.Debug("exits")
	return nil
}

// AddRead registers the given file-descriptor with readable event to the poller.
func (p *Poller) AddRead(fd int) error {
	// fmt.Println("AddRead---->", fd)
	if fd == 0 {
		return nil
	}
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_ADD, Filter: unix.EVFILT_READ},
	}, nil, nil)
	return os.NewSyscallError("kevent add", err)
}

func (p *Poller) AddWrite(fd int) error {
	// fmt.Println("AddWrite---->", fd)
	if fd == 0 {
		return nil
	}
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_ADD, Filter: unix.EVFILT_WRITE},
	}, nil, nil)
	return os.NewSyscallError("kevent add", err)
}

// DeleteRead deletes the given file-descriptor from the poller.
func (p *Poller) DeleteRead(fd int) error {
	if fd == 0 {
		return nil
	}
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_DELETE, Filter: unix.EVFILT_READ},
	}, nil, nil)
	return os.NewSyscallError("kevent delete", err)
}

// DeleteWrite deletes the given file-descriptor from the poller.
func (p *Poller) DeleteWrite(fd int) error {
	if fd == 0 {
		return nil
	}
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{
		{Ident: uint64(fd), Flags: unix.EV_DELETE, Filter: unix.EVFILT_WRITE},
	}, nil, nil)
	return os.NewSyscallError("kevent delete", err)
}

func (p *Poller) DeleteReadAndWrite(fd int) error {
	if fd == 0 {
		return nil
	}
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
	p.Debug("close")
	p.shutdown.Store(true)
	p.trigger()
	return nil
}

// close closes the poller.
func (p *Poller) close() error {
	return os.NewSyscallError("close", unix.Close(p.fd))
}

func (p *Poller) trigger() {
	_, _ = unix.Kevent(p.fd, []unix.Kevent_t{{Ident: 0, Filter: unix.EVFILT_USER, Fflags: unix.NOTE_TRIGGER}}, nil, nil)
}
