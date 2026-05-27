package clusterv2

import (
	"context"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
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

func (r transportServerResource) Stop(context.Context) error {
	if r.server != nil {
		r.server.Stop()
	}
	return nil
}

type transportClientResource struct {
	client *clusternet.TransportClient
}

func (r transportClientResource) Start(ctx context.Context) error {
	return ctxErr(ctx)
}

func (r transportClientResource) Stop(context.Context) error {
	if r.client != nil {
		r.client.Stop()
	}
	return nil
}
