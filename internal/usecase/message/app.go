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
	// EventStore owns durable message event projection reads and writes.
	EventStore MessageEventStore
	// PermissionStore provides authoritative membership and channel reads for send authorization.
	PermissionStore PermissionStore
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
	eventStore             MessageEventStore
	permissions            PermissionStore
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
		eventStore:             opts.EventStore,
		permissions:            permissions,
		sendHook:               opts.SendHook,
		systemUIDs:             opts.SystemUIDs,
		personWhitelistEnabled: opts.PersonWhitelistEnabled,
		systemDeviceID:         opts.SystemDeviceID,
		now:                    opts.Now,
	}
}

// PermissionStore provides authoritative membership and channel reads for send authorization.
type PermissionStore interface {
	GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
	ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error)
	HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error)
}

// SystemUIDChecker identifies internal system senders that bypass business permissions.
type SystemUIDChecker interface {
	IsSystemUID(uid string) bool
}
