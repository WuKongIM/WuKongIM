package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerPluginRPCServiceID is the cluster RPC service for node-local manager plugin reads.
const ManagerPluginRPCServiceID uint8 = clusternet.RPCManagerPlugins

// HandleManagerPluginRPC handles one encoded manager plugin read RPC payload.
func (a *Adapter) HandleManagerPluginRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerPluginRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager plugin rpc decode failed",
			wklog.Event("internalv2.access.node.manager_plugin_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil {
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case managerPluginOpList:
		if a.managerPlugins == nil {
			return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
		}
		list, err := a.managerPlugins.ListNodePlugins(ctx, req.NodeID)
		status := managerPluginRPCStatusForError(err)
		a.logManagerPluginError(req, status, err)
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: status, Plugins: list.Plugins})
	case managerPluginOpGet:
		if a.managerPlugins == nil {
			return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
		}
		plugin, err := a.managerPlugins.GetNodePlugin(ctx, req.NodeID, req.PluginNo)
		status := managerPluginRPCStatusForError(err)
		a.logManagerPluginError(req, status, err)
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: status, Plugin: plugin})
	case managerPluginOpHTTPForward:
		if a.pluginHTTPRoutes == nil || req.ForwardReq == nil {
			return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
		}
		resp, err := a.pluginHTTPRoutes.Route(ctx, req.ForwardReq.GetPluginNo(), req.ForwardReq.GetRequest())
		status := managerPluginRPCStatusForError(err)
		a.logManagerPluginError(req, status, err)
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: status, ForwardResp: resp})
	case managerPluginOpUpdateConfig:
		if a.managerPlugins == nil {
			return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
		}
		plugin, err := a.managerPlugins.UpdateNodePluginConfig(ctx, req.NodeID, req.PluginNo, req.Config)
		status := managerPluginRPCStatusForError(err)
		a.logManagerPluginError(req, status, err)
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: status, Plugin: plugin})
	case managerPluginOpRestart:
		if a.managerPlugins == nil {
			return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
		}
		plugin, err := a.managerPlugins.RestartNodePlugin(ctx, req.NodeID, req.PluginNo)
		status := managerPluginRPCStatusForError(err)
		a.logManagerPluginError(req, status, err)
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: status, Plugin: plugin})
	case managerPluginOpUninstall:
		if a.managerPlugins == nil {
			return encodeManagerPluginResponse(managerPluginRPCResponse{Status: rpcStatusRejected})
		}
		err := a.managerPlugins.UninstallNodePlugin(ctx, req.NodeID, req.PluginNo)
		status := managerPluginRPCStatusForError(err)
		a.logManagerPluginError(req, status, err)
		return encodeManagerPluginResponse(managerPluginRPCResponse{Status: status})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown manager plugin op %q", req.Op)
		a.rpcLogger().Warn("manager plugin rpc unknown operation",
			wklog.Event("internalv2.access.node.manager_plugin_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// ListManagerPlugins reads plugin inventory from nodeID.
func (c *Client) ListManagerPlugins(ctx context.Context, nodeID uint64) ([]managementusecase.Plugin, error) {
	resp, err := c.callManagerPlugin(ctx, nodeID, managerPluginRPCRequest{Op: managerPluginOpList, NodeID: nodeID})
	if err != nil {
		return nil, err
	}
	if err := managerPluginRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	return append([]managementusecase.Plugin(nil), resp.Plugins...), nil
}

// GetManagerPlugin reads one plugin detail from nodeID.
func (c *Client) GetManagerPlugin(ctx context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	resp, err := c.callManagerPlugin(ctx, nodeID, managerPluginRPCRequest{Op: managerPluginOpGet, NodeID: nodeID, PluginNo: pluginNo})
	if err != nil {
		return managementusecase.Plugin{}, err
	}
	if err := managerPluginRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.Plugin{}, err
	}
	return resp.Plugin, nil
}

// UpdateManagerPluginConfig persists desired config on nodeID.
func (c *Client) UpdateManagerPluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (managementusecase.Plugin, error) {
	resp, err := c.callManagerPlugin(ctx, nodeID, managerPluginRPCRequest{
		Op:       managerPluginOpUpdateConfig,
		NodeID:   nodeID,
		PluginNo: pluginNo,
		Config:   append(json.RawMessage(nil), config...),
	})
	if err != nil {
		return managementusecase.Plugin{}, err
	}
	if err := managerPluginRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.Plugin{}, err
	}
	return resp.Plugin, nil
}

// RestartManagerPlugin restarts one plugin process on nodeID.
func (c *Client) RestartManagerPlugin(ctx context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	resp, err := c.callManagerPlugin(ctx, nodeID, managerPluginRPCRequest{Op: managerPluginOpRestart, NodeID: nodeID, PluginNo: pluginNo})
	if err != nil {
		return managementusecase.Plugin{}, err
	}
	if err := managerPluginRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.Plugin{}, err
	}
	return resp.Plugin, nil
}

// UninstallManagerPlugin disables and removes one plugin process on nodeID.
func (c *Client) UninstallManagerPlugin(ctx context.Context, nodeID uint64, pluginNo string) error {
	resp, err := c.callManagerPlugin(ctx, nodeID, managerPluginRPCRequest{Op: managerPluginOpUninstall, NodeID: nodeID, PluginNo: pluginNo})
	if err != nil {
		return err
	}
	return managerPluginRPCErrorForStatus(resp.Status)
}

// ForwardPluginHTTP invokes one plugin HTTP route on nodeID.
func (c *Client) ForwardPluginHTTP(ctx context.Context, nodeID uint64, req *pluginproto.ForwardHttpReq) (*pluginproto.HttpResponse, error) {
	if req == nil {
		req = &pluginproto.ForwardHttpReq{}
	}
	resp, err := c.callManagerPlugin(ctx, nodeID, managerPluginRPCRequest{
		Op:         managerPluginOpHTTPForward,
		NodeID:     nodeID,
		PluginNo:   req.GetPluginNo(),
		ForwardReq: req,
	})
	if err != nil {
		return nil, err
	}
	if err := managerPluginRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	if resp.ForwardResp == nil {
		return &pluginproto.HttpResponse{}, nil
	}
	return resp.ForwardResp, nil
}

func (c *Client) callManagerPlugin(ctx context.Context, nodeID uint64, req managerPluginRPCRequest) (managerPluginRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerPluginRPCResponse{}, fmt.Errorf("internalv2/access/node: manager plugin rpc client not configured")
	}
	body, err := encodeManagerPluginRequest(req)
	if err != nil {
		return managerPluginRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerPluginRPCServiceID, body)
	if err != nil {
		return managerPluginRPCResponse{}, err
	}
	return decodeManagerPluginResponse(respBody)
}

func managerPluginRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, pluginusecase.ErrPluginNotFound):
		return rpcStatusNotFound
	case errors.Is(err, pluginusecase.ErrPluginNoRequired), errors.Is(err, managementusecase.ErrPluginNodeIDRequired), errors.Is(err, managementusecase.ErrPluginNodeUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerPluginRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusNotFound:
		return pluginusecase.ErrPluginNotFound
	case rpcStatusRejected:
		return managementusecase.ErrPluginNodeUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager plugin rpc status %q", status)
	}
}

func (a *Adapter) logManagerPluginError(req managerPluginRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager plugin rpc rejected",
		wklog.Event("internalv2.access.node.manager_plugin_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.String("pluginNo", req.PluginNo),
		wklog.Error(err),
	)
}
