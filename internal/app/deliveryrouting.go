package app

import (
	"context"
	"errors"
	"strings"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	errRemoteAckNotifierRequired     = errors.New("app: remote ack notifier required")
	errRemoteOfflineNotifierRequired = errors.New("app: remote offline notifier required")
)

const (
	committedRouteRetryAttempts = 3
	committedRouteRetryBackoff  = 20 * time.Millisecond
)

type asyncCommittedDispatcher struct {
	localNodeID uint64
	preferLocal bool
	logger      wklog.Logger
	channelLog  interface {
		Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	}
	delivery     committedDeliverySubmitter
	conversation committedConversationSubmitter
	nodeClient   committedNodeSubmitter
}

func (d asyncCommittedDispatcher) SubmitCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
	if d.delivery == nil && d.conversation == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	go func() {
		d.routeCommitted(ctx, env)
	}()
	return nil
}

func (d asyncCommittedDispatcher) routeCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) {
	if d.preferLocal {
		d.logCommittedRoute(env, "prefer_local", d.localNodeID, nil)
		d.submitLocal(ctx, env)
		return
	}
	if d.channelLog == nil {
		d.logCommittedRoute(env, "no_channel_log", d.localNodeID, nil)
		d.submitLocal(ctx, env)
		return
	}

	for attempt := 0; attempt < committedRouteRetryAttempts; attempt++ {
		status, err := d.channelLog.Status(channel.ChannelID{
			ID:   env.ChannelID,
			Type: env.ChannelType,
		})
		if err == nil && status.Leader != 0 {
			ownerNodeID := uint64(status.Leader)
			if ownerNodeID == d.localNodeID {
				d.logCommittedRoute(env, "local_owner", ownerNodeID, nil)
				d.submitLocal(ctx, env)
				return
			}
			if d.nodeClient != nil {
				if err := d.nodeClient.SubmitCommitted(ctx, ownerNodeID, env); err == nil {
					d.logCommittedRoute(env, "remote_owner", ownerNodeID, nil)
					return
				} else {
					d.logCommittedRoute(env, "remote_owner_submit_failed", ownerNodeID, err)
				}
			}
		} else if err != nil {
			d.logCommittedRoute(env, "status_failed", 0, err)
		}
		if attempt < committedRouteRetryAttempts-1 {
			time.Sleep(time.Duration(attempt+1) * committedRouteRetryBackoff)
		}
	}
	d.logCommittedRoute(env, "conversation_fallback", 0, nil)
	d.submitConversationFallback(ctx, env)
}

func (d asyncCommittedDispatcher) logCommittedRoute(env deliveryruntime.CommittedEnvelope, stage string, ownerNodeID uint64, err error) {
	if d.logger == nil {
		return
	}
	fields := []wklog.Field{
		wklog.Event("delivery.diag.committed_route"),
		wklog.String("stage", stage),
		wklog.String("channelID", env.ChannelID),
		wklog.Int("channelType", int(env.ChannelType)),
		wklog.Uint64("messageID", env.MessageID),
		wklog.Uint64("messageSeq", env.MessageSeq),
	}
	if ownerNodeID != 0 {
		fields = append(fields, wklog.Uint64("ownerNodeID", ownerNodeID))
	}
	if err != nil {
		fields = append(fields, wklog.Error(err))
		d.logger.Warn("committed message routing observed failure", fields...)
		return
	}
	d.logger.Info("committed message routed", fields...)
}

func (d asyncCommittedDispatcher) submitLocal(ctx context.Context, env deliveryruntime.CommittedEnvelope) {
	if d.delivery != nil {
		_ = d.delivery.SubmitCommitted(ctx, env)
	}
	d.submitConversation(ctx, env.Message)
}

func (d asyncCommittedDispatcher) submitConversation(ctx context.Context, msg channel.Message) {
	if d.conversation != nil {
		_ = d.conversation.SubmitCommitted(ctx, msg)
	}
}

