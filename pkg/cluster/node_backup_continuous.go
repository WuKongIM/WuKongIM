package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	// ErrBackupSourceCompacted reports a continuous cursor older than retained Slot logs.
	ErrBackupSourceCompacted = errors.New("cluster: backup source log compacted")
)

// BackupCaptureAuthority is one fresh local Slot Raft leadership proof.
type BackupCaptureAuthority struct {
	// HashSlot and SlotID identify the physical partition and logical Raft Group.
	HashSlot uint16
	SlotID   uint32
	// HolderNodeID and LeaderTerm identify the fresh local Raft leader.
	HolderNodeID uint64
	LeaderTerm   uint64
	// ConfigEpoch fences control-plane Slot placement changes.
	ConfigEpoch uint64
}

// BackupMetadataHighWatermark is one routed logical Slot metadata boundary.
type BackupMetadataHighWatermark struct {
	// HashSlot and SlotID identify the logical and physical source partitions.
	HashSlot uint16
	SlotID   uint32
	// RaftIndex is the last applied command that mutates HashSlot. Unrelated
	// commands in the shared physical Slot do not advance this logical cut.
	RaftIndex uint64
	// ObservedAtUnixMillis is the UTC observation time after reading runtime status.
	ObservedAtUnixMillis int64
}

// BackupMetadataLogPageRequest selects a bounded forward committed-log page.
type BackupMetadataLogPageRequest struct {
	// HashSlot identifies the logical metadata partition.
	HashSlot uint16
	// AfterIndex is the last fully scanned physical Slot Raft index.
	AfterIndex uint64
	// ThroughIndex pins the page scan to one previously observed logical boundary.
	ThroughIndex uint64
	// TargetBytes is the desired aggregate encoded page size.
	TargetBytes int64
	// MaxBytes is the hard limit for one oversized portable record.
	MaxBytes int64
	// MaxRecords bounds physical Raft entries examined by this call.
	MaxRecords int
}

// BackupMetadataLogPage is one forward page from the authoritative Slot RaftDB.
type BackupMetadataLogPage struct {
	// Records contains portable commands that mutate the requested Hash Slot.
	Records [][]byte
	// NextIndex is the greatest physical Raft index examined by this page.
	NextIndex uint64
	// Done reports that every entry through the pinned logical index was examined.
	Done bool
}

// ObserveBackupCaptureAuthority returns a fresh proof only on the exact local
// Slot Leader. Cached routing alone is insufficient because it intentionally
// retains the last known leader across transient Raft observation gaps.
func (n *Node) ObserveBackupCaptureAuthority(ctx context.Context, hashSlot uint16) (BackupCaptureAuthority, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupCaptureAuthority{}, err
	}
	if n == nil || n.defaultSlotRuntime == nil {
		return BackupCaptureAuthority{}, ErrNotStarted
	}
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return BackupCaptureAuthority{}, err
	}
	if route.Leader != n.NodeID() {
		return BackupCaptureAuthority{}, ErrNotLeader
	}
	status, err := n.defaultSlotRuntime.Status(multiraft.SlotID(route.SlotID))
	if err != nil {
		return BackupCaptureAuthority{}, mapSlotLogRuntimeError(err)
	}
	if uint32(status.SlotID) != route.SlotID ||
		uint64(status.NodeID) != n.NodeID() ||
		uint64(status.LeaderID) != n.NodeID() ||
		status.Term != route.LeaderTerm ||
		status.Role != multiraft.RoleLeader ||
		route.ConfigEpoch == 0 {
		return BackupCaptureAuthority{}, ErrNotLeader
	}
	return BackupCaptureAuthority{
		HashSlot: hashSlot, SlotID: route.SlotID, HolderNodeID: n.NodeID(),
		LeaderTerm: status.Term, ConfigEpoch: route.ConfigEpoch,
	}, nil
}

