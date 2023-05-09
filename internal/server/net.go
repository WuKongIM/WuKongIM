package server

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/panjf2000/gnet/v2"
)

type NetEventHandler interface {
	// 建立连接
	OnConnect(c conn)
	// 收到包 [data]为完整的数据包的数据
	OnPacket(c conn, data []byte) []byte
	// 连接关闭
	OnClose(c conn)
}

type Net struct {
	s            *Server
	eventHandler NetEventHandler
	engine       *wknet.Engine
}

func NewNet(s *Server, eventHandler NetEventHandler) *Net {
	return &Net{
		s:            s,
		eventHandler: eventHandler,
		engine:       wknet.NewEngine(),
	}
}

func (n *Net) Start() {
	n.s.waitGroupWrapper.Wrap(func() {
		gnet.Run(newGNetEventHandler(n), n.s.opts.Addr, gnet.WithLogLevel(n.s.opts.Logger.Level), gnet.WithMulticore(false), gnet.WithNumEventLoop(1))
	})

	// n.engine.OnData(func(conn wknet.Conn) error {
	// 	buff, _ := conn.Peek(-1)
	// 	data, _ := gnetUnpacket(buff)
	// 	wrapConn := conn.Context()
	// 	if wrapConn == nil {
	// 		wrapConn = NewConnWrapLimnet(n.s.GenClientID(), conn)
	// 		conn.SetContext(wrapConn)
	// 	}
	// 	rdata := n.eventHandler.OnPacket(wrapConn.(*ConnWrapLimnet), data)

	// 	if len(rdata) == 0 {
	// 		conn.Discard(len(data))
	// 	} else {
	// 		conn.Discard(len(data) - len(rdata))
	// 	}
	// 	return nil
	// })

	// n.engine.Start()

	// addrs := strings.Split(n.s.opts.Addr, "://")
	// ln, err := wknet.Listen(addrs[0], addrs[1][2:])
	// if err != nil {
	// 	panic(err)
	// }

	// n.action = NewAction(ln, n.s, n.eventHandler)

	// n.action.Start()

	// go func() {
	// 	for {
	// 		conns, err := ln.Accept()
	// 		if err != nil {
	// 			n.s.Error("接受连接失败", zap.Error(err))
	// 			continue
	// 		}
	// 		// fmt.Println("-------accept--->", len(conns))
	// 		for _, conn := range conns {
	// 			action := conn.Action()
	// 			fmt.Println("action---->", action, conn.FD(), "conns---->", len(conns))
	// 			if action.IsSet(wknet.ActionOpen) {
	// 				cn := NewConnWrapLimnet(n.s.GenClientID(), conn)
	// 				conn.SetContext(cn)
	// 				n.eventHandler.OnConnect(cn)
	// 				action.Clear(wknet.ActionOpen)

	// 			} else if action.IsSet(wknet.ActionClose) {
	// 				conn.Close()
	// 				cn := conn.Context().(*ConnWrapLimnet)
	// 				n.eventHandler.OnClose(cn)
	// 				action.Clear(wknet.ActionClose)

	// 			} else if action.IsSet(wknet.ActionRead) {
	// 				cn := conn.Context().(*ConnWrapLimnet)
	// 				var packet [0xFFFF]byte
	// 				num, err := conn.Read(packet[:])
	// 				if err != nil {

	// 					if er, ok := err.(net.Error); ok && er.Timeout() {
	// 						fmt.Println("read err--------------->", "Timeout")
	// 					} else if err == syscall.EAGAIN {
	// 						fmt.Println("read err--------------->", "EAGAIN")
	// 					} else {
	// 						n.s.Error("读取数据失败", zap.Error(err), zap.Int("fd", conn.FD()), zap.Int("num", num))
	// 						conn.Close()
	// 					}

	// 					continue
	// 				}
	// 				if num > 0 {
	// 					n.eventHandler.OnPacket(cn, packet[:num])
	// 				}
	// 				action.Clear(wknet.ActionRead)

	// 			}
	// 			conn.SetAction(action)

	// 		}

	// 	}
	// }()

}

func (n *Net) Stop() {
	n.engine.Stop()
	// n.action.Stop()
	// gnet.Stop(context.Background(), n.s.opts.Addr)

}

type ConnWrapLimnet struct {
	conn    wknet.Conn
	id      uint32
	authed  bool
	version uint8
}

func NewConnWrapLimnet(id uint32, conn wknet.Conn) *ConnWrapLimnet {
	return &ConnWrapLimnet{
		conn: conn,
		id:   id,
	}
}

func (c *ConnWrapLimnet) GetID() uint32 {
	return c.id
}

func (c *ConnWrapLimnet) Write(buf []byte) (int, error) {

	return c.conn.Write(buf)
}
func (c *ConnWrapLimnet) Authed() bool {
	return c.authed
}
func (c *ConnWrapLimnet) SetAuthed(v bool) {
	c.authed = v
}
func (c *ConnWrapLimnet) SetWriteDeadline(t time.Time) error {
	return nil
}
func (c *ConnWrapLimnet) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *ConnWrapLimnet) Close() error {
	return c.conn.Close()
}

func (c *ConnWrapLimnet) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// Version 协议版本
func (c *ConnWrapLimnet) Version() uint8 {
	return c.version
}

// SetVersion 设置连接的协议版本
func (c *ConnWrapLimnet) SetVersion(version uint8) {
	c.version = version
}

func (c *ConnWrapLimnet) OutboundBuffered() int {
	return 0
}

