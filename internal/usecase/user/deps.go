package user

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	ErrUserStoreRequired   = errors.New("usecase/user: user store required")
	ErrDeviceStoreRequired = errors.New("usecase/user: device store required")
)

type UserStore interface {
	GetUser(ctx context.Context, uid string) (metadb.User, error)
	CreateUser(ctx context.Context, u metadb.User) error
}

type DeviceStore interface {
	UpsertDevice(ctx context.Context, d metadb.Device) error
}

// DeviceReader loads the stored device record used by device-quit handling.
type DeviceReader interface {
	GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

// PresenceDirectory exposes authoritative presence lookups for online status.
type PresenceDirectory interface {
	EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error)
}

// SystemUIDStore persists the reserved system account UID list.
type SystemUIDStore interface {
	AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}

// Options configures the user usecase dependencies.
type Options struct {
	Users        UserStore
	Devices      DeviceStore
	DeviceReader DeviceReader
	Online       online.Registry
	Presence     PresenceDirectory
	SystemUIDs   SystemUIDStore
	SystemUID    string
	AfterFunc    func(time.Duration, func())
	Logger       wklog.Logger
}
