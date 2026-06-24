package presence

import (
	"context"
	"fmt"
)

// HashSlotResolver resolves the current hash slot for a UID.
type HashSlotResolver func(uid string) (uint16, error)

// OwnerSeqGenerator allocates the owner-local fencing sequence for a route.
type OwnerSeqGenerator func(uid string) uint64

// Options configures the presence usecase.
type Options struct {
	// Local stores owner-local route projections and local session records.
	Local LocalRegistry
	// Authority registers virtual routes with the UID authority.
	Authority AuthorityClient
	// OwnerActions routes conflict actions to the owner node of the real session.
	OwnerActions OwnerActionClient
	// OnlineStatusObserver observes best-effort owner-local UID online status changes.
	OnlineStatusObserver OnlineStatusObserver
	// OwnerNodeID identifies this gateway owner node in authority route identities.
	OwnerNodeID uint64
	// OwnerBootID identifies this owner process generation.
	OwnerBootID uint64
	// HashSlot resolves UID hash slots for owner-local route metadata.
	HashSlot HashSlotResolver
	// OwnerSeq allocates monotonic owner sequences; nil falls back to SessionID.
	OwnerSeq OwnerSeqGenerator
}

// App orchestrates entry-agnostic presence activation and lookup.
type App struct {
	local        LocalRegistry
	authority    AuthorityClient
	ownerAction  OwnerActionClient
	onlineStatus OnlineStatusObserver
	ownerNodeID  uint64
	ownerBootID  uint64
	hashSlot     HashSlotResolver
	ownerSeq     OwnerSeqGenerator
}

// New creates a presence App.
func New(opts Options) *App {
	return &App{
		local:        opts.Local,
		authority:    opts.Authority,
		ownerAction:  opts.OwnerActions,
		onlineStatus: opts.OnlineStatusObserver,
		ownerNodeID:  opts.OwnerNodeID,
		ownerBootID:  opts.OwnerBootID,
		hashSlot:     opts.HashSlot,
		ownerSeq:     opts.OwnerSeq,
	}
}

func (a *App) observeOnlineStatus(ctx context.Context, route OwnerRoute) {
	if a.onlineStatus == nil || a.local == nil || route.UID == "" {
		return
	}
	deviceOnlineCount, totalOnlineCount := a.activeLocalSessionCounts(route.UID, route.DeviceFlag)
	if totalOnlineCount == 0 {
		return
	}
	_ = a.onlineStatus.ObserveOnlineStatus(ctx, OnlineStatusEvent{
		UID:    route.UID,
		Online: true,
		Value:  onlineStatusValue(route.UID, route.DeviceFlag, true, route.SessionID, deviceOnlineCount, totalOnlineCount),
	})
}

func (a *App) observeOfflineIfLastLocalSession(ctx context.Context, route OwnerRoute, removedActive bool) {
	if !removedActive || a.onlineStatus == nil || a.local == nil || route.UID == "" {
		return
	}
	deviceOnlineCount, totalOnlineCount := a.activeLocalSessionCounts(route.UID, route.DeviceFlag)
	if totalOnlineCount > 0 {
		return
	}
	_ = a.onlineStatus.ObserveOnlineStatus(ctx, OnlineStatusEvent{
		UID:    route.UID,
		Online: false,
		Value:  onlineStatusValue(route.UID, route.DeviceFlag, false, route.SessionID, deviceOnlineCount, totalOnlineCount),
	})
}

func (a *App) activeLocalSessionCounts(uid string, deviceFlag uint8) (int, int) {
	deviceOnlineCount := 0
	totalOnlineCount := 0
	for _, session := range a.local.LocalSessionsByUID(uid) {
		if session.State == RouteStateActive {
			totalOnlineCount++
			if session.Route.DeviceFlag == deviceFlag {
				deviceOnlineCount++
			}
		}
	}
	return deviceOnlineCount, totalOnlineCount
}

func onlineStatusValue(uid string, deviceFlag uint8, online bool, sessionID uint64, deviceOnlineCount, totalOnlineCount int) string {
	onlineValue := 0
	if online {
		onlineValue = 1
	}
	return fmt.Sprintf("%s-%d-%d-%d-%d-%d", uid, deviceFlag, onlineValue, sessionID, deviceOnlineCount, totalOnlineCount)
}

func (a *App) ownerRoute(cmd ActivateCommand) (OwnerRoute, error) {
	hashSlot := uint16(0)
	if a.hashSlot != nil {
		var err error
		hashSlot, err = a.hashSlot(cmd.UID)
		if err != nil {
			return OwnerRoute{}, err
		}
	}
	ownerSeq := cmd.SessionID
	if a.ownerSeq != nil {
		ownerSeq = a.ownerSeq(cmd.UID)
	}
	return OwnerRoute{
		UID:              cmd.UID,
		HashSlot:         hashSlot,
		OwnerNodeID:      a.ownerNodeID,
		OwnerBootID:      a.ownerBootID,
		OwnerSeq:         ownerSeq,
		SessionID:        cmd.SessionID,
		DeviceID:         cmd.DeviceID,
		DeviceFlag:       cmd.DeviceFlag,
		DeviceLevel:      cmd.DeviceLevel,
		Listener:         cmd.Listener,
		ConnectedUnix:    cmd.ConnectedUnix,
		LastActivityUnix: cmd.ConnectedUnix,
	}, nil
}

func routeFromOwnerRoute(conn OwnerRoute) Route {
	return Route{
		UID:           conn.UID,
		OwnerNodeID:   conn.OwnerNodeID,
		OwnerBootID:   conn.OwnerBootID,
		OwnerSeq:      conn.OwnerSeq,
		SessionID:     conn.SessionID,
		DeviceID:      conn.DeviceID,
		DeviceFlag:    conn.DeviceFlag,
		DeviceLevel:   conn.DeviceLevel,
		Listener:      conn.Listener,
		ConnectedUnix: conn.ConnectedUnix,
		LastSeenUnix:  conn.LastActivityUnix,
	}
}
