package presence

// HashSlotResolver resolves the current hash slot for a UID.
type HashSlotResolver func(uid string) uint16

// OwnerSeqGenerator allocates the owner-local fencing sequence for a route.
type OwnerSeqGenerator func(uid string) uint64

// Options configures the presence usecase.
type Options struct {
	// Local stores owner-local real gateway session routes.
	Local LocalRegistry
	// Authority registers virtual routes with the UID authority.
	Authority AuthorityClient
	// OwnerNodeID identifies this gateway owner node in authority route identities.
	OwnerNodeID uint64
	// OwnerBootID identifies this owner process generation.
	OwnerBootID uint64
	// HashSlot resolves UID hash slots for local rehydrate indexing.
	HashSlot HashSlotResolver
	// OwnerSeq allocates monotonic owner sequences; nil falls back to SessionID.
	OwnerSeq OwnerSeqGenerator
}

// App orchestrates entry-agnostic presence activation and lookup.
type App struct {
	local       LocalRegistry
	authority   AuthorityClient
	ownerNodeID uint64
	ownerBootID uint64
	hashSlot    HashSlotResolver
	ownerSeq    OwnerSeqGenerator
}

// New creates a presence App.
func New(opts Options) *App {
	return &App{
		local:       opts.Local,
		authority:   opts.Authority,
		ownerNodeID: opts.OwnerNodeID,
		ownerBootID: opts.OwnerBootID,
		hashSlot:    opts.HashSlot,
		ownerSeq:    opts.OwnerSeq,
	}
}

func (a *App) onlineConn(cmd ActivateCommand) OnlineConn {
	hashSlot := uint16(0)
	if a.hashSlot != nil {
		hashSlot = a.hashSlot(cmd.UID)
	}
	ownerSeq := cmd.SessionID
	if a.ownerSeq != nil {
		ownerSeq = a.ownerSeq(cmd.UID)
	}
	return OnlineConn{
		UID:           cmd.UID,
		HashSlot:      hashSlot,
		OwnerNodeID:   a.ownerNodeID,
		OwnerBootID:   a.ownerBootID,
		OwnerSeq:      ownerSeq,
		SessionID:     cmd.SessionID,
		DeviceID:      cmd.DeviceID,
		DeviceFlag:    cmd.DeviceFlag,
		DeviceLevel:   cmd.DeviceLevel,
		Listener:      cmd.Listener,
		ConnectedUnix: cmd.ConnectedUnix,
		State:         RouteStatePending,
		Session:       cmd.Session,
	}
}

func routeFromConn(conn OnlineConn) Route {
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
	}
}
