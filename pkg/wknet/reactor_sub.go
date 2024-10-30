//go:build linux || freebsd || dragonfly || darwin
// +build linux freebsd dragonfly darwin

package wknet

import (
	"bytes"
	"fmt"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet/netpoll"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

// ReactorSub is a sub reactor.
type ReactorSub struct {
	poller    *netpoll.Poller
	eg        *Engine
	idx       int // index of the current sub reactor
	connCount atomic.Int32
	wklog.Log
	ReadBuffer []byte
	cache      bytes.Buffer // temporary buffer for scattered bytes

	stopped atomic.Bool
}

// NewReactorSub instantiates a sub reactor.
func NewReactorSub(eg *Engine, index int) *ReactorSub {
	poller := netpoll.NewPoller(index, "connPoller")

	return &ReactorSub{
		eg:         eg,
		poller:     poller,
		idx:        index,
		Log:        wklog.NewWKLog(fmt.Sprintf("ReactorSub-%d", index)),
		ReadBuffer: make([]byte, eg.options.ReadBufferSize),
	}
}

// AddConn adds a connection to the sub reactor.
func (r *ReactorSub) AddConn(conn Conn) error {
	r.eg.AddConn(conn)
	r.connCount.Inc()
	return r.poller.AddRead(conn.Fd().fd)
}

// Start starts the sub reactor.
func (r *ReactorSub) Start() error {
	go r.run()
	return nil
}

// Stop stops the sub reactor.
func (r *ReactorSub) Stop() error {
	r.stopped.Store(true)
	return r.poller.Close()
}

func (r *ReactorSub) AddWrite(conn Conn) error {
	return r.poller.AddWrite(conn.Fd().fd)
}

func (r *ReactorSub) AddRead(conn Conn) error {
	return r.poller.AddRead(conn.Fd().fd)
}

func (r *ReactorSub) RemoveWrite(conn Conn) error {
	return r.poller.DeleteWrite(conn.Fd().fd)
}

func (r *ReactorSub) RemoveRead(conn Conn) error {
	return r.poller.DeleteRead(conn.Fd().fd)
}

func (r *ReactorSub) RemoveReadAndWrite(conn Conn) error {
	return r.poller.DeleteReadAndWrite(conn.Fd().fd)
}

func (r *ReactorSub) DeleteFd(conn Conn) error {
	return r.poller.Delete(conn.Fd().fd)
}

func (r *ReactorSub) ConnInc() {
	r.connCount.Inc()
}
func (r *ReactorSub) ConnDec() {
	r.connCount.Dec()
}

func (r *ReactorSub) run() {
	defer func() {
		if err := recover(); err != nil {
			r.Panic("reactorSub panic", zap.Any("err", err), zap.Stack("stack"))
		}
	}()

	err := r.poller.Polling(func(fd int, event netpoll.PollEvent) (err error) {
		conn := r.eg.GetConn(fd)
		if conn == nil {
			return nil
		}
		switch event {
		case netpoll.PollEventClose:
			r.Debug("conn 连接关闭！", zap.Int64("id", conn.ID()), zap.Int("fd", fd))
			_ = r.CloseConn(conn, unix.ECONNRESET)
		case netpoll.PollEventRead:
			err = r.read(conn)
		case netpoll.PollEventWrite:
			err = r.write(conn)
		}
		return
	})

	if err != nil && !r.stopped.Load() {
		r.Panic("poller error", zap.Error(err), zap.Int("idx", r.idx))
	}
}

func (r *ReactorSub) CloseConn(c Conn, er error) (rerr error) {
	r.Debug("close connn", zap.Error(er), zap.Int64("id", c.ID()), zap.Int("fd", c.Fd().fd))
	return c.CloseWithErr(er)
}

func (r *ReactorSub) read(c Conn) error {
	var err error
	var n int
	if n, err = c.ReadToInboundBuffer(); err != nil {
		if err == unix.EAGAIN {
			return nil
		}
		if err1 := r.CloseConn(c, err); err1 != nil {
			r.Warn("failed to close conn", zap.Error(err1))
		}
		return nil
	}
	if n == 0 {
		return r.CloseConn(c, os.NewSyscallError("read", unix.ECONNRESET))
	}
	if err = r.eg.eventHandler.OnData(c); err != nil {
		if err == unix.EAGAIN {
			return nil
		}
		if err1 := r.CloseConn(c, err); err1 != nil {
			r.Warn("failed to close conn", zap.Error(err1))
		}
		r.Warn("failed to call OnData", zap.Error(err))
		return nil
	}
	return nil
}

func (r *ReactorSub) write(c Conn) error {
	err := c.Flush()
	switch err {
	case nil:
	case unix.EAGAIN:
		return nil
	default:
		return r.CloseConn(c, os.NewSyscallError("write", err))
	}
	return nil
}
