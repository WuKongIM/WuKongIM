package client

import (
	"time"

	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
)

type clientEvent struct {
	c *Client
}

func (c *clientEvent) OnTick() (delay time.Duration, action gnet.Action) {

	delay = 100 * time.Millisecond
	for _, conn := range c.c.conns {

		if c.c.opts.LogDetailOn {
			c.c.Info("OnTick.....", zap.Any("status", conn.status.Load()))
		}

		if conn.status.CompareAndSwap(disconnect, connecting) {
			go conn.startDial()
		}

		if conn.status.Load() != authed && conn.status.CompareAndSwap(connected, authing) {
			go conn.startAuth()
		}

		if conn.status.Load() == authed {
			conn.tick()
		}
	}
	return
}

func (c *clientEvent) OnClose(gc gnet.Conn, err error) (action gnet.Action) {
	ctx := gc.Context()
	if ctx != nil {
		ctx.(*conn).close(err)
	}

	return
}

func (c *clientEvent) OnTraffic(gc gnet.Conn) (action gnet.Action) {
	conn := gc.Context().(*conn)
	conn.idleTick = 0
	return conn.onTraffic(gc)
}

func (c *clientEvent) OnShutdown(gnet.Engine) {
}

func (c *clientEvent) OnBoot(eng gnet.Engine) (action gnet.Action) {
	c.c.eng = eng
	return
}

func (c *clientEvent) OnOpen(gc gnet.Conn) (out []byte, action gnet.Action) {
	return
}
