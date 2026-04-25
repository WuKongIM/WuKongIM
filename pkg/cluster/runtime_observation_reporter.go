package cluster

import (
	"context"
	"sort"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

type runtimeObservationReporterConfig struct {
	nodeID           uint64
	now              func() time.Time
	snapshot         func() ([]controllermeta.SlotRuntimeView, error)
	send             func(context.Context, runtimeObservationReport) error
	flushDebounce    time.Duration
	fullSyncInterval time.Duration
}

type runtimeObservationReporter struct {
	cfg runtimeObservationReporterConfig

	mu           sync.Mutex
	mirror       map[uint32]controllermeta.SlotRuntimeView
	dirtyViews   map[uint32]controllermeta.SlotRuntimeView
	closedSlots  map[uint32]struct{}
	needFullSync bool
	dirtySince   time.Time
	lastFullSync time.Time
}

func newRuntimeObservationReporter(cfg runtimeObservationReporterConfig) *runtimeObservationReporter {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &runtimeObservationReporter{
		cfg:         cfg,
		mirror:      make(map[uint32]controllermeta.SlotRuntimeView),
		dirtyViews:  make(map[uint32]controllermeta.SlotRuntimeView),
		closedSlots: make(map[uint32]struct{}),
	}
}

func (r *runtimeObservationReporter) tick(ctx context.Context) error {
	if r == nil || r.cfg.snapshot == nil || r.cfg.send == nil {
		return ErrNotStarted
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := r.cfg.now()
	views, err := r.cfg.snapshot()
	if err != nil {
		return err
	}
	views = cloneRuntimeViews(views)

	r.mu.Lock()
	for _, view := range views {
		delete(r.closedSlots, view.SlotID)
		current, ok := r.mirror[view.SlotID]
		if !ok || !runtimeViewEquivalent(current, view) {
			r.dirtyViews[view.SlotID] = view
			if r.dirtySince.IsZero() {
				r.dirtySince = now
			}
		}
	}

	if r.needFullSync || r.fullSyncDueLocked(now) {
		report := runtimeObservationReport{
			NodeID:     r.cfg.nodeID,
			ObservedAt: now,
			FullSync:   true,
			Views:      cloneRuntimeViews(views),
		}
		r.mu.Unlock()

		if err := r.cfg.send(ctx, report); err != nil {
			return err
		}

		r.mu.Lock()
		r.mirror = runtimeViewsBySlot(views)
		r.dirtyViews = make(map[uint32]controllermeta.SlotRuntimeView)
		r.closedSlots = make(map[uint32]struct{})
		r.needFullSync = false
		r.dirtySince = time.Time{}
		r.lastFullSync = now
		r.mu.Unlock()
		return nil
	}

	if len(r.dirtyViews) == 0 && len(r.closedSlots) == 0 {
		r.mu.Unlock()
		return nil
	}
	if r.cfg.flushDebounce > 0 && !r.dirtySince.IsZero() && now.Sub(r.dirtySince) < r.cfg.flushDebounce {
		r.mu.Unlock()
		return nil
	}

	report := runtimeObservationReport{
		NodeID:      r.cfg.nodeID,
		ObservedAt:  now,
		Views:       sortedRuntimeViews(r.dirtyViews),
		ClosedSlots: sortedClosedSlots(r.closedSlots),
	}
	r.mu.Unlock()

	if err := r.cfg.send(ctx, report); err != nil {
		return err
	}

	r.mu.Lock()
	for slotID, view := range r.dirtyViews {
		r.mirror[slotID] = cloneRuntimeView(view)
	}
	for slotID := range r.closedSlots {
		delete(r.mirror, slotID)
	}
	r.dirtyViews = make(map[uint32]controllermeta.SlotRuntimeView)
	r.closedSlots = make(map[uint32]struct{})
	r.dirtySince = time.Time{}
	r.mu.Unlock()
	return nil
}

func (r *runtimeObservationReporter) markClosed(slotID uint32) {
	if r == nil || slotID == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.closedSlots[slotID] = struct{}{}
	delete(r.dirtyViews, slotID)
	if r.dirtySince.IsZero() {
		r.dirtySince = r.cfg.now()
	}
}

func (r *runtimeObservationReporter) requestFullSync() {
	if r == nil {
		return
	}

	r.mu.Lock()
	r.needFullSync = true
	r.mu.Unlock()
}

func (r *runtimeObservationReporter) fullSyncDueLocked(now time.Time) bool {
	return r.cfg.fullSyncInterval > 0 && !r.lastFullSync.IsZero() && now.Sub(r.lastFullSync) >= r.cfg.fullSyncInterval
}

func runtimeViewsBySlot(views []controllermeta.SlotRuntimeView) map[uint32]controllermeta.SlotRuntimeView {
	out := make(map[uint32]controllermeta.SlotRuntimeView, len(views))
	for _, view := range views {
		out[view.SlotID] = cloneRuntimeView(view)
	}
	return out
}

func sortedRuntimeViews(views map[uint32]controllermeta.SlotRuntimeView) []controllermeta.SlotRuntimeView {
	if len(views) == 0 {
		return nil
	}

	out := make([]controllermeta.SlotRuntimeView, 0, len(views))
	for _, view := range views {
		out = append(out, cloneRuntimeView(view))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SlotID < out[j].SlotID
	})
	return out
}

func sortedClosedSlots(slots map[uint32]struct{}) []uint32 {
	if len(slots) == 0 {
		return nil
	}

	out := make([]uint32, 0, len(slots))
	for slotID := range slots {
		out = append(out, slotID)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out
}

func cloneRuntimeViews(src []controllermeta.SlotRuntimeView) []controllermeta.SlotRuntimeView {
	if len(src) == 0 {
		return nil
	}

	dst := make([]controllermeta.SlotRuntimeView, len(src))
	for i := range src {
		dst[i] = cloneRuntimeView(src[i])
	}
	return dst
}

func cloneRuntimeView(src controllermeta.SlotRuntimeView) controllermeta.SlotRuntimeView {
	src.CurrentPeers = cloneUint64Slice(src.CurrentPeers)
	return src
}