func (d asyncCommittedDispatcher) submitConversationFallback(ctx context.Context, env deliveryruntime.CommittedEnvelope) {
	d.submitConversation(ctx, env.Message)
	if flusher, ok := d.conversation.(committedConversationSubmitterFlusher); ok {
		_ = flusher.Flush(ctx)
	}
}

type localDeliveryResolver struct {
	subscribers deliveryusecase.SubscriberResolver
	authority   presence.Authoritative
	pageSize    int
	logger      wklog.Logger
}

type localResolveToken struct {
	snapshot deliveryusecase.SnapshotToken
	pending  []deliveryruntime.RouteKey
	done     bool
}

func (r localDeliveryResolver) BeginResolve(ctx context.Context, key deliveryruntime.ChannelKey, _ deliveryruntime.CommittedEnvelope) (any, error) {
	if r.subscribers == nil {
		return nil, nil
	}
	snapshot, err := r.subscribers.BeginSnapshot(ctx, channel.ChannelID{
		ID:   key.ChannelID,
		Type: key.ChannelType,
	})
	if err != nil {
		return nil, err
	}
	return &localResolveToken{snapshot: snapshot}, nil
}

func (r localDeliveryResolver) ResolvePage(ctx context.Context, token any, cursor string, limit int) ([]deliveryruntime.RouteKey, string, bool, error) {
	if r.subscribers == nil || r.authority == nil {
		return nil, "", true, nil
	}
	if limit <= 0 {
		limit = r.pageSize
	}
	if limit <= 0 {
		limit = 128
	}

	resolveToken, ok := token.(*localResolveToken)
	if !ok {
		return nil, "", true, nil
	}

	out := make([]deliveryruntime.RouteKey, 0, limit)
	if len(resolveToken.pending) > 0 {
		taken := limit
		if taken > len(resolveToken.pending) {
			taken = len(resolveToken.pending)
		}
		out = append(out, resolveToken.pending[:taken]...)
		resolveToken.pending = resolveToken.pending[taken:]
		if len(out) == limit || resolveToken.done {
			return out, cursor, resolveToken.done && len(resolveToken.pending) == 0, nil
		}
	}

	pageSize := r.pageSize
	if pageSize <= 0 {
		pageSize = 128
	}

	for len(out) < limit {
		if resolveToken.done {
			return out, cursor, true, nil
		}

		uids, nextCursor, done, err := r.subscribers.NextPage(ctx, resolveToken.snapshot, cursor, pageSize)
		if err != nil {
			return nil, "", false, err
		}
		cursor = nextCursor
		resolveToken.done = done
		if len(uids) == 0 {
			if done {
				return out, cursor, true, nil
			}
			continue
		}

		endpointsByUID, err := r.authority.EndpointsByUIDs(ctx, uids)
		if err != nil {
			return nil, "", false, err
		}

		expanded := make([]deliveryruntime.RouteKey, 0, len(uids))
		missing := make([]string, 0, len(uids))
		for _, uid := range uids {
			routes := endpointsByUID[uid]
			if len(routes) == 0 {
				missing = append(missing, uid)
			}
			for _, route := range routes {
				expanded = append(expanded, deliveryruntime.RouteKey{
					UID:       route.UID,
					NodeID:    route.NodeID,
					BootID:    route.BootID,
					SessionID: route.SessionID,
				})
			}
		}
		if r.logger != nil {
			fields := []wklog.Field{
				wklog.Event("delivery.diag.resolve_page"),
				wklog.String("cursor", cursor),
				wklog.Int("uids", len(uids)),
				wklog.Int("routes", len(expanded)),
			}
			if len(missing) > 0 {
				fields = append(fields, wklog.String("missingUIDs", strings.Join(missing, ",")))
				r.logger.Warn("delivery resolver found missing authoritative endpoints", fields...)
			} else {
				r.logger.Info("delivery resolver expanded authoritative endpoints", fields...)
			}
		}
		if len(expanded) == 0 {
			if done {
				return out, cursor, true, nil
			}
			continue
		}

		remaining := limit - len(out)
		if len(expanded) <= remaining {
			out = append(out, expanded...)
			continue
		}
		out = append(out, expanded[:remaining]...)
		resolveToken.pending = append(resolveToken.pending[:0], expanded[remaining:]...)
		return out, cursor, false, nil
	}
	return out, cursor, false, nil
}

