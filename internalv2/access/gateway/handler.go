package gateway

import (
	"context"
	"errors"
	"time"

	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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

// Options configures the internalv2 gateway handler.
type Options struct {
	// Messages processes single SEND commands.
	Messages MessageUsecase
	// Presence activates and deactivates authenticated gateway sessions.
	Presence PresenceUsecase
	// SendTimeout bounds each gateway SEND request.
	SendTimeout time.Duration
}

// Handler adapts pkg/gateway frames to internalv2 message usecases.
type Handler struct {
	messages    MessageUsecase
	presence    PresenceUsecase
	sendTimeout time.Duration
}

// New creates a gateway Handler.
func New(opts Options) *Handler {
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = defaultSendTimeout
	}
	return &Handler{
		messages:    opts.Messages,
		presence:    opts.Presence,
		sendTimeout: opts.SendTimeout,
	}
}

func (h *Handler) OnListenerError(string, error) {}

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
	if h == nil || h.presence == nil {
		return nil
	}
	return h.presence.Deactivate(requestContextFromContext(&ctx), deactivateCommandFromContext(&ctx))
}

func (h *Handler) OnSessionActivateRollback(ctx coregateway.Context, _ error) {
	if h == nil || h.presence == nil {
		return
	}
	_ = h.presence.Deactivate(requestContextFromContext(&ctx), deactivateCommandFromContext(&ctx))
}

func (h *Handler) OnSessionError(coregateway.Context, error) {}

func (h *Handler) OnFrame(ctx coregateway.Context, f frame.Frame) error {
	switch pkt := f.(type) {
	case *frame.PingPacket:
		h.touchPresence(&ctx, time.Now())
		return ctx.WriteFrame(&frame.PongPacket{})
	case *frame.SendPacket:
		return h.handleSend(&ctx, pkt)
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
	_ = h.presence.Touch(requestContextFromContext(ctx), presence.TouchCommand{
		SessionID:    sessionID,
		ActivityUnix: now.Unix(),
	})
}

func (h *Handler) handleSend(ctx *coregateway.Context, pkt *frame.SendPacket) error {
	cmd, err := mapSendCommand(ctx, pkt)
	if err != nil {
		if errors.Is(err, ErrUnauthenticatedSession) {
			return writeSendack(ctx, pkt, message.SendResult{Reason: message.ReasonAuthFail})
		}
		return err
	}
	if ctx == nil || ctx.RequestContext == nil {
		return writeSendack(ctx, pkt, message.SendResult{Reason: message.ReasonSystemError})
	}

	reqCtx, cancel := context.WithTimeout(ctx.RequestContext, h.sendTimeout)
	defer cancel()

	result := h.sendOne(reqCtx, cmd)
	return writeSendack(ctx, pkt, result)
}

func (h *Handler) sendOne(ctx context.Context, cmd message.SendCommand) message.SendResult {
	if h == nil || h.messages == nil {
		return message.SendResult{Reason: message.ReasonSystemError}
	}
	result, err := h.messages.Send(ctx, cmd)
	if err != nil {
		result.Reason = reasonForError(err)
	}
	return result
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
