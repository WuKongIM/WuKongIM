package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type pluginHTTPForwardRequest struct {
	PluginNo string
	Request  *pluginproto.HttpRequest
}

type pluginHTTPForwardResponse struct {
	Status   string
	Error    string
	Response *pluginproto.HttpResponse
}

func (a *Adapter) handlePluginHTTPForwardRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodePluginHTTPForwardRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.pluginHTTPRoutes == nil {
		return encodePluginHTTPForwardResponse(pluginHTTPForwardResponse{Status: rpcStatusUnavailable})
	}
	resp, err := a.pluginHTTPRoutes.Route(ctx, req.PluginNo, req.Request)
	if err != nil {
		return encodePluginHTTPForwardResponse(pluginHTTPForwardResponse{Status: rpcStatusUnavailable, Error: err.Error()})
	}
	return encodePluginHTTPForwardResponse(pluginHTTPForwardResponse{Status: rpcStatusOK, Response: resp})
}

// ForwardPluginHTTP calls one remote node's node-local plugin HTTP route provider.
func (c *Client) ForwardPluginHTTP(ctx context.Context, nodeID uint64, req *pluginproto.ForwardHttpReq) (*pluginproto.HttpResponse, error) {
	if c == nil || c.cluster == nil {
		return nil, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return nil, fmt.Errorf("access/node: plugin http target node required")
	}
	if req == nil {
		req = &pluginproto.ForwardHttpReq{}
	}
	body, err := encodePluginHTTPForwardRequest(pluginHTTPForwardRequest{
		PluginNo: req.GetPluginNo(),
		Request:  req.GetRequest(),
	})
	if err != nil {
		return nil, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, pluginHTTPForwardRPCServiceID, body)
	if err != nil {
		return nil, err
	}
	resp, err := decodePluginHTTPForwardResponse(respBody)
	if err != nil {
		return nil, err
	}
	if resp.Status != rpcStatusOK {
		if resp.Error != "" {
			return nil, fmt.Errorf("access/node: plugin http forward unavailable: %s", resp.Error)
		}
		return nil, fmt.Errorf("access/node: plugin http forward unavailable")
	}
	if resp.Response == nil {
		return &pluginproto.HttpResponse{}, nil
	}
	return resp.Response, nil
}
