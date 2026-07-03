package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	rpcStatusUnsupported             = "unsupported"
	pluginManagementStatusNotFound   = "not_found"
	pluginManagementStatusBadRequest = "bad_request"
)

var (
	// ErrPluginManagementUnavailable reports that the target node cannot serve plugin management now.
	ErrPluginManagementUnavailable error = pluginManagementStatusError(rpcStatusUnavailable)
	// ErrPluginManagementUnsupported reports that the target node does not support plugin management RPCs.
	ErrPluginManagementUnsupported error = pluginManagementStatusError(rpcStatusUnsupported)
)

type pluginManagementStatusError string

func (e pluginManagementStatusError) Error() string {
	switch string(e) {
	case rpcStatusUnsupported:
		return "access/node: plugin management unsupported"
	case rpcStatusUnavailable:
		return "access/node: plugin management unavailable"
	default:
		return "access/node: plugin management " + string(e)
	}
}

func (e pluginManagementStatusError) PluginNodeStatus() string { return string(e) }

const (
	pluginManagementOpList         = "list"
	pluginManagementOpGet          = "get"
	pluginManagementOpUpdateConfig = "update_config"
	pluginManagementOpRestart      = "restart"
	pluginManagementOpUninstall    = "uninstall"
)

type pluginManagementRequest struct {
	Op       string
	NodeID   uint64
	PluginNo string
	Config   json.RawMessage
}

type pluginManagementResponse struct {
	Status    string
	ErrorCode string
	Error     string
	List      *pluginusecase.LocalPluginList
	Detail    *pluginusecase.LocalPluginDetail
}

func (a *Adapter) handlePluginManagementRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodePluginManagementRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.pluginManagement == nil {
		return encodePluginManagementResponse(pluginManagementResponse{Status: rpcStatusUnavailable})
	}

	var resp pluginManagementResponse
	switch req.Op {
	case pluginManagementOpList:
		list, err := a.pluginManagement.ListLocalPlugins(ctx)
		if err != nil {
			return encodePluginManagementResponse(pluginManagementErrorResponse(err))
		}
		if list.NodeID == 0 {
			list.NodeID = req.NodeID
		}
		resp = pluginManagementResponse{Status: rpcStatusOK, List: &list}
	case pluginManagementOpGet:
		detail, err := a.pluginManagement.GetLocalPlugin(ctx, req.PluginNo)
		if err != nil {
			return encodePluginManagementResponse(pluginManagementErrorResponse(err))
		}
		if detail.NodeID == 0 {
			detail.NodeID = req.NodeID
		}
		resp = pluginManagementResponse{Status: rpcStatusOK, Detail: &detail}
	case pluginManagementOpUpdateConfig:
		detail, err := a.pluginManagement.UpdateLocalConfig(ctx, req.PluginNo, req.Config)
		if err != nil {
			return encodePluginManagementResponse(pluginManagementErrorResponse(err))
		}
		if detail.NodeID == 0 {
			detail.NodeID = req.NodeID
		}
		resp = pluginManagementResponse{Status: rpcStatusOK, Detail: &detail}
	case pluginManagementOpRestart:
		detail, err := a.pluginManagement.RestartLocalPlugin(ctx, req.PluginNo)
		if err != nil {
			return encodePluginManagementResponse(pluginManagementErrorResponse(err))
		}
		if detail.NodeID == 0 {
			detail.NodeID = req.NodeID
		}
		resp = pluginManagementResponse{Status: rpcStatusOK, Detail: &detail}
	case pluginManagementOpUninstall:
		if err := a.pluginManagement.UninstallLocalPlugin(ctx, req.PluginNo); err != nil {
			return encodePluginManagementResponse(pluginManagementErrorResponse(err))
		}
		resp = pluginManagementResponse{Status: rpcStatusOK}
	default:
		resp = pluginManagementResponse{Status: rpcStatusUnsupported, Error: fmt.Sprintf("unknown plugin management op %q", req.Op)}
	}
	return encodePluginManagementResponse(resp)
}

// ListNodePlugins returns one remote node's local plugin inventory.
func (c *Client) ListNodePlugins(ctx context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error) {
	resp, err := c.callPluginManagement(ctx, nodeID, pluginManagementRequest{Op: pluginManagementOpList, NodeID: nodeID})
	if err != nil {
		return pluginusecase.LocalPluginList{}, err
	}
	if resp.List == nil {
		return pluginusecase.LocalPluginList{NodeID: nodeID}, nil
	}
	return *resp.List, nil
}

// GetNodePlugin returns one remote node-local plugin detail.
func (c *Client) GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	resp, err := c.callPluginManagement(ctx, nodeID, pluginManagementRequest{Op: pluginManagementOpGet, NodeID: nodeID, PluginNo: pluginNo})
	if err != nil {
		return pluginusecase.LocalPluginDetail{}, err
	}
	if resp.Detail == nil {
		return pluginusecase.LocalPluginDetail{NodeID: nodeID, No: pluginNo}, nil
	}
	return *resp.Detail, nil
}

