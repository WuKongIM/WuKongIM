package presence

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	routeActionKindClose         = "close"
	routeActionKindKickThenClose = "kick_then_close"
	routeActionKickReason        = "login in other device"
	defaultRouteCloseDelay       = 2 * time.Second
)

type localActionDispatcher struct {
	app *App
}

func (d localActionDispatcher) ApplyRouteAction(ctx context.Context, action RouteAction) error {
	if d.app == nil {
		return nil
	}
	if action.NodeID != 0 && d.app.localNodeID != 0 && action.NodeID != d.app.localNodeID {
		return ErrRemoteActionDispatchRequired
	}
	return d.app.ApplyRouteAction(ctx, action)
}

func (a *App) Activate(ctx context.Context, cmd ActivateCommand) error {
	if a.router == nil {
		return ErrRouterRequired
	}
	if cmd.Session == nil {
		return ErrSessionRequired
	}

	conn := a.localConnFromActivate(cmd)
	if err := a.online.Register(conn); err != nil {
		return err
	}

	slotID := a.router.SlotForKey(conn.UID)
	result, err := a.authority.RegisterAuthoritative(ctx, RegisterAuthoritativeCommand{
		SlotID: slotID,
		Route:  a.routeFromConn(conn),
	})
	if err != nil {
		fields := append([]wklog.Field{
			wklog.Event("presence.activate.authority_register.failed"),
		}, presenceRouteFields(conn.UID, conn.SessionID, conn.SlotID, 0)...)
		fields = append(fields, wklog.Error(err))
		a.activateLogger().Error("register authoritative route failed", fields...)
		a.online.Unregister(conn.SessionID)
		return err
	}
	if err := a.dispatchActions(ctx, result.Actions); err != nil {
		fields := append([]wklog.Field{
			wklog.Event("presence.activate.route_action_dispatch.failed"),
		}, presenceRouteFields(conn.UID, conn.SessionID, conn.SlotID, a.localNodeID)...)
		fields = append(fields, wklog.Error(err))
		a.activateLogger().Error("dispatch route actions failed", fields...)
		if unregisterErr := a.bestEffortUnregister(ctx, slotID, conn); unregisterErr != nil {
			unregisterFields := append([]wklog.Field{
				wklog.Event("presence.activate.authority_unregister.failed"),
			}, presenceRouteFields(conn.UID, conn.SessionID, slotID, 0)...)
			unregisterFields = append(unregisterFields, wklog.Error(unregisterErr))
			a.activateLogger().Warn("unregister authoritative route failed", unregisterFields...)
		}
		a.online.Unregister(conn.SessionID)
		return err
	}
	return nil
}

func (a *App) Deactivate(ctx context.Context, cmd DeactivateCommand) error {
	conn, ok := a.online.Connection(cmd.SessionID)
	if !ok {
		return nil
	}
	a.online.Unregister(cmd.SessionID)

	if a.router == nil {
		return nil
	}
	uid := conn.UID
	if uid == "" {
		uid = cmd.UID
	}
	if uid == "" {
		return nil
	}
	slotID := conn.SlotID
	if slotID == 0 {
		slotID = a.router.SlotForKey(uid)
	}
	if err := a.bestEffortUnregister(ctx, slotID, conn); err != nil {
		fields := append([]wklog.Field{
			wklog.Event("presence.activate.authority_unregister.failed"),
		}, presenceRouteFields(uid, conn.SessionID, slotID, 0)...)
		fields = append(fields, wklog.Error(err))
		a.activateLogger().Warn("unregister authoritative route failed", fields...)
	}
	return nil
}

