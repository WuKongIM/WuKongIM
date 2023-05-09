package server

import (
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/limnet"
	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
)

type connContext struct {
	isDisableRead  bool
	conn           limnet.Conn
	frameCacheLock sync.RWMutex
	frameCaches    []lmproto.Frame
	s              *Server
	inflightCount  atomic.Int32 // frame inflight count
}

func newConnContext(s *Server) *connContext {

	return &connContext{
		s:              s,
		frameCacheLock: sync.RWMutex{},
	}
}

func (c *connContext) putFrame(frame lmproto.Frame) {
	c.frameCacheLock.Lock()
	defer c.frameCacheLock.Unlock()

	c.inflightCount.Add(1)

	c.frameCaches = append(c.frameCaches, frame)
	if int(c.inflightCount.Load()) > c.s.opts.ConnFrameQueueMaxSize {
		c.disableRead()
	}

}

func (c *connContext) popFrames() []lmproto.Frame {
	c.frameCacheLock.RLock()
	defer c.frameCacheLock.RUnlock()
	newFrames := c.frameCaches
	c.frameCaches = make([]lmproto.Frame, 0, 250)
	return newFrames

}

func (c *connContext) finishFrames(count int) {
	c.inflightCount.Add(-int32(count))
	if int(c.inflightCount.Load()) <= c.s.opts.ConnFrameQueueMaxSize {
		c.enableRead()
	}
}

// disable read data from  conn
func (c *connContext) disableRead() {
	if c.isDisableRead {
		return
	}
	// fmt.Println("############disableRead############")
	c.conn.ReactorSub().RemoveRead(c.conn.Fd())
	c.isDisableRead = true
}

// enable read data from  conn
func (c *connContext) enableRead() {
	if !c.isDisableRead {
		return
	}
	// fmt.Println("############enableRead############")
	c.conn.ReactorSub().AddRead(c.conn.Fd())
	c.isDisableRead = false
}

func (c *connContext) init() {
	c.frameCaches = make([]lmproto.Frame, 0, 250)
}

func (c *connContext) release() {
	c.inflightCount.Store(0)
	c.isDisableRead = false
	c.frameCaches = nil
	c.conn = nil
}
