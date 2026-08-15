package channels

import (
	"context"
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	channelMetaStageSlotRead      = "meta_slot_read"
	channelMetaStageCreateBuild   = "meta_create_build"
	channelMetaStageCreatePropose = "meta_create_propose"
	channelMetaStageCreateWrite   = "meta_create_write"
	channelMetaStageFinalRead     = "meta_final_read"
)

// SlotMetaSource resolves Channel metadata from Slot authoritative runtime metadata.
type SlotMetaSource struct {
	reader   RuntimeMetaReader
	batcher  *metaCreateBatcher
	batchErr error
	opts     SlotMetaSourceOptions
}

// NewSlotMetaSource creates a Slot-backed ChannelMetaSource.
func NewSlotMetaSource(reader RuntimeMetaReader, opts ...SlotMetaSourceOptions) *SlotMetaSource {
	cfg := SlotMetaSourceOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	cfg.DefaultReplicas = append([]ch.NodeID(nil), cfg.DefaultReplicas...)
	source := &SlotMetaSource{reader: reader, opts: cfg}
	if cfg.Router != nil && cfg.BatchStore != nil {
		source.batcher = newMetaCreateBatcher(cfg.Router, cfg.BatchStore, cfg.BatchObserver, cfg.Goroutines, source.buildRuntimeMetaBatch, source.observeMetaStage)
	} else if cfg.Router != nil || cfg.BatchStore != nil {
		source.batchErr = fmt.Errorf("%w: runtime metadata batch router/store must be configured together", ch.ErrInvalidConfig)
	}
	return source
}

// Close stops new metadata-create admission, cancels queued entries, and joins
// the bounded in-flight Slot batches owned by this source.
func (s *SlotMetaSource) Close() error {
	if s == nil || s.batcher == nil {
		return nil
	}
	return s.batcher.close()
}

// ResolveChannelMeta returns metadata for id from authoritative Slot storage.
func (s *SlotMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	started := time.Now()
	meta, err := s.readRuntimeMeta(ctx, id)
	s.observeMetaStage(channelMetaStageSlotRead, metaStageResult(err), time.Since(started))
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return ch.Meta{}, fmt.Errorf("%w: %v", ch.ErrChannelNotFound, id)
		}
		return ch.Meta{}, err
	}
	return projectRuntimeMeta(meta), nil
}

// EnsureChannelMeta returns metadata for append admission, creating it when absent.
func (s *SlotMetaSource) EnsureChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	started := time.Now()
	meta, err := s.readRuntimeMeta(ctx, id)
	s.observeMetaStage(channelMetaStageSlotRead, metaStageResult(err), time.Since(started))
	if err == nil {
		return projectRuntimeMeta(meta), nil
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return ch.Meta{}, err
	}
	if s.batchErr != nil {
		return ch.Meta{}, s.batchErr
	}
	if s.batcher == nil {
		return ch.Meta{}, fmt.Errorf("%w: missing runtime metadata batch router/store", ch.ErrInvalidConfig)
	}
	started = time.Now()
	outcome := s.batcher.ensure(ctx, id)
	meta, err = outcome.meta, outcome.err
	s.observeMetaStage(channelMetaStageCreateWrite, metaStageResult(err), time.Since(started))
	if err != nil {
		return ch.Meta{}, err
	}
	return projectRuntimeMeta(meta), nil
}

func (s *SlotMetaSource) buildRuntimeMetaBatch(ctx context.Context, plans []runtimeMetaCreatePlanItem) ([]RuntimeMetaCreateItem, error) {
	ids := make([]ch.ChannelID, len(plans))
	routes := make([]routing.Route, len(plans))
	for i, plan := range plans {
		ids[i], routes[i] = plan.id, plan.route
	}
	placements := make([]ChannelPlacement, len(plans))
	if s.opts.Placement != nil {
		var err error
		placements, err = s.opts.Placement.ResolveChannelPlacementBatch(ctx, ids, routes)
		if err != nil {
			return nil, err
		}
		if len(placements) != len(plans) {
			return nil, fmt.Errorf("%w: aligned channel placement batch", ch.ErrInvalidConfig)
		}
	} else {
		for i := range placements {
			placements[i] = ChannelPlacement{
				Leader: firstNodeID(s.opts.DefaultReplicas), Replicas: append([]ch.NodeID(nil), s.opts.DefaultReplicas...), MinISR: s.opts.DefaultMinISR,
			}
		}
	}
	items := make([]RuntimeMetaCreateItem, len(plans))
	for i, plan := range plans {
		meta, err := RuntimeMetaFromPlacement(plan.id, placements[i])
		if err != nil {
			return nil, err
		}
		items[i] = RuntimeMetaCreateItem{HashSlot: plan.route.HashSlot, Meta: meta}
	}
	return items, nil
}

func (s *SlotMetaSource) readRuntimeMeta(ctx context.Context, id ch.ChannelID) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.reader == nil {
		return metadb.ChannelRuntimeMeta{}, fmt.Errorf("%w: slot metadata reader is nil", ch.ErrInvalidConfig)
	}
	meta, err := s.reader.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
		return metadb.ChannelRuntimeMeta{}, fmt.Errorf("%w: resolved %s/%d for %v", ch.ErrStaleMeta, meta.ChannelID, meta.ChannelType, id)
	}
	return meta, nil
}

// RuntimeMetaFromPlacement builds one normalized create-only candidate from an
// authoritative placement decision. It is shared by ordinary append creation
// and person-directory prepare batches so both paths use identical metadata.
func RuntimeMetaFromPlacement(id ch.ChannelID, placement ChannelPlacement) (metadb.ChannelRuntimeMeta, error) {
	replicas := projectUint64NodeIDs(placement.Replicas)
	if len(replicas) == 0 {
		return metadb.ChannelRuntimeMeta{}, fmt.Errorf("%w: empty initial channel replicas", ch.ErrInvalidConfig)
	}
	leader := uint64(placement.Leader)
	if leader == 0 {
		leader = replicas[0]
	}
	minISR := placement.MinISR
	if minISR <= 0 {
		minISR = 1
	}
	if minISR > len(replicas) {
		return metadb.ChannelRuntimeMeta{}, fmt.Errorf("%w: initial min ISR exceeds replicas", ch.ErrInvalidConfig)
	}
	return metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Leader:       leader,
		Replicas:     replicas,
		ISR:          append([]uint64(nil), replicas...),
		MinISR:       int64(minISR),
		Status:       uint8(ch.StatusActive),
	}), nil
}

func (s *SlotMetaSource) observeMetaStage(stage string, result string, d time.Duration) {
	if s == nil || s.opts.Observer == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	s.opts.Observer.ObserveChannelAppendStage(stage, result, d)
}

func metaStageResult(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, metadb.ErrNotFound) {
		return "miss"
	}
	return "err"
}

func firstNodeID(nodes []ch.NodeID) ch.NodeID {
	if len(nodes) == 0 {
		return 0
	}
	return nodes[0]
}
