package gnet

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

const Name = "gnet"

type Options struct {
	// Multicore enables gnet's CPU-scaled multi event-loop mode when true.
	Multicore bool
	// NumEventLoop sets an explicit gnet event-loop count; zero keeps gnet's default.
	NumEventLoop int
	// ReusePort enables SO_REUSEPORT where the operating system and gnet support it.
	ReusePort bool
	// ReadBufferCap sets gnet's per-event-loop read buffer cap; zero keeps gnet's default.
	ReadBufferCap int
	// WriteBufferCap sets gnet's per-connection static write buffer cap; zero keeps gnet's default.
	WriteBufferCap int
}

type Factory struct {
	options Options
}

func NewFactory(options ...Options) *Factory {
	f := &Factory{}
	if len(options) > 0 {
		f.options = options[0]
	}
	return f
}

func (f *Factory) Name() string {
	return Name
}

func (f *Factory) Options() Options {
	if f == nil {
		return Options{}
	}
	return f.options
}

func (f *Factory) Build(specs []transport.ListenerSpec) ([]transport.Listener, error) {
	group := newEngineGroupWithOptions(specs, f.options)
	listeners := make([]transport.Listener, 0, len(specs))

	for i, spec := range specs {
		switch spec.Options.Network {
		case "tcp", "websocket":
		default:
			return nil, fmt.Errorf("gateway/transport/gnet: unsupported network %q", spec.Options.Network)
		}

		listeners = append(listeners, &listenerHandle{
			opts:    spec.Options,
			runtime: group.runtimes[i],
			group:   group,
		})
	}

	return listeners, nil
}

var _ transport.Factory = (*Factory)(nil)

func (o Options) gnetOptions() []gnetv2.Option {
	opts := make([]gnetv2.Option, 0, 5)
	if o.Multicore {
		opts = append(opts, gnetv2.WithMulticore(true))
	}
	if o.NumEventLoop > 0 {
		opts = append(opts, gnetv2.WithNumEventLoop(o.NumEventLoop))
	}
	if o.ReusePort {
		opts = append(opts, gnetv2.WithReusePort(true))
	}
	if o.ReadBufferCap > 0 {
		opts = append(opts, gnetv2.WithReadBufferCap(o.ReadBufferCap))
	}
	if o.WriteBufferCap > 0 {
		opts = append(opts, gnetv2.WithWriteBufferCap(o.WriteBufferCap))
	}
	return opts
}
