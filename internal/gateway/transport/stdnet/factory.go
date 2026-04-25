package stdnet

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

const Name = "stdnet"

type Factory struct{}

func NewFactory() *Factory {
	return &Factory{}
}

func (f *Factory) Name() string {
	return Name
}

func (f *Factory) Build(specs []transport.ListenerSpec) ([]transport.Listener, error) {
	listeners := make([]transport.Listener, 0, len(specs))
	for _, spec := range specs {
		var (
			listener transport.Listener
			err      error
		)
		switch spec.Options.Network {
		case "tcp":
			listener, err = NewTCPListener(spec.Options, spec.Handler)
		case "websocket":
			listener, err = NewWSListener(spec.Options, spec.Handler)
		default:
			err = fmt.Errorf("gateway/transport/stdnet: unsupported network %q", spec.Options.Network)
		}
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}
