package node

import (
	"context"
	"encoding/json"
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type authoritativeRPCResponse interface {
	rpcStatus() string
	rpcLeaderID() uint64
}

func (c *Client) RegisterAuthoritative(ctx context.Context, cmd presence.RegisterAuthoritativeCommand) (presence.RegisterAuthoritativeResult, error) {
	resp, err := c.callPresenceAuthoritative(ctx, multiraft.SlotID(cmd.SlotID), presenceRPCRequest{
		Op:     presenceOpRegister,
		SlotID: cmd.SlotID,
		Route:  &cmd.Route,
	})
	if err != nil {
		return presence.RegisterAuthoritativeResult{}, err
	}
	if resp.Register == nil {
		return presence.RegisterAuthoritativeResult{}, nil
	}
	return *resp.Register, nil
}

func (c *Client) UnregisterAuthoritative(ctx context.Context, cmd presence.UnregisterAuthoritativeCommand) error {
	_, err := c.callPresenceAuthoritative(ctx, multiraft.SlotID(cmd.SlotID), presenceRPCRequest{
		Op:     presenceOpUnregister,
		SlotID: cmd.SlotID,
		Route:  &cmd.Route,
	})
	return err
}

func (c *Client) HeartbeatAuthoritative(ctx context.Context, cmd presence.HeartbeatAuthoritativeCommand) (presence.HeartbeatAuthoritativeResult, error) {
	resp, err := c.callPresenceAuthoritative(ctx, multiraft.SlotID(cmd.Lease.SlotID), presenceRPCRequest{
		Op:    presenceOpHeartbeat,
		Lease: &cmd.Lease,
	})
	if err != nil {
		return presence.HeartbeatAuthoritativeResult{}, err
	}
	if resp.Heartbeat == nil {
		return presence.HeartbeatAuthoritativeResult{}, nil
	}
	return *resp.Heartbeat, nil
}

func (c *Client) ReplayAuthoritative(ctx context.Context, cmd presence.ReplayAuthoritativeCommand) error {
	_, err := c.callPresenceAuthoritative(ctx, multiraft.SlotID(cmd.Lease.SlotID), presenceRPCRequest{
		Op:     presenceOpReplay,
		Lease:  &cmd.Lease,
		Routes: cmd.Routes,
	})
	return err
}

func (c *Client) EndpointsByUID(ctx context.Context, uid string) ([]presence.Route, error) {
	slotID := c.cluster.SlotForKey(uid)
	resp, err := c.callPresenceAuthoritative(ctx, slotID, presenceRPCRequest{
		Op:     presenceOpEndpoints,
		SlotID: uint64(slotID),
		UID:    uid,
	})
	if err != nil {
		return nil, err
	}
	return resp.Endpoints, nil
}

func (c *Client) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	slotID := c.cluster.SlotForKey(uids[0])
	resp, err := c.callPresenceAuthoritative(ctx, slotID, presenceRPCRequest{
		Op:     presenceOpEndpointsByUIDs,
		SlotID: uint64(slotID),
		UIDs:   append([]string(nil), uids...),
	})
	if err != nil {
		return nil, err
	}
	return resp.EndpointMap, nil
}

func (c *Client) ApplyRouteAction(ctx context.Context, action presence.RouteAction) error {
	resp, err := c.callPresenceDirect(ctx, multiraft.NodeID(action.NodeID), 0, presenceRPCRequest{
		Op:     presenceOpApplyAction,
		Action: &action,
	})
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected presence status %q", resp.Status)
	}
	return nil
}

func (c *Client) SubmitCommitted(ctx context.Context, nodeID uint64, env deliveryruntime.CommittedEnvelope) error {
	resp, err := callDeliveryDirect(ctx, c, nodeID, deliverySubmitRPCServiceID, deliverySubmitRequest{
		Envelope: env,
	}, decodeDeliveryResponse)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected delivery submit status %q", resp.Status)
	}
	return nil
}

func (c *Client) PushBatch(ctx context.Context, nodeID uint64, cmd DeliveryPushCommand) (deliveryPushResponse, error) {
	resp, err := callDeliveryDirect(ctx, c, nodeID, deliveryPushRPCServiceID, cmd, decodeDeliveryPushResponse)
	if err != nil {
		return deliveryPushResponse{}, err
	}
	if resp.Status != rpcStatusOK {
		return deliveryPushResponse{}, fmt.Errorf("access/node: unexpected delivery push status %q", resp.Status)
	}
	return resp, nil
}

func (c *Client) NotifyAck(ctx context.Context, nodeID uint64, cmd message.RouteAckCommand) error {
	resp, err := callDeliveryDirect(ctx, c, nodeID, deliveryAckRPCServiceID, deliveryAckRequest{
		Command: cmd,
	}, decodeDeliveryResponse)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected delivery ack status %q", resp.Status)
	}
	return nil
}

func (c *Client) NotifyOffline(ctx context.Context, nodeID uint64, cmd message.SessionClosedCommand) error {
	resp, err := callDeliveryDirect(ctx, c, nodeID, deliveryOfflineRPCServiceID, deliveryOfflineRequest{
		Command: cmd,
	}, decodeDeliveryResponse)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected delivery offline status %q", resp.Status)
	}
	return nil
}

func (c *Client) LoadLatestConversationMessage(ctx context.Context, nodeID uint64, key channel.ChannelID, maxBytes int) (channel.Message, bool, error) {
	resp, err := callConversationFactsDirect(ctx, c, nodeID, conversationFactsRequest{
		Op:       conversationFactsOpLatest,
		Key:      newConversationFactsChannelKey(key),
		MaxBytes: maxBytes,
	})
	if err != nil {
		return channel.Message{}, false, err
	}
	if len(resp.Messages) == 0 {
		return channel.Message{}, false, nil
	}
	return resp.Messages[0], true, nil
}

