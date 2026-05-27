package channels

import (
	"context"

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

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var _ ChannelMetaSource = (*SlotMetaSource)(nil)
var _ ChannelMetaEnsurer = (*SlotMetaSource)(nil)
var _ ChannelMetaEnsurer = (*StaticMetaSource)(nil)