// ObserveBackupMetadataHighWatermark routes to the Slot Leader's durable logical cut.
func (n *Node) ObserveBackupMetadataHighWatermark(ctx context.Context, hashSlot uint16) (BackupMetadataHighWatermark, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupMetadataHighWatermark{}, err
	}
	if n == nil || n.defaultSlotRuntime == nil || n.defaultSlotRaftDB == nil {
		return BackupMetadataHighWatermark{}, ErrNotStarted
	}
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return BackupMetadataHighWatermark{}, err
	}
	if route.Leader != n.NodeID() {
		response, err := n.callBackupContinuousSlot(ctx, route.Leader, backupContinuousSlotRPCRequest{
			Action: backupContinuousSlotActionObserve, HashSlot: hashSlot,
		})
		if err != nil {
			return BackupMetadataHighWatermark{}, err
		}
		if response.Watermark.HashSlot != hashSlot ||
			response.Watermark.SlotID != route.SlotID ||
			response.Watermark.ObservedAtUnixMillis <= 0 {
			return BackupMetadataHighWatermark{}, fmt.Errorf("cluster: invalid remote backup metadata watermark")
		}
		return response.Watermark, nil
	}
	return n.observeBackupMetadataHighWatermarkLocal(ctx, hashSlot)
}

func (n *Node) observeBackupMetadataHighWatermarkLocal(ctx context.Context, hashSlot uint16) (BackupMetadataHighWatermark, error) {
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return BackupMetadataHighWatermark{}, err
	}
	if route.Leader != n.NodeID() {
		return BackupMetadataHighWatermark{}, ErrNotLeader
	}
	status, err := n.defaultSlotRuntime.Status(multiraft.SlotID(route.SlotID))
	if err != nil {
		return BackupMetadataHighWatermark{}, mapSlotLogRuntimeError(err)
	}
	if n.backupMetadataIndex == nil {
		n.mu.Lock()
		if n.backupMetadataIndex == nil {
			n.backupMetadataIndex = newBackupMetadataLogIndex()
		}
		n.mu.Unlock()
	}
	raftIndex, err := n.backupMetadataIndex.highWatermark(
		ctx,
		route.SlotID,
		n.defaultSlotRaftDB.ForSlot(uint64(route.SlotID)),
		hashSlot,
		status.AppliedIndex,
	)
	if err != nil {
		return BackupMetadataHighWatermark{}, err
	}
	return BackupMetadataHighWatermark{
		HashSlot: hashSlot, SlotID: route.SlotID, RaftIndex: raftIndex,
		ObservedAtUnixMillis: time.Now().UTC().UnixMilli(),
	}, nil
}

// ReadBackupMetadataLogPage routes exact applied logical Slot commands from the
// Leader's retained RaftDB and filters them by logical Hash Slot.
func (n *Node) ReadBackupMetadataLogPage(ctx context.Context, request BackupMetadataLogPageRequest) (BackupMetadataLogPage, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupMetadataLogPage{}, err
	}
	if n == nil || n.defaultSlotRuntime == nil || n.defaultSlotRaftDB == nil {
		return BackupMetadataLogPage{}, ErrNotStarted
	}
	route, err := n.RouteHashSlot(request.HashSlot)
	if err != nil {
		return BackupMetadataLogPage{}, err
	}
	if route.Leader != n.NodeID() {
		request.TargetBytes = min(request.TargetBytes, int64(backupContinuousSlotChunkBytes))
		page, err := n.callBackupContinuousMetadataPage(ctx, route.Leader, request)
		if err != nil {
			return BackupMetadataLogPage{}, err
		}
		if page.NextIndex <= request.AfterIndex || page.NextIndex > request.ThroughIndex ||
			page.Done != (page.NextIndex == request.ThroughIndex) ||
			len(page.Records) > request.MaxRecords {
			return BackupMetadataLogPage{}, fmt.Errorf("cluster: invalid remote backup metadata page")
		}
		return page, nil
	}
	return n.readBackupMetadataLogPageLocal(ctx, request)
}

func (n *Node) readBackupMetadataLogPageLocal(ctx context.Context, request BackupMetadataLogPageRequest) (BackupMetadataLogPage, error) {
	route, err := n.RouteHashSlot(request.HashSlot)
	if err != nil {
		return BackupMetadataLogPage{}, err
	}
	if route.Leader != n.NodeID() {
		return BackupMetadataLogPage{}, ErrNotLeader
	}
	status, err := n.defaultSlotRuntime.Status(multiraft.SlotID(route.SlotID))
	if err != nil {
		return BackupMetadataLogPage{}, mapSlotLogRuntimeError(err)
	}
	if request.ThroughIndex > status.AppliedIndex {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata cut exceeds applied index")
	}
	if n.backupMetadataIndex == nil {
		n.mu.Lock()
		if n.backupMetadataIndex == nil {
			n.backupMetadataIndex = newBackupMetadataLogIndex()
		}
		n.mu.Unlock()
	}
	return n.backupMetadataIndex.readPage(
		ctx,
		route.SlotID,
		n.defaultSlotRaftDB.ForSlot(uint64(route.SlotID)),
		request,
	)
}

