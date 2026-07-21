package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sort"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxMessageShardCount = 1 << 16

// ChannelFence identifies one Channel replica selected by an authoritative
// hash-slot metadata view. It deliberately carries no messages.
type ChannelFence struct {
	// ChannelID is the durable business channel identity.
	ChannelID string
	// ChannelType is the durable business channel type.
	ChannelType uint8
	// LeaderNodeID is the Channel leader recorded in the metadata view.
	LeaderNodeID uint64
	// ChannelEpoch fences membership changes.
	ChannelEpoch uint64
	// LeaderEpoch fences leader changes.
	LeaderEpoch uint64
	// MinISR records the committed quorum policy represented by the metadata view.
	MinISR int64
	// RetentionThroughSeq is the semantic permanent-retention boundary.
	RetentionThroughSeq uint64
	// FromExclusive is the previously published committed high watermark.
	FromExclusive uint64
}

// MessageShardCapture contains uploaded message objects and the exact resolved cuts.
type MessageShardCapture struct {
	// Objects are verified immutable encrypted message chunks.
	Objects []backupartifact.ObjectEntry
	// Boundaries are the exact committed cuts encoded in Objects.
	Boundaries []backupartifact.ChannelBoundary
}

// MessageShard is one bounded group captured by the same source node.
type MessageShard struct {
	// ID is unique within one logical partition and safe for repository keys.
	ID string
	// NodeID is the source replica node.
	NodeID uint64
	// Channels contains exact metadata fences for this shard.
	Channels []ChannelFence
}

// PartitionPlan owns one pinned metadata stream and the message shard plan
// derived from the same stable applied boundary.
type PartitionPlan interface {
	// Cut returns the hash-slot boundary represented by Metadata.
	Cut() backupartifact.PartitionCut
	// OpenMetadata returns the pinned portable metadata stream once.
	OpenMetadata(context.Context) (io.ReadCloser, error)
	// MessageShards returns bounded source-node groups in stable order.
	MessageShards() []MessageShard
	// Base returns the prior partition layer for an incremental chain.
	Base() *backupartifact.PartitionReference
	// Close releases the pinned metadata view.
	Close() error
}

// PartitionPlanner creates a stable hash-slot metadata/message plan.
type PartitionPlanner interface {
	OpenPlan(context.Context, CaptureRequest) (PartitionPlan, error)
}

// MessageShardCapturer uploads one message shard directly from its selected
// source node and returns verified immutable object references.
type MessageShardCapturer interface {
	CaptureMessageShard(context.Context, CaptureRequest, MessageShard) (MessageShardCapture, error)
}

// DistributedWorkerOptions configures cross-node logical partition capture.
type DistributedWorkerOptions struct {
	Planner    PartitionPlanner
	Messages   MessageShardCapturer
	Replicator StreamReplicator
	Manifests  PartitionManifestStore
}

// DistributedWorker captures metadata locally and delegates message shards to
// their selected replica nodes without proxying message payloads through the
// Controller coordinator.
type DistributedWorker struct {
	planner    PartitionPlanner
	messages   MessageShardCapturer
	replicator StreamReplicator
	manifests  PartitionManifestStore
}

// NewDistributedWorker creates a partition worker with bounded cross-node fanout.
func NewDistributedWorker(options DistributedWorkerOptions) (*DistributedWorker, error) {
	if options.Planner == nil || options.Messages == nil || options.Replicator == nil || options.Manifests == nil {
		return nil, fmt.Errorf("%w: distributed worker dependencies are incomplete", ErrInvalidCapture)
	}
	return &DistributedWorker{planner: options.Planner, messages: options.Messages, replicator: options.Replicator, manifests: options.Manifests}, nil
}