type localDeliveryPush struct {
	online        online.Registry
	localNodeID   uint64
	gatewayBootID uint64
	logger        wklog.Logger
}

func (p localDeliveryPush) Push(_ context.Context, cmd deliveryruntime.PushCommand) (deliveryruntime.PushResult, error) {
	return p.pushEnvelope(cmd.Envelope, cmd.Routes), nil
}

func (p localDeliveryPush) pushEnvelope(env deliveryruntime.CommittedEnvelope, routes []deliveryruntime.RouteKey) deliveryruntime.PushResult {
	result := deliveryruntime.PushResult{}
	frameCacheCap := len(routes)
	if frameCacheCap > 2 {
		frameCacheCap = 2
	}
	framesByUID := make(map[string]frame.Frame, frameCacheCap)
	for _, route := range routes {
		switch {
		case env.SenderSessionID != 0 && route.SessionID == env.SenderSessionID:
			continue
		case p.localNodeID != 0 && route.NodeID != p.localNodeID:
			result.Dropped = append(result.Dropped, route)
		case p.gatewayBootID != 0 && route.BootID != p.gatewayBootID:
			result.Dropped = append(result.Dropped, route)
		default:
			conn, ok := p.online.Connection(route.SessionID)
			if !ok || conn.UID != route.UID || conn.State != online.LocalRouteStateActive || conn.Session == nil {
				result.Dropped = append(result.Dropped, route)
				continue
			}
			f, ok := framesByUID[route.UID]
			if !ok {
				f = buildRealtimeRecvPacket(env.Message, route.UID)
				framesByUID[route.UID] = f
			}
			if err := conn.Session.WriteFrame(f); err != nil {
				result.Retryable = append(result.Retryable, route)
				continue
			}
			result.Accepted = append(result.Accepted, route)
		}
	}
	if p.logger != nil {
		p.logger.Info("local delivery push finished",
			wklog.Event("delivery.diag.local_push"),
			wklog.String("channelID", env.ChannelID),
			wklog.Int("channelType", int(env.ChannelType)),
			wklog.Uint64("messageID", env.MessageID),
			wklog.Uint64("messageSeq", env.MessageSeq),
			wklog.Int("accepted", len(result.Accepted)),
			wklog.Int("retryable", len(result.Retryable)),
			wklog.Int("dropped", len(result.Dropped)),
		)
	}
	return result
}

type distributedDeliveryPush struct {
	localNodeID uint64
	local       localDeliveryPush
	client      *accessnode.Client
	codec       codec.Protocol
	logger      wklog.Logger
}

