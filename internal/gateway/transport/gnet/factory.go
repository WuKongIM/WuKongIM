package gnet

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

const Name = "gnet"

type Factory struct{}

func NewFactory() *Factory {
	return &Factory{}
}

func (f *Factory) Name() string {
	return Name
}

func (f *Factory) Build(specs []transport.ListenerSpec) ([]transport.Listener, error) {
	group := newEngineGroup(specs)
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
