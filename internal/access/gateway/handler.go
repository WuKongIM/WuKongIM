package gateway

import (
	"context"
	"errors"
	"time"

	coregateway "github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var ErrUnsupportedFrame = errors.New("access/gateway: unsupported frame")
var ErrUnauthenticatedSession = errors.New("access/gateway: unauthenticated session")
var ErrMissingRequestContext = errors.New("access/gateway: missing request context")
var ErrPresenceRequired = errors.New("access/gateway: presence usecase required")

const defaultSendTimeout = 10 * time.Second

type MessageUsecase interface {
	Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error)
	RecvAck(cmd message.RecvAckCommand) error
	SessionClosed(cmd message.SessionClosedCommand) error
}

type PresenceUsecase interface {
	Activate(ctx context.Context, cmd presence.ActivateCommand) error
	Deactivate(ctx context.Context, cmd presence.DeactivateCommand) error
}

type Options struct {
	LocalNodeID uint64
	Online      online.Registry
	Messages    MessageUsecase
	Presence    PresenceUsecase
	Now         func() time.Time
	SendTimeout time.Duration
	Logger      wklog.Logger
}

type Handler struct {
	localNodeID uint64
	online      online.Registry
	messages    MessageUsecase
	presence    PresenceUsecase
	now         func() time.Time
	sendTimeout time.Duration
	logger      wklog.Logger
}

func New(opts Options) *Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = defaultSendTimeout
	}
	if opts.Online == nil {
		if provider, ok := opts.Messages.(interface{ OnlineRegistry() online.Registry }); ok {
			opts.Online = provider.OnlineRegistry()
		}
		if opts.Online == nil {
			opts.Online = online.NewRegistry()
		}
	}
	if opts.Messages == nil {
		opts.Messages = message.New(message.Options{
			Online:      opts.Online,
			LocalNodeID: opts.LocalNodeID,
			Now:         opts.Now,
		})
	}

	return &Handler{
		localNodeID: opts.LocalNodeID,
		online:      opts.Online,
		messages:    opts.Messages,
		presence:    opts.Presence,
		now:         opts.Now,
		sendTimeout: opts.SendTimeout,
		logger:      opts.Logger,
	}
}

func (h *Handler) OnListenerError(string, error) {}

func (h *Handler) OnSessionActivate(ctx *coregateway.Context) (*frame.ConnackPacket, error) {
	if h == nil {
		return nil, nil
	}
	if h.presence == nil {
		return nil, ErrPresenceRequired
	}
	cmd, err := activateCommandFromContext(ctx, h.now())
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("access.gateway.conn.auth_failed"),
		}, gatewayContextFields(ctx)...)
		fields = append(fields, wklog.Error(err))
		h.connLogger().Warn("reject unauthenticated session", fields...)
		return nil, err
	}
	return nil, h.presence.Activate(requestContextFromContext(ctx), cmd)
}

func (h *Handler) OnSessionOpen(*coregateway.Context) error {
	return nil
}

func (h *Handler) OnSessionClose(ctx *coregateway.Context) error {
	if h == nil || ctx == nil || ctx.Session == nil {
		return nil
	}

	var err error
	if h.messages != nil {
		uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
		err = errors.Join(err, h.messages.SessionClosed(message.SessionClosedCommand{
			UID:       uid,
			SessionID: ctx.Session.ID(),
		}))
	}
	if h.presence != nil {
		err = errors.Join(err, h.presence.Deactivate(requestContextFromContext(ctx), deactivateCommandFromContext(ctx)))
	}
	return err
}

func (h *Handler) OnSessionError(*coregateway.Context, error) {}

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
	return fields
}

func gatewaySendFields(ctx *coregateway.Context, channelID string, channelType uint8) []wklog.Field {
	fields := gatewayContextFields(ctx)
	if channelID != "" {
		fields = append(fields, wklog.ChannelID(channelID))
	}
	fields = append(fields, wklog.ChannelType(int64(channelType)))
	return fields
}
