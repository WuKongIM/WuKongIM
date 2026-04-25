package delivery

import "github.com/WuKongIM/WuKongIM/pkg/wklog"

type Options struct {
	Runtime Runtime
	Logger  wklog.Logger
}

type App struct {
	runtime Runtime
	logger  wklog.Logger
}

func New(opts Options) *App {
	if opts.Runtime == nil {
		opts.Runtime = noopRuntime{}
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &App{runtime: opts.Runtime, logger: opts.Logger}
}

type noopRuntime struct{}
