package delivery

import (
	"context"
	"errors"
	"time"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var errRecvMessageIDOverflow = errors.New("internal/infra/delivery: delivery message id overflows recv packet")

// LocalSessionWriterOptions configures the narrow owner-local session adapter.
type LocalSessionWriterOptions struct {
	// Online resolves exact active owner-local sessions.
	Online *online.Registry
	// Now supplies packet timestamps.
	Now func() time.Time
	// Logger records bounded physical write failures.
	Logger wklog.Logger
}

// LocalSessionWriter validates, builds, and writes one exact local route.
// Pending-ACK state belongs to runtime/delivery and is deliberately absent.
type LocalSessionWriter struct {
	// online is the owner-local exact-session registry.
	online *online.Registry
	// now supplies protocol packet timestamps.
	now func() time.Time
	// logger records bounded packet-build and write diagnostics.
	logger wklog.Logger
}

var _ runtimedelivery.LocalSessionWriter = (*LocalSessionWriter)(nil)

// NewLocalSessionWriter creates the owner-local physical write adapter.
func NewLocalSessionWriter(opts LocalSessionWriterOptions) *LocalSessionWriter {
	return &LocalSessionWriter{online: opts.Online, now: opts.Now, logger: opts.Logger}
}

// WriteSession performs final exact-session validation and one physical write.
func (w *LocalSessionWriter) WriteSession(ctx context.Context, write runtimedelivery.LocalSessionWrite) runtimedelivery.SessionWriteResult {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return runtimedelivery.SessionWriteResult{Disposition: runtimedelivery.SessionWriteRetryable, Err: err}
		}
	}
	session, ok := exactLocalSession(w.online, write.Route)
	if !ok {
		return runtimedelivery.SessionWriteResult{Disposition: runtimedelivery.SessionWriteDropped}
	}
	packet, err := buildOnlineDeliveryRecvPacket(write.Event, write.Route.UID, int32(w.nowTime().Unix()))
	if err != nil {
		w.loggerOrNop().Warn("delivery recv packet build failed",
			wklog.Event("internal.infra.delivery.recv_packet_build_failed"),
			wklog.UID(write.Route.UID),
			wklog.SessionID(write.Route.SessionID),
			wklog.Uint64("messageID", write.Event.MessageID),
			wklog.Error(err),
		)
		return runtimedelivery.SessionWriteResult{Disposition: runtimedelivery.SessionWriteDropped, Err: err}
	}
	if err := session.Session.WriteDelivery(packet); err != nil {
		disposition := runtimedelivery.SessionWriteRetryable
		if terminalLocalDeliveryWriteError(err) {
			disposition = runtimedelivery.SessionWriteDropped
		}
		w.loggerOrNop().Warn("delivery write failed",
			wklog.Event("internal.infra.delivery.write_failed"),
			wklog.UID(write.Route.UID),
			wklog.SessionID(write.Route.SessionID),
			wklog.Uint64("messageID", write.Event.MessageID),
			wklog.Bool("terminal", disposition == runtimedelivery.SessionWriteDropped),
			wklog.Error(err),
		)
		return runtimedelivery.SessionWriteResult{Disposition: disposition, Err: err}
	}
	return runtimedelivery.SessionWriteResult{Disposition: runtimedelivery.SessionWriteAccepted}
}

func exactLocalSession(registry *online.Registry, route onlinedelivery.Route) (online.LocalSession, bool) {
	if registry == nil || route.UID == "" || route.SessionID == 0 || route.OwnerNodeID == 0 || route.OwnerBootID == 0 || route.OwnerSeq == 0 {
		return online.LocalSession{}, false
	}
	session, ok := registry.LocalSession(route.SessionID)
	if !ok || session.State != online.RouteStateActive || session.Session == nil {
		return online.LocalSession{}, false
	}
	local := session.Route
	if local.UID != route.UID ||
		local.SessionID != route.SessionID ||
		local.OwnerNodeID != route.OwnerNodeID ||
		local.OwnerBootID != route.OwnerBootID ||
		local.OwnerSeq != route.OwnerSeq ||
		local.DeviceID != route.DeviceID ||
		local.DeviceFlag != route.DeviceFlag ||
		local.DeviceLevel != route.DeviceLevel {
		return online.LocalSession{}, false
	}
	return session, true
}

func terminalLocalDeliveryWriteError(err error) bool {
	return errors.Is(err, gatewaysession.ErrSessionClosed) ||
		errors.Is(err, gatewaytransport.ErrOutboundBytesExceeded)
}

func buildOnlineDeliveryRecvPacket(event channelappendcontract.CommittedEnvelope, uid string, timestamp int32) (*frame.RecvPacket, error) {
	if event.MessageID > uint64(1<<63-1) {
		return nil, errRecvMessageIDOverflow
	}
	channelID := event.ChannelID
	if event.ChannelType == frame.ChannelTypePerson {
		channelID = onlineDeliveryPersonChannelView(event, uid)
	}
	return &frame.RecvPacket{
		Framer:      frame.Framer{RedDot: event.RedDot},
		MessageID:   int64(event.MessageID),
		MessageSeq:  event.MessageSeq,
		ClientMsgNo: event.ClientMsgNo,
		Timestamp:   timestamp,
		ChannelID:   channelID,
		ChannelType: event.ChannelType,
		FromUID:     event.FromUID,
		Payload:     event.Payload,
	}, nil
}

func onlineDeliveryPersonChannelView(event channelappendcontract.CommittedEnvelope, recipientUID string) string {
	if recipientUID == "" {
		return event.ChannelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
	if err != nil {
		return event.FromUID
	}
	switch recipientUID {
	case left:
		return right
	case right:
		return left
	default:
		return event.FromUID
	}
}

func (w *LocalSessionWriter) nowTime() time.Time {
	if w != nil && w.now != nil {
		return w.now()
	}
	return time.Now()
}

func (w *LocalSessionWriter) loggerOrNop() wklog.Logger {
	if w == nil || w.logger == nil {
		return wklog.NewNop()
	}
	return w.logger
}
