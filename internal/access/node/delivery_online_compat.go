package node

import (
	"context"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

// OnlineDeliveryOwnerPush accepts canonical owner pushes while the stable wire
// codec remains compatible with the legacy delivery DTOs.
type OnlineDeliveryOwnerPush interface {
	PushOwner(context.Context, onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error)
}

type onlineDeliveryOwnerPushAdapter struct {
	push OnlineDeliveryOwnerPush
}

// AdaptOnlineDeliveryOwnerPush exposes a canonical owner pusher through the
// existing server-side legacy port while the version-one wire remains stable.
func AdaptOnlineDeliveryOwnerPush(push OnlineDeliveryOwnerPush) DeliveryOwnerPush {
	if push == nil {
		return nil
	}
	return &onlineDeliveryOwnerPushAdapter{push: push}
}

func (a *onlineDeliveryOwnerPushAdapter) Push(
	ctx context.Context,
	cmd runtimedelivery.PushCommand,
) (runtimedelivery.PushResult, error) {
	result, err := a.push.PushOwner(ctx, onlineDeliveryPushFromLegacy(cmd))
	return legacyDeliveryResultFromOnline(result), err
}

func onlineDeliveryPushFromLegacy(cmd runtimedelivery.PushCommand) onlinedelivery.OwnerPush {
	return onlinedelivery.OwnerPush{
		OwnerNodeID: cmd.OwnerNodeID,
		Event:       onlineDeliveryEnvelopeFromLegacy(cmd.Envelope),
		Routes:      onlineDeliveryRoutesFromLegacy(cmd.Routes),
	}
}

func legacyDeliveryPushFromOnline(push onlinedelivery.OwnerPush) runtimedelivery.PushCommand {
	return runtimedelivery.PushCommand{
		OwnerNodeID: push.OwnerNodeID,
		Envelope:    legacyDeliveryEnvelopeFromOnline(push.Event),
		Routes:      legacyDeliveryRoutesFromOnline(push.Routes),
	}
}

func onlineDeliveryEnvelopeFromLegacy(env runtimedelivery.Envelope) channelappendcontract.CommittedEnvelope {
	return channelappendcontract.CommittedEnvelope{
		MessageID:         env.MessageID,
		MessageSeq:        env.MessageSeq,
		ChannelID:         env.ChannelID,
		ChannelType:       env.ChannelType,
		FromUID:           env.FromUID,
		SenderNodeID:      env.SenderNodeID,
		SenderSessionID:   env.SenderSessionID,
		ClientMsgNo:       env.ClientMsgNo,
		RedDot:            env.RedDot,
		Payload:           append([]byte(nil), env.Payload...),
		MessageScopedUIDs: append([]string(nil), env.MessageScopedUIDs...),
	}
}

func legacyDeliveryEnvelopeFromOnline(event channelappendcontract.CommittedEnvelope) runtimedelivery.Envelope {
	return runtimedelivery.Envelope{
		MessageID:         event.MessageID,
		MessageSeq:        event.MessageSeq,
		ChannelID:         event.ChannelID,
		ChannelType:       event.ChannelType,
		FromUID:           event.FromUID,
		SenderNodeID:      event.SenderNodeID,
		SenderSessionID:   event.SenderSessionID,
		ClientMsgNo:       event.ClientMsgNo,
		RedDot:            event.RedDot,
		Payload:           append([]byte(nil), event.Payload...),
		MessageScopedUIDs: append([]string(nil), event.MessageScopedUIDs...),
	}
}

func onlineDeliveryRoutesFromLegacy(routes []runtimedelivery.Route) []onlinedelivery.Route {
	if routes == nil {
		return nil
	}
	out := make([]onlinedelivery.Route, 0, len(routes))
	for _, route := range routes {
		out = append(out, onlinedelivery.Route{
			UID: route.UID, OwnerNodeID: route.OwnerNodeID, OwnerBootID: route.OwnerBootID,
			OwnerSeq: route.OwnerSeq, SessionID: route.SessionID, DeviceID: route.DeviceID,
			DeviceFlag: route.DeviceFlag, DeviceLevel: route.DeviceLevel,
		})
	}
	return out
}

func legacyDeliveryRoutesFromOnline(routes []onlinedelivery.Route) []runtimedelivery.Route {
	if routes == nil {
		return nil
	}
	out := make([]runtimedelivery.Route, 0, len(routes))
	for _, route := range routes {
		out = append(out, runtimedelivery.Route{
			UID: route.UID, OwnerNodeID: route.OwnerNodeID, OwnerBootID: route.OwnerBootID,
			OwnerSeq: route.OwnerSeq, SessionID: route.SessionID, DeviceID: route.DeviceID,
			DeviceFlag: route.DeviceFlag, DeviceLevel: route.DeviceLevel,
		})
	}
	return out
}

func onlineDeliveryResultFromLegacy(result runtimedelivery.PushResult) onlinedelivery.OwnerPushResult {
	return onlinedelivery.OwnerPushResult{
		Accepted:  onlineDeliveryRoutesFromLegacy(result.Accepted),
		Retryable: onlineDeliveryRoutesFromLegacy(result.Retryable),
		Dropped:   onlineDeliveryRoutesFromLegacy(result.Dropped),
	}
}

func legacyDeliveryResultFromOnline(result onlinedelivery.OwnerPushResult) runtimedelivery.PushResult {
	return runtimedelivery.PushResult{
		Accepted:  legacyDeliveryRoutesFromOnline(result.Accepted),
		Retryable: legacyDeliveryRoutesFromOnline(result.Retryable),
		Dropped:   legacyDeliveryRoutesFromOnline(result.Dropped),
	}
}
