package gateway

import (
	"context"
	"errors"
	"time"

	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	// ErrUnsupportedFrame reports a gateway frame that internalv2 does not handle.
	ErrUnsupportedFrame = errors.New("internalv2/access/gateway: unsupported frame")
	// ErrUnauthenticatedSession reports a SEND without an authenticated session uid.
	ErrUnauthenticatedSession = errors.New("internalv2/access/gateway: unauthenticated session")
	// ErrMissingRequestContext reports a gateway event without a request context.
	ErrMissingRequestContext = errors.New("internalv2/access/gateway: missing request context")
	// ErrSendBatchResultCountMismatch reports non-aligned batch usecase results.
	ErrSendBatchResultCountMismatch = errors.New("internalv2/access/gateway: send batch result count mismatch")
	// ErrPresenceRequired reports an activation request without a presence usecase.
	ErrPresenceRequired = errors.New("internalv2/access/gateway: presence usecase required")
)

const defaultSendTimeout = 5 * time.Second

// MessageUsecase is the single-message entry used by the gateway adapter.
type MessageUsecase interface {
	Send(context.Context, message.SendCommand) (message.SendResult, error)
}

// MessageBatchUsecase is the batch SEND entry used by gateway micro-batching.
type MessageBatchUsecase interface {
	SendBatch([]message.SendBatchItem) []message.SendBatchItemResult
}

// PresenceUsecase is the session lifecycle entry used by the gateway adapter.
type PresenceUsecase interface {
	Activate(context.Context, presence.ActivateCommand) error
	Deactivate(context.Context, presence.DeactivateCommand) error
	Touch(context.Context, presence.TouchCommand) error
}

// DeliveryUsecase is the delivery feedback entry used by the gateway adapter.
type DeliveryUsecase interface {
	Recvack(context.Context, delivery.RecvackCommand) error
	SessionClosed(context.Context, delivery.SessionClosedCommand) error
}

// Options configures the internalv2 gateway handler.
type Options struct {
	// Messages processes single SEND commands.
	Messages MessageUsecase
	// Presence activates and deactivates authenticated gateway sessions.
	Presence PresenceUsecase
	// Delivery receives client recvacks and session close cleanup events.
	Delivery DeliveryUsecase
	// OwnerNodeID is the local gateway owner node id stamped on SEND commands.
	OwnerNodeID uint64
	// SendTimeout bounds each gateway SEND request.
	SendTimeout time.Duration
	// SendackObserver receives low-cardinality SEND acknowledgement diagnostics.
	SendackObserver SendackObserver
	// Logger records gateway boundary errors that otherwise terminate in adapter callbacks.
	Logger wklog.Logger
}

// Handler adapts pkg/gateway frames to internalv2 message usecases.
type Handler struct {
	messages        MessageUsecase
	presence        PresenceUsecase
	delivery        DeliveryUsecase
	ownerNodeID     uint64
	sendTimeout     time.Duration
	sendackObserver SendackObserver
	logger          wklog.Logger
}

// New creates a gateway Handler.
func New(opts Options) *Handler {
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = defaultSendTimeout
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &Handler{
		messages:        opts.Messages,
		presence:        opts.Presence,
		delivery:        opts.Delivery,
		ownerNodeID:     opts.OwnerNodeID,
		sendTimeout:     opts.SendTimeout,
		sendackObserver: opts.SendackObserver,
		logger:          opts.Logger,
	}
}

func (h *Handler) OnListenerError(listener string, err error) {
	if err == nil {
		return
	}
	h.connLogger().Error("gateway listener error",
		wklog.Event("internalv2.access.gateway.listener_error"),
		wklog.String("listener", listener),
		wklog.Error(err),
	)
}

