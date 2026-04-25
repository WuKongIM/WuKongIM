package gateway

import (
	"context"
	"time"

	coregateway "github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
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
		UID:         uid,
		DeviceID:    deviceIDFromValue(ctx.Session.Value(coregateway.SessionValueDeviceID)),
		DeviceFlag:  deviceFlagFromValue(ctx.Session.Value(coregateway.SessionValueDeviceFlag)),
		DeviceLevel: deviceLevelFromValue(ctx.Session.Value(coregateway.SessionValueDeviceLevel)),
		Listener:    listener,
		ConnectedAt: now,
		Session:     ctx.Session,
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
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func deviceFlagFromValue(value any) frame.DeviceFlag {
	switch v := value.(type) {
	case frame.DeviceFlag:
		return v
	case uint8:
		return frame.DeviceFlag(v)
	case int:
		return frame.DeviceFlag(v)
	case int32:
		return frame.DeviceFlag(v)
	case int64:
		return frame.DeviceFlag(v)
	default:
		return 0
	}
}

func deviceLevelFromValue(value any) frame.DeviceLevel {
	switch v := value.(type) {
	case frame.DeviceLevel:
		return v
	case uint8:
		return frame.DeviceLevel(v)
	case int:
		return frame.DeviceLevel(v)
	case int32:
		return frame.DeviceLevel(v)
	case int64:
		return frame.DeviceLevel(v)
	default:
		return 0
	}
}
