package backup

import (
	"context"
	"errors"
	"fmt"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// LocalMessageSnapshotNode exposes local committed-message snapshot capture.
type LocalMessageSnapshotNode interface {
	NodeID() uint64
	OpenBackupMessageSnapshot(context.Context, uint16, []clusterpkg.BackupChannelFence) (clusterpkg.BackupMessageSnapshot, error)
}

// LocalMessageShardCapturer uploads message payloads directly from one node.
type LocalMessageShardCapturer struct {
	node       LocalMessageSnapshotNode
	replicator *ChunkReplicator
}

// NewLocalMessageShardCapturer creates a direct local source-node capturer.
func NewLocalMessageShardCapturer(node LocalMessageSnapshotNode, replicator *ChunkReplicator) (*LocalMessageShardCapturer, error) {
	if node == nil || node.NodeID() == 0 || replicator == nil {
		return nil, fmt.Errorf("backup local message capturer: node and replicator are required")
	}
	return &LocalMessageShardCapturer{node: node, replicator: replicator}, nil
}

// CaptureMessageShard pins the resolved committed cuts, streams them through
// compression/encryption, and verifies both repositories.
func (c *LocalMessageShardCapturer) CaptureMessageShard(ctx context.Context, request runtimebackup.CaptureRequest, shard runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error) {
	if c == nil || c.node == nil || shard.NodeID != c.node.NodeID() || shard.ID == "" || len(shard.Channels) == 0 {
		return runtimebackup.MessageShardCapture{}, runtimebackup.ErrInvalidCapture
	}
	fences := make([]clusterpkg.BackupChannelFence, len(shard.Channels))
	for index, channel := range shard.Channels {
		fences[index] = clusterpkg.BackupChannelFence{
			ChannelID: channel.ChannelID, ChannelType: channel.ChannelType, LeaderNodeID: channel.LeaderNodeID,
			ChannelEpoch: channel.ChannelEpoch, LeaderEpoch: channel.LeaderEpoch, MinISR: channel.MinISR,
			RetentionThroughSeq: channel.RetentionThroughSeq, FromExclusive: channel.FromExclusive,
		}
	}
	snapshot, err := c.node.OpenBackupMessageSnapshot(ctx, request.HashSlot, fences)
	if err != nil {
		if errors.Is(err, channelruntime.ErrStaleMeta) || errors.Is(err, channelruntime.ErrNotLeader) {
			return runtimebackup.MessageShardCapture{}, runtimebackup.ErrStaleCapture
		}
		return runtimebackup.MessageShardCapture{}, err
	}
	entries, replicateErr := c.replicator.Replicate(ctx, StreamDescriptor{JobID: request.JobID, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindMessages, ShardID: shard.ID}, snapshot.Reader)
	closeErr := snapshot.Reader.Close()
	if replicateErr != nil {
		return runtimebackup.MessageShardCapture{}, replicateErr
	}
	if closeErr != nil {
		return runtimebackup.MessageShardCapture{}, closeErr
	}
	boundaries := make([]backupartifact.ChannelBoundary, len(snapshot.Boundaries))
	for index, boundary := range snapshot.Boundaries {
		boundaries[index] = backupartifact.ChannelBoundary{ChannelID: boundary.ChannelID, ChannelType: boundary.ChannelType, Epoch: boundary.Epoch, LogStartOffset: boundary.LogStartOffset, HW: boundary.HW}
	}
	return runtimebackup.MessageShardCapture{Objects: entries, Boundaries: boundaries}, nil
}

// RemoteMessageShardClient captures one message shard on another node.
type RemoteMessageShardClient interface {
	CaptureBackupMessageShard(context.Context, uint64, runtimebackup.CaptureRequest, runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error)
}

// MessageShardRouter keeps message bytes on source nodes while routing only
// bounded capture commands and object references through cluster RPC.
type MessageShardRouter struct {
	local  *LocalMessageShardCapturer
	remote RemoteMessageShardClient
}

// NewMessageShardRouter creates local/remote capture routing.
func NewMessageShardRouter(local *LocalMessageShardCapturer, remote RemoteMessageShardClient) (*MessageShardRouter, error) {
	if local == nil || remote == nil {
		return nil, fmt.Errorf("backup message shard router: local and remote capturers are required")
	}
	return &MessageShardRouter{local: local, remote: remote}, nil
}

// CaptureMessageShard dispatches locally or through the bounded node RPC.
func (r *MessageShardRouter) CaptureMessageShard(ctx context.Context, request runtimebackup.CaptureRequest, shard runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error) {
	if r == nil || r.local == nil || r.remote == nil {
		return runtimebackup.MessageShardCapture{}, runtimebackup.ErrInvalidCapture
	}
	if shard.NodeID == r.local.node.NodeID() {
		return r.local.CaptureMessageShard(ctx, request, shard)
	}
	return r.remote.CaptureBackupMessageShard(ctx, shard.NodeID, request, shard)
}

var _ runtimebackup.MessageShardCapturer = (*LocalMessageShardCapturer)(nil)
var _ runtimebackup.MessageShardCapturer = (*MessageShardRouter)(nil)
