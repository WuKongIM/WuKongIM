package presence

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	ErrRouterRequired               = errors.New("usecase/presence: router required")
	ErrSessionRequired              = errors.New("usecase/presence: session required")
	ErrRemoteActionDispatchRequired = errors.New("usecase/presence: remote action dispatcher required")
)

type Options struct {
	LocalNodeID      uint64
	GatewayBootID    uint64
	Online           online.Registry
	Router           Router
	AuthorityClient  Authoritative
	ActionDispatcher ActionDispatcher
	AfterFunc        func(time.Duration, func())
	LeaseTTL         time.Duration
	CloseDelay       time.Duration
	Now              func() time.Time
	Logger           wklog.Logger
}

type App struct {
	dir           *directory
	now           func() time.Time
	online        online.Registry
	router        Router
	authority     Authoritative
	actions       ActionDispatcher
	afterFunc     func(time.Duration, func())
	localNodeID   uint64
	gatewayBootID uint64
	leaseTTL      time.Duration
	closeDelay    time.Duration
	logger        wklog.Logger
}

func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Online == nil {
		opts.Online = online.NewRegistry()
	}
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = defaultLeaseTTL
	}
	if opts.CloseDelay <= 0 {
		opts.CloseDelay = defaultRouteCloseDelay
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}

	app := &App{
		dir:           newDirectory(),
		now:           opts.Now,
		online:        opts.Online,
		router:        opts.Router,
		afterFunc:     opts.AfterFunc,
		localNodeID:   opts.LocalNodeID,
		gatewayBootID: opts.GatewayBootID,
		leaseTTL:      opts.LeaseTTL,
		closeDelay:    opts.CloseDelay,
		logger:        opts.Logger,
	}
	if opts.AuthorityClient != nil {
		app.authority = opts.AuthorityClient
	} else {
		app.authority = app
	}
	if opts.ActionDispatcher != nil {
		app.actions = opts.ActionDispatcher
	} else {
		app.actions = localActionDispatcher{app: app}
	}
	return app
}

func (a *App) activateLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("activate")
}

func (a *App) authorityLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("authority")
}