// Capture captures one logical hash slot and publishes its immutable manifest.
func (w *DistributedWorker) Capture(ctx context.Context, request CaptureRequest) (backupcontract.PartitionReport, error) {
	if stringsTrimmedEmpty(request.JobID) || request.BackupEpoch == 0 || !validFingerprint(request.ConfigFingerprint) {
		return backupcontract.PartitionReport{}, ErrInvalidCapture
	}
	plan, err := w.planner.OpenPlan(ctx, request)
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	if plan == nil {
		return backupcontract.PartitionReport{}, fmt.Errorf("%w: planner returned no plan", ErrInvalidCapture)
	}
	defer plan.Close()
	cut := plan.Cut()
	if cut.HashSlot != request.HashSlot || cut.RaftIndex == 0 || cut.CommittedAtMillis <= 0 {
		return backupcontract.PartitionReport{}, ErrStaleCapture
	}
	metadataReader, err := plan.OpenMetadata(ctx)
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	metadata, replicateErr := w.replicator.Replicate(ctx, StreamDescriptor{JobID: request.JobID, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindMetadata}, metadataReader)
	closeErr := metadataReader.Close()
	if replicateErr != nil {
		return backupcontract.PartitionReport{}, replicateErr
	}
	if closeErr != nil {
		return backupcontract.PartitionReport{}, closeErr
	}
	objects := append([]backupartifact.ObjectEntry(nil), metadata...)
	boundaries := make([]backupartifact.ChannelBoundary, 0)
	shards := plan.MessageShards()
	if len(shards) > maxMessageShardCount {
		return backupcontract.PartitionReport{}, fmt.Errorf("%w: message shard count exceeds limit", ErrInvalidCapture)
	}
	seenShardIDs := make(map[string]struct{}, len(shards))
	for _, shard := range shards {
		if shard.ID == "" || shard.NodeID == 0 || len(shard.Channels) == 0 {
			return backupcontract.PartitionReport{}, fmt.Errorf("%w: message shard identity is incomplete", ErrInvalidCapture)
		}
		if _, exists := seenShardIDs[shard.ID]; exists {
			return backupcontract.PartitionReport{}, fmt.Errorf("%w: duplicate message shard id", ErrInvalidCapture)
		}
		seenShardIDs[shard.ID] = struct{}{}
		captured, err := w.messages.CaptureMessageShard(ctx, request, shard)
		if err != nil {
			return backupcontract.PartitionReport{}, err
		}
		for _, entry := range captured.Objects {
			if entry.HashSlot != request.HashSlot || entry.Kind != backupartifact.ObjectKindMessages {
				return backupcontract.PartitionReport{}, fmt.Errorf("%w: message shard returned mismatched object", ErrStaleCapture)
			}
		}
		if len(captured.Boundaries) != len(shard.Channels) {
			return backupcontract.PartitionReport{}, fmt.Errorf("%w: message shard boundary count mismatch", ErrStaleCapture)
		}
		objects = append(objects, captured.Objects...)
		boundaries = append(boundaries, captured.Boundaries...)
	}
	indexBody, err := backupartifact.MarshalChannelIndex(request.HashSlot, boundaries)
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	indexObjects, err := w.replicator.Replicate(ctx, StreamDescriptor{JobID: request.JobID, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindChannelIndex}, bytes.NewReader(indexBody))
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	objects = append(objects, indexObjects...)
	return w.publishPartition(ctx, request, cut, plan.Base(), objects)
}

func (w *DistributedWorker) publishPartition(ctx context.Context, request CaptureRequest, cut backupartifact.PartitionCut, base *backupartifact.PartitionReference, objects []backupartifact.ObjectEntry) (backupcontract.PartitionReport, error) {
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	manifest := backupartifact.PartitionManifest{Format: backupartifact.PartitionManifestFormat, Version: backupartifact.PartitionManifestVersion, JobID: request.JobID, BackupEpoch: request.BackupEpoch, Cut: cut, Base: base, Objects: objects}
	body, err := backupartifact.MarshalPartitionManifest(manifest)
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	key := fmt.Sprintf("partition-manifests/%s/%05d.json", request.JobID, request.HashSlot)
	if err := w.manifests.Put(ctx, key, checksum, body); err != nil {
		return backupcontract.PartitionReport{}, err
	}
	var ciphertextBytes uint64
	for _, object := range objects {
		if object.CiphertextBytes <= 0 || uint64(object.CiphertextBytes) > math.MaxUint64-ciphertextBytes {
			return backupcontract.PartitionReport{}, fmt.Errorf("%w: partition ciphertext size overflow", ErrInvalidCapture)
		}
		ciphertextBytes += uint64(object.CiphertextBytes)
	}
	return backupcontract.PartitionReport{JobID: request.JobID, BackupEpoch: request.BackupEpoch, HashSlot: request.HashSlot, RaftIndex: cut.RaftIndex, CommittedAtUnixMillis: cut.CommittedAtMillis, ManifestKey: key, ManifestSHA256: checksum, ObjectCount: uint64(len(objects)), CiphertextBytes: ciphertextBytes}, nil
}

func stringsTrimmedEmpty(value string) bool {
	for _, char := range value {
		if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
			return false
		}
	}
	return true
}
