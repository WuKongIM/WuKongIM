package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func activateCommandFromContext(ctx *coregateway.Context, now time.Time) (presence.ActivateCommand, error) {
	if ctx == nil || ctx.Session == nil {
		return presence.ActivateCommand{}, ErrUnauthenticatedSession
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" {
		return presence.ActivateCommand{}, ErrUnauthenticatedSession
	}

	listener := ctx.Listener
	if listener == "" {
		listener = ctx.Session.Listener()
	}

	return presence.ActivateCommand{
		UID:           uid,
		DeviceID:      deviceIDFromValue(ctx.Session.Value(coregateway.SessionValueDeviceID)),
		DeviceFlag:    deviceFlagFromValue(ctx.Session.Value(coregateway.SessionValueDeviceFlag)),
		DeviceLevel:   deviceLevelFromValue(ctx.Session.Value(coregateway.SessionValueDeviceLevel)),
		Listener:      listener,
		ConnectedUnix: now.Unix(),
		SessionID:     ctx.Session.ID(),
		Session:       gatewayPresenceSession{ctx: ctx},
	}, nil
}

func deactivateCommandFromContext(ctx *coregateway.Context) presence.DeactivateCommand {
	if ctx == nil || ctx.Session == nil {
		return presence.DeactivateCommand{}
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	return presence.DeactivateCommand{
		UID:       uid,
		SessionID: ctx.Session.ID(),
	}
}

func requestContextFromContext(ctx *coregateway.Context) context.Context {
	if ctx == nil || ctx.RequestContext == nil {
		return context.Background()
	}
	return ctx.RequestContext
}

func deviceIDFromValue(value any) string {
	if deviceID, _ := value.(string); deviceID != "" {
		return deviceID
	}
	return ""
}

func deviceFlagFromValue(value any) uint8 {
	switch v := value.(type) {
	case frame.DeviceFlag:
		return uint8(v)
	case uint8:
		return v
	case int:
		return uint8(v)
	case int32:
		return uint8(v)
	case int64:
		return uint8(v)
	default:
		return 0
	}
}

func deviceLevelFromValue(value any) uint8 {
	switch v := value.(type) {
	case frame.DeviceLevel:
		return uint8(v)
	case uint8:
		return v
	case int:
		return uint8(v)
	case int32:
		return uint8(v)
	case int64:
		return uint8(v)
	default:
		return 0
	}
}

// gatewayPresenceSession closes a gateway session for presence conflict actions.
type gatewayPresenceSession struct {
	// ctx carries the concrete session and core close hook captured at activation.
	ctx *coregateway.Context
}

var _ presence.SessionHandle = gatewayPresenceSession{}

// WriteDelivery writes a server-push frame to the concrete gateway session.
func (s gatewayPresenceSession) WriteDelivery(payload any) error {
	f, ok := payload.(frame.Frame)
	if !ok {
		return errors.New("internalv2/access/gateway: delivery payload must be a frame")
	}
	if s.ctx == nil || s.ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return s.ctx.Session.WriteFrame(f)
}

// CloseSession closes the concrete gateway session using the core close path when present.
func (s gatewayPresenceSession) CloseSession(reason string) error {
	if s.ctx == nil {
		return session.ErrSessionClosed
	}
	var err error
	if reason != "" {
		err = errors.New(reason)
	}
	return s.ctx.CloseSession(coregateway.CloseReasonPolicyViolation, err)
}

// RemoteAddr returns the client address observed by the concrete gateway session.
func (s gatewayPresenceSession) RemoteAddr() string {
	if s.ctx == nil || s.ctx.Session == nil {
		return ""
	}
	return s.ctx.Session.RemoteAddr()
}

// LocalAddr returns the listener address observed by the concrete gateway session.
func (s gatewayPresenceSession) LocalAddr() string {
	if s.ctx == nil || s.ctx.Session == nil {
		return ""
	}
	return s.ctx.Session.LocalAddr()
}
