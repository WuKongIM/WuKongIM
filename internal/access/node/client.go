package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ErrMessageScopedDeliverySubmitUnsupported means the target node cannot decode non-legacy delivery-submit payloads.
var ErrMessageScopedDeliverySubmitUnsupported = errors.New("access/node: message scoped delivery submit unsupported")

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
	if c == nil || c.cluster == nil {
		return fmt.Errorf("access/node: cluster not configured")
	}
	if env.CMDConversationIntentSubmitted {
		supported, err := c.SupportsDeliverySubmitV3(ctx, nodeID)
		if err != nil {
			return err
		}
		if !supported {
			return ErrMessageScopedDeliverySubmitUnsupported
		}
	} else if len(env.MessageScopedUIDs) > 0 {
		supported, err := c.SupportsMessageScopedDeliverySubmit(ctx, nodeID)
		if err != nil {
			return err
		}
		if !supported {
			return ErrMessageScopedDeliverySubmitUnsupported
		}
	}
	body, err := encodeDeliverySubmitRequestBinary(deliverySubmitRequest{
		Envelope: env,
	})
	if err != nil {
		return err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliverySubmitRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeDeliveryResponse(respBody)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected delivery submit status %q", resp.Status)
	}
	return nil
}

// SupportsMessageScopedDeliverySubmit probes whether a node supports delivery-submit v2 scoped subscribers.
func (c *Client) SupportsMessageScopedDeliverySubmit(ctx context.Context, nodeID uint64) (bool, error) {
	if c == nil || c.cluster == nil {
		return false, fmt.Errorf("access/node: cluster not configured")
	}
	if c.hasMessageScopedDeliverySubmitSupport(nodeID) {
		return true, nil
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliverySubmitRPCServiceID, encodeDeliverySubmitCapabilityProbe())
	if err != nil {
		if isDeliverySubmitCodecUnsupported(err) {
			return false, nil
		}
		return false, err
	}
	resp, err := decodeDeliveryResponse(respBody)
	if err != nil {
		return false, err
	}
	if resp.Status != rpcStatusOK {
		return false, fmt.Errorf("access/node: unexpected delivery submit capability status %q", resp.Status)
	}
	c.rememberMessageScopedDeliverySubmitSupport(nodeID)
	return true, nil
}

// SupportsDeliverySubmitV3 probes whether a node supports delivery-submit v3 metadata.
func (c *Client) SupportsDeliverySubmitV3(ctx context.Context, nodeID uint64) (bool, error) {
	if c == nil || c.cluster == nil {
		return false, fmt.Errorf("access/node: cluster not configured")
	}
	if c.hasDeliverySubmitV3Support(nodeID) {
		return true, nil
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliverySubmitRPCServiceID, encodeDeliverySubmitV3CapabilityProbe())
	if err != nil {
		if isDeliverySubmitCodecUnsupported(err) {
			return false, nil
		}
		return false, err
	}
	resp, err := decodeDeliveryResponse(respBody)
	if err != nil {
		return false, err
	}
	if resp.Status != rpcStatusOK {
		return false, fmt.Errorf("access/node: unexpected delivery submit v3 capability status %q", resp.Status)
	}
	c.rememberDeliverySubmitV3Support(nodeID)
	return true, nil
}

func (c *Client) hasMessageScopedDeliverySubmitSupport(nodeID uint64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.messageScopedDeliverySubmitSupport[nodeID]
}

func (c *Client) rememberMessageScopedDeliverySubmitSupport(nodeID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.messageScopedDeliverySubmitSupport == nil {
		c.messageScopedDeliverySubmitSupport = make(map[uint64]bool)
	}
	c.messageScopedDeliverySubmitSupport[nodeID] = true
}

func (c *Client) hasDeliverySubmitV3Support(nodeID uint64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.deliverySubmitV3Support[nodeID]
}

func (c *Client) rememberDeliverySubmitV3Support(nodeID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deliverySubmitV3Support == nil {
		c.deliverySubmitV3Support = make(map[uint64]bool)
	}
	c.deliverySubmitV3Support[nodeID] = true
}

func isDeliverySubmitCodecUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid delivery submit request codec") ||
		strings.Contains(msg, "unknown rpc service")
}