func (a *App) ApplyRouteAction(ctx context.Context, action RouteAction) error {
	_ = ctx
	if action.NodeID != 0 && a.localNodeID != 0 && action.NodeID != a.localNodeID {
		return nil
	}
	if action.BootID != a.gatewayBootID {
		return nil
	}

	conn, ok := a.online.Connection(action.SessionID)
	if !ok {
		return nil
	}
	if conn.State == online.LocalRouteStateClosing {
		return nil
	}
	if conn.UID != action.UID {
		err := fmt.Errorf("presence: fenced route mismatch for session %d", action.SessionID)
		fields := append([]wklog.Field{
			wklog.Event("presence.activate.route_action.rejected"),
		}, presenceRouteFields(action.UID, action.SessionID, conn.SlotID, action.NodeID)...)
		fields = append(fields, wklog.Error(err))
		a.activateLogger().Error("reject fenced route action", fields...)
		return err
	}

	conn, ok = a.online.MarkClosing(action.SessionID)
	if !ok || conn.State != online.LocalRouteStateClosing {
		err := fmt.Errorf("presence: failed to move session %d to closing", action.SessionID)
		fields := append([]wklog.Field{
			wklog.Event("presence.activate.route_action.failed"),
		}, presenceRouteFields(action.UID, action.SessionID, conn.SlotID, action.NodeID)...)
		fields = append(fields, wklog.Error(err))
		a.activateLogger().Error("move route to closing failed", fields...)
		return err
	}

	go a.finishRouteAction(conn, action)
	return nil
}

func (a *App) dispatchActions(ctx context.Context, actions []RouteAction) error {
	for _, action := range actions {
		if err := a.actions.ApplyRouteAction(ctx, action); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) bestEffortUnregister(ctx context.Context, slotID uint64, conn online.OnlineConn) error {
	return a.authority.UnregisterAuthoritative(ctx, UnregisterAuthoritativeCommand{
		SlotID: slotID,
		Route:  a.routeFromConn(conn),
	})
}

func (a *App) localConnFromActivate(cmd ActivateCommand) online.OnlineConn {
	listener := cmd.Listener
	if listener == "" && cmd.Session != nil {
		listener = cmd.Session.Listener()
	}
	connectedAt := cmd.ConnectedAt
	if connectedAt.IsZero() {
		connectedAt = a.now()
	}
	slotID := uint64(0)
	if a.router != nil {
		slotID = a.router.SlotForKey(cmd.UID)
	}
	return online.OnlineConn{
		SessionID:   cmd.Session.ID(),
		UID:         cmd.UID,
		DeviceID:    cmd.DeviceID,
		DeviceFlag:  cmd.DeviceFlag,
		DeviceLevel: cmd.DeviceLevel,
		SlotID:      slotID,
		State:       online.LocalRouteStateActive,
		Listener:    listener,
		ConnectedAt: connectedAt,
		Session:     cmd.Session,
	}
}

func (a *App) routeFromConn(conn online.OnlineConn) Route {
	return Route{
		UID:         conn.UID,
		NodeID:      a.localNodeID,
		BootID:      a.gatewayBootID,
		SessionID:   conn.SessionID,
		DeviceID:    conn.DeviceID,
		DeviceFlag:  uint8(conn.DeviceFlag),
		DeviceLevel: uint8(conn.DeviceLevel),
		Listener:    conn.Listener,
	}
}

func (a *App) finishRouteAction(conn online.OnlineConn, action RouteAction) {
	if action.Kind == routeActionKindKickThenClose && conn.Session != nil {
		_ = conn.Session.WriteFrame(&frame.DisconnectPacket{
			ReasonCode: frame.ReasonConnectKick,
			Reason:     routeActionKickReason,
		})
	}
	delay := a.closeDelay
	if action.DelayMS > 0 {
		delay = time.Duration(action.DelayMS) * time.Millisecond
	}
	if conn.Session == nil {
		return
	}
	a.afterFunc(delay, func() {
		_ = conn.Session.Close()
	})
}

func presenceRouteFields(uid string, sessionID, slotID, nodeID uint64) []wklog.Field {
	fields := make([]wklog.Field, 0, 4)
	if uid != "" {
		fields = append(fields, wklog.UID(uid))
	}
	if sessionID != 0 {
		fields = append(fields, wklog.SessionID(sessionID))
	}
	if slotID != 0 {
		fields = append(fields, wklog.SlotID(slotID))
	}
	if nodeID != 0 {
		fields = append(fields, wklog.NodeID(nodeID))
	}
	return fields
}
