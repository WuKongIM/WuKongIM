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
	PersistAfter(ctx context.Context, data []byte) error
	// Reply 调用插件的Reply方法
	Reply(ctx context.Context, data []byte) error
}

type PluginMethod uint64

const (
	PluginNone         PluginMethod = 0      // 默认值
	PluginSend                      = 1 << 0 // 二进制：0000 0001
	PluginPersistAfter              = 1 << 1 // 二进制：0000 0100
	PluginReply                     = 1 << 2 // 二进制：0000 1000
)

func (x PluginMethod) String() string {
	switch x {
	case PluginNone:
		return "PluginNone"
	case PluginSend:
		return "PluginSend"
	case PluginPersistAfter:
		return "PluginPersistAfter"
	case PluginReply:
		return "PluginReply"
	default:
		return "PluginUnknown"
	}
}

func (x PluginMethod) Uint64() uint64 {
	return uint64(x)
}

type PluginResponse struct {
	MessageId uint64
	Frame     wkproto.Frame
}