func (p distributedDeliveryPush) Push(ctx context.Context, cmd deliveryruntime.PushCommand) (deliveryruntime.PushResult, error) {
	if p.codec == nil {
		p.codec = codec.New()
	}

	localRoutes := make([]deliveryruntime.RouteKey, 0, len(cmd.Routes))
	remoteRoutes := make(map[uint64]map[string][]deliveryruntime.RouteKey)
	for _, route := range cmd.Routes {
		if route.NodeID == p.localNodeID {
			localRoutes = append(localRoutes, route)
			continue
		}
		if remoteRoutes[route.NodeID] == nil {
			remoteRoutes[route.NodeID] = make(map[string][]deliveryruntime.RouteKey)
		}
		remoteRoutes[route.NodeID][route.UID] = append(remoteRoutes[route.NodeID][route.UID], route)
	}

	result := deliveryruntime.PushResult{}
	if len(localRoutes) > 0 {
		localResult := p.local.pushEnvelope(cmd.Envelope, localRoutes)
		result.Accepted = append(result.Accepted, localResult.Accepted...)
		result.Retryable = append(result.Retryable, localResult.Retryable...)
		result.Dropped = append(result.Dropped, localResult.Dropped...)
	}

	for nodeID, routesByUID := range remoteRoutes {
		for uid, routes := range routesByUID {
			if p.client == nil {
				result.Retryable = append(result.Retryable, routes...)
				continue
			}
			f := buildRealtimeRecvPacket(cmd.Envelope.Message, uid)
			frameBytes, err := p.codec.EncodeFrame(f, frame.LatestVersion)
			if err != nil {
				return deliveryruntime.PushResult{}, err
			}
			resp, err := p.client.PushBatch(ctx, nodeID, accessnode.DeliveryPushCommand{
				OwnerNodeID: p.localNodeID,
				ChannelID:   cmd.Envelope.ChannelID,
				ChannelType: cmd.Envelope.ChannelType,
				MessageID:   cmd.Envelope.MessageID,
				MessageSeq:  cmd.Envelope.MessageSeq,
				Routes:      append([]deliveryruntime.RouteKey(nil), routes...),
				Frame:       append([]byte(nil), frameBytes...),
			})
			if err != nil {
				if p.logger != nil {
					p.logger.Warn("remote delivery push failed",
						wklog.Event("delivery.diag.remote_push"),
						wklog.String("channelID", cmd.Envelope.ChannelID),
						wklog.Int("channelType", int(cmd.Envelope.ChannelType)),
						wklog.Uint64("messageID", cmd.Envelope.MessageID),
						wklog.Uint64("messageSeq", cmd.Envelope.MessageSeq),
						wklog.Uint64("targetNodeID", nodeID),
						wklog.String("uid", uid),
						wklog.Int("routes", len(routes)),
						wklog.Error(err),
					)
				}
				result.Retryable = append(result.Retryable, routes...)
				continue
			}
			if p.logger != nil {
				p.logger.Info("remote delivery push finished",
					wklog.Event("delivery.diag.remote_push"),
					wklog.String("channelID", cmd.Envelope.ChannelID),
					wklog.Int("channelType", int(cmd.Envelope.ChannelType)),
					wklog.Uint64("messageID", cmd.Envelope.MessageID),
					wklog.Uint64("messageSeq", cmd.Envelope.MessageSeq),
					wklog.Uint64("targetNodeID", nodeID),
					wklog.String("uid", uid),
					wklog.Int("routes", len(routes)),
					wklog.Int("accepted", len(resp.Accepted)),
					wklog.Int("retryable", len(resp.Retryable)),
					wklog.Int("dropped", len(resp.Dropped)),
				)
			}
			result.Accepted = append(result.Accepted, resp.Accepted...)
			result.Retryable = append(result.Retryable, resp.Retryable...)
			result.Dropped = append(result.Dropped, resp.Dropped...)
		}
	}
	return result, nil
}

type ackRouting struct {
	localNodeID uint64
	local       routeAcker
	remoteAcks  *deliveryruntime.AckIndex
	notifier    deliveryOwnerNotifier
}

func (r ackRouting) AckRoute(ctx context.Context, cmd message.RouteAckCommand) error {
	if r.remoteAcks != nil {
		if binding, ok := r.remoteAcks.Lookup(cmd.SessionID, cmd.MessageID); ok {
			if binding.OwnerNodeID != 0 && binding.OwnerNodeID != r.localNodeID {
				if r.notifier == nil {
					return errRemoteAckNotifierRequired
				}
				if err := r.notifier.NotifyAck(ctx, binding.OwnerNodeID, cmd); err != nil {
					return err
				}
				r.remoteAcks.Remove(cmd.SessionID, cmd.MessageID)
				return nil
			}
			if r.local == nil {
				r.remoteAcks.Remove(cmd.SessionID, cmd.MessageID)
				return nil
			}
			if err := r.local.AckRoute(ctx, cmd); err != nil {
				return err
			}
			r.remoteAcks.Remove(cmd.SessionID, cmd.MessageID)
			return nil
		}
	}
	if r.local == nil {
		return nil
	}
	return r.local.AckRoute(ctx, cmd)
}

type offlineRouting struct {
	localNodeID uint64
	local       sessionCloser
	remoteAcks  *deliveryruntime.AckIndex
	notifier    deliveryOwnerNotifier
}