func (h *Handler) OnSessionActivate(ctx *coregateway.Context) (*frame.ConnackPacket, error) {
	if h == nil || h.presence == nil {
		return nil, ErrPresenceRequired
	}
	cmd, err := activateCommandFromContext(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	if err := h.presence.Activate(requestContextFromContext(ctx), cmd); err != nil {
		return nil, classifyActivationError(err)
	}
	return nil, nil
}

func (h *Handler) OnSessionOpen(coregateway.Context) error { return nil }

func (h *Handler) OnSessionClose(ctx coregateway.Context) error {
	if h == nil {
		return nil
	}
	reqCtx := requestContextFromContext(&ctx)
	var presenceErr error
	if h.presence != nil {
		presenceErr = h.presence.Deactivate(reqCtx, deactivateCommandFromContext(&ctx))
		if presenceErr != nil {
			fields := append([]wklog.Field{
				wklog.Event("internalv2.access.gateway.session_close_presence_failed"),
				wklog.SourceModule("presence.deactivate"),
			}, gatewayContextFields(&ctx)...)
			fields = append(fields, wklog.Error(presenceErr))
			h.connLogger().Warn("gateway session presence cleanup failed", fields...)
		}
	}
	var deliveryErr error
	if h.delivery != nil && ctx.Session != nil {
		uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
		if uid != "" && ctx.Session.ID() != 0 {
			deliveryErr = h.delivery.SessionClosed(reqCtx, delivery.SessionClosedCommand{UID: uid, SessionID: ctx.Session.ID()})
			if deliveryErr != nil {
				fields := append([]wklog.Field{
					wklog.Event("internalv2.access.gateway.session_close_delivery_failed"),
					wklog.SourceModule("delivery.session_closed"),
				}, gatewayContextFields(&ctx)...)
				fields = append(fields, wklog.Error(deliveryErr))
				h.connLogger().Warn("gateway session delivery cleanup failed", fields...)
			}
		}
	}
	return errors.Join(presenceErr, deliveryErr)
}

func (h *Handler) OnSessionActivateRollback(ctx coregateway.Context, _ error) {
	if h == nil || h.presence == nil {
		return
	}
	if err := h.presence.Deactivate(requestContextFromContext(&ctx), deactivateCommandFromContext(&ctx)); err != nil {
		fields := append([]wklog.Field{
			wklog.Event("internalv2.access.gateway.activation_rollback_failed"),
			wklog.SourceModule("presence.deactivate"),
		}, gatewayContextFields(&ctx)...)
		fields = append(fields, wklog.Error(err))
		h.connLogger().Warn("gateway activation rollback failed", fields...)
	}
}

func (h *Handler) OnSessionError(ctx coregateway.Context, err error) {
	if err == nil {
		return
	}
	fields := append([]wklog.Field{
		wklog.Event("internalv2.access.gateway.session_error"),
	}, gatewayContextFields(&ctx)...)
	fields = append(fields, wklog.Error(err))
	h.connLogger().Warn("gateway session error", fields...)
}

func (h *Handler) OnFrame(ctx coregateway.Context, f frame.Frame) error {
	switch pkt := f.(type) {
	case *frame.PingPacket:
		h.touchPresence(&ctx, time.Now())
		return ctx.WriteFrame(&frame.PongPacket{})
	case *frame.SendPacket:
		return h.handleSend(&ctx, pkt)
	case *frame.RecvackPacket:
		return h.handleRecvack(&ctx, pkt)
	default:
		return ErrUnsupportedFrame
	}
}

func (h *Handler) touchPresence(ctx *coregateway.Context, now time.Time) {
	if h == nil || h.presence == nil || ctx == nil || ctx.Session == nil {
		return
	}
	sessionID := ctx.Session.ID()
	if sessionID == 0 {
		return
	}
	if err := h.presence.Touch(requestContextFromContext(ctx), presence.TouchCommand{
		SessionID:    sessionID,
		ActivityUnix: now.Unix(),
	}); err != nil {
		fields := append([]wklog.Field{
			wklog.Event("internalv2.access.gateway.presence_touch_failed"),
			wklog.SourceModule("presence.touch"),
		}, gatewayContextFields(ctx)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("gateway presence touch failed", fields...)
	}
}

func (h *Handler) handleSend(ctx *coregateway.Context, pkt *frame.SendPacket) error {
	cmd, err := mapSendCommandWithPayload(ctx, pkt, h.ownerNodeID, true)
	if err != nil {
		if errors.Is(err, ErrUnauthenticatedSession) {
			return h.writeSendack(ctx, pkt, message.SendResult{Reason: message.ReasonAuthFail}, sendackSourceSingleResult, sendackErrorClassUnauthenticated)
		}
		h.logSendMappingFailure(ctx, pkt, err)
		return err
	}
	if ctx == nil || ctx.RequestContext == nil {
		h.logMissingRequestContext(ctx, pkt, sendackSourceSingleMissingRequestContext)
		return h.writeSendack(ctx, pkt, message.SendResult{Reason: message.ReasonSystemError}, sendackSourceSingleMissingRequestContext, sendackErrorClassMissingRequestContext)
	}

	reqCtx, cancel := context.WithTimeout(ctx.RequestContext, h.sendTimeout)
	defer cancel()

	result, source, class := h.sendOne(reqCtx, cmd)
	return h.writeSendack(ctx, pkt, result, source, class)
}

func (h *Handler) handleRecvack(ctx *coregateway.Context, pkt *frame.RecvackPacket) error {
	if h == nil || h.delivery == nil || ctx == nil || ctx.Session == nil || pkt == nil {
		return nil
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" || ctx.Session.ID() == 0 || pkt.MessageID <= 0 {
		return nil
	}
	err := h.delivery.Recvack(requestContextFromContext(ctx), delivery.RecvackCommand{
		UID:        uid,
		SessionID:  ctx.Session.ID(),
		MessageID:  uint64(pkt.MessageID),
		MessageSeq: pkt.MessageSeq,
	})
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("internalv2.access.gateway.recvack_failed"),
			wklog.SourceModule("delivery.recvack"),
			wklog.MessageID(pkt.MessageID),
			wklog.MessageSeq(uint64(pkt.MessageSeq)),
		}, gatewayContextFields(ctx)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("gateway recvack failed", fields...)
	}
	return err
}

