package app

import (
	"context"
	"errors"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

func registerLegacySlotProxyRPCHandlers(store *metastore.Store, mux *transport.RPCMux) {
	if store == nil || mux == nil {
		return
	}
	store.RegisterRPCHandlers(func(serviceID uint8, handler func(context.Context, []byte) ([]byte, error)) {
		mux.Handle(serviceID, transport.RPCHandler(func(ctx context.Context, payload []byte) ([]byte, error) {
			body, err := handler(ctx, payload)
			return body, mapSlotProxyErrorToLegacy(err)
		}))
	})
}

func mapSlotProxyErrorToLegacy(err error) error {
	switch {
	case errors.Is(err, metastore.ErrNoLeader):
		return raftcluster.ErrNoLeader
	case errors.Is(err, metastore.ErrNotLeader):
		return raftcluster.ErrNotLeader
	case errors.Is(err, metastore.ErrSlotNotFound):
		return raftcluster.ErrSlotNotFound
	default:
		return err
	}
}
