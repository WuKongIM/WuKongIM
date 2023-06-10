package server

import (
	"io/ioutil"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewServerOptions NewServerOptions
func NewServerOptions() *Options {
	opts := NewOptions()
	opts.Mode = TestMode
	opts.UnitTest = true
	dir, err := ioutil.TempDir("", "wukongim-test")
	if err != nil {
		panic(err)
	}
	opts.DataDir = dir

	return opts
}

// NewTestServer NewTestServer
func NewTestServer(ots ...*Options) *Server {
	var opts *Options
	if len(ots) > 0 {
		opts = ots[0]
	} else {
		opts = NewTestOptions()
	}

	l := New(opts)
	return l
}
func NewTestOptions(logLevel ...zapcore.Level) *Options {
	opt := NewOptions()
	opt.UnitTest = true

	opts := wklog.NewOptions()
	if len(logLevel) > 0 {
		opts.Level = logLevel[0]
	} else {
		opts.Level = zap.DebugLevel
	}

	opts.LogDir, _ = ioutil.TempDir("", "limlog")
	wklog.Configure(opts)
	return opt
}

type TestConn struct {
	writeChan chan []byte
	authed    bool
	connID    uint32
	version   uint8
}

func NewTestConn() *TestConn {
	return &TestConn{
		writeChan: make(chan []byte),
	}
}

func (t *TestConn) GetID() uint32 {
	return t.connID
}

func (t *TestConn) SetID(id uint32) {
	t.connID = id
}

func (t *TestConn) Write(buf []byte) (n int, err error) {
	t.writeChan <- buf
	n = len(buf)
	return
}

func (t *TestConn) WriteChan() chan []byte {

	return t.writeChan
}

func (t *TestConn) Close() error {
	return nil
}

func (t *TestConn) Authed() bool {
	return t.authed
}

func (t *TestConn) SetAuthed(v bool) {
	t.authed = v
}

func (t *TestConn) Version() uint8 {
	return t.version
}

func (t *TestConn) SetVersion(version uint8) {
	t.version = version
}

func (t *TestConn) SetWriteDeadline(tm time.Time) error {
	return nil
}

func (t *TestConn) SetReadDeadline(tm time.Time) error {
	return nil
}

func (t *TestConn) RemoteAddr() net.Addr {
	return nil
}

func (t *TestConn) OutboundBuffered() int {
	return 0
}
