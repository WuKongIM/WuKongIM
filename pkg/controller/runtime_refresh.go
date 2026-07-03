package controller

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func (r *Runtime) startRefreshLoop() {
	if r.refreshCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.refreshCancel = cancel
	r.refreshWG.Add(1)
	goroutine.SafeGo(r.cfg.Goroutines, "controller", "refresh_loop", func() {
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
	})
}

// controlTick keeps bootstrap progress and local watcher state moving without bypassing Raft semantics.
func (r *Runtime) controlTick(ctx context.Context) error {
	if r.syncClient != nil {
		return r.syncTick(ctx)
	}
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

func (r *Runtime) syncTick(ctx context.Context) error {
	if r.server == nil {
		return nil
	}
	if err := r.server.SyncOnce(ctx); err != nil {
		return err
	}
	st := r.server.LocalState()
	r.mu.RLock()
	currentRevision := r.state.Revision
	currentChecksum := r.state.Checksum
	r.mu.RUnlock()
	if currentRevision == st.Revision && currentChecksum == st.Checksum {
		return nil
	}
	return r.publishState(st)
}

func (r *Runtime) publishIfChanged(ctx context.Context, revision uint64) error {
	r.mu.RLock()
	currentRevision := r.state.Revision
	currentChecksum := r.state.Checksum
	r.mu.RUnlock()
	if currentRevision != revision {
		return r.publishFromState(ctx)
	}
	st := r.sm.Snapshot(ctx)
	if currentChecksum == st.Checksum {
		return nil
	}
	return r.publishState(st)
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
	publishLatestStateEvent(r.watch, clone)
	r.mu.Unlock()
	return nil
}

func publishLatestStateEvent(ch chan StateEvent, st ClusterState) {
	if ch == nil {
		return
	}
	// gofail: var wkControllerStateEventDrop string
	// if gofailDropControllerStateEvent(wkControllerStateEventDrop, st.Revision) { return }
	event := StateEvent{State: st.Clone()}
	select {
	case ch <- event:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- event:
	default:
	}
}

func gofailDropControllerStateEvent(raw string, revision uint64) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if raw == "all" {
		return true
	}
	target, value, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(target) != "revision" {
		return false
	}
	want, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return err == nil && want == revision
}