type ConnWrapGNet struct {
	conn    gnet.Conn
	id      uint32
	status  int
	version uint8
	client  *client
	sync.RWMutex
	authed bool
}

func NewConnWrapGNet(conn gnet.Conn) *ConnWrapGNet {
	return &ConnWrapGNet{
		conn: conn,
	}
}

func (c *ConnWrapGNet) GetID() uint32 {
	return c.id
}

// 写数据
func (c *ConnWrapGNet) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	if c.conn != nil {
		// TODO：作者说 c.conn.Write 是非线程安全的 c.conn.AsyncWrite 是线程安全的 但是c.conn.AsyncWrite压测不理性
		// err := c.conn.AsyncWrite(buf, func(c gnet.Conn, err error) error {
		// 	return nil
		// })
		// return len(buf), err

		n, err := c.conn.Write(buf)
		if err != nil {
			fmt.Println("writer is err", err)
			return 0, err
		}
		if n > 0 {
			return n, nil
		}
		fmt.Println("writer is n->", n)
	}
	fmt.Println("conn is null")
	return 0, nil
	// return c.conn.Flush()
}

func (c *ConnWrapGNet) OutboundBuffered() int {
	return c.conn.OutboundBuffered()
}

func (c *ConnWrapGNet) Authed() bool {
	return c.authed
}
func (c *ConnWrapGNet) SetAuthed(v bool) {
	c.authed = v
}

func (c *ConnWrapGNet) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
func (c *ConnWrapGNet) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// 关闭连接
func (c *ConnWrapGNet) Close() error {
	return c.conn.Close()
}

// 获取连接地址
func (c *ConnWrapGNet) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// Context 获取用户上下文内容
func (c *ConnWrapGNet) Client() *client {
	return c.client
}

// SetContext 设置用户上下文内容
func (c *ConnWrapGNet) SetClient(cli *client) {
	c.client = cli
}

// Status 自定义连接状态
func (c *ConnWrapGNet) Status() int {
	return c.status
}

// SetStatus 设置状态
func (c *ConnWrapGNet) SetStatus(status int) {
	c.status = status
}

// Version 协议版本
func (c *ConnWrapGNet) Version() uint8 {
	return c.version
}

// SetVersion 设置连接的协议版本
func (c *ConnWrapGNet) SetVersion(version uint8) {
	c.version = version
}

func (c *ConnWrapGNet) reset() {
	c.id = 0
	c.authed = false
	c.version = 0
	c.status = 0
	c.client = nil
	c.conn = nil
}

type GNetEventHandler struct {
	net *Net
	gnet.BuiltinEventEngine
	connWrapGNetPool sync.Pool
}

func newGNetEventHandler(net *Net) *GNetEventHandler {
	return &GNetEventHandler{
		net: net,
		connWrapGNetPool: sync.Pool{
			New: func() any {
				return NewConnWrapGNet(nil)
			},
		},
	}
}

func (g *GNetEventHandler) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	g.net.eventHandler.OnConnect(g.getGNetConnWrap(c))
	return nil, gnet.None
}

func (g *GNetEventHandler) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	connWrap := g.getGNetConnWrap(c)
	g.net.eventHandler.OnClose(connWrap)
	g.connWrapGNetPool.Put(connWrap)
	return gnet.None
}

func (g *GNetEventHandler) OnTraffic(c gnet.Conn) (action gnet.Action) {
	buff, _ := c.Peek(-1)
	data, _ := gnetUnpacket(buff)
	rdata := g.net.eventHandler.OnPacket(g.getGNetConnWrap(c), data)
	if len(rdata) == 0 {
		c.Discard(len(data))
	} else {
		c.Discard(len(data) - len(rdata))
	}
	return
}

func (g *GNetEventHandler) getGNetConnWrap(c gnet.Conn) *ConnWrapGNet {
	connObj := c.Context()
	if connObj != nil {
		return connObj.(*ConnWrapGNet)
	}
	connWrap := g.connWrapGNetPool.Get().(*ConnWrapGNet)
	connWrap.reset()

	connWrap.conn = c
	clientID := g.net.s.GenClientID()
	connWrap.id = clientID
	c.SetContext(connWrap)
	return connWrap
}

func gnetUnpacket(buff []byte) ([]byte, error) {
	// buff, _ := c.Peek(-1)
	if len(buff) <= 0 {
		return nil, nil
	}
	offset := 0

	for len(buff) > offset {
		typeAndFlags := buff[offset]
		packetType := wkproto.FrameType(typeAndFlags >> 4)
		if packetType == wkproto.PING || packetType == wkproto.PONG {
			offset++
			continue
		}
		reminLen, readSize, has := decodeLength(buff[offset+1:])
		if !has {
			break
		}
		dataEnd := offset + readSize + reminLen + 1
		if len(buff) >= dataEnd { // 总数据长度大于当前包数据长度 说明还有包可读。
			offset = dataEnd
			continue
		} else {
			break
		}
	}

	if offset > 0 {
		return buff[:offset], nil
	}

	return nil, nil
}

func decodeLength(data []byte) (int, int, bool) {
	var rLength uint32
	var multiplier uint32
	offset := 0
	for multiplier < 27 { //fix: Infinite '(digit & 128) == 1' will cause the dead loop
		if offset >= len(data) {
			return 0, 0, false
		}
		digit := data[offset]
		offset++
		rLength |= uint32(digit&127) << multiplier
		if (digit & 128) == 0 {
			break
		}
		multiplier += 7
	}
	return int(rLength), offset, true
}
