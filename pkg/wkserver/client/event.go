package client

import (
	"time"

	"github.com/panjf2000/gnet/v2"
)

type clientEvent struct {
	c *Client
}

func (c *clientEvent) OnTick() (delay time.Duration, action gnet.Action) {

	delay = 100 * time.Millisecond
	for _, conn := range c.c.conns {

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
		cn := ctx.(connCtx)
		if cn.no == c.c.conn().no {
			c.c.conn().close(err)
		}
	}

	return
}

func (c *clientEvent) OnTraffic(gc gnet.Conn) (action gnet.Action) {
	c.c.conn().idleTick = 0
	return c.c.conn().onTraffic(gc)
}

func (c *clientEvent) OnShutdown(gnet.Engine) {
	c.c.Foucs("gnet shutdown")
}

func (c *clientEvent) OnBoot(eng gnet.Engine) (action gnet.Action) {
	c.c.eng = eng
	return
}

func (c *clientEvent) OnOpen(gc gnet.Conn) (out []byte, action gnet.Action) {
	return
}
