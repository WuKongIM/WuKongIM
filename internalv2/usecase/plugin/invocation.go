package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

const (
	// PathSend is the legacy PDK RPC path for the Send hook.
	PathSend = "/plugin/send"
	// PathPersistAfter is the legacy PDK RPC path for the PersistAfter hook.
	PathPersistAfter = "/plugin/persist_after"
	// PathReceive is the legacy PDK RPC path for the Receive hook.
	PathReceive = "/plugin/receive"
	// PathRoute is the legacy PDK RPC path for plugin HTTP routing.
	PathRoute = "/plugin/route"
	// MsgTypePersistAfter is the legacy one-way message type for async PersistAfter.
	MsgTypePersistAfter uint32 = 2
	// MsgTypeReceive is the legacy one-way message type for async Receive.
	MsgTypeReceive uint32 = 3
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

// InvokeReceive calls or sends one plugin's Receive hook.
func (a *App) InvokeReceive(ctx context.Context, plugin ObservedPlugin, packet *pluginproto.RecvPacket) error {
	data, err := packet.Marshal()
	if err != nil {
		return fmt.Errorf("plugin %q Receive marshal: %w", plugin.No, err)
	}
	if plugin.ReplySync {
		_, err = a.invoker.RequestPlugin(ctx, plugin.No, PathReceive, data)
		if err != nil {
			return fmt.Errorf("plugin %q Receive sync %s: %w", plugin.No, PathReceive, err)
		}
		return nil
	}
	if err := a.invoker.SendPlugin(plugin.No, MsgTypeReceive, data); err != nil {
		return fmt.Errorf("plugin %q Receive async msgType %d: %w", plugin.No, MsgTypeReceive, err)
	}
	return nil
}

// Route calls one plugin's HTTP-compatible route hook.
func (a *App) Route(ctx context.Context, pluginNo string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	if a == nil || a.invoker == nil {
		return nil, ErrInvokerRequired
	}
	pluginNo = strings.TrimSpace(pluginNo)
	if pluginNo == "" {
		return nil, ErrPluginNoRequired
	}
	req = clonePluginHTTPRequest(req)
	dropHopByHopHeaders(req.Headers)
	if err := a.validateHTTPForwardRequestSize(req); err != nil {
		return nil, err
	}
	data, err := req.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal plugin http request: %w", err)
	}
	respData, err := a.invoker.RequestPlugin(ctx, pluginNo, PathRoute, data)
	if err != nil {
		return nil, err
	}
	var resp pluginproto.HttpResponse
	if err := resp.Unmarshal(respData); err != nil {
		return nil, fmt.Errorf("unmarshal plugin http response: %w", err)
	}
	if err := a.validateHTTPForwardResponseSize(&resp); err != nil {
		return nil, err
	}
	return clonePluginHTTPResponse(&resp), nil
}
