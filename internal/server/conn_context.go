package server

import (
	"sync"

	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

type connStats struct {
	inMsgs      atomic.Int64 // recv msg count
	outMsgs     atomic.Int64
	inBytes     atomic.Int64
	outBytes    atomic.Int64
	slowClients atomic.Int64
}

type connContext struct {
	connStats
	isDisableRead  bool
	conn           wknet.Conn
	frameCacheLock sync.RWMutex
	frameCaches    []wkproto.Frame
	s              *Server
	inflightCount  int // frame inflight count
	wklog.Log
}

func newConnContext(s *Server) *connContext {

	return &connContext{
		s:              s,
		frameCacheLock: sync.RWMutex{},
		Log:            wklog.NewWKLog("connContext"),
	}
}

func (c *connContext) putFrame(frame wkproto.Frame) {
	c.frameCacheLock.Lock()
	defer c.frameCacheLock.Unlock()

	c.inflightCount++

	c.inMsgs.Add(1)

	c.frameCaches = append(c.frameCaches, frame)
	if c.s.opts.UserMsgQueueMaxSize > 0 && int(c.inflightCount) > c.s.opts.UserMsgQueueMaxSize {
		c.disableRead()
	}

}

func (c *connContext) popFrames() []wkproto.Frame {
	c.frameCacheLock.RLock()
	defer c.frameCacheLock.RUnlock()
	newFrames := c.frameCaches[:]
	// copy(newFrames, c.frameCaches)
	c.frameCaches = make([]wkproto.Frame, 0, 250)
	return newFrames

}

func (c *connContext) finishFrames(count int) {
	c.frameCacheLock.RLock()
	defer c.frameCacheLock.RUnlock()
	c.inflightCount -= count
	if c.s.opts.UserMsgQueueMaxSize > 0 && int(c.inflightCount) <= c.s.opts.UserMsgQueueMaxSize {
		c.enableRead()
	}
}

// disable read data from  conn
func (c *connContext) disableRead() {
	if c.isDisableRead {
		return
	}
	c.isDisableRead = true
	c.Info("流量限制开始!", zap.String("uid", c.conn.UID()), zap.Int64("id", c.conn.ID()), zap.Int("fd", c.conn.Fd()))
	c.conn.ReactorSub().RemoveRead(c.conn.Fd())

}

// enable read data from  conn
func (c *connContext) enableRead() {
	if !c.isDisableRead {
		return
	}
	c.isDisableRead = false
	c.Info("流量限制解除!", zap.String("uid", c.conn.UID()), zap.Int64("id", c.conn.ID()), zap.Int("fd", c.conn.Fd()))
	c.conn.ReactorSub().AddRead(c.conn.Fd())
}

func (c *connContext) init() {
	c.frameCaches = make([]wkproto.Frame, 0, 250)
	c.Log = wklog.NewWKLog("connContext")
}

func (c *connContext) release() {
	c.inflightCount = 0
	c.Log = nil
	c.isDisableRead = false
	c.frameCaches = nil
	c.conn = nil
}
