package wknet

import (
	"bytes"
	"fmt"
	"os"
	"syscall"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type ReactorSub struct {
	eg         *Engine
	idx        int // index of the current sub reactor
	ReadBuffer []byte
	wklog.Log
	cache     bytes.Buffer // temporary buffer for scattered bytes
	connCount atomic.Int32
}

// NewReactorSub instantiates a sub reactor.
func NewReactorSub(eg *Engine, index int) *ReactorSub {
	return &ReactorSub{
		eg:         eg,
		idx:        index,
		ReadBuffer: make([]byte, eg.options.ReadBufferSize),
		Log:        wklog.NewWKLog(fmt.Sprintf("ReactorSub-%d", index)),
	}
}

// Start starts the sub reactor.
func (r *ReactorSub) Start() error {
	fmt.Println("warn：此项目在windows系统上存在许多BUG，请尽量在Linux系统运行此项目")
	return nil
}

// Stop stops the sub reactor.
func (r *ReactorSub) Stop() error {
	return nil
}

func (r *ReactorSub) DeleteFd(conn Conn) error {

	return nil
}

func (r *ReactorSub) ConnInc() {
	r.connCount.Inc()
}
func (r *ReactorSub) ConnDec() {
	r.connCount.Dec()
}

// AddConn adds a connection to the sub reactor.
func (r *ReactorSub) AddConn(conn Conn) error {
	r.eg.AddConn(conn)
	r.ConnInc()

	go r.readLoop(conn)
	return nil
}

func (r *ReactorSub) CloseConn(c Conn, er error) (rerr error) {
	r.Debug("connection error", zap.Error(er))
	return c.Close()
}

func (r *ReactorSub) AddWrite(conn Conn) error {
	go conn.Flush()
	return nil
}

func (r *ReactorSub) AddRead(conn Conn) error {
	return nil
}

func (r *ReactorSub) RemoveRead(conn Conn) error {
	return nil
}

func (r *ReactorSub) RemoveWrite(conn Conn) error {
	return nil
}

func (r *ReactorSub) readLoop(conn Conn) {
	for {
		n, err := conn.ReadToInboundBuffer()
		if err != nil {
			if err == syscall.EAGAIN {
				continue
			}
			r.Error("readLoop error", zap.Error(err))
			if err1 := r.CloseConn(conn, err); err1 != nil {
				r.Warn("failed to close conn", zap.Error(err1))
			}
			return
		}
		if n == 0 {
			r.CloseConn(conn, os.NewSyscallError("read", syscall.ECONNRESET))
			return
		}
		if err = r.eg.eventHandler.OnData(conn); err != nil {
			if err == syscall.EAGAIN {
				continue
			}
			if err1 := r.CloseConn(conn, err); err1 != nil {
				r.Warn("failed to close conn", zap.Error(err1))
			}
			r.Warn("failed to call OnData", zap.Error(err))
			return
		}
	}

}
