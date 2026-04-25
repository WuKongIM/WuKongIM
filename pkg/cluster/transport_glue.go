package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type transportLayer struct {
	cfg        Config
	server     *transport.Server
	rpcMux     *transport.RPCMux
	raftPool   *transport.Pool
	rpcPool    *transport.Pool
	raftClient *transport.Client
	fwdClient  *transport.Client
	discovery  *StaticDiscovery
}

func newTransportLayer(cfg Config, discovery *StaticDiscovery, rpcMux *transport.RPCMux) *transportLayer {
	if rpcMux == nil {
		rpcMux = transport.NewRPCMux()
	}
	return &transportLayer{
		cfg:       cfg,
		rpcMux:    rpcMux,
		discovery: discovery,
	}
}

func (t *transportLayer) Start(
	listenAddr string,
	handleRaft func([]byte),
	handleForward func(context.Context, []byte) ([]byte, error),
	handleController func(context.Context, []byte) ([]byte, error),
	handleManagedSlot func(context.Context, []byte) ([]byte, error),
) error {
	if t == nil {
		return ErrNotStarted
	}
	if t.discovery == nil {
		t.discovery = NewStaticDiscovery(t.cfg.Nodes)
	}

	t.server = transport.NewServerWithConfig(transport.ServerConfig{
		ConnConfig: transport.ConnConfig{
			Observer: t.cfg.TransportObserver,
		},
		Logger: defaultLogger(t.cfg.Logger).Named("transport"),
	})
	t.server.Handle(msgTypeRaft, handleRaft)
	t.rpcMux.Handle(rpcServiceForward, handleForward)
	t.rpcMux.Handle(rpcServiceController, handleController)
	t.rpcMux.Handle(rpcServiceManagedSlot, handleManagedSlot)
	t.server.HandleRPCMux(t.rpcMux)
	if err := t.server.Start(listenAddr); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	t.raftPool = transport.NewPool(transport.PoolConfig{
		Discovery:   t.discovery,
		Size:        t.cfg.PoolSize,
		DialTimeout: t.cfg.DialTimeout,
		QueueSizes:  [3]int{2048, 0, 0},
		DefaultPri:  transport.PriorityRaft,
		Observer:    t.cfg.TransportObserver,
	})
	t.rpcPool = transport.NewPool(transport.PoolConfig{
		Discovery:   t.discovery,
		Size:        t.cfg.PoolSize,
		DialTimeout: t.cfg.DialTimeout,
		QueueSizes:  [3]int{0, 1024, 256},
		DefaultPri:  transport.PriorityRPC,
		Observer:    t.cfg.TransportObserver,
	})
	t.raftClient = transport.NewClient(t.raftPool)
	t.fwdClient = transport.NewClient(t.rpcPool)
	return nil
}

func (t *transportLayer) Stop() {
	if t == nil {
		return
	}
	if t.fwdClient != nil {
		t.fwdClient.Stop()
	}
	if t.raftClient != nil {
		t.raftClient.Stop()
	}
	if t.raftPool != nil {
		t.raftPool.Close()
	}
	if t.rpcPool != nil {
		t.rpcPool.Close()
	}
	if t.server != nil {
		t.server.Stop()
	}
}
