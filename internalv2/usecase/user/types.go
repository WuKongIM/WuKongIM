package user

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// DefaultSystemUID is the built-in system account UID reserved by the user usecase.
const DefaultSystemUID = "____system"

const (
	updateTokenCloseDelay  = 10 * time.Second
	updateTokenKickReason  = "账号在其他设备上登录"
	deviceQuitCloseDelay   = 2 * time.Second
	deviceQuitMissingToken = ""
	systemUIDChannelID     = "__wk_internal_system_uids__"
	systemUIDChannelType   = int64(frame.SYSTEM)
	systemUIDPageLimit     = 1000
)

var (
	// ErrUserStoreRequired reports that the user metadata store is missing.
	ErrUserStoreRequired = errors.New("internalv2/usecase/user: user store required")
	// ErrDeviceStoreRequired reports that the device metadata store is missing.
	ErrDeviceStoreRequired = errors.New("internalv2/usecase/user: device store required")
)

// UserStore persists UID metadata.
type UserStore interface {
	GetUser(ctx context.Context, uid string) (metadb.User, error)
	CreateUser(ctx context.Context, user metadb.User) error
}

// DeviceStore persists per-device token metadata.
type DeviceStore interface {
	UpsertDevice(ctx context.Context, device metadb.Device) error
}

// DeviceReader loads per-device token metadata.
type DeviceReader interface {
	GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

// PresenceDirectory exposes authoritative online route lookups.
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
	// Users stores durable UID metadata.
	Users UserStore
	// Devices stores durable per-device token metadata.
	Devices DeviceStore
	// DeviceReader loads stored device metadata for device-quit requests.
	DeviceReader DeviceReader
	// Online indexes owner-local gateway sessions.
	Online *online.Registry
	// Presence reads authoritative online routes for status queries.
	Presence PresenceDirectory
	// SystemUIDs stores reserved system account UIDs.
	SystemUIDs SystemUIDStore
	// SystemUID overrides the built-in system account UID in tests.
	SystemUID string
	// AfterFunc schedules delayed local session closes.
	AfterFunc func(time.Duration, func())
	// Logger records user-usecase failures that are otherwise best-effort.
	Logger wklog.Logger
}

// App coordinates legacy-compatible user operations.
type App struct {
	users        UserStore
	devices      DeviceStore
	deviceReader DeviceReader
	online       *online.Registry
	presence     PresenceDirectory
	systemUIDs   SystemUIDStore
	systemUID    string
	afterFunc    func(time.Duration, func())
	logger       wklog.Logger

	systemUIDCacheMu sync.RWMutex
	systemUIDCache   map[string]struct{}
}

// UpdateTokenCommand updates one UID/device token.
type UpdateTokenCommand struct {
	// UID identifies the user.
	UID string
	// Token is the new device token.
	Token string
	// DeviceFlag is the WuKong protocol device category.
	DeviceFlag frame.DeviceFlag
	// DeviceLevel is the WuKong protocol device conflict level.
	DeviceLevel frame.DeviceLevel
}

// DeviceQuitCommand requests that a user's device token be cleared.
type DeviceQuitCommand struct {
	// UID identifies the user.
	UID string
	// DeviceFlag selects one device flag, or -1 for APP/WEB/PC.
	DeviceFlag int
}

// OnlineStatus describes one device-level online entry returned by the legacy API.
type OnlineStatus struct {
	UID        string `json:"uid"`
	DeviceFlag uint8  `json:"device_flag"`
	Online     int    `json:"online"`
}

// New creates a user usecase.
func New(opts Options) *App {
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	}
	if opts.DeviceReader == nil {
		if reader, ok := opts.Devices.(DeviceReader); ok {
			opts.DeviceReader = reader
		}
	}
	if opts.SystemUID == "" {
		opts.SystemUID = DefaultSystemUID
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &App{
		users:          opts.Users,
		devices:        opts.Devices,
		deviceReader:   opts.DeviceReader,
		online:         opts.Online,
		presence:       opts.Presence,
		systemUIDs:     opts.SystemUIDs,
		systemUID:      opts.SystemUID,
		afterFunc:      opts.AfterFunc,
		logger:         opts.Logger,
		systemUIDCache: make(map[string]struct{}),
	}
}

func (c UpdateTokenCommand) validate() error {
	switch {
	case c.UID == "":
		return errors.New("uid不能为空！")
	case c.Token == "":
		return errors.New("token不能为空！")
	case strings.Contains(c.UID, "@"), strings.Contains(c.UID, "#"), strings.Contains(c.UID, "&"):
		return errors.New("uid不能包含特殊字符！")
	default:
		return nil
	}
}