func (c *Client) LoadLatestConversationMessages(ctx context.Context, nodeID uint64, keys []channel.ChannelID, maxBytes int) (map[channel.ChannelID]channel.Message, error) {
	resp, err := callConversationFactsDirect(ctx, c, nodeID, conversationFactsRequest{
		Op:       conversationFactsOpLatest,
		Keys:     conversationFactsChannelKeys(keys),
		MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[channel.ChannelID]channel.Message, len(resp.Entries))
	for _, entry := range resp.Entries {
		if len(entry.Messages) == 0 {
			continue
		}
		out[entry.Key.channelID()] = entry.Messages[0]
	}
	return out, nil
}

func (c *Client) LoadRecentConversationMessages(ctx context.Context, nodeID uint64, key channel.ChannelID, limit, maxBytes int) ([]channel.Message, error) {
	resp, err := callConversationFactsDirect(ctx, c, nodeID, conversationFactsRequest{
		Op:       conversationFactsOpRecent,
		Key:      newConversationFactsChannelKey(key),
		Limit:    limit,
		MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	return append([]channel.Message(nil), resp.Messages...), nil
}

func (c *Client) LoadRecentConversationMessagesBatch(ctx context.Context, nodeID uint64, keys []channel.ChannelID, limit, maxBytes int) (map[channel.ChannelID][]channel.Message, error) {
	resp, err := callConversationFactsDirect(ctx, c, nodeID, conversationFactsRequest{
		Op:       conversationFactsOpRecent,
		Keys:     conversationFactsChannelKeys(keys),
		Limit:    limit,
		MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[channel.ChannelID][]channel.Message, len(resp.Entries))
	for _, entry := range resp.Entries {
		out[entry.Key.channelID()] = append([]channel.Message(nil), entry.Messages...)
	}
	return out, nil
}

func conversationFactsChannelKeys(keys []channel.ChannelID) []conversationFactsChannelKey {
	out := make([]conversationFactsChannelKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, newConversationFactsChannelKey(key))
	}
	return out
}

func (c *Client) callPresenceAuthoritative(ctx context.Context, slotID multiraft.SlotID, req presenceRPCRequest) (presenceRPCResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, c, slotID, presenceRPCServiceID, body, decodePresenceResponse)
}

func (c *Client) callPresenceDirect(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, req presenceRPCRequest) (presenceRPCResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, nodeID, slotID, presenceRPCServiceID, body)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	return decodePresenceResponse(respBody)
}

func callDeliveryDirect[T any](
	ctx context.Context,
	c *Client,
	nodeID uint64,
	serviceID uint8,
	req any,
	decode func([]byte) (T, error),
) (T, error) {
	var zero T

	if c.cluster == nil {
		return zero, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, serviceID, body)
	if err != nil {
		return zero, err
	}
	return decode(respBody)
}

func callConversationFactsDirect(ctx context.Context, c *Client, nodeID uint64, req conversationFactsRequest) (conversationFactsResponse, error) {
	return callDirectRPC(ctx, c, nodeID, conversationFactsRPCServiceID, req, decodeConversationFactsResponse)
}

func callDirectRPC[T any](
	ctx context.Context,
	c *Client,
	nodeID uint64,
	serviceID uint8,
	req any,
	decode func([]byte) (T, error),
) (T, error) {
	var zero T

	if c.cluster == nil {
		return zero, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, serviceID, body)
	if err != nil {
		return zero, err
	}
	return decode(respBody)
}

func callAuthoritativeRPC[T authoritativeRPCResponse](
	ctx context.Context,
	c *Client,
	slotID multiraft.SlotID,
	serviceID uint8,
	payload []byte,
	decode func([]byte) (T, error),
) (T, error) {
	var zero T

	if c.cluster == nil {
		return zero, fmt.Errorf("access/node: cluster not configured")
	}

	peers := c.cluster.PeersForSlot(slotID)
	if len(peers) == 0 {
		return zero, raftcluster.ErrSlotNotFound
	}

	tried := make(map[multiraft.NodeID]struct{}, len(peers))
	candidates := append([]multiraft.NodeID(nil), peers...)
	var lastErr error

	for len(candidates) > 0 {
		peer := candidates[0]
		candidates = candidates[1:]
		if _, ok := tried[peer]; ok {
			continue
		}
		tried[peer] = struct{}{}

		respBody, err := c.cluster.RPCService(ctx, peer, slotID, serviceID, payload)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := decode(respBody)
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.rpcStatus() {
		case rpcStatusOK:
			return resp, nil
		case rpcStatusNotLeader:
			if leaderID := multiraft.NodeID(resp.rpcLeaderID()); leaderID != 0 {
				if _, ok := tried[leaderID]; !ok {
					candidates = append([]multiraft.NodeID{leaderID}, candidates...)
				}
				continue
			}
		case rpcStatusNoLeader:
			lastErr = raftcluster.ErrNoLeader
			continue
		case rpcStatusNoSlot:
			lastErr = raftcluster.ErrSlotNotFound
			continue
		default:
			lastErr = fmt.Errorf("access/node: unexpected rpc status %q", resp.rpcStatus())
			continue
		}
	}

	if lastErr != nil {
		return zero, lastErr
	}
	return zero, raftcluster.ErrNoLeader
}

func decodePresenceResponse(body []byte) (presenceRPCResponse, error) {
	var resp presenceRPCResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}

var (
	_ presence.Authoritative    = (*Client)(nil)
	_ presence.ActionDispatcher = (*Client)(nil)
)
