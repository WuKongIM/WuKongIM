package channels

import (
	"context"
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelMetaSource resolves authoritative ChannelV2 metadata.
type ChannelMetaSource interface {
	// ResolveChannelMeta returns metadata for id.
	ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error)
}

// ChannelMetaEnsurer resolves metadata and may create it for append admission.
type ChannelMetaEnsurer interface {
	// EnsureChannelMeta returns metadata for id, creating the initial record when needed.
	EnsureChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error)
}

// RuntimeMetaReader reads authoritative ChannelRuntimeMeta from unified metadata storage.
type RuntimeMetaReader interface {
	// GetChannelRuntimeMeta reads one authoritative runtime metadata record.
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// RuntimeMetaWriter persists authoritative ChannelRuntimeMeta through Slot ownership.
type RuntimeMetaWriter interface {
	// UpsertChannelRuntimeMeta persists one runtime metadata record.
	UpsertChannelRuntimeMeta(context.Context, metadb.ChannelRuntimeMeta) error
}

// ChannelPlacement describes the initial ChannelV2 route chosen by Slot metadata.
type ChannelPlacement struct {
	// Leader is the initial ChannelV2 leader.
	Leader ch.NodeID
	// Replicas are the initial ChannelV2 replicas.
	Replicas []ch.NodeID
	// MinISR is the initial write quorum size.
	MinISR int
}

// ChannelPlacementResolver resolves first-append placement from Slot routing.
type ChannelPlacementResolver interface {
	// ResolveChannelPlacement returns the initial placement for id.
	ResolveChannelPlacement(context.Context, ch.ChannelID) (ChannelPlacement, error)
}

// PlacementRouter routes channel IDs to their authoritative Slot placement.
type PlacementRouter interface {
	// RouteKey returns the current route for key.
	RouteKey(string) (routing.Route, error)
}

// SlotPlacementResolver derives initial channel placement from Slot routes.
type SlotPlacementResolver struct {
	router PlacementRouter
	minISR int
}

// NewSlotPlacementResolver creates a placement resolver backed by router.
func NewSlotPlacementResolver(router PlacementRouter, minISR int) *SlotPlacementResolver {
	return &SlotPlacementResolver{router: router, minISR: minISR}
}

// ResolveChannelPlacement returns the Slot leader and peers for id.
func (r *SlotPlacementResolver) ResolveChannelPlacement(ctx context.Context, id ch.ChannelID) (ChannelPlacement, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelPlacement{}, err
	}
	if r == nil || r.router == nil {
		return ChannelPlacement{}, fmt.Errorf("%w: placement router is nil", ch.ErrInvalidConfig)
	}
	route, err := r.router.RouteKey(id.ID)
	if err != nil {
		return ChannelPlacement{}, err
	}
	replicas := make([]ch.NodeID, 0, len(route.Peers))
	for _, peer := range route.Peers {
		replicas = append(replicas, ch.NodeID(peer))
	}
	return ChannelPlacement{Leader: ch.NodeID(route.Leader), Replicas: replicas, MinISR: r.minISR}, nil
}

// SlotMetaSourceOptions configures first-append metadata creation.
type SlotMetaSourceOptions struct {
	// DefaultReplicas are the initial ChannelV2 replicas when metadata is missing.
	DefaultReplicas []ch.NodeID
	// DefaultMinISR is the initial write quorum; defaults to 1 when replicas exist.
	DefaultMinISR int
	// Placement resolves the initial ChannelV2 route from Slot routing.
	Placement ChannelPlacementResolver
	// Writer persists missing metadata; when nil, reader is used if it implements RuntimeMetaWriter.
	Writer RuntimeMetaWriter
}

// SlotMetaSource resolves ChannelV2 metadata from Slot authoritative runtime metadata.
type SlotMetaSource struct {
	reader RuntimeMetaReader
	writer RuntimeMetaWriter
	opts   SlotMetaSourceOptions
}

// NewSlotMetaSource creates a Slot-backed ChannelMetaSource.
func NewSlotMetaSource(reader RuntimeMetaReader, opts ...SlotMetaSourceOptions) *SlotMetaSource {
	cfg := SlotMetaSourceOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	writer := cfg.Writer
	if writer == nil {
		if w, ok := reader.(RuntimeMetaWriter); ok {
			writer = w
		}
	}
	cfg.DefaultReplicas = append([]ch.NodeID(nil), cfg.DefaultReplicas...)
	return &SlotMetaSource{reader: reader, writer: writer, opts: cfg}
}