func readBackupMetadataLogPage(ctx context.Context, storage slotLogStorage, request BackupMetadataLogPageRequest) (BackupMetadataLogPage, error) {
	if storage == nil {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata storage is unavailable")
	}
	if err := validateBackupMetadataLogPageRequest(request); err != nil {
		return BackupMetadataLogPage{}, err
	}
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return BackupMetadataLogPage{}, err
	}
	nextIndex := request.AfterIndex + 1
	if request.AfterIndex == math.MaxUint64 {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata cursor overflow")
	}
	if nextIndex < first {
		return BackupMetadataLogPage{}, ErrBackupSourceCompacted
	}
	if nextIndex > request.ThroughIndex {
		return BackupMetadataLogPage{NextIndex: request.AfterIndex, Done: true}, nil
	}
	hi := request.ThroughIndex + 1
	if request.ThroughIndex-nextIndex+1 > uint64(request.MaxRecords) {
		hi = nextIndex + uint64(request.MaxRecords)
	}
	entries, err := storage.Entries(ctx, nextIndex, hi, uint64(request.MaxBytes))
	if err != nil {
		return BackupMetadataLogPage{}, err
	}
	if len(entries) == 0 {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata page made no progress")
	}
	page := BackupMetadataLogPage{Records: make([][]byte, 0, len(entries))}
	var pageBytes int64
	for _, entry := range entries {
		record, applies, err := portableBackupMetadataRecord(entry, request.HashSlot)
		if err != nil {
			return BackupMetadataLogPage{}, err
		}
		if applies {
			recordBytes := int64(4 + len(record))
			if recordBytes > request.MaxBytes || pageBytes > request.MaxBytes-recordBytes {
				if page.NextIndex < nextIndex {
					return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata record exceeds page limit")
				}
				break
			}
			if len(page.Records) > 0 && pageBytes > request.TargetBytes-recordBytes {
				break
			}
			page.Records = append(page.Records, record)
			pageBytes += recordBytes
		}
		page.NextIndex = entry.Index
		if pageBytes >= request.TargetBytes {
			break
		}
	}
	if page.NextIndex < nextIndex {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata page made no progress")
	}
	page.Done = page.NextIndex >= request.ThroughIndex
	return page, nil
}

func validateBackupMetadataLogPageRequest(request BackupMetadataLogPageRequest) error {
	if request.ThroughIndex == 0 || request.ThroughIndex == math.MaxUint64 ||
		request.AfterIndex > request.ThroughIndex ||
		request.TargetBytes <= 0 || request.TargetBytes > request.MaxBytes ||
		request.MaxBytes <= 0 || request.MaxBytes > MaxCaptureBackupRecordBytes ||
		request.MaxRecords <= 0 || request.MaxRecords > 1<<20 {
		return fmt.Errorf("cluster: invalid backup metadata page request")
	}
	return nil
}

func portableBackupMetadataRecord(entry raftpb.Entry, hashSlot uint16) ([]byte, bool, error) {
	if entry.Type != raftpb.EntryNormal || len(entry.Data) == 0 {
		return nil, false, nil
	}
	if len(entry.Data) < slotProposalEnvelopeSize {
		return nil, false, fmt.Errorf("cluster: corrupt backup metadata proposal envelope")
	}
	envelopeHashSlot := binary.BigEndian.Uint16(entry.Data[:2])
	createdAtMillis := int64(binary.BigEndian.Uint64(entry.Data[2:slotProposalEnvelopeSize]))
	command := entry.Data[slotProposalEnvelopeSize:]
	hashSlots, err := metafsm.DecodeCommandHashSlots(command, envelopeHashSlot)
	if err != nil {
		return nil, false, err
	}
	applies := false
	for _, candidate := range hashSlots {
		if candidate == hashSlot {
			applies = true
			break
		}
	}
	if !applies {
		return nil, false, nil
	}
	record, err := backupartifact.MarshalMetadataLogRecord(backupartifact.MetadataLogRecord{
		HashSlot: hashSlot, RaftIndex: entry.Index, RaftTerm: entry.Term,
		CommittedAtUnixMillis: createdAtMillis, Command: command,
	})
	return record, true, err
}
