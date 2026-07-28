package backup

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	fullBackupRuntimeMetaPageSize    = 1024
	fullBackupMaximumChannelsPerSlot = 1 << 20
	fullBackupChannelsPerShard       = 4096
)

// FullPartitionNode exposes the stable metadata views needed to plan one
// online Hash Slot export.
type FullPartitionNode interface {
	CaptureBackupHashSlotSnapshot(
		context.Context,
		uint16,
		uint64,
	) (multiraft.CapturedHashSlotSnapshot, error)
	ListBackupChannelRuntimeMetaPage(
		context.Context,
		uint16,
		metadb.ChannelRuntimeMetaCursor,
		int,
	) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
}

// FullPartition is one stable metadata cut and its bounded message-source plan.
type FullPartition struct {
	Cut             backupartifact.SlotCut
	Metadata        io.ReadCloser
	MetadataRecords uint64
	MessageShards   []backupcontract.MessageShard
}

// Close releases the pinned metadata view.
func (p *FullPartition) Close() error {
	if p == nil || p.Metadata == nil {
		return nil
	}
	err := p.Metadata.Close()
	p.Metadata = nil
	return err
}

// OpenFullPartition pins metadata and accepts the message-source scan only
// when the Slot applied boundary remains unchanged.
func OpenFullPartition(
	ctx context.Context,
	node FullPartitionNode,
	hashSlot uint16,
	configurationVersion uint64,
	expectedLeaderTerm uint64,
) (*FullPartition, error) {
	if node == nil || configurationVersion == 0 || expectedLeaderTerm == 0 ||
		int(hashSlot) >= backupcontract.HashSlotCount {
		return nil, fmt.Errorf("backup full partition: invalid request")
	}
	first, err := node.CaptureBackupHashSlotSnapshot(
		ctx, hashSlot, expectedLeaderTerm,
	)
	if err != nil {
		return nil, err
	}
	if err := validateCapturedHashSlot(first, hashSlot); err != nil {
		if first.Reader != nil {
			_ = first.Reader.Close()
		}
		return nil, err
	}
	metadata, stats, err := metadb.InspectBackupHashSlotSnapshotHeader(
		first.Reader,
	)
	if err != nil {
		return nil, err
	}
	first.Reader = metadata
	keep := false
	defer func() {
		if !keep {
			_ = first.Reader.Close()
		}
	}()

	shards, err := fullMessageShards(ctx, node, hashSlot)
	if err != nil {
		return nil, err
	}
	second, err := node.CaptureBackupHashSlotSnapshot(
		ctx, hashSlot, expectedLeaderTerm,
	)
	if err != nil {
		return nil, err
	}
	if second.Reader != nil {
		_ = second.Reader.Close()
	}
	if err := validateCapturedHashSlot(second, hashSlot); err != nil {
		return nil, err
	}
	if second.SlotID != first.SlotID ||
		second.AppliedTerm != first.AppliedTerm ||
		second.LeaderTerm != first.LeaderTerm ||
		second.AppliedIndex != first.AppliedIndex {
		return nil, fmt.Errorf("backup full partition: metadata changed during capture")
	}
	keep = true
	return &FullPartition{
		Cut: backupartifact.SlotCut{
			PhysicalSlotID:       uint32(first.SlotID),
			LeaderTerm:           first.LeaderTerm,
			AppliedTerm:          first.AppliedTerm,
			ConfigurationVersion: configurationVersion,
			AppliedIndex:         first.AppliedIndex,
			CapturedAtUnixMillis: first.CapturedAtUnixMillis,
		},
		Metadata:        first.Reader,
		MetadataRecords: stats.EntryCount,
		MessageShards:   shards,
	}, nil
}

func validateCapturedHashSlot(
	snapshot multiraft.CapturedHashSlotSnapshot,
	hashSlot uint16,
) error {
	if snapshot.Reader == nil || snapshot.HashSlot != hashSlot ||
		snapshot.SlotID == 0 || uint64(snapshot.SlotID) > math.MaxUint32 ||
		snapshot.AppliedIndex == 0 ||
		snapshot.CommitIndex != snapshot.AppliedIndex ||
		snapshot.AppliedTerm == 0 || snapshot.LeaderTerm == 0 ||
		snapshot.CapturedAtUnixMillis <= 0 {
		return fmt.Errorf("backup full partition: stale Slot capture")
	}
	return nil
}

func fullMessageShards(
	ctx context.Context,
	node FullPartitionNode,
	hashSlot uint16,
) ([]backupcontract.MessageShard, error) {
	byNode := make(map[uint64][]backupcontract.ChannelFence)
	cursor := metadb.ChannelRuntimeMetaCursor{}
	channelCount := 0
	for {
		page, next, done, err := node.ListBackupChannelRuntimeMetaPage(
			ctx, hashSlot, cursor, fullBackupRuntimeMetaPageSize,
		)
		if err != nil {
			return nil, err
		}
		if len(page) > fullBackupMaximumChannelsPerSlot-channelCount {
			return nil, fmt.Errorf("backup full partition: Channel count exceeds limit")
		}
		channelCount += len(page)
		for _, metadata := range page {
			if metadata.ChannelID == "" ||
				metadata.ChannelType < 0 || metadata.ChannelType > math.MaxUint8 ||
				metadata.Leader == 0 || metadata.ChannelEpoch == 0 ||
				metadata.LeaderEpoch == 0 || metadata.MinISR <= 0 {
				return nil, fmt.Errorf("backup full partition: incomplete Channel metadata")
			}
			byNode[metadata.Leader] = append(
				byNode[metadata.Leader],
				backupcontract.ChannelFence{
					ChannelID:           metadata.ChannelID,
					ChannelType:         uint8(metadata.ChannelType),
					LeaderNodeID:        metadata.Leader,
					ChannelEpoch:        metadata.ChannelEpoch,
					LeaderEpoch:         metadata.LeaderEpoch,
					MinISR:              metadata.MinISR,
					RetentionThroughSeq: metadata.RetentionThroughSeq,
				},
			)
		}
		if done {
			break
		}
		if len(page) == 0 || next == cursor {
			return nil, fmt.Errorf("backup full partition: metadata cursor did not advance")
		}
		cursor = next
	}
	nodeIDs := make([]uint64, 0, len(byNode))
	for nodeID := range byNode {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(left, right int) bool {
		return nodeIDs[left] < nodeIDs[right]
	})
	shards := make([]backupcontract.MessageShard, 0)
	for _, nodeID := range nodeIDs {
		channels := byNode[nodeID]
		for start, ordinal := 0, 0; start < len(channels); ordinal++ {
			end := min(start+fullBackupChannelsPerShard, len(channels))
			shards = append(shards, backupcontract.MessageShard{
				ID:     fmt.Sprintf("n%d-%04d", nodeID, ordinal),
				NodeID: nodeID,
				Channels: append(
					[]backupcontract.ChannelFence(nil), channels[start:end]...,
				),
			})
			start = end
		}
	}
	return shards, nil
}
