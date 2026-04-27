package gateway

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/gateway/core"
	protojsonrpc "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/jsonrpc"
	protowkproto "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/wkproto"
	protowsmux "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/wsmux"
	gnettransport "github.com/WuKongIM/WuKongIM/internal/gateway/transport/gnet"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport/stdnet"
)

type Gateway struct {
	server *core.Server
}

func New(opts Options) (*Gateway, error) {
	registry, err := buildRegistry()
	if err != nil {
		return nil, err
	}

	server, err := core.NewServer(registry, &opts)
	if err != nil {
		return nil, err
	}

	return &Gateway{server: server}, nil
}

func (g *Gateway) Start() error {
	if g == nil || g.server == nil {
		return ErrGatewayClosed
	}
	return g.server.Start()
}

func (g *Gateway) Stop() error {
	if g == nil || g.server == nil {
		return nil
	}
	return g.server.Stop()
}

func (g *Gateway) ListenerAddr(name string) string {
	if g == nil || g.server == nil {
		return ""
	}
	addr := g.server.ListenerAddr(name)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	return addr
}

func (g *Gateway) SetAcceptingNewSessions(accepting bool) {
	if g == nil || g.server == nil {
		return
	}
	g.server.SetAcceptingNewSessions(accepting)
}

func (g *Gateway) AcceptingNewSessions() bool {
	if g == nil || g.server == nil {
		return false
	}
	return g.server.AcceptingNewSessions()
}

func (g *Gateway) SessionSummary() core.SessionSummary {
	if g == nil || g.server == nil {
		return core.SessionSummary{SessionsByListener: map[string]int{}}
	}
	return g.server.SessionSummary()
}

func buildRegistry() (*core.Registry, error) {
	registry := core.NewRegistry()
	if err := registry.RegisterTransport(stdnet.NewFactory()); err != nil {
		return nil, err
	}
	if err := registry.RegisterTransport(gnettransport.NewFactory()); err != nil {
		return nil, err
	}
	if err := registry.RegisterProtocol(protowkproto.New()); err != nil {
		return nil, err
	}
	if err := registry.RegisterProtocol(protojsonrpc.New()); err != nil {
		return nil, err
	}
	if err := registry.RegisterProtocol(protowsmux.New()); err != nil {
		return nil, err
	}
	return registry, nil
}
