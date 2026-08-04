package channels

import (
	"context"
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
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
	reader RuntimeMetaReader
	// creator owns initial create-only proposals; ordinary runtime-meta upsert
	// must remain outside this append-admission path.
	creator RuntimeMetaCreator
	opts    SlotMetaSourceOptions
}

// NewSlotMetaSource creates a Slot-backed ChannelMetaSource.
func NewSlotMetaSource(reader RuntimeMetaReader, opts ...SlotMetaSourceOptions) *SlotMetaSource {
	cfg := SlotMetaSourceOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	creator := cfg.Creator
	if creator == nil {
		if c, ok := reader.(RuntimeMetaCreator); ok {
			creator = c
		}
	}
	cfg.DefaultReplicas = append([]ch.NodeID(nil), cfg.DefaultReplicas...)
	return &SlotMetaSource{reader: reader, creator: creator, opts: cfg}
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
	if s.creator == nil {
		return ch.Meta{}, fmt.Errorf("%w: missing slot metadata creator", ch.ErrChannelNotFound)
	}
	started = time.Now()
	buildStarted := time.Now()
	candidate, err := s.initialRuntimeMeta(ctx, id)
	s.observeMetaStage(channelMetaStageCreateBuild, metaStageResult(err), time.Since(buildStarted))
	if err == nil {
		proposeStarted := time.Now()
		_, err = s.creator.CreateChannelRuntimeMeta(ctx, candidate)
		s.observeMetaStage(channelMetaStageCreatePropose, metaStageResult(err), time.Since(proposeStarted))
	}
	s.observeMetaStage(channelMetaStageCreateWrite, metaStageResult(err), time.Since(started))
	if err != nil {
		return ch.Meta{}, err
	}
	started = time.Now()
	meta, err = s.readRuntimeMeta(ctx, id)
	s.observeMetaStage(channelMetaStageFinalRead, metaStageResult(err), time.Since(started))
	if err != nil {
		return ch.Meta{}, err
	}
	return projectRuntimeMeta(meta), nil
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

func (s *SlotMetaSource) initialRuntimeMeta(ctx context.Context, id ch.ChannelID) (metadb.ChannelRuntimeMeta, error) {
	placement, err := s.initialPlacement(ctx, id)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
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

func (s *SlotMetaSource) initialPlacement(ctx context.Context, id ch.ChannelID) (ChannelPlacement, error) {
	if s.opts.Placement != nil {
		placement, err := s.opts.Placement.ResolveChannelPlacement(ctx, id)
		if err != nil {
			return ChannelPlacement{}, err
		}
		placement.Replicas = append([]ch.NodeID(nil), placement.Replicas...)
		return placement, nil
	}
	return ChannelPlacement{
		Leader:   firstNodeID(s.opts.DefaultReplicas),
		Replicas: append([]ch.NodeID(nil), s.opts.DefaultReplicas...),
		MinISR:   s.opts.DefaultMinISR,
	}, nil
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
