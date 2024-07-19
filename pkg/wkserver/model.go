package wkserver

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
	gproto "google.golang.org/protobuf/proto"
)

type Handler func(c *Context)

type Context struct {
	conn    wknet.Conn
	req     *proto.Request
	connReq *proto.Connect
	proto   proto.Protocol
	wklog.Log
}

func NewContext(conn wknet.Conn) *Context {

	return &Context{
		conn:  conn,
		proto: proto.New(),
		Log:   wklog.NewWKLog("Context"),
	}
}

func (c *Context) WriteMessage(m gproto.Message) {
	data, err := gproto.Marshal(m)
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	c.Write(data)
}

func (c *Context) Write(data []byte) {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:        id,
		Status:    proto.Status_OK,
		Body:      data,
		Timestamp: time.Now().UnixMilli(),
	}
	respData, err := resp.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp.Uint8())
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	_, err = c.conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
	err = c.conn.WakeWrite()
	if err != nil {
		c.Debug("WakeWrite is error", zap.Error(err))
	}
}

func (c *Context) WriteOk() {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:     id,
		Status: proto.Status_OK,
	}
	respData, err := resp.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp.Uint8())
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	_, err = c.conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
	err = c.conn.WakeWrite()
	if err != nil {
		c.Debug("WakeWrite is error", zap.Error(err))
		return
	}
}

func (c *Context) WriteErr(err error) {
	c.WriteErrorAndStatus(err, proto.Status_ERROR)
}

func (c *Context) WriteErrorAndStatus(err error, status proto.Status) {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:     id,
		Status: status,
		Body:   []byte(err.Error()),
	}
	respData, err := resp.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp.Uint8())
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	_, err = c.conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
	err = c.conn.WakeWrite()
	if err != nil {
		c.Debug("WakeWrite is error")
	}
}

func (c *Context) WriteStatus(status proto.Status) {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:     id,
		Status: status,
	}
	respData, err := resp.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp.Uint8())
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	_, err = c.conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
	err = c.conn.WakeWrite()
	if err != nil {
		c.Debug("WakeWrite is error")
	}
}

func (c *Context) Body() []byte {
	return c.req.Body
}

func (c *Context) ConnReq() *proto.Connect {
	return c.connReq
}

func (c *Context) WriteConnack(connack *proto.Connack) {
	data, err := connack.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(data, proto.MsgTypeConnack.Uint8())
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	_, err = c.conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
	err = c.conn.WakeWrite()
	if err != nil {
		c.Debug("WakeWrite is error", zap.Error(err))
		return
	}
}

func (c *Context) Conn() wknet.Conn {

	return c.conn
}
