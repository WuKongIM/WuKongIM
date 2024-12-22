package client

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type Handler func(c *Context)

type Context struct {
	cli   *Client
	proto proto.Protocol
	wklog.Log
	req *proto.Request
}

func NewContext(cli *Client) *Context {
	return &Context{
		cli:   cli,
		proto: proto.New(),
		Log:   wklog.NewWKLog("Context"),
	}
}

func (c *Context) Write(data []byte) {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:     id,
		Status: proto.StatusOK,
		Body:   data,
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
	err = c.cli.Write(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
}

func (c *Context) Body() []byte {
	return c.req.Body
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
	err = c.cli.Write(msgData)
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
	err = c.cli.Write(msgData)
	if err != nil {
		c.Debug("WriteToOutboundBuffer is error", zap.Error(err))
		return
	}
}
