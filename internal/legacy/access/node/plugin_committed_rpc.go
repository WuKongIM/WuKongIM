package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type pluginCommittedRequest struct {
	Event messageevents.MessageCommitted
}

type pluginCommittedResponse struct {
	Status string
	Error  string
}

func (a *Adapter) handlePluginCommittedRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodePluginCommittedRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.pluginCommitted == nil {
		return encodePluginCommittedResponse(pluginCommittedResponse{Status: rpcStatusUnavailable})
	}
	if err := a.pluginCommitted.PersistAfterCommitted(ctx, req.Event.Clone()); err != nil {
		return encodePluginCommittedResponse(pluginCommittedResponse{Status: rpcStatusUnavailable, Error: err.Error()})
	}
	return encodePluginCommittedResponse(pluginCommittedResponse{Status: rpcStatusOK})
}

// SubmitPluginCommitted forwards plugin PersistAfter side effects to the channel owner node.
func (c *Client) SubmitPluginCommitted(ctx context.Context, nodeID uint64, event messageevents.MessageCommitted) error {
	if c == nil || c.cluster == nil {
		return fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return fmt.Errorf("access/node: plugin committed target node required")
	}
	body, err := encodePluginCommittedRequest(pluginCommittedRequest{Event: event.Clone()})
	if err != nil {
		return err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, pluginCommittedRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodePluginCommittedResponse(respBody)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		if resp.Error != "" {
			return fmt.Errorf("access/node: plugin committed unavailable: %s", resp.Error)
		}
		return fmt.Errorf("access/node: plugin committed unavailable")
	}
	return nil
}