func (h *Handler) sendOne(ctx context.Context, cmd message.SendCommand) (message.SendResult, string, string) {
	if h == nil || h.messages == nil {
		if h != nil {
			h.frameLogger().Error("gateway message usecase missing",
				wklog.Event("internalv2.access.gateway.message_usecase_missing"),
				wklog.SourceModule("message.send"),
			)
		}
		return message.SendResult{Reason: message.ReasonSystemError}, sendackSourceSingleResult, sendackErrorClassOther
	}
	result, err := h.messages.Send(ctx, cmd)
	if err != nil {
		result.Reason = reasonForError(err)
		class := sendackErrorClassForError(err)
		h.logSendFailure(cmd, sendackSourceSingleError, class, err)
		return result, sendackSourceSingleError, class
	}
	return result, sendackSourceSingleResult, sendackErrorClassNone
}

func (h *Handler) writeSendack(ctx *coregateway.Context, pkt *frame.SendPacket, result message.SendResult, source string, class string) error {
	if err := writeSendack(ctx, pkt, result); err != nil {
		fields := append([]wklog.Field{
			wklog.Event("internalv2.access.gateway.sendack_write_failed"),
			wklog.SourceModule("gateway.write_sendack"),
			wklog.String("source", source),
			wklog.String("errorClass", class),
		}, gatewaySendFields(ctx, pkt)...)
		fields = append(fields, wklog.Error(err))
		h.frameLogger().Warn("gateway sendack write failed", fields...)
		return err
	}
	if h != nil && h.sendackObserver != nil {
		h.sendackObserver.SendackWritten(SendackEvent{Reason: result.Reason, Source: source, ErrorClass: class})
	}
	return nil
}

func (h *Handler) logSendFailure(cmd message.SendCommand, source, class string, err error) {
	if err == nil || !shouldLogSendErrorClass(class) {
		return
	}
	fields := []wklog.Field{
		wklog.Event("internalv2.access.gateway.send_failed"),
		wklog.SourceModule("message.send"),
		wklog.String("source", source),
		wklog.String("errorClass", class),
		wklog.UID(cmd.FromUID),
		wklog.ChannelID(cmd.ChannelID),
		wklog.ChannelType(int64(cmd.ChannelType)),
		wklog.Error(err),
	}
	if cmd.ClientMsgNo != "" {
		fields = append(fields, wklog.ClientMsgNo(cmd.ClientMsgNo))
	}
	h.frameLogger().Warn("gateway send failed", fields...)
}

