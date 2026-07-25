package cluster

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	backupMetadataIndexScanBytes   = 64 << 20
	backupMetadataIndexScanRecords = 4096
)

// backupMetadataLogIndex is an ephemeral sparse secondary index. Rebuilding it
// after restart scans each retained physical Slot log once; it never participates
// in metadata proposals or foreground SEND.
type backupMetadataLogIndex struct {
	mu    sync.Mutex
	slots map[uint32]*backupMetadataSlotIndex
}

type backupMetadataSlotIndex struct {
	mu             sync.Mutex
	firstIndex     uint64
	indexedThrough uint64
	byHashSlot     map[uint16][]uint64
}

func newBackupMetadataLogIndex() *backupMetadataLogIndex {
	return &backupMetadataLogIndex{slots: make(map[uint32]*backupMetadataSlotIndex)}
}

func (i *backupMetadataLogIndex) slot(slotID uint32) *backupMetadataSlotIndex {
	i.mu.Lock()
	defer i.mu.Unlock()
	index := i.slots[slotID]
	if index == nil {
		index = &backupMetadataSlotIndex{byHashSlot: make(map[uint16][]uint64)}
		i.slots[slotID] = index
	}
	return index
}

func (i *backupMetadataLogIndex) highWatermark(ctx context.Context, slotID uint32, storage slotLogStorage, hashSlot uint16, appliedIndex uint64) (uint64, error) {
	if i == nil {
		return 0, fmt.Errorf("cluster: backup metadata index is unavailable")
	}
	if storage == nil || appliedIndex == math.MaxUint64 {
		return 0, fmt.Errorf("cluster: invalid backup metadata high watermark request")
	}
	index := i.slot(slotID)
	index.mu.Lock()
	defer index.mu.Unlock()

	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return 0, err
	}
	index.prune(first)
	request := BackupMetadataLogPageRequest{
		HashSlot:     hashSlot,
		ThroughIndex: appliedIndex,
		TargetBytes:  backupMetadataIndexScanBytes,
		MaxBytes:     MaxCaptureBackupRecordBytes,
		MaxRecords:   backupMetadataIndexScanRecords,
	}
	for index.indexedThrough < appliedIndex {
		if err := index.extend(ctx, storage, request); err != nil {
			return 0, err
		}
	}
	indexes := index.byHashSlot[hashSlot]
	offset := sort.Search(len(indexes), func(position int) bool {
		return indexes[position] > appliedIndex
	})
	if offset == 0 {
		return 0, nil
	}
	return indexes[offset-1], nil
}

func (i *backupMetadataLogIndex) readPage(ctx context.Context, slotID uint32, storage slotLogStorage, request BackupMetadataLogPageRequest) (BackupMetadataLogPage, error) {
	if i == nil {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata index is unavailable")
	}
	if err := validateBackupMetadataLogPageRequest(request); err != nil {
		return BackupMetadataLogPage{}, err
	}
	index := i.slot(slotID)
	index.mu.Lock()
	defer index.mu.Unlock()

	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return BackupMetadataLogPage{}, err
	}
	next := request.AfterIndex + 1
	if next < first {
		return BackupMetadataLogPage{}, ErrBackupSourceCompacted
	}
	if next > request.ThroughIndex {
		return BackupMetadataLogPage{NextIndex: request.AfterIndex, Done: true}, nil
	}
	index.prune(first)
	requiredIndexThrough := request.ThroughIndex
	if request.ThroughIndex-request.AfterIndex > uint64(request.MaxRecords) {
		requiredIndexThrough = request.AfterIndex + uint64(request.MaxRecords)
	}
	for index.indexedThrough < requiredIndexThrough {
		if err := index.extend(ctx, storage, request); err != nil {
			return BackupMetadataLogPage{}, err
		}
	}
	windowEnd := request.ThroughIndex
	if index.indexedThrough < windowEnd {
		windowEnd = index.indexedThrough
	}
	if windowEnd-request.AfterIndex > uint64(request.MaxRecords) {
		windowEnd = request.AfterIndex + uint64(request.MaxRecords)
	}
	if windowEnd < next {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata index made no progress")
	}
	page := BackupMetadataLogPage{NextIndex: windowEnd}
	indexes := index.byHashSlot[request.HashSlot]
	offset := sort.Search(len(indexes), func(position int) bool { return indexes[position] > request.AfterIndex })
	entries, err := storage.Entries(
		ctx, next, windowEnd+1, uint64(request.TargetBytes),
	)
	if err != nil {
		return BackupMetadataLogPage{}, err
	}
	if len(entries) == 0 {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: indexed backup metadata window is missing")
	}
	var pageBytes int64
	page.NextIndex = request.AfterIndex
	for _, entry := range entries {
		if offset < len(indexes) && indexes[offset] < entry.Index {
			return BackupMetadataLogPage{}, fmt.Errorf("cluster: indexed backup metadata entry is missing")
		}
		if offset < len(indexes) && indexes[offset] == entry.Index {
			record, applies, err := portableBackupMetadataRecord(entry, request.HashSlot)
			if err != nil {
				return BackupMetadataLogPage{}, err
			}
			if !applies {
				return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata sparse index is inconsistent")
			}
			recordBytes := int64(4 + len(record))
			if recordBytes > request.MaxBytes {
				return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata record exceeds hard limit")
			}
			if len(page.Records) > 0 && pageBytes > request.TargetBytes-recordBytes {
				break
			}
			page.Records = append(page.Records, record)
			pageBytes += recordBytes
			offset++
		}
		page.NextIndex = entry.Index
		if pageBytes >= request.TargetBytes {
			break
		}
	}
	if page.NextIndex == request.AfterIndex {
		return BackupMetadataLogPage{}, fmt.Errorf("cluster: backup metadata window made no progress")
	}
	page.Done = page.NextIndex >= request.ThroughIndex
	return page, nil
}

