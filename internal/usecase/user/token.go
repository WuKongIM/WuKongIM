package user

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	updateTokenCloseDelay = 10 * time.Second
	updateTokenKickReason = "账号在其他设备上登录"
)

func (a *App) UpdateToken(ctx context.Context, cmd UpdateTokenCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	if a.users == nil {
		return ErrUserStoreRequired
	}
	if a.devices == nil {
		return ErrDeviceStoreRequired
	}

	_, err := a.users.GetUser(ctx, cmd.UID)
	if errors.Is(err, metadb.ErrNotFound) {
		if err := a.users.CreateUser(ctx, metadb.User{UID: cmd.UID}); err != nil && !errors.Is(err, metadb.ErrAlreadyExists) {
			return err
		}
	} else if err != nil {
		return err
	}

	if err := a.devices.UpsertDevice(ctx, metadb.Device{
		UID:         cmd.UID,
		DeviceFlag:  int64(cmd.DeviceFlag),
		Token:       cmd.Token,
		DeviceLevel: int64(cmd.DeviceLevel),
	}); err != nil {
		return err
	}

	if cmd.DeviceLevel != frame.DeviceLevelMaster || a.online == nil {
		return nil
	}

	for _, conn := range a.online.ConnectionsByUID(cmd.UID) {
		if conn.DeviceFlag != cmd.DeviceFlag || conn.Session == nil {
			continue
		}
		sess := conn.Session
		_ = sess.WriteFrame(&frame.DisconnectPacket{
			ReasonCode: frame.ReasonConnectKick,
			Reason:     updateTokenKickReason,
		})
		a.afterFunc(updateTokenCloseDelay, func() { _ = sess.Close() })
	}
	return nil
}