func (h *Handler) logSendMappingFailure(ctx *coregateway.Context, pkt *frame.SendPacket, err error) {
	if err == nil {
		return
	}
	fields := append([]wklog.Field{
		wklog.Event("internalv2.access.gateway.send_rejected"),
		wklog.SourceModule("gateway.map_send"),
	}, gatewaySendFields(ctx, pkt)...)
	fields = append(fields, wklog.Error(err))
	h.frameLogger().Warn("gateway send rejected", fields...)
}

func (h *Handler) logMissingRequestContext(ctx *coregateway.Context, pkt *frame.SendPacket, source string) {
	fields := append([]wklog.Field{
		wklog.Event("internalv2.access.gateway.missing_request_context"),
		wklog.SourceModule("gateway.request_context"),
		wklog.String("source", source),
		wklog.String("errorClass", sendackErrorClassMissingRequestContext),
		wklog.Error(ErrMissingRequestContext),
	}, gatewaySendFields(ctx, pkt)...)
	h.frameLogger().Warn("gateway request context missing", fields...)
}

func shouldLogSendErrorClass(class string) bool {
	switch class {
	case sendackErrorClassNotLeader, sendackErrorClassStaleRoute, sendackErrorClassRouteNotReady,
		sendackErrorClassCanceled, sendackErrorClassTimeout, sendackErrorClassOther,
		sendackErrorClassMissingRequestContext:
		return true
	default:
		return false
	}
}

func (h *Handler) connLogger() wklog.Logger {
	if h == nil || h.logger == nil {
		return wklog.NewNop()
	}
	return h.logger.Named("conn")
}

func (h *Handler) frameLogger() wklog.Logger {
	if h == nil || h.logger == nil {
		return wklog.NewNop()
	}
	return h.logger.Named("frame")
}

func gatewayContextFields(ctx *coregateway.Context) []wklog.Field {
	if ctx == nil || ctx.Session == nil {
		return nil
	}
	fields := []wklog.Field{wklog.SessionID(ctx.Session.ID())}
	if uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string); uid != "" {
		fields = append(fields, wklog.UID(uid))
	}
	if ctx.Listener != "" {
		fields = append(fields, wklog.String("listener", ctx.Listener))
	}
	return fields
}

func gatewaySendFields(ctx *coregateway.Context, pkt *frame.SendPacket) []wklog.Field {
	fields := gatewayContextFields(ctx)
	if pkt == nil {
		return fields
	}
	if pkt.ChannelID != "" {
		fields = append(fields, wklog.ChannelID(pkt.ChannelID))
	}
	fields = append(fields,
		wklog.ChannelType(int64(pkt.ChannelType)),
		wklog.Int("clientSeq", int(pkt.ClientSeq)),
	)
	if pkt.ClientMsgNo != "" {
		fields = append(fields, wklog.ClientMsgNo(pkt.ClientMsgNo))
	}
	return fields
}

var _ coregateway.Handler = (*Handler)(nil)
var _ coregateway.SendBatchHandler = (*Handler)(nil)

// activationAuthFailureError preserves the original activation error and exposes a bounded gateway metric class.
type activationAuthFailureError struct {
	class string
	err   error
}

func (e activationAuthFailureError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e activationAuthFailureError) Unwrap() error {
	return e.err
}

func (e activationAuthFailureError) GatewayAuthFailure() string {
	return e.class
}

func classifyActivationError(err error) error {
	if err == nil {
		return nil
	}
	class := ""
	switch {
	case errors.Is(err, authoritypresence.ErrRouteNotReady):
		class = "activation_route_not_ready"
	case errors.Is(err, authoritypresence.ErrNotLeader):
		class = "activation_not_leader"
	case errors.Is(err, authoritypresence.ErrStaleRoute):
		class = "activation_stale_route"
	case errors.Is(err, context.Canceled):
		class = "activation_context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		class = "activation_context_deadline"
	}
	if class == "" {
		return err
	}
	return activationAuthFailureError{class: class, err: err}
}
