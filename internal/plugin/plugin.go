package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	rproto "github.com/WuKongIM/wkrpc/proto"
	"github.com/panjf2000/gnet/v2"
)

type Plugin struct {
	conn gnet.Conn
	info *pluginproto.PluginInfo
	s    *Server
}

func newPlugin(s *Server, conn gnet.Conn, info *pluginproto.PluginInfo) *Plugin {
	p := &Plugin{
		conn: conn,
		info: info,
		s:    s,
	}
	return p
}

func (p *Plugin) GetNo() string {
	return p.info.No
}

func (p *Plugin) send(data []byte) error {
	return p.conn.AsyncWrite(data, nil)
}

func (p *Plugin) hasMethod(method types.PluginMethod) bool {
	return p.info.Method&method.Uint64() != 0
}

func (p *Plugin) asyncInvoke(method types.PluginMethod, data []byte) error {
	if !p.hasMethod(method) {
		return nil
	}
	return p.send(data)
}

func (p *Plugin) invoke(ctx context.Context, method types.PluginMethod, data []byte) ([]byte, error) {
	if !p.hasMethod(method) {
		return nil, nil
	}
	ph := getPathByMethod(method)
	if ph == "" {
		return nil, errors.New("invalid method")
	}

	resp, err := p.s.rpcServer.RequestWithContext(ctx, p.info.No, ph, data)
	if err != nil {
		return nil, err
	}
	if resp.Status != rproto.StatusOK {
		return nil, fmt.Errorf("rpc error status: %d", resp.Status)
	}
	return resp.Body, nil
}

// 发送消息
func (p *Plugin) Send(ctx context.Context, sendPacket *pluginproto.SendPacket) (*pluginproto.SendPacket, error) {

	data, err := sendPacket.Marshal()
	if err != nil {
		return nil, err
	}

	respData, err := p.invoke(ctx, types.PluginSend, data)
	if err != nil {
		return nil, err
	}
	respPacket := &pluginproto.SendPacket{}
	err = respPacket.Unmarshal(respData)
	if err != nil {
		return nil, err
	}

	return respPacket, nil
}

// 存储后
func (p *Plugin) PersistAfter(ctx context.Context, data []byte) error {

	if p.info.PersistAfterSync {
		_, err := p.invoke(ctx, types.PluginPersistAfter, data)
		if err != nil {
			return err
		}
		return nil
	}
	return p.asyncInvoke(types.PluginPersistAfter, data)
}

// 回复消息
func (p *Plugin) Reply(ctx context.Context, data []byte) error {
	if p.info.ReplySync {
		_, err := p.invoke(ctx, types.PluginReply, data)
		if err != nil {
			return err
		}
		return nil
	}
	return p.asyncInvoke(types.PluginReply, data)
}

func getPathByMethod(method types.PluginMethod) string {
	switch method {
	case types.PluginSend:
		return "/plugin/send"
	case types.PluginPersistAfter:
		return "/plugin/persist_after"
	case types.PluginReply:
		return "/plugin/reply"
	default:
		return ""
	}

}
