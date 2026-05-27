package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
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

// Options configures the internalv2 gateway handler.
type Options struct {
	// Messages processes single SEND commands.
	Messages MessageUsecase
	// SendTimeout bounds each gateway SEND request.
	SendTimeout time.Duration
}

// Handler adapts pkg/gateway frames to internalv2 message usecases.
type Handler struct {
	messages    MessageUsecase
	sendTimeout time.Duration
}

// New creates a gateway Handler.
func New(opts Options) *Handler {
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = defaultSendTimeout
	}
	return &Handler{
		messages:    opts.Messages,
		sendTimeout: opts.SendTimeout,
	}
}

func (h *Handler) OnListenerError(string, error) {}

func (h *Handler) OnSessionOpen(coregateway.Context) error { return nil }

func (h *Handler) OnSessionClose(coregateway.Context) error { return nil }

func (h *Handler) OnSessionError(coregateway.Context, error) {}

func (h *Handler) OnFrame(ctx coregateway.Context, f frame.Frame) error {
	pkt, ok := f.(*frame.SendPacket)
	if !ok {
		return ErrUnsupportedFrame
	}
	return h.handleSend(&ctx, pkt)
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
