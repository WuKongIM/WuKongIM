package message

import (
	"context"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Options configures the message usecase.
type Options struct {
	// Submitter owns channel-authority send routing and append admission.
	Submitter Submitter
	// Reader owns compatible channel message sync reads.
	Reader ChannelMessageReader
	// Memberships authorizes ordinary message pulls and supplies visibility floors.
	Memberships SyncMembershipStore
	// ChannelState rejects terminally disbanded channels during ordinary pulls.
	ChannelState SyncChannelStateStore
	// EventStore owns durable message event projection reads and writes.
	EventStore MessageEventStore
	// PermissionStore provides authoritative membership and channel reads for send authorization.
	PermissionStore PermissionStore
	// PersonDirectory establishes both UID-owned memberships before the first
	// persistent ordinary append to a canonical person channel.
	PersonDirectory PersonDirectoryEnsurer
	// SendHook optionally mutates or rejects permission-accepted sends before append admission.
	SendHook SendHook
	// SystemUIDs identifies internal system senders that bypass business permissions.
	SystemUIDs SystemUIDChecker
	// PersonWhitelistEnabled enables receiver-side personal allowlist checks.
	PersonWhitelistEnabled bool
	// SystemDeviceID identifies trusted system-device sessions after SendBan passes.
	SystemDeviceID string
	// PermissionCacheTTL enables a bounded read-through permission cache. Zero keeps reads uncached.
	PermissionCacheTTL time.Duration
	// Now supplies wall time for permission cache expiry.
	Now func() time.Time
}

// App is a thin message facade over channel append submission and sync reads.
type App struct {
	submitter              Submitter
	reader                 ChannelMessageReader
	memberships            SyncMembershipStore
	channelState           SyncChannelStateStore
	eventStore             MessageEventStore
	permissions            PermissionStore
	personDirectory        PersonDirectoryEnsurer
	sendHook               SendHook
	systemUIDs             SystemUIDChecker
	personWhitelistEnabled bool
	systemDeviceID         string
	now                    func() time.Time
}

// New creates a message App.
func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	permissions := newPermissionCache(opts.PermissionStore, opts.PermissionCacheTTL, opts.Now)
	return &App{
		submitter:              opts.Submitter,
		reader:                 opts.Reader,
		memberships:            opts.Memberships,
		channelState:           opts.ChannelState,
		eventStore:             opts.EventStore,
		permissions:            permissions,
		personDirectory:        opts.PersonDirectory,
		sendHook:               opts.SendHook,
		systemUIDs:             opts.SystemUIDs,
		personWhitelistEnabled: opts.PersonWhitelistEnabled,
		systemDeviceID:         opts.SystemDeviceID,
		now:                    opts.Now,
	}
}

// SyncMembershipStore reads UID-owned ordinary membership state for message pulls.
type SyncMembershipStore interface {
	GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error)
}

// SyncChannelStateStore reads terminal channel business state for message pull.
type SyncChannelStateStore interface {
	GetChannelForMessagePull(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
}

// ResetAfterRestore invalidates optional read-through authorization results so
// the restored membership and channel metadata are authoritative immediately.
func (a *App) ResetAfterRestore() {
	if a == nil {
		return
	}
	if cache, ok := a.permissions.(*permissionCache); ok {
		cache.resetAfterRestore()
	}
}

// PermissionStore provides authoritative membership and channel reads for send authorization.
type PermissionStore interface {
	GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
	ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error)
	HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error)
}

// PersonDirectoryEnsurer establishes the durable discovery invariant for a
// canonical person channel before its first persistent ordinary message.
type PersonDirectoryEnsurer interface {
	EnsurePersonChannelDirectory(ctx context.Context, channelID string, channelType int64) error
}

// SystemUIDChecker identifies internal system senders that bypass business permissions.
type SystemUIDChecker interface {
	IsSystemUID(uid string) bool
}
