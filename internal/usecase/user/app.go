package user

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type App struct {
	users     UserStore
	devices   DeviceStore
	online    online.Registry
	afterFunc func(time.Duration, func())
	logger    wklog.Logger
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
	return &App{
		users:     opts.Users,
		devices:   opts.Devices,
		online:    opts.Online,
		afterFunc: opts.AfterFunc,
		logger:    opts.Logger,
	}
}
