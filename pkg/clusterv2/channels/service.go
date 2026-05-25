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
}

// Service wraps ChannelV2 and exposes both client and replication surfaces.
type Service struct {
	runtime channelRuntime
}

// NewService creates a Service from cfg.
func NewService(cfg Config) (*Service, error) {
	runtime := cfg.Runtime
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
	return &Service{runtime: combined}, nil
}

// Runtime returns the ChannelV2 public cluster surface.
func (s *Service) Runtime() ch.Cluster { return s.runtime }

// Server returns the ChannelV2 replication server surface.
func (s *Service) Server() channeltransport.Server { return s.runtime }

// ApplyMeta applies authoritative metadata to the local ChannelV2 runtime.
func (s *Service) ApplyMeta(meta ch.Meta) error { return s.runtime.ApplyMeta(meta) }

// Append appends one message.
func (s *Service) Append(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error) {
	return s.runtime.Append(ctx, req)
}

// AppendBatch appends messages to one channel.
func (s *Service) AppendBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return s.runtime.AppendBatch(ctx, req)
}

// Fetch reads committed messages.
func (s *Service) Fetch(ctx context.Context, req ch.FetchRequest) (ch.FetchResult, error) {
	return s.runtime.Fetch(ctx, req)
}

// Tick advances ChannelV2 background work.
func (s *Service) Tick(ctx context.Context) error { return s.runtime.Tick(ctx) }

// Close closes the ChannelV2 runtime.
func (s *Service) Close() error { return s.runtime.Close() }
