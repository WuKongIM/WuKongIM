package user

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// DefaultSystemUID is the built-in system account UID reserved by the user usecase.
const DefaultSystemUID = "____system"

type App struct {
	users        UserStore
	devices      DeviceStore
	deviceReader DeviceReader
	online       online.Registry
	presence     PresenceDirectory
	systemUIDs   SystemUIDStore
	systemUID    string
	afterFunc    func(time.Duration, func())
	logger       wklog.Logger

	systemUIDCacheMu sync.RWMutex
	systemUIDCache   map[string]struct{}
}

func New(opts Options) *App {
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	}
	if opts.Online == nil {
		opts.Online = online.NewRegistry()
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	if opts.DeviceReader == nil {
		if reader, ok := opts.Devices.(DeviceReader); ok {
			opts.DeviceReader = reader
		}
	}
	if opts.SystemUID == "" {
		opts.SystemUID = DefaultSystemUID
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
