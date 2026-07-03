package user

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	deviceQuitCloseDelay   = 2 * time.Second
	systemUIDChannelID     = "__wk_internal_system_uids__"
	systemUIDChannelType   = int64(frame.SYSTEM)
	systemUIDPageLimit     = 1000
	deviceQuitMissingToken = ""
)

// DeviceQuit clears stored device tokens and kicks matching local sessions.
func (a *App) DeviceQuit(ctx context.Context, cmd DeviceQuitCommand) error {
	if a == nil || a.devices == nil || a.deviceReader == nil {
		return ErrDeviceStoreRequired
	}
	for _, flag := range deviceQuitFlags(cmd.DeviceFlag) {
		_ = a.quitDevice(ctx, cmd.UID, flag)
	}
	return nil
}

// OnlineStatus returns legacy online-status entries for online devices only.
func (a *App) OnlineStatus(ctx context.Context, uids []string) ([]OnlineStatus, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	if a != nil && a.presence != nil {
		routesByUID, err := a.presence.EndpointsByUIDs(ctx, uids)
		if err != nil {
			return nil, err
		}
		statuses := make([]OnlineStatus, 0)
		for _, uid := range uids {
			for _, route := range routesByUID[uid] {
				statuses = append(statuses, OnlineStatus{
					UID:        route.UID,
					DeviceFlag: route.DeviceFlag,
					Online:     1,
				})
			}
		}
		return statuses, nil
	}
	if a == nil || a.online == nil {
		return nil, nil
	}
	statuses := make([]OnlineStatus, 0)
	for _, uid := range uids {
		for _, conn := range a.online.ConnectionsByUID(uid) {
			statuses = append(statuses, OnlineStatus{
				UID:        conn.UID,
				DeviceFlag: uint8(conn.DeviceFlag),
				Online:     1,
			})
		}
	}
	return statuses, nil
}

// AddSystemUIDs persists system account UIDs and adds them to the local cache.
func (a *App) AddSystemUIDs(ctx context.Context, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	if a == nil || a.systemUIDs == nil {
		return ErrUserStoreRequired
	}
	if err := a.systemUIDs.AddChannelSubscribers(ctx, systemUIDChannelID, systemUIDChannelType, uids); err != nil {
		return err
	}
	return a.AddSystemUIDsToCache(uids)
}

// RemoveSystemUIDs removes persisted system account UIDs and local cache rows.
func (a *App) RemoveSystemUIDs(ctx context.Context, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	if a == nil || a.systemUIDs == nil {
		return ErrUserStoreRequired
	}
	if err := a.systemUIDs.RemoveChannelSubscribers(ctx, systemUIDChannelID, systemUIDChannelType, uids); err != nil {
		return err
	}
	return a.RemoveSystemUIDsFromCache(uids)
}

// ListSystemUIDs returns the persisted system account UID list.
func (a *App) ListSystemUIDs(ctx context.Context) ([]string, error) {
	if a == nil || a.systemUIDs == nil {
		return nil, ErrUserStoreRequired
	}
	var out []string
	cursor := ""
	for {
		uids, nextCursor, done, err := a.systemUIDs.ListChannelSubscribers(ctx, systemUIDChannelID, systemUIDChannelType, cursor, systemUIDPageLimit)
		if err != nil {
			return nil, err
		}
		out = append(out, uids...)
		if done {
			return out, nil
		}
		if nextCursor == "" || nextCursor == cursor {
			return out, nil
		}
		cursor = nextCursor
	}
}

// AddSystemUIDsToCache adds UIDs to the process-local system account cache.
func (a *App) AddSystemUIDsToCache(uids []string) error {
	if a == nil {
		return nil
	}
	a.systemUIDCacheMu.Lock()
	defer a.systemUIDCacheMu.Unlock()
	if a.systemUIDCache == nil {
		a.systemUIDCache = make(map[string]struct{})
	}
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		a.systemUIDCache[uid] = struct{}{}
	}
	return nil
}

// RemoveSystemUIDsFromCache removes UIDs from the process-local system account cache.
func (a *App) RemoveSystemUIDsFromCache(uids []string) error {
	if a == nil {
		return nil
	}
	a.systemUIDCacheMu.Lock()
	defer a.systemUIDCacheMu.Unlock()
	for _, uid := range uids {
		delete(a.systemUIDCache, uid)
	}
	return nil
}

// IsSystemUID reports whether uid is currently in the process-local system cache.
func (a *App) IsSystemUID(uid string) bool {
	if a == nil {
		return false
	}
	if uid != "" && uid == a.systemUID {
		return true
	}
	a.systemUIDCacheMu.RLock()
	defer a.systemUIDCacheMu.RUnlock()
	_, ok := a.systemUIDCache[uid]
	return ok
}

func (a *App) quitDevice(ctx context.Context, uid string, flag frame.DeviceFlag) error {
	device, err := a.deviceReader.GetDevice(ctx, uid, int64(flag))
	if errors.Is(err, metadb.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if device.UID == "" {
		return nil
	}
	if err := a.devices.UpsertDevice(ctx, metadb.Device{
		UID:         uid,
		DeviceFlag:  int64(flag),
		Token:       deviceQuitMissingToken,
		DeviceLevel: int64(frame.DeviceLevelMaster),
	}); err != nil {
		return err
	}
	a.kickLocalDevice(uid, flag, deviceQuitCloseDelay, "")
	return nil
}

func (a *App) kickLocalDevice(uid string, flag frame.DeviceFlag, delay time.Duration, reason string) {
	if a == nil || a.online == nil {
		return
	}
	for _, conn := range a.online.ConnectionsByUID(uid) {
		if conn.DeviceFlag != flag || conn.Session == nil {
			continue
		}
		sess := conn.Session
		_ = sess.WriteFrame(&frame.DisconnectPacket{
			ReasonCode: frame.ReasonConnectKick,
			Reason:     reason,
		})
		a.afterFunc(delay, func() { _ = sess.Close() })
	}
}

func deviceQuitFlags(flag int) []frame.DeviceFlag {
	if flag == -1 {
		return []frame.DeviceFlag{frame.APP, frame.WEB, frame.PC}
	}
	return []frame.DeviceFlag{frame.DeviceFlag(flag)}
}
