package presence

import "context"

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

func (a *App) observeOnlineStatus(ctx context.Context, uid string, online bool) {
	if a.onlineStatus == nil || a.local == nil || uid == "" {
		return
	}
	if !a.hasActiveLocalSession(uid) {
		return
	}
	value := uid + "-0"
	if online {
		value = uid + "-1"
	}
	_ = a.onlineStatus.ObserveOnlineStatus(ctx, OnlineStatusEvent{
		UID:    uid,
		Online: online,
		Value:  value,
	})
}

func (a *App) observeOfflineIfLastLocalSession(ctx context.Context, uid string) {
	if a.onlineStatus == nil || a.local == nil || uid == "" {
		return
	}
	if a.hasActiveLocalSession(uid) {
		return
	}
	_ = a.onlineStatus.ObserveOnlineStatus(ctx, OnlineStatusEvent{
		UID:    uid,
		Online: false,
		Value:  uid + "-0",
	})
}

func (a *App) hasActiveLocalSession(uid string) bool {
	for _, session := range a.local.LocalSessionsByUID(uid) {
		if session.State == RouteStateActive {
			return true
		}
	}
	return false
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
