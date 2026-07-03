package plugin

import (
	"context"
	"fmt"

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
	// PathConfigUpdate is the legacy PDK RPC path for config update notification.
	PathConfigUpdate = "/plugin/config_update"
)

const (
	// MsgTypePersistAfter is the legacy one-way message type for async PersistAfter.
	MsgTypePersistAfter uint32 = 2
	// MsgTypeReceive is the legacy one-way message type for async Receive.
	MsgTypeReceive uint32 = 3
)

// InvokeSend calls one plugin's Send hook and returns the possibly mutated packet.
func (a *App) InvokeSend(ctx context.Context, pluginNo string, packet *pluginproto.SendPacket) (*pluginproto.SendPacket, error) {
	if a.invoker == nil {
		return nil, ErrInvokerRequired
	}
	data, err := packet.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal plugin send packet: %w", err)
	}
	respData, err := a.invoker.RequestPlugin(ctx, pluginNo, PathSend, data)
	if err != nil {
		return nil, err
	}
	var resp pluginproto.SendPacket
	if err := resp.Unmarshal(respData); err != nil {
		return nil, fmt.Errorf("unmarshal plugin send response: %w", err)
	}
	return &resp, nil
}

// InvokePersistAfter calls or sends one plugin's PersistAfter hook.
func (a *App) InvokePersistAfter(ctx context.Context, plugin ObservedPlugin, batch *pluginproto.MessageBatch) error {
	if a.invoker == nil {
		return ErrInvokerRequired
	}
	data, err := batch.Marshal()
	if err != nil {
		return fmt.Errorf("marshal plugin persist-after batch: %w", err)
	}
	if plugin.PersistAfterSync {
		_, err = a.invoker.RequestPlugin(ctx, plugin.No, PathPersistAfter, data)
		return err
	}
	return a.invoker.SendPlugin(plugin.No, MsgTypePersistAfter, data)
}

// InvokeReceive calls or sends one plugin's Receive hook.
func (a *App) InvokeReceive(ctx context.Context, plugin ObservedPlugin, packet *pluginproto.RecvPacket) error {
	if a.invoker == nil {
		return ErrInvokerRequired
	}
	data, err := packet.Marshal()
	if err != nil {
		return fmt.Errorf("marshal plugin receive packet: %w", err)
	}
	if plugin.ReplySync {
		_, err = a.invoker.RequestPlugin(ctx, plugin.No, PathReceive, data)
		return err
	}
	return a.invoker.SendPlugin(plugin.No, MsgTypeReceive, data)
}

// Route calls one plugin's HTTP-compatible route hook.
func (a *App) Route(ctx context.Context, pluginNo string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	if a.invoker == nil {
		return nil, ErrInvokerRequired
	}
	if err := validatePluginNo(pluginNo); err != nil {
		return nil, err
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
	return &resp, nil
}

// StopPlugin asks one plugin to stop through the invoker's legacy /stop mapping.
func (a *App) StopPlugin(ctx context.Context, pluginNo string) error {
	if a.invoker == nil {
		return ErrInvokerRequired
	}
	if pluginNo == "" {
		return ErrPluginNoRequired
	}
	if err := validatePluginNo(pluginNo); err != nil {
		return err
	}
	return a.invoker.Stop(ctx, pluginNo)
}
