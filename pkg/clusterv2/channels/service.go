package channels

import (
	"context"
	"fmt"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelservice "github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

type channelRuntime interface {
	ch.Cluster
	channeltransport.Server
}

// ForwardClient forwards client append calls to the authoritative channel leader.
type ForwardClient interface {
	// ForwardAppend forwards one append request to node.
	ForwardAppend(context.Context, ch.NodeID, ch.AppendRequest) (ch.AppendResult, error)
	// ForwardAppendBatch forwards one append batch request to node.
	ForwardAppendBatch(context.Context, ch.NodeID, ch.AppendBatchRequest) (ch.AppendBatchResult, error)
}

// Config wires a ChannelV2 service wrapper.
type Config struct {
	// Runtime optionally supplies an already constructed ChannelV2 runtime.
	Runtime any
	// LocalNode is this node's ChannelV2 node ID when constructing Runtime.
	LocalNode ch.NodeID
	// ReactorCount is the number of ChannelV2 reactor partitions.
	ReactorCount int
	// MailboxSize bounds each ChannelV2 reactor mailbox.
	MailboxSize int
	// Store opens ChannelV2 stores when constructing Runtime.
	Store channelstore.Factory
	// Transport sends ChannelV2 replication RPCs when constructing Runtime.
	Transport channeltransport.Client
	// MetaSource resolves authoritative channel metadata.
	MetaSource ChannelMetaSource
	// Forward sends client append calls to the resolved channel leader.
	Forward ForwardClient
}

// Service wraps ChannelV2 and exposes both client and replication surfaces.
type Service struct {
	runtime    channelRuntime
	localNode  ch.NodeID
	metaSource ChannelMetaSource
	ensurer    ChannelMetaEnsurer
	forward    ForwardClient
}

// NewService creates a Service from cfg.
func NewService(cfg Config) (*Service, error) {
	runtime := cfg.Runtime
	if cfg.Forward == nil {
		if forward, ok := cfg.Transport.(ForwardClient); ok {
			cfg.Forward = forward
		}
	}
	if runtime == nil {
		cluster, err := channelservice.New(channelservice.Config{LocalNode: cfg.LocalNode, ReactorCount: cfg.ReactorCount, MailboxSize: cfg.MailboxSize, Store: cfg.Store, Transport: cfg.Transport, MetaResolver: cfg.MetaSource})
		if err != nil {
			return nil, err
		}
		runtime = cluster
	}
	combined, ok := runtime.(channelRuntime)
	if !ok {
		return nil, fmt.Errorf("channels: runtime must implement channelv2.Cluster and channelv2/transport.Server")
	}
	ensurer, _ := cfg.MetaSource.(ChannelMetaEnsurer)
	return &Service{runtime: combined, localNode: cfg.LocalNode, metaSource: cfg.MetaSource, ensurer: ensurer, forward: cfg.Forward}, nil
}

// Runtime returns the ChannelV2 public cluster surface.
func (s *Service) Runtime() ch.Cluster { return s.runtime }

// Server returns the ChannelV2 replication server surface.
func (s *Service) Server() channeltransport.Server { return s.runtime }

// ApplyMeta applies authoritative metadata to the local ChannelV2 runtime.
func (s *Service) ApplyMeta(meta ch.Meta) error { return s.runtime.ApplyMeta(meta) }

// Append appends one message.
func (s *Service) Append(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error) {
	meta, ok, err := s.resolveAppendMeta(ctx, req.ChannelID)
	if err != nil {
		return ch.AppendResult{}, err
	}
	if ok {
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if s.forward == nil {
				return ch.AppendResult{}, ch.ErrNotLeader
			}
			return s.forward.ForwardAppend(ctx, meta.Leader, req)
		}
		if err := s.runtime.ApplyMeta(meta); err != nil {
			return ch.AppendResult{}, err
		}
	}
	return s.runtime.Append(ctx, req)
}

// AppendBatch appends messages to one channel.
func (s *Service) AppendBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	meta, ok, err := s.resolveAppendMeta(ctx, req.ChannelID)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	if ok {
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if s.forward == nil {
				return ch.AppendBatchResult{}, ch.ErrNotLeader
			}
			return s.forward.ForwardAppendBatch(ctx, meta.Leader, req)
		}
		if err := s.runtime.ApplyMeta(meta); err != nil {
			return ch.AppendBatchResult{}, err
		}
	}
	return s.runtime.AppendBatch(ctx, req)
}

// Tick advances ChannelV2 background work.
func (s *Service) Tick(ctx context.Context) error { return s.runtime.Tick(ctx) }

// Close closes the ChannelV2 runtime.
func (s *Service) Close() error { return s.runtime.Close() }

func (s *Service) resolveAppendMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, bool, error) {
	if s == nil {
		return ch.Meta{}, false, nil
	}
	if s.ensurer != nil {
		meta, err := s.ensurer.EnsureChannelMeta(ctx, id)
		if err != nil {
			return ch.Meta{}, true, err
		}
		return normalizeAppendMeta(id, meta)
	}
	if s.metaSource == nil {
		return ch.Meta{}, false, nil
	}
	meta, err := s.metaSource.ResolveChannelMeta(ctx, id)
	if err != nil {
		return ch.Meta{}, true, err
	}
	return normalizeAppendMeta(id, meta)
}

func normalizeAppendMeta(id ch.ChannelID, meta ch.Meta) (ch.Meta, bool, error) {
	if meta.ID == (ch.ChannelID{}) {
		meta.ID = id
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	if meta.ID != id || meta.Key != ch.ChannelKeyForID(id) {
		return ch.Meta{}, true, ch.ErrStaleMeta
	}
	return meta, true, nil
}