func (i *backupMetadataSlotIndex) extend(ctx context.Context, storage slotLogStorage, request BackupMetadataLogPageRequest) error {
	if i.indexedThrough >= request.ThroughIndex {
		return nil
	}
	next := i.indexedThrough + 1
	if next < i.firstIndex {
		next = i.firstIndex
	}
	hi := request.ThroughIndex + 1
	if request.ThroughIndex-next+1 > uint64(request.MaxRecords) {
		hi = next + uint64(request.MaxRecords)
	}
	entries, err := storage.Entries(ctx, next, hi, uint64(request.TargetBytes))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("cluster: backup metadata index scan made no progress")
	}
	for _, entry := range entries {
		hashSlots, err := backupMetadataEntryHashSlots(entry)
		if err != nil {
			return err
		}
		for _, hashSlot := range hashSlots {
			indexes := i.byHashSlot[hashSlot]
			if len(indexes) == 0 || indexes[len(indexes)-1] != entry.Index {
				i.byHashSlot[hashSlot] = append(indexes, entry.Index)
			}
		}
		i.indexedThrough = entry.Index
	}
	return nil
}

func (i *backupMetadataSlotIndex) prune(first uint64) {
	if first == 0 {
		return
	}
	if i.firstIndex == 0 {
		i.firstIndex = first
	}
	if i.indexedThrough < first-1 {
		clear(i.byHashSlot)
		i.indexedThrough = first - 1
		i.firstIndex = first
		return
	}
	if first <= i.firstIndex {
		return
	}
	for hashSlot, indexes := range i.byHashSlot {
		offset := sort.Search(len(indexes), func(position int) bool { return indexes[position] >= first })
		if offset == len(indexes) {
			delete(i.byHashSlot, hashSlot)
			continue
		}
		if offset > 0 {
			i.byHashSlot[hashSlot] = append([]uint64(nil), indexes[offset:]...)
		}
	}
	i.firstIndex = first
}

func backupMetadataEntryHashSlots(entry raftpb.Entry) ([]uint16, error) {
	if entry.Type != raftpb.EntryNormal || len(entry.Data) == 0 {
		return nil, nil
	}
	if len(entry.Data) < slotProposalEnvelopeSize {
		return nil, fmt.Errorf("cluster: corrupt backup metadata proposal envelope")
	}
	envelopeHashSlot := uint16(entry.Data[0])<<8 | uint16(entry.Data[1])
	return metafsm.DecodeCommandHashSlots(entry.Data[slotProposalEnvelopeSize:], envelopeHashSlot)
}
