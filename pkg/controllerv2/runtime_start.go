package controllerv2

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/server"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
)

func (r *Runtime) startVoter(ctx context.Context) error {
	sm, err := fsm.New(r.store)
	if err != nil {
		return err
	}
	transport := r.cfg.RaftTransport
	if transport == nil {
		transport = noopRaftTransport{}
	}
	service, err := cv2raft.NewService(cv2raft.Config{
		NodeID:         r.cfg.NodeID,
		Peers:          r.raftPeers(),
		AllowBootstrap: r.cfg.AllowBootstrap,
		RaftDir:        filepath.Join(r.cfg.StateDir, "raft"),
		StateMachine:   sm,
		Transport:      transport,
		TickInterval:   r.cfg.TickInterval,
	})
	if err != nil {
		return err
	}
	r.sm, r.raft = sm, service
	if err := service.Start(ctx); err != nil {
		r.sm, r.raft = nil, nil
		return err
	}
	srv, err := server.New(server.Config{StateSource: sm, Proposer: service, Now: r.cfg.Now})
	if err != nil {
		_ = service.Stop()
		return err
	}
	r.server = srv
	r.syncServer = r.newStateSyncServer()
	if len(r.cfg.Voters) > 1 {
		if st := sm.Snapshot(ctx); st.Revision != 0 && len(st.Slots) >= int(r.cfg.InitialSlotCount) {
			if err := r.publishFromState(ctx); err != nil {
				_ = service.Stop()
				return err
			}
		}
		r.startRefreshLoop()
		return nil
	}
	if err := r.bootstrapIfNeeded(ctx); err != nil {
		_ = service.Stop()
		return err
	}
	if err := r.publishFromState(ctx); err != nil {
		_ = service.Stop()
		return err
	}
	r.startRefreshLoop()
	return nil
}

func (r *Runtime) startMirror(ctx context.Context) error {
	client := r.cfg.SyncClient
	if client == nil {
		if r.cfg.SyncPeers == nil {
			return errors.New("controllerv2: mirror sync peers required")
		}
		client = cv2sync.NewClient(cv2sync.ClientConfig{
			ClusterID: r.cfg.ClusterID,
			Store:     r.store,
			Peers:     r.cfg.SyncPeers,
		})
	}
	srv, err := server.New(server.Config{SyncClient: syncClientAdapter{client: client}})
	if err != nil {
		return err
	}
	r.server = srv
	if err := srv.SyncOnce(ctx); err != nil {
		return err
	}
	return r.publishState(srv.LocalState())
}
