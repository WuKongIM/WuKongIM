package app

import (
	"context"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/messageupdates"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func (a *App) wireMessageUpdateHints(opts *message.Options) {
	_, ok := a.cluster.(messageupdates.Source)
	if !ok || a.presence == nil || a.online == nil {
		return
	}
	subscribers, ok := a.cluster.(message.UpdateSubscribers)
	if !ok {
		return
	}
	peers, ok := a.cluster.(deliveryinfra.HintPeerCaller)
	if !ok {
		return
	}
	registrar, ok := a.cluster.(nodeRPCRegistrar)
	if !ok {
		return
	}
	hints := &deliveryinfra.MessageUpdateHints{Online: a.online, Presence: a.presence, Peers: peers, NodeID: a.cfg.NodeID}
	opts.UpdateHints = hints
	opts.UpdateSubscribers = subscribers
	registrar.RegisterRPC(clusternet.RPCMessageUpdateHint, accessnode.MessageUpdateRPC{Writer: hints})
	a.messageUpdateHintsReady = true
}
func (a *App) wireMessageUpdateWorker() {
	if a.messages == nil || !a.messageUpdateHintsReady {
		return
	}
	source, ok := a.cluster.(messageupdates.Source)
	if !ok {
		return
	}
	a.messageUpdateWorker = messageupdates.New(source, a.messages, a.goroutines, func(count int) {
		if a.logger != nil {
			a.logger.Warn("message update repair will retry", wklog.Event("internal.app.message_update_repair_retry"), wklog.Int("failures", count))
		}
	})
}

// messageContentEpoch reuses the Controller's successful-restore generation;
// it survives ordinary restarts and is not rolled back with message metadata.
func (a *App) messageContentEpoch(ctx context.Context) (uint64, error) {
	if a.scheduledBackup == nil {
		return 0, nil
	}
	state, err := a.scheduledBackup.State(ctx)
	return state.ManagerSessionEpoch, err
}

// messageContentReadFence samples one atomic state; it never reads Controller or DB.
func (a *App) messageContentReadFence() (uint64, bool) {
	fence := a.restoreReadFence.Load()
	return fence, fence&1 != 0
}
