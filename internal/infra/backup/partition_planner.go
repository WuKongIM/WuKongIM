package backup

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	backupRuntimeMetaPageSize     = 1024
	maxBackupChannelsPerHashSlot  = 1 << 20
	backupChannelsPerMessageShard = 4096
)

// PartitionPlanNode exposes exact Slot snapshot and runtime-meta reads without
// leaking cluster implementation details into the backup runtime.
type PartitionPlanNode interface {
	CaptureBackupHashSlotSnapshot(context.Context, uint16) (multiraft.CapturedHashSlotSnapshot, error)
	ListBackupChannelRuntimeMetaPage(context.Context, uint16, metadb.ChannelRuntimeMetaCursor, int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
}

// PartitionBaseResolver loads the prior signed partition reference and its
// per-channel committed boundaries from the repository, never Controller.
type PartitionBaseResolver interface {
	ResolvePartitionBase(context.Context, string, uint16) (*backupartifact.PartitionReference, []backupartifact.ChannelBoundary, error)
}

// PartitionPlannerOptions configures stable logical hash-slot planning.
type PartitionPlannerOptions struct {
	Node PartitionPlanNode
	Base PartitionBaseResolver
}

// PartitionPlanner derives Channel source groups only when a full metadata scan
// is fenced by an unchanged Slot applied index.
type PartitionPlanner struct {
	node PartitionPlanNode
	base PartitionBaseResolver
}

// NewPartitionPlanner creates a stable logical-partition planner.
func NewPartitionPlanner(options PartitionPlannerOptions) (*PartitionPlanner, error) {
	if options.Node == nil {
		return nil, fmt.Errorf("backup partition planner: node is required")
	}
	return &PartitionPlanner{node: options.Node, base: options.Base}, nil
}

// OpenPlan pins metadata, scans Channel ownership, and rejects the plan if any
// Slot command applied while ownership was being collected.
func (p *PartitionPlanner) OpenPlan(ctx context.Context, request runtimebackup.CaptureRequest) (runtimebackup.PartitionPlan, error) {
	var baseReference *backupartifact.PartitionReference
	var baseBoundaries []backupartifact.ChannelBoundary
	if request.Kind == backupartifact.RestorePointIncremental {
		if p.base == nil || request.BaseRestorePointID == "" {
			return nil, fmt.Errorf("backup partition planner: incremental base is required")
		}
		var err error
		baseReference, baseBoundaries, err = p.base.ResolvePartitionBase(ctx, request.BaseRestorePointID, request.HashSlot)
		if err != nil {
			return nil, err
		}
	}
	first, err := p.node.CaptureBackupHashSlotSnapshot(ctx, request.HashSlot)
	if err != nil {
		return nil, err
	}
	if first.Reader == nil || first.AppliedIndex == 0 || first.CommitIndex != first.AppliedIndex || first.CapturedAtUnixMillis <= 0 || first.HashSlot != request.HashSlot {
		if first.Reader != nil {
			_ = first.Reader.Close()
		}
		return nil, runtimebackup.ErrStaleCapture
	}
	metadataReader, metadataStats, err := metadb.InspectBackupHashSlotSnapshotHeader(first.Reader)
	if err != nil {
		return nil, err
	}
	first.Reader = metadataReader
	closeFirst := true
	defer func() {
		if closeFirst {
			_ = first.Reader.Close()
		}
	}()
	shards, err := p.listMessageShards(ctx, request.HashSlot, baseBoundaries)
	if err != nil {
		return nil, err
	}
	second, err := p.node.CaptureBackupHashSlotSnapshot(ctx, request.HashSlot)
	if err != nil {
		return nil, err
	}
	if second.Reader != nil {
		_ = second.Reader.Close()
	}
	if second.CommitIndex != second.AppliedIndex || second.AppliedIndex != first.AppliedIndex || second.Term != first.Term || second.SlotID != first.SlotID {
		return nil, runtimebackup.ErrStaleCapture
	}
	closeFirst = false
	return &partitionPlan{
		cut:             backupartifact.PartitionCut{HashSlot: request.HashSlot, RaftIndex: first.AppliedIndex, CommittedAtMillis: first.CapturedAtUnixMillis},
		metadata:        first.Reader,
		metadataRecords: metadataStats.EntryCount,
		shards:          shards,
		base:            clonePartitionReference(baseReference),
	}, nil
}

func (p *PartitionPlanner) listMessageShards(ctx context.Context, hashSlot uint16, base []backupartifact.ChannelBoundary) ([]runtimebackup.MessageShard, error) {
	baseByChannel := make(map[channelBoundaryIdentity]backupartifact.ChannelBoundary, len(base))
	for _, boundary := range base {
		identity := channelBoundaryIdentity{id: boundary.ChannelID, channelType: boundary.ChannelType}
		if boundary.ChannelID == "" || boundary.Epoch == 0 {
			return nil, fmt.Errorf("backup partition planner: invalid base Channel boundary")
		}
		if _, exists := baseByChannel[identity]; exists {
			return nil, fmt.Errorf("backup partition planner: duplicate base Channel boundary")
		}
		baseByChannel[identity] = boundary
	}
	byNode := make(map[uint64][]runtimebackup.ChannelFence)
	cursor := metadb.ChannelRuntimeMetaCursor{}
	channelCount := 0
	for {
		page, next, done, err := p.node.ListBackupChannelRuntimeMetaPage(ctx, hashSlot, cursor, backupRuntimeMetaPageSize)
		if err != nil {
			return nil, err
		}
		if channelCount > maxBackupChannelsPerHashSlot-len(page) {
			return nil, fmt.Errorf("backup partition planner: channel count exceeds limit")
		}
		channelCount += len(page)
		for _, meta := range page {
			if meta.ChannelID == "" || meta.ChannelType < 0 || meta.ChannelType > 255 || meta.Leader == 0 || meta.ChannelEpoch == 0 || meta.LeaderEpoch == 0 || meta.MinISR <= 0 {
				return nil, fmt.Errorf("backup partition planner: incomplete Channel runtime metadata")
			}
			fromExclusive := uint64(0)
			if boundary, ok := baseByChannel[channelBoundaryIdentity{id: meta.ChannelID, channelType: uint8(meta.ChannelType)}]; ok {
				fromExclusive = boundary.HW
			}
			byNode[meta.Leader] = append(byNode[meta.Leader], runtimebackup.ChannelFence{
				ChannelID: meta.ChannelID, ChannelType: uint8(meta.ChannelType), LeaderNodeID: meta.Leader,
				ChannelEpoch: meta.ChannelEpoch, LeaderEpoch: meta.LeaderEpoch, MinISR: meta.MinISR,
				RetentionThroughSeq: meta.RetentionThroughSeq,
				FromExclusive:       fromExclusive,
			})
		}
		if done {
			break
		}
		if len(page) == 0 || next == cursor {
			return nil, fmt.Errorf("backup partition planner: runtime metadata cursor did not advance")
		}
		cursor = next
	}
	nodeIDs := make([]uint64, 0, len(byNode))
	for nodeID := range byNode {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	shards := make([]runtimebackup.MessageShard, 0)
	for _, nodeID := range nodeIDs {
		channels := byNode[nodeID]
		for start, ordinal := 0, 0; start < len(channels); start, ordinal = start+backupChannelsPerMessageShard, ordinal+1 {
			end := start + backupChannelsPerMessageShard
			if end > len(channels) {
				end = len(channels)
			}
			shards = append(shards, runtimebackup.MessageShard{
				ID: fmt.Sprintf("n%d-%04d", nodeID, ordinal), NodeID: nodeID,
				Channels: append([]runtimebackup.ChannelFence(nil), channels[start:end]...),
			})
		}
	}
	return shards, nil
}

type partitionPlan struct {
	cut             backupartifact.PartitionCut
	metadata        io.ReadCloser
	metadataRecords uint64
	shards          []runtimebackup.MessageShard
	base            *backupartifact.PartitionReference
	mu              sync.Mutex
	opened          bool
}

func (p *partitionPlan) Cut() backupartifact.PartitionCut { return p.cut }

func (p *partitionPlan) OpenMetadata(context.Context) (io.ReadCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.opened || p.metadata == nil {
		return nil, fmt.Errorf("backup partition planner: metadata stream already opened")
	}
	p.opened = true
	return p.metadata, nil
}

func (p *partitionPlan) MessageShards() []runtimebackup.MessageShard {
	out := make([]runtimebackup.MessageShard, len(p.shards))
	for index, shard := range p.shards {
		out[index] = shard
		out[index].Channels = append([]runtimebackup.ChannelFence(nil), shard.Channels...)
	}
	return out
}

func (p *partitionPlan) MetadataRecordCount() uint64 { return p.metadataRecords }

func (p *partitionPlan) Base() *backupartifact.PartitionReference {
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePartitionReference(p.base)
}

func (p *partitionPlan) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.metadata == nil {
		return nil
	}
	err := p.metadata.Close()
	p.metadata = nil
	return err
}

var _ runtimebackup.PartitionPlanner = (*PartitionPlanner)(nil)

type channelBoundaryIdentity struct {
	id          string
	channelType uint8
}

func clonePartitionReference(reference *backupartifact.PartitionReference) *backupartifact.PartitionReference {
	if reference == nil {
		return nil
	}
	copy := *reference
	return &copy
}
