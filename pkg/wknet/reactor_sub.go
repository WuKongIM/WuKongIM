package wknet

import (
	"bytes"
	"errors"
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
}

// NewReactorSub instantiates a sub reactor.
func NewReactorSub(eg *Engine, index int) *ReactorSub {
	poller := netpoll.NewPoller("connPoller")

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
	return r.poller.AddRead(conn.Fd())
}

// Start starts the sub reactor.
func (r *ReactorSub) Start() error {
	go r.run()
	return nil
}

// Stop stops the sub reactor.
func (r *ReactorSub) Stop() error {

	return r.poller.Close()
}

func (r *ReactorSub) AddWrite(fd int) error {
	return r.poller.AddWrite(fd)
}

func (r *ReactorSub) AddRead(fd int) error {
	return r.poller.AddRead(fd)
}

func (r *ReactorSub) RemoveWrite(fd int) error {
	return r.poller.DeleteWrite(fd)
}

func (r *ReactorSub) RemoveRead(fd int) error {
	return r.poller.DeleteRead(fd)
}

func (r *ReactorSub) RemoveReadAndWrite(fd int) error {
	return r.poller.DeleteReadAndWrite(fd)
}

func (r *ReactorSub) run() {
	err := r.poller.Polling(func(fd int, event netpoll.PollEvent) (err error) {
		conn := r.eg.GetConn(fd)
		if conn == nil {
			return nil
		}
		switch event {
		case netpoll.PollEventClose:
			err = conn.Close()
			r.closeConn(conn, unix.ECONNRESET)
		case netpoll.PollEventRead:
			err = r.read(conn)
		case netpoll.PollEventWrite:
			err = r.write(conn)
		}
		return
	})
	if err != nil {
		panic(err)
	}
}

func (r *ReactorSub) CloseConn(c Conn, er error) (rerr error) {
	err0 := r.poller.Delete(c.Fd())
	if err0 != nil {
		rerr = fmt.Errorf("failed to delete fd=%d from poller in subReactor(%d): %v", c.Fd(), r.idx, err0)
	}
	err1 := c.Close()
	if err1 != nil {
		err1 = fmt.Errorf("failed to close fd=%d in event-loop(%d): %v", c.Fd(), r.idx, os.NewSyscallError("close", err1))
		if rerr != nil {
			rerr = errors.New(rerr.Error() + " & " + err1.Error())
		} else {
			rerr = err1
		}
	}
	r.closeConn(c, er)
	return
}

func (r *ReactorSub) closeConn(c Conn, er error) {
	r.eg.RemoveConn(c)               // remove from the engine
	r.connCount.Dec()                // decrease the connection count
	r.eg.eventHandler.OnClose(c, er) // call the close handler
	c.Release()                      // release the connection

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
		return err
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
		return err
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