func (r offlineRouting) SessionClosed(ctx context.Context, cmd message.SessionClosedCommand) error {
	var err error
	localBindings := make([]deliveryruntime.AckBinding, 0)
	if r.remoteAcks != nil {
		ownerBindings := make(map[uint64][]deliveryruntime.AckBinding)
		for _, binding := range r.remoteAcks.LookupSession(cmd.SessionID) {
			if binding.OwnerNodeID == 0 || binding.OwnerNodeID == r.localNodeID {
				localBindings = append(localBindings, binding)
				continue
			}
			ownerBindings[binding.OwnerNodeID] = append(ownerBindings[binding.OwnerNodeID], binding)
		}
		for ownerNodeID, bindings := range ownerBindings {
			if r.notifier == nil {
				err = errors.Join(err, errRemoteOfflineNotifierRequired)
				continue
			}
			notifyErr := r.notifier.NotifyOffline(ctx, ownerNodeID, cmd)
			err = errors.Join(err, notifyErr)
			if notifyErr != nil {
				continue
			}
			for _, binding := range bindings {
				r.remoteAcks.Remove(binding.SessionID, binding.MessageID)
			}
		}
	}
	if r.local != nil {
		localErr := r.local.SessionClosed(ctx, cmd)
		err = errors.Join(err, localErr)
		if localErr == nil && r.remoteAcks != nil {
			for _, binding := range localBindings {
				r.remoteAcks.Remove(binding.SessionID, binding.MessageID)
			}
		}
	} else if r.remoteAcks != nil {
		for _, binding := range localBindings {
			r.remoteAcks.Remove(binding.SessionID, binding.MessageID)
		}
	}
	return err
}

type routeAcker interface {
	AckRoute(ctx context.Context, cmd message.RouteAckCommand) error
}

type sessionCloser interface {
	SessionClosed(ctx context.Context, cmd message.SessionClosedCommand) error
}

type deliveryOwnerNotifier interface {
	NotifyAck(ctx context.Context, nodeID uint64, cmd message.RouteAckCommand) error
	NotifyOffline(ctx context.Context, nodeID uint64, cmd message.SessionClosedCommand) error
}

type committedNodeSubmitter interface {
	SubmitCommitted(ctx context.Context, nodeID uint64, env deliveryruntime.CommittedEnvelope) error
}

type committedDeliverySubmitter interface {
	SubmitCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error
}

type committedConversationSubmitter interface {
	SubmitCommitted(ctx context.Context, msg channel.Message) error
}

type committedConversationSubmitterFlusher interface {
	committedConversationSubmitter
	Flush(ctx context.Context) error
}

func buildRealtimeRecvPacket(msg channel.Message, recipientUID string) *frame.RecvPacket {
	framer := msg.Framer
	framer.FrameType = frame.RECV

	packet := &frame.RecvPacket{
		Framer:      framer,
		Setting:     msg.Setting,
		MsgKey:      msg.MsgKey,
		Expire:      msg.Expire,
		MessageID:   int64(msg.MessageID),
		MessageSeq:  msg.MessageSeq,
		ClientMsgNo: msg.ClientMsgNo,
		StreamNo:    msg.StreamNo,
		StreamId:    msg.StreamID,
		StreamFlag:  msg.StreamFlag,
		Timestamp:   msg.Timestamp,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		Topic:       msg.Topic,
		FromUID:     msg.FromUID,
		Payload:     append([]byte(nil), msg.Payload...),
		ClientSeq:   msg.ClientSeq,
	}
	if msg.ChannelType == frame.ChannelTypePerson && recipientUID != "" {
		packet.ChannelID = recipientChannelView(msg, recipientUID)
		packet.ChannelType = frame.ChannelTypePerson
	}
	return packet
}

func recipientChannelView(msg channel.Message, recipientUID string) string {
	if recipientUID == "" {
		return msg.ChannelID
	}
	left, right, err := deliveryusecase.DecodePersonChannel(msg.ChannelID)
	if err != nil {
		return msg.FromUID
	}
	switch recipientUID {
	case left:
		return right
	case right:
		return left
	default:
		return msg.FromUID
	}
}
