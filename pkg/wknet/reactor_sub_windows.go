package wknet

import (
	"bytes"
	"fmt"

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
	conn.Flush()
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

		_, err := conn.ReadToInboundBuffer()
		if err != nil {
			r.Error("readLoop error: %v", zap.Error(err))
			return
		}
	}
}
