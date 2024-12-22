package wkserver

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
	gproto "google.golang.org/protobuf/proto"
)

type Handler func(c *Context)

type Context struct {
	conn    gnet.Conn
	req     *proto.Request
	connReq *proto.Connect
	proto   proto.Protocol
	wklog.Log
}

func NewContext(conn gnet.Conn) *Context {

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
		Status:    proto.StatusOK,
		Body:      data,
		Timestamp: time.Now().UnixMilli(),
	}
	respData, err := resp.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp)
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	err = c.conn.AsyncWrite(msgData, nil)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
}

func (c *Context) WriteOk() {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:     id,
		Status: proto.StatusOK,
	}
	respData, err := resp.Marshal()
	if err != nil {
		c.Debug("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp)
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	err = c.conn.AsyncWrite(msgData, nil)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
}

func (c *Context) WriteErr(err error) {
	c.WriteErrorAndStatus(err, proto.StatusError)
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
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp)
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	err = c.conn.AsyncWrite(msgData, nil)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
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
	msgData, err := c.proto.Encode(respData, proto.MsgTypeResp)
	if err != nil {
		c.Debug("encode is error", zap.Error(err))
		return
	}
	err = c.conn.AsyncWrite(msgData, nil)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
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
		c.Info("marshal is error", zap.Error(err))
		return
	}
	msgData, err := c.proto.Encode(data, proto.MsgTypeConnack)
	if err != nil {
		c.Info("encode is error", zap.Error(err))
		return
	}
	err = c.conn.AsyncWrite(msgData, nil)
	if err != nil {
		c.Info("asyncWrite is error", zap.Error(err))
		return
	}
}

func (c *Context) Conn() gnet.Conn {

	return c.conn
}