// UpdateNodePluginConfig persists desired config on one remote node.
func (c *Client) UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	resp, err := c.callPluginManagement(ctx, nodeID, pluginManagementRequest{Op: pluginManagementOpUpdateConfig, NodeID: nodeID, PluginNo: pluginNo, Config: append(json.RawMessage(nil), config...)})
	if err != nil {
		return pluginusecase.LocalPluginDetail{}, err
	}
	if resp.Detail == nil {
		return pluginusecase.LocalPluginDetail{NodeID: nodeID, No: pluginNo}, nil
	}
	return *resp.Detail, nil
}

// RestartNodePlugin restarts one plugin on a remote node.
func (c *Client) RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	resp, err := c.callPluginManagement(ctx, nodeID, pluginManagementRequest{Op: pluginManagementOpRestart, NodeID: nodeID, PluginNo: pluginNo})
	if err != nil {
		return pluginusecase.LocalPluginDetail{}, err
	}
	if resp.Detail == nil {
		return pluginusecase.LocalPluginDetail{NodeID: nodeID, No: pluginNo}, nil
	}
	return *resp.Detail, nil
}

// UninstallNodePlugin disables and removes one plugin on a remote node.
func (c *Client) UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error {
	_, err := c.callPluginManagement(ctx, nodeID, pluginManagementRequest{Op: pluginManagementOpUninstall, NodeID: nodeID, PluginNo: pluginNo})
	return err
}

func (c *Client) callPluginManagement(ctx context.Context, nodeID uint64, req pluginManagementRequest) (pluginManagementResponse, error) {
	if c == nil || c.cluster == nil {
		return pluginManagementResponse{}, fmt.Errorf("%w: cluster not configured", ErrPluginManagementUnavailable)
	}
	if nodeID == 0 {
		return pluginManagementResponse{}, fmt.Errorf("access/node: plugin management target node required")
	}
	body, err := encodePluginManagementRequest(req)
	if err != nil {
		return pluginManagementResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, pluginManagementRPCServiceID, body)
	if err != nil {
		if isPluginManagementRPCUnsupported(err) {
			return pluginManagementResponse{}, ErrPluginManagementUnsupported
		}
		return pluginManagementResponse{}, fmt.Errorf("%w: %v", ErrPluginManagementUnavailable, err)
	}
	resp, err := decodePluginManagementResponse(respBody)
	if err != nil {
		return pluginManagementResponse{}, fmt.Errorf("%w: %v", ErrPluginManagementUnsupported, err)
	}
	switch resp.Status {
	case rpcStatusOK:
		return resp, nil
	case rpcStatusUnsupported:
		if resp.Error != "" {
			return pluginManagementResponse{}, fmt.Errorf("%w: %s", ErrPluginManagementUnsupported, resp.Error)
		}
		return pluginManagementResponse{}, ErrPluginManagementUnsupported
	case rpcStatusUnavailable:
		if resp.Error != "" {
			return pluginManagementResponse{}, fmt.Errorf("%w: %s", ErrPluginManagementUnavailable, resp.Error)
		}
		return pluginManagementResponse{}, ErrPluginManagementUnavailable
	case pluginManagementStatusNotFound:
		return pluginManagementResponse{}, fmt.Errorf("%w: %s", pluginusecase.ErrPluginNotFound, resp.Error)
	case pluginManagementStatusBadRequest:
		return pluginManagementResponse{}, pluginManagementBadRequestError(resp)
	default:
		return pluginManagementResponse{}, fmt.Errorf("access/node: unexpected plugin management status %q", resp.Status)
	}
}

func pluginManagementErrorResponse(err error) pluginManagementResponse {
	switch {
	case err == nil:
		return pluginManagementResponse{Status: rpcStatusOK}
	case errors.Is(err, pluginusecase.ErrPluginNotFound):
		return pluginManagementResponse{Status: pluginManagementStatusNotFound, ErrorCode: "plugin_not_found", Error: err.Error()}
	case errors.Is(err, pluginusecase.ErrPluginNoRequired):
		return pluginManagementResponse{Status: pluginManagementStatusBadRequest, ErrorCode: "plugin_no_required", Error: err.Error()}
	case errors.Is(err, pluginusecase.ErrInvalidPluginNo):
		return pluginManagementResponse{Status: pluginManagementStatusBadRequest, ErrorCode: "invalid_plugin_no", Error: err.Error()}
	default:
		return pluginManagementResponse{Status: rpcStatusUnavailable, Error: err.Error()}
	}
}

func pluginManagementBadRequestError(resp pluginManagementResponse) error {
	switch resp.ErrorCode {
	case "plugin_no_required":
		return fmt.Errorf("%w: %s", pluginusecase.ErrPluginNoRequired, resp.Error)
	case "invalid_plugin_no":
		return fmt.Errorf("%w: %s", pluginusecase.ErrInvalidPluginNo, resp.Error)
	default:
		return fmt.Errorf("%w: %s", pluginusecase.ErrInvalidPluginNo, resp.Error)
	}
}

func isPluginManagementRPCUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid plugin management response codec") ||
		strings.Contains(msg, "unknown rpc service")
}
