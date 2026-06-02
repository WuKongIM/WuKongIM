package app

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type deliveryRuntimeAdapter struct {
	// manager executes synchronous fanout and ack mutations.
	manager *runtimedelivery.Manager
}

type deliveryCommittedSink struct {
	// delivery receives committed message events through the delivery usecase.
	delivery *deliveryusecase.App
}

func (s deliveryCommittedSink) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	if s.delivery == nil {
		return nil
	}
	return s.delivery.SubmitCommitted(ctx, event)
}

func (a deliveryRuntimeAdapter) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.SubmitCommitted(ctx, event)
}

func (a deliveryRuntimeAdapter) Recvack(ctx context.Context, cmd deliveryusecase.RecvackCommand) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.Recvack(ctx, runtimedelivery.Recvack{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	})
}

func (a deliveryRuntimeAdapter) SessionClosed(ctx context.Context, cmd deliveryusecase.SessionClosedCommand) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.SessionClosed(ctx, runtimedelivery.SessionClosed{UID: cmd.UID, SessionID: cmd.SessionID})
}

type localOwnerPusher struct {
	// online resolves owner-local concrete sessions.
	online *online.Registry
	// delivery tracks pending recvacks after successful local writes.
	delivery *runtimedelivery.Manager
}

func (p localOwnerPusher) Push(_ context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	var result runtimedelivery.PushResult
	for _, route := range cmd.Routes {
		session, ok := p.localSession(route)
		if !ok {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		packet := buildRecvPacket(cmd.Envelope, route.UID)
		if err := session.Session.WriteDelivery(packet); err != nil {
			result.Retryable = append(result.Retryable, route)
			continue
		}
		if p.delivery != nil {
			p.delivery.BindPendingAck(runtimedelivery.PendingRecvAck{
				UID:         route.UID,
				SessionID:   route.SessionID,
				MessageID:   cmd.Envelope.MessageID,
				MessageSeq:  cmd.Envelope.MessageSeq,
				ChannelID:   cmd.Envelope.ChannelID,
				ChannelType: cmd.Envelope.ChannelType,
			})
		}
		result.Accepted = append(result.Accepted, route)
	}
	return result, nil
}

func (p localOwnerPusher) localSession(route runtimedelivery.Route) (online.LocalSession, bool) {
	if p.online == nil || route.UID == "" || route.SessionID == 0 {
		return online.LocalSession{}, false
	}
	session, ok := p.online.LocalSession(route.SessionID)
	if !ok || session.State != online.RouteStateActive || session.Session == nil {
		return online.LocalSession{}, false
	}
	local := session.Route
	if local.UID != route.UID || local.SessionID != route.SessionID {
		return online.LocalSession{}, false
	}
	if route.OwnerNodeID != 0 && local.OwnerNodeID != route.OwnerNodeID {
		return online.LocalSession{}, false
	}
	if route.OwnerBootID != 0 && local.OwnerBootID != route.OwnerBootID {
		return online.LocalSession{}, false
	}
	if route.OwnerSeq != 0 && local.OwnerSeq != route.OwnerSeq {
		return online.LocalSession{}, false
	}
	return session, true
}

func buildRecvPacket(env runtimedelivery.Envelope, uid string) *frame.RecvPacket {
	_ = uid
	return &frame.RecvPacket{
		Framer: frame.Framer{
			RedDot: env.RedDot,
		},
		MessageID:   int64(env.MessageID),
		MessageSeq:  env.MessageSeq,
		ClientMsgNo: env.ClientMsgNo,
		Timestamp:   int32(time.Now().Unix()),
		ChannelID:   env.ChannelID,
		ChannelType: env.ChannelType,
		FromUID:     env.FromUID,
		Payload:     append([]byte(nil), env.Payload...),
	}
}

type noopSubscriberPlanner struct{}

func (noopSubscriberPlanner) NextPartitionPage(context.Context, runtimedelivery.FanoutTask, string, int) (runtimedelivery.UIDPage, error) {
	return runtimedelivery.UIDPage{Done: true}, nil
}

type presenceResolverAdapter struct {
	// presence resolves authoritative routes for selected UIDs.
	presence *presence.App
}

func (r presenceResolverAdapter) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]runtimedelivery.Route, error) {
	out := make(map[string][]runtimedelivery.Route, len(uids))
	if r.presence == nil {
		return out, nil
	}
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		routes, err := r.presence.EndpointsByUID(ctx, uid)
		if err != nil {
			return nil, err
		}
		for _, route := range routes {
			out[uid] = append(out[uid], runtimedelivery.Route{
				UID:         route.UID,
				OwnerNodeID: route.OwnerNodeID,
				OwnerBootID: route.OwnerBootID,
				OwnerSeq:    route.OwnerSeq,
				SessionID:   route.SessionID,
				DeviceID:    route.DeviceID,
				DeviceFlag:  route.DeviceFlag,
				DeviceLevel: route.DeviceLevel,
			})
		}
	}
	return out, nil
}
