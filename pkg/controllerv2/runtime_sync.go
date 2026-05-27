package controllerv2

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
	"go.etcd.io/raft/v3/raftpb"
)

func (r *Runtime) newStateSyncServer() *cv2sync.Server {
	return cv2sync.NewServer(cv2sync.ServerConfig{
		NodeID:    r.cfg.NodeID,
		ClusterID: r.cfg.ClusterID,
		LeaderID:  r.LeaderID,
		Ready: func() bool {
			if r.sm == nil {
				return false
			}
			return r.sm.Snapshot(context.Background()).Revision != 0
		},
		Snapshot: func(ctx context.Context) (state.ClusterState, error) {
			if r.sm == nil {
				return state.ClusterState{}, ErrNotStarted
			}
			return r.sm.Snapshot(ctx), nil
		},
	})
}

type noopRaftTransport struct{}

func (noopRaftTransport) Send([]raftpb.Message) {}

type syncClientAdapter struct {
	client *cv2sync.Client
}

func (a syncClientAdapter) SyncOnce(ctx context.Context) (state.ClusterState, error) {
	if a.client == nil {
		return state.ClusterState{}, errors.New("controllerv2: sync client is required")
	}
	if err := a.client.SyncOnce(ctx); err != nil {
		return state.ClusterState{}, err
	}
	st, ok := a.client.LocalState()
	if !ok {
		return state.ClusterState{}, errors.New("controllerv2: sync produced no state")
	}
	return st, nil
}
