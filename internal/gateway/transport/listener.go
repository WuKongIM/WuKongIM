package transport

import "github.com/WuKongIM/WuKongIM/pkg/wklog"

type ListenerOptions struct {
	Name    string
	Network string
	Address string
	Path    string
	OnError func(error)
	Logger  wklog.Logger
}
