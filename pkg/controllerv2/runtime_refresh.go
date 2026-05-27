package controllerv2

import (
	"context"
	"errors"
	"time"

	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
)

func (r *Runtime) startRefreshLoop() {
	if r.refreshCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.refreshCancel = cancel
	r.refreshWG.Add(1)
	go func() {
		defer r.refreshWG.Done()
		interval := r.cfg.TickInterval * 5
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.controlTick(ctx)
			}
		}
	}()
}

// controlTick keeps bootstrap progress and local watcher state moving without bypassing Raft semantics.
func (r *Runtime) controlTick(ctx context.Context) error {
	if r.sm == nil || r.server == nil {
		return nil
	}
	st := r.sm.Snapshot(ctx)
	if st.Revision == 0 {
		if r.cfg.AllowBootstrap && r.isLocalLeader() {
			if err := r.raft.Propose(ctx, r.initCommand()); err != nil && !errors.Is(err, cv2raft.ErrNotLeader) {
				return err
			}
		}
		return nil
	}
	if len(st.Slots) < int(r.cfg.InitialSlotCount) {
		if r.isLocalLeader() {
			if err := r.server.TickPlanner(ctx); err != nil && !errors.Is(err, cv2raft.ErrNotLeader) {
				return err
			}
		}
		return nil
	}
	return r.publishIfChanged(ctx, st.Revision)
}

func (r *Runtime) publishIfChanged(ctx context.Context, revision uint64) error {
	r.mu.RLock()
	currentRevision := r.state.Revision
	r.mu.RUnlock()
	if currentRevision == revision {
		return nil
	}
	return r.publishFromState(ctx)
}

func (r *Runtime) publishFromState(ctx context.Context) error {
	st := r.sm.Snapshot(ctx)
	return r.publishState(st)
}

func (r *Runtime) publishState(st ClusterState) error {
	if err := st.Validate(); err != nil {
		return err
	}
	clone := st.Clone()
	r.mu.Lock()
	r.state = clone.Clone()
	r.mu.Unlock()
	select {
	case r.watch <- StateEvent{State: clone.Clone()}:
	default:
	}
	return nil
}
