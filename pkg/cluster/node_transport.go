package cluster

import (
	"context"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

type transportServerResource struct {
	server *clusternet.TransportServer
	addr   string
}

func (r transportServerResource) Start(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return r.server.Start(r.addr)
}

// Quiesce keeps runtime dependencies alive until admitted handlers return.
func (r transportServerResource) Quiesce(ctx context.Context) error {
	return r.server.StopContext(ctx)
}

func (r transportServerResource) Stop(ctx context.Context) error { return r.Quiesce(ctx) }

type transportClientResource struct {
	client *clusternet.TransportClient
}

func (r transportClientResource) Start(ctx context.Context) error {
	return ctxErr(ctx)
}

// Quiesce runs after incoming handlers join, preserving their nested RPC path.
func (r transportClientResource) Quiesce(ctx context.Context) error {
	return r.client.StopContext(ctx)
}

func (r transportClientResource) Stop(ctx context.Context) error { return r.Quiesce(ctx) }
