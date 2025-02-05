package types

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type Plugin interface {
	// GetNo 获取插件编号
	GetNo() string
	// Send 调用插件的Send方法
	Send(ctx context.Context, sendPacket *pluginproto.SendPacket) (*pluginproto.SendPacket, error)
	// PersistAfter 调用插件的PersistAfter方法
	PersistAfter(ctx context.Context, messages *pluginproto.MessageBatch) error
	// Reply 调用插件的Reply方法
	Reply(ctx context.Context, data []byte) error
}

type PluginMethod string

const (
	PluginSend         PluginMethod = "Send"
	PluginPersistAfter PluginMethod = "PersistAfter"
	PluginReply        PluginMethod = "Reply"
)

type PluginMethodType uint32

const (
	PluginMethodTypeSend         PluginMethodType = 1
	PluginMethodTypePersistAfter PluginMethodType = 2
	PluginMethodTypeReply        PluginMethodType = 3
)

func (p PluginMethod) Type() PluginMethodType {
	switch p {
	case PluginSend:
		return PluginMethodTypeSend
	case PluginPersistAfter:
		return PluginMethodTypePersistAfter
	case PluginReply:
		return PluginMethodTypeReply
	}
	return 0
}

type PluginResponse struct {
	MessageId uint64
	Frame     wkproto.Frame
}
