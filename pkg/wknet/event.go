package wknet

import "net"

type OnConnect func(conn Conn) error
type OnData func(conn Conn) error
type OnClose func(conn Conn, err error)
type OnNewConn func(id int64, connFd int, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error)
type OnNewInboundConn func(conn Conn, eg *Engine) InboundBuffer
type OnNewOutboundConn func(conn Conn, eg *Engine) OutboundBuffer

type EventHandler struct {
	// OnConnect is called when a new connection is established.
	OnConnect func(conn Conn) error
	// OnData is called when a data is received.
	OnData func(conn Conn) error
	// OnClose is called when a connection is closed.
	OnClose func(conn Conn, err error)
	// OnCreateConn is called when a new connection is established.
	OnNewConn OnNewConn
	// OnNewInboundConn is called when need create a new inbound buffer.
	OnNewInboundConn OnNewInboundConn
	// OnNewOutboundConn is called when need create a new outbound buffer.
	OnNewOutboundConn OnNewOutboundConn
}

func NewEventHandler() *EventHandler {
	return &EventHandler{
		OnConnect: func(conn Conn) error { return nil },
		OnData:    func(conn Conn) error { return nil },
		OnClose:   func(conn Conn, err error) {},
		OnNewConn: func(id int64, connFd int, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
			return CreateConn(id, connFd, localAddr, remoteAddr, eg, reactorSub)
		},
		OnNewInboundConn:  func(conn Conn, eg *Engine) InboundBuffer { return NewDefaultBuffer() },
		OnNewOutboundConn: func(conn Conn, eg *Engine) OutboundBuffer { return NewDefaultBuffer() },
	}
}