func (c *Client) PushBatch(ctx context.Context, nodeID uint64, cmd DeliveryPushCommand) (DeliveryPushResponse, error) {
	body, err := encodeDeliveryPushCommandBinary(cmd)
	if err != nil {
		return DeliveryPushResponse{}, err
	}
	resp, err := c.callDeliveryPushDirect(ctx, nodeID, body)
	if err != nil {
		return DeliveryPushResponse{}, err
	}
	if resp.Status != rpcStatusOK {
		return DeliveryPushResponse{}, fmt.Errorf("access/node: unexpected delivery push status %q", resp.Status)
	}
	return resp, nil
}

func (c *Client) PushBatchItems(ctx context.Context, nodeID uint64, cmd DeliveryPushBatchCommand) (DeliveryPushResponse, error) {
	body, err := encodeDeliveryPushBatchCommandBinary(cmd)
	if err != nil {
		return DeliveryPushResponse{}, err
	}
	resp, err := c.callDeliveryPushDirect(ctx, nodeID, body)
	if err != nil && isDeliveryPushCodecUnsupported(err) {
		legacyBody, encodeErr := encodeDeliveryPushBatchCommandLegacyBinary(cmd)
		if encodeErr != nil {
			return DeliveryPushResponse{}, encodeErr
		}
		resp, err = c.callDeliveryPushDirect(ctx, nodeID, legacyBody)
	}
	if err != nil {
		return DeliveryPushResponse{}, err
	}
	if resp.Status != rpcStatusOK {
		return DeliveryPushResponse{}, fmt.Errorf("access/node: unexpected delivery push status %q", resp.Status)
	}
	return resp, nil
}

func isDeliveryPushCodecUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid delivery push request codec")
}

func (c *Client) callDeliveryPushDirect(ctx context.Context, nodeID uint64, body []byte) (DeliveryPushResponse, error) {
	if c.cluster == nil {
		return DeliveryPushResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliveryPushRPCServiceID, body)
	if err != nil {
		return DeliveryPushResponse{}, err
	}
	return decodeDeliveryPushResponse(respBody)
}

func (c *Client) NotifyAck(ctx context.Context, nodeID uint64, cmd deliveryevents.RouteAck) error {
	if c == nil || c.cluster == nil {
		return fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeDeliveryAckRequestBinary(deliveryAckRequest{
		Command: cmd,
	})
	if err != nil {
		return err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliveryAckRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeDeliveryResponse(respBody)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected delivery ack status %q", resp.Status)
	}
	return nil
}

// NotifyAckBatch sends multiple delivery acknowledgement notifications to one owner node.
func (c *Client) NotifyAckBatch(ctx context.Context, nodeID uint64, commands []deliveryevents.RouteAck) error {
	if len(commands) == 0 {
		return nil
	}
	if len(commands) == 1 {
		return c.NotifyAck(ctx, nodeID, commands[0])
	}
	if c == nil || c.cluster == nil {
		return fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeDeliveryAckRequestBinary(deliveryAckRequest{
		Commands: commands,
	})
	if err != nil {
		return err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliveryAckRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeDeliveryResponse(respBody)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: unexpected delivery ack status %q", resp.Status)
	}
	return nil
}

func (c *Client) NotifyOffline(ctx context.Context, nodeID uint64, cmd deliveryevents.SessionClosed) error {
	if c == nil || c.cluster == nil {
		return fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeDeliveryOfflineRequestBinary(deliveryOfflineRequest{
		Command: cmd,
	})
	if err != nil {
		return err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliveryOfflineRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeDeliveryResponse(respBody)
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
	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, c, slotID, presenceRPCServiceID, body, decodePresenceResponse)
}

func (c *Client) callPresenceDirect(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, req presenceRPCRequest) (presenceRPCResponse, error) {
	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, nodeID, slotID, presenceRPCServiceID, body)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	return decodePresenceResponse(respBody)
}

func callConversationFactsDirect(ctx context.Context, c *Client, nodeID uint64, req conversationFactsRequest) (conversationFactsResponse, error) {
	if c == nil || c.cluster == nil {
		return conversationFactsResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeConversationFactsRequestBinary(req)
	if err != nil {
		return conversationFactsResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, conversationFactsRPCServiceID, body)
	if err != nil {
		return conversationFactsResponse{}, err
	}
	return decodeConversationFactsResponse(respBody)
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
	return decodePresenceRPCResponseBinary(body)
}

var (
	_ presence.Authoritative    = (*Client)(nil)
	_ presence.ActionDispatcher = (*Client)(nil)
)
