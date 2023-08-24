package wkserver

import (
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type Handler func(c *Context)

type Context struct {
	conn   wknet.Conn
	req    *proto.Request
	server *Server
}

func (c *Context) Write(data []byte) error {
	var id uint64 = 0
	if c.req != nil {
		id = c.req.Id
	}
	resp := &proto.Response{
		Id:     id,
		Status: proto.Status_OK,
		Body:   data,
	}
	respData, err := resp.Marshal()
	if err != nil {
		return err
	}
	msgData, err := c.server.proto.Encode(respData, proto.MsgTypeResp.Uint8())
	if err != nil {
		return err
	}
	_, err = c.conn.Write(msgData)
	return err
}

func (c *Context) WriteOk() error {
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
		return err
	}
	msgData, err := c.server.proto.Encode(respData, proto.MsgTypeResp.Uint8())
	if err != nil {
		return err
	}
	_, err = c.conn.Write(msgData)
	return err
}

func (c *Context) Body() []byte {
	return c.req.Body
}
