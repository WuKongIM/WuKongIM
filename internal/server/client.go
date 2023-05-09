package server

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/valyala/bytebufferpool"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

type clientStats struct {
	inMsgs   atomic.Int64 // 收取的消息数量
	outMsgs  atomic.Int64 // 发出去的消息数量
	inBytes  atomic.Int64 // 收到的字节数
	outBytes atomic.Int64 // 发出去的字节数
}

type inboundStats struct {
	start            time.Time
	pendingByteCount atomic.Int64
}

type client struct {
	clientStats
	conn        conn
	uid         string              // 用户UID
	deviceFlag  wkproto.DeviceFlag  // 设备标示
	deviceLevel wkproto.DeviceLevel // 设备等级
	deviceID    string              // 设备ID
	s           *Server
	aesKey      string // 消息密钥（用于加解密消息的）
	aesIV       string
	wklog.Log
	lastActivity time.Time // 最后一次活动时间
	uptime       time.Time // 客户端启动时间

	outbound *Outbound // 发件箱

	in          inboundStats
	inbound     *bytebufferpool.ByteBuffer // 收件箱
	inboundLock sync.RWMutex

	sendRateLimiter *rate.Limiter // 发送消息限流器

	timer   *timingwheel.Timer
	stopped atomic.Bool // 是否已关闭

	inboundCond *sync.Cond
}

func (c *client) Start() {

	c.stopped.Store(false)

	c.Log = wklog.NewWKLog(fmt.Sprintf("Client[%d]", c.ID()))
	c.Activity()
	c.uptime = time.Now()
	outboundOpts := NewOutboundOptions()
	outboundOpts.WriteDeadline = c.s.opts.WriteTimeout
	c.outbound = NewOutbound(c.conn, outboundOpts, c.s)
	c.inbound = bytebufferpool.Get()
	c.outbound.Start()

	if c.s.opts.ClientSendRateEverySecond > 0 {
		c.sendRateLimiter = rate.NewLimiter(rate.Limit(c.s.opts.ClientSendRateEverySecond), c.s.opts.ClientSendRateEverySecond)
	}

	if c.s.opts.ConnIdleTime > 0 {
		c.lastActivity = time.Now()
		c.timer = c.s.Schedule(c.s.opts.ConnIdleTime/2, c.scanCloseConn())
	}

	c.inboundCond = sync.NewCond(&c.inboundLock)

	go c.readLoop()

}

func (c *client) close() error {
	if c.conn != nil {
		return c.conn.Close() // 关闭后网络框架会触发server的OnClose方法 然后会执行client.Stop方法
	}
	return nil
}

func (c *client) Stop() error {
	c.stopped.Store(true)
	if c.sendRateLimiter != nil {
		c.sendRateLimiter = nil
	}

	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.flushInboundSignal()
	bytebufferpool.Put(c.inbound)
	return c.outbound.Close()
}

func (c *client) Version() uint8 {
	return c.conn.Version()
}

func (c *client) Allow() bool {
	if c.sendRateLimiter == nil {
		return true
	}
	return c.sendRateLimiter.Allow()
}

func (c *client) Write(data []byte) error {
	c.Activity()
	return c.outbound.Write(data)
}

func (c *client) ID() uint32 {
	return c.conn.GetID()
}

func (c *client) Activity() {
	c.lastActivity = time.Now()
}

// 将数据追加到收件箱
func (c *client) InboundAppend(data []byte) {
	c.inboundLock.Lock()
	defer c.inboundLock.Unlock()

	c.in.pendingByteCount.Add(int64(len(data)))

	c.inbound.Write(data)

	c.flushInboundSignal()
}

func (c *client) InboundBytes() []byte {
	c.inboundLock.Lock()
	defer c.inboundLock.Unlock()
	return c.inboundBytes()
}

func (c *client) inboundBytes() []byte {
	return c.inbound.Bytes()
}

func (c *client) InboundDiscord(size int) {
	c.inboundLock.Lock()
	defer c.inboundLock.Unlock()
	c.inboundDiscord(size)
}

func (c *client) inboundDiscord(size int) {
	c.inbound.B = c.inbound.B[size:]
	c.in.pendingByteCount.Sub(int64(size))
}

func (c *client) readLoop() {
	var stopped bool
	for {
		c.inboundLock.Lock()

		if stopped = c.stopped.Load(); !stopped {
			if c.in.pendingByteCount.Load() == 0 {
				c.inboundCond.Wait()
				stopped = c.stopped.Load()
			}
		}
		if stopped {
			c.inboundLock.Unlock()
			return
		}

		c.handleInbound()

		c.inboundLock.Unlock()
	}
}

func (c *client) handleInbound() error {

	c.in.start = time.Now()

	msgBytes := c.inboundBytes() // 获取到消息
	if len(msgBytes) == 0 {
		return errors.New("收件箱没有数据！")
	}

	// 处理包
	offset := 0
	frames := make([]wkproto.Frame, 0)
	for len(msgBytes) > offset {
		frame, size, err := c.s.opts.Proto.DecodeFrame(msgBytes[offset:], c.Version())
		if err != nil { //
			c.Warn("Failed to decode the message", zap.Error(err))
			c.close()
			return err
		}
		if frame == nil {
			break
		}
		frames = append(frames, frame)
		offset += size
	}
	c.inboundDiscord(offset) // 删除掉客户端收件箱处理了的字节数量的大小

	if len(frames) > 0 {
		c.s.handleFrames(frames, c)
	}
	return nil
}

func (c *client) flushInboundSignal() {
	c.inboundCond.Signal()
}
func (c *client) scanCloseConn() func() {
	return func() {
		if c.stopped.Load() {
			return
		}
		now := time.Now()
		intervals := now.Sub(c.lastActivity)   // 最后一次活跃距离现在的时间间隔
		if intervals > c.s.opts.ConnIdleTime { // 如果大于闲置时间 则关闭此连接
			c.Debug("客户端超时关闭！", zap.String("uid", c.uid), zap.String("deviceFlag", c.deviceFlag.String()), zap.String("deviceID", c.deviceID), zap.Int64("aliveSecond", now.Unix()-c.uptime.Unix()))
			_ = c.close()
		}
	}
}
