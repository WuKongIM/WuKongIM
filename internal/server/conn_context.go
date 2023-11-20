package server

import (
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// type connStats struct {
// 	inMsgs   atomic.Int64 // recv msg count
// 	outMsgs  atomic.Int64
// 	inBytes  atomic.Int64
// 	outBytes atomic.Int64
// }

type connContext struct {
	isDisableRead  bool
	conn           wknet.Conn
	frameCacheLock sync.RWMutex
	frameCaches    []wkproto.Frame
	s              *Server
	inflightCount  int // frame inflight count
	wklog.Log

	subscriberInfos    map[string]*wkstore.SubscribeInfo // 订阅的频道数据, key: channel, value: SubscriberInfo
	subscriberInfoLock sync.RWMutex
}

func newConnContext(s *Server) *connContext {
	c := &connContext{
		s:               s,
		frameCacheLock:  sync.RWMutex{},
		Log:             wklog.NewWKLog("connContext"),
		subscriberInfos: make(map[string]*wkstore.SubscribeInfo),
	}
	return c
}

func (c *connContext) putFrame(frame wkproto.Frame) {
	c.frameCacheLock.Lock()
	defer c.frameCacheLock.Unlock()

	c.inflightCount++
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
	c.s.slowClients.Add(1)
	c.isDisableRead = true
	c.Info("流量限制开始!", zap.String("uid", c.conn.UID()), zap.Int64("id", c.conn.ID()))
	c.conn.ReactorSub().RemoveRead(c.conn)

}

// enable read data from  conn
func (c *connContext) enableRead() {
	if !c.isDisableRead {
		return
	}
	c.s.slowClients.Sub(1)
	c.isDisableRead = false
	c.Info("流量限制解除!", zap.String("uid", c.conn.UID()), zap.Int64("id", c.conn.ID()))
	c.conn.ReactorSub().AddRead(c.conn)
}

// 订阅频道
func (c *connContext) subscribeChannel(channelID string, channelType uint8, param map[string]interface{}) {
	c.subscriberInfoLock.Lock()
	defer c.subscriberInfoLock.Unlock()
	key := fmt.Sprintf("%s-%d", channelID, channelType)
	if c.conn != nil {
		c.subscriberInfos[key] = &wkstore.SubscribeInfo{
			UID:   c.conn.UID(),
			Param: param,
		}
	}
}

// 取消订阅
func (c *connContext) unscribeChannel(channelID string, channelType uint8) {
	c.subscriberInfoLock.Lock()
	defer c.subscriberInfoLock.Unlock()
	key := fmt.Sprintf("%s-%d", channelID, channelType)
	delete(c.subscriberInfos, key)
}

// 是否订阅
// func (c *connContext) isSubscribed(channelID string, channelType uint8) bool {
// 	c.subscriberInfoLock.RLock()
// 	defer c.subscriberInfoLock.RUnlock()
// 	key := fmt.Sprintf("%s-%d", channelID, channelType)
// 	_, ok := c.subscriberInfos[key]
// 	return ok
// }

func (c *connContext) getSubscribeInfo(channelID string, channelType uint8) *wkstore.SubscribeInfo {
	c.subscriberInfoLock.RLock()
	defer c.subscriberInfoLock.RUnlock()
	key := fmt.Sprintf("%s-%d", channelID, channelType)
	return c.subscriberInfos[key]
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
