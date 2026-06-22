package plugin

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
)

const (
	// PathSend is the legacy PDK RPC path for the Send hook.
	PathSend = "/plugin/send"
	// PathPersistAfter is the legacy PDK RPC path for the PersistAfter hook.
	PathPersistAfter = "/plugin/persist_after"
	// MsgTypePersistAfter is the legacy one-way message type for async PersistAfter.
	MsgTypePersistAfter uint32 = 2
)

// InvokeSend calls one plugin's Send hook and returns the possibly mutated packet.
func (a *App) InvokeSend(ctx context.Context, pluginNo string, packet *pluginproto.SendPacket) (*pluginproto.SendPacket, error) {
	data, err := packet.Marshal()
	if err != nil {
		return nil, fmt.Errorf("plugin %q Send marshal: %w", pluginNo, err)
	}
	respData, err := a.invoker.RequestPlugin(ctx, pluginNo, PathSend, data)
	if err != nil {
		return nil, fmt.Errorf("plugin %q Send %s: %w", pluginNo, PathSend, err)
	}
	var resp pluginproto.SendPacket
	if err := resp.Unmarshal(respData); err != nil {
		return nil, fmt.Errorf("plugin %q Send response unmarshal: %w", pluginNo, err)
	}
	return &resp, nil
}

// InvokePersistAfter calls or sends one plugin's PersistAfter hook.
func (a *App) InvokePersistAfter(ctx context.Context, plugin ObservedPlugin, batch *pluginproto.MessageBatch) error {
	data, err := batch.Marshal()
	if err != nil {
		return fmt.Errorf("plugin %q PersistAfter marshal: %w", plugin.No, err)
	}
	if plugin.PersistAfterSync {
		_, err = a.invoker.RequestPlugin(ctx, plugin.No, PathPersistAfter, data)
		if err != nil {
			return fmt.Errorf("plugin %q PersistAfter sync %s: %w", plugin.No, PathPersistAfter, err)
		}
		return nil
	}
	if err := a.invoker.SendPlugin(plugin.No, MsgTypePersistAfter, data); err != nil {
		return fmt.Errorf("plugin %q PersistAfter async msgType %d: %w", plugin.No, MsgTypePersistAfter, err)
	}
	return nil
}