// ResolveChannelMeta returns metadata for id from authoritative Slot storage.
func (s *SlotMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	if s == nil || s.reader == nil {
		return ch.Meta{}, fmt.Errorf("%w: slot metadata reader is nil", ch.ErrInvalidConfig)
	}
	meta, err := s.reader.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return ch.Meta{}, fmt.Errorf("%w: %v", ch.ErrChannelNotFound, id)
		}
		return ch.Meta{}, err
	}
	if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
		return ch.Meta{}, fmt.Errorf("%w: resolved %s/%d for %v", ch.ErrStaleMeta, meta.ChannelID, meta.ChannelType, id)
	}
	return projectRuntimeMeta(meta), nil
}

// EnsureChannelMeta returns metadata for append admission, creating it when absent.
func (s *SlotMetaSource) EnsureChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	if s == nil || s.reader == nil {
		return ch.Meta{}, fmt.Errorf("%w: slot metadata reader is nil", ch.ErrInvalidConfig)
	}
	meta, err := s.reader.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err == nil {
		if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
			return ch.Meta{}, fmt.Errorf("%w: resolved %s/%d for %v", ch.ErrStaleMeta, meta.ChannelID, meta.ChannelType, id)
		}
		return projectRuntimeMeta(meta), nil
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return ch.Meta{}, err
	}
	if s.writer == nil {
		return ch.Meta{}, fmt.Errorf("%w: missing slot metadata writer", ch.ErrChannelNotFound)
	}
	candidate, err := s.initialRuntimeMeta(ctx, id)
	if err != nil {
		return ch.Meta{}, err
	}
	if err := s.writer.UpsertChannelRuntimeMeta(ctx, candidate); err != nil {
		return ch.Meta{}, err
	}
	meta, err = s.reader.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return ch.Meta{}, err
	}
	if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
		return ch.Meta{}, fmt.Errorf("%w: resolved %s/%d for %v", ch.ErrStaleMeta, meta.ChannelID, meta.ChannelType, id)
	}
	return projectRuntimeMeta(meta), nil
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

func firstNodeID(nodes []ch.NodeID) ch.NodeID {
	if len(nodes) == 0 {
		return 0
	}
	return nodes[0]
}

// StaticMetaSource is an immutable in-memory metadata source for tests and smoke runs.
type StaticMetaSource struct {
	items map[ch.ChannelID]ch.Meta
}

// NewStaticMetaSource creates a StaticMetaSource from metas.
func NewStaticMetaSource(metas []ch.Meta) *StaticMetaSource {
	items := make(map[ch.ChannelID]ch.Meta, len(metas))
	for _, meta := range metas {
		if meta.ID == (ch.ChannelID{}) {
			continue
		}
		if meta.Key == "" {
			meta.Key = ch.ChannelKeyForID(meta.ID)
		}
		items[meta.ID] = cloneMeta(meta)
	}
	return &StaticMetaSource{items: items}
}

// ResolveChannelMeta returns metadata for id.
func (s *StaticMetaSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	meta, ok := s.items[id]
	if !ok {
		return ch.Meta{}, fmt.Errorf("%w: %v", ch.ErrChannelNotFound, id)
	}
	return cloneMeta(meta), nil
}

// EnsureChannelMeta returns existing static metadata for append admission.
func (s *StaticMetaSource) EnsureChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	return s.ResolveChannelMeta(ctx, id)
}

func cloneMeta(meta ch.Meta) ch.Meta {
	meta.Replicas = append([]ch.NodeID(nil), meta.Replicas...)
	meta.ISR = append([]ch.NodeID(nil), meta.ISR...)
	return meta
}

func projectRuntimeMeta(meta metadb.ChannelRuntimeMeta) ch.Meta {
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	id := ch.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	var leaseUntil time.Time
	if meta.LeaseUntilMS > 0 {
		leaseUntil = time.UnixMilli(meta.LeaseUntilMS).UTC()
	}
	return ch.Meta{
		Key:         ch.ChannelKeyForID(id),
		ID:          id,
		Epoch:       meta.ChannelEpoch,
		LeaderEpoch: meta.LeaderEpoch,
		Leader:      ch.NodeID(meta.Leader),
		Replicas:    projectNodeIDs(meta.Replicas),
		ISR:         projectNodeIDs(meta.ISR),
		MinISR:      int(meta.MinISR),
		LeaseUntil:  leaseUntil,
		Status:      ch.Status(meta.Status),
	}
}

func projectNodeIDs(ids []uint64) []ch.NodeID {
	out := make([]ch.NodeID, 0, len(ids))
	for _, id := range ids {
		out = append(out, ch.NodeID(id))
	}
	return out
}

func projectUint64NodeIDs(ids []ch.NodeID) []uint64 {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		out = append(out, uint64(id))
	}
	return out
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var _ ChannelMetaSource = (*SlotMetaSource)(nil)
var _ ChannelMetaEnsurer = (*SlotMetaSource)(nil)
var _ ChannelMetaEnsurer = (*StaticMetaSource)(nil)
