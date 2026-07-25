package backup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// SourcePinNode exposes real Slot Raft compaction holds and retained-byte estimates.
type SourcePinNode interface {
	HoldBackupSourcePin(context.Context, uint16, uint64) (clusterpkg.BackupSourcePinObservation, error)
	ReleaseBackupSourcePin(context.Context, uint16, uint32) error
}

// ClusterSourcePinManager accounts node-local pins and selects one deterministic
// byte-budget victim without coupling independent Slot workers.
type ClusterSourcePinManager struct {
	node SourcePinNode
	now  func() time.Time

	mu   sync.Mutex
	pins map[uint16]sourcePinRecord
	// hashSlotLocks serialize lease replacement for one logical Slot.
	hashSlotLocks map[uint16]*sync.Mutex
	// physicalSlotLocks serialize floor measurement for logical Slots that
	// temporarily share one physical Raft log.
	physicalSlotLocks map[uint32]*sync.Mutex
}

type sourcePinRecord struct {
	lease                  backupcontract.SlotCaptureLease
	slotID                 uint32
	afterIndex             uint64
	pinStartedAtUnixMillis int64
	pinnedBytes            uint64
}

// NewClusterSourcePinManager creates a bounded per-node source pin adapter.
func NewClusterSourcePinManager(node SourcePinNode, now func() time.Time) (*ClusterSourcePinManager, error) {
	if node == nil {
		return nil, fmt.Errorf("backup source pins: cluster Node is required")
	}
	if now == nil {
		now = time.Now
	}
	return &ClusterSourcePinManager{
		node: node, now: now,
		pins:              make(map[uint16]sourcePinRecord),
		hashSlotLocks:     make(map[uint16]*sync.Mutex),
		physicalSlotLocks: make(map[uint32]*sync.Mutex),
	}, nil
}

// Observe acquires the physical hold before measuring bytes and returns the
// deterministic largest/oldest Slot as the aggregate-budget victim.
func (m *ClusterSourcePinManager) Observe(
	ctx context.Context,
	hashSlot uint16,
	lease backupcontract.SlotCaptureLease,
	frontier backupcontract.SlotFrontier,
) (runtimebackup.SourcePinObservation, error) {
	if m == nil || m.node == nil || frontier.HashSlot != hashSlot ||
		frontier.SourceSlotID != lease.SlotID ||
		frontier.SourcePinStartedAtUnixMillis <= 0 ||
		!backupcontract.SlotCaptureLeasesEqual(frontier.Lease, lease) {
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrInvalidCapture
	}
	hashSlotLock := m.lockForHashSlot(hashSlot)
	hashSlotLock.Lock()
	defer hashSlotLock.Unlock()
	afterIndex, err := parseMetadataLogCursor(frontier.Metadata.SourceCursor)
	if err != nil {
		return runtimebackup.SourcePinObservation{}, err
	}
	m.mu.Lock()
	previous, previousFound := m.pins[hashSlot]
	m.mu.Unlock()
	unlockPhysical := m.lockPhysicalSlots(lease.SlotID, previous.slotID)
	defer unlockPhysical()
	if previousFound &&
		!backupcontract.SlotCaptureLeasesEqual(previous.lease, lease) &&
		previous.slotID != lease.SlotID {
		if err := m.node.ReleaseBackupSourcePin(ctx, hashSlot, previous.slotID); err != nil {
			return runtimebackup.SourcePinObservation{}, err
		}
		m.mu.Lock()
		if current, ok := m.pins[hashSlot]; ok &&
			backupcontract.SlotCaptureLeasesEqual(current.lease, previous.lease) {
			delete(m.pins, hashSlot)
		}
		m.mu.Unlock()
		if err := m.refreshPhysicalFloor(
			ctx, previous.slotID, false, 0, 0,
		); err != nil {
			return runtimebackup.SourcePinObservation{}, err
		}
	}
	observation, err := m.node.HoldBackupSourcePin(ctx, hashSlot, afterIndex)
	if err != nil {
		if errors.Is(err, clusterpkg.ErrBackupSourceCompacted) {
			return runtimebackup.SourcePinObservation{}, runtimebackup.ErrCaptureSourceCompacted
		}
		return runtimebackup.SourcePinObservation{}, err
	}
	if observation.HashSlot != hashSlot || observation.SlotID != lease.SlotID {
		_ = m.node.ReleaseBackupSourcePin(context.Background(), hashSlot, observation.SlotID)
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrCaptureLeaseFenced
	}
	now := m.now().UTC()
	acquired := time.UnixMilli(frontier.SourcePinStartedAtUnixMillis).UTC()
	if now.Before(acquired) {
		_ = m.node.ReleaseBackupSourcePin(context.Background(), hashSlot, observation.SlotID)
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrInvalidCapture
	}
	m.mu.Lock()
	m.pins[hashSlot] = sourcePinRecord{
		lease: lease, slotID: observation.SlotID, afterIndex: afterIndex,
		pinStartedAtUnixMillis: frontier.SourcePinStartedAtUnixMillis,
		pinnedBytes:            observation.PinnedBytes,
	}
	m.mu.Unlock()
	if err := m.refreshPhysicalFloor(
		ctx, observation.SlotID, true, hashSlot, afterIndex,
	); err != nil {
		return runtimebackup.SourcePinObservation{}, err
	}
	m.mu.Lock()
	total, victimHashSlot, victimSet := m.accountingLocked()
	m.mu.Unlock()
	return runtimebackup.SourcePinObservation{
		Age: now.Sub(acquired), PinnedBytes: observation.PinnedBytes,
		NodePinnedBytes: total, NodeBudgetVictim: victimSet && victimHashSlot == hashSlot,
	}, nil
}

// Release removes only the exact recorded lease so a stale worker cannot drop
// a new leader's replacement hold in the same process.
func (m *ClusterSourcePinManager) Release(
	ctx context.Context,
	hashSlot uint16,
	lease backupcontract.SlotCaptureLease,
) (runtimebackup.SourcePinObservation, error) {
	if m == nil || m.node == nil {
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrInvalidCapture
	}
	hashSlotLock := m.lockForHashSlot(hashSlot)
	hashSlotLock.Lock()
	defer hashSlotLock.Unlock()
	m.mu.Lock()
	record, found := m.pins[hashSlot]
	if found && !backupcontract.SlotCaptureLeasesEqual(record.lease, lease) {
		m.mu.Unlock()
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrCaptureLeaseFenced
	}
	m.mu.Unlock()
	unlockPhysical := m.lockPhysicalSlots(record.slotID, lease.SlotID)
	defer unlockPhysical()
	slotID := lease.SlotID
	if found {
		slotID = record.slotID
	}
	if err := m.node.ReleaseBackupSourcePin(ctx, hashSlot, slotID); err != nil {
		return runtimebackup.SourcePinObservation{}, err
	}
	m.mu.Lock()
	delete(m.pins, hashSlot)
	m.mu.Unlock()
	if err := m.refreshPhysicalFloor(ctx, slotID, false, 0, 0); err != nil {
		return runtimebackup.SourcePinObservation{}, err
	}
	m.mu.Lock()
	remaining := m.nodePinnedBytesLocked()
	m.mu.Unlock()
	return runtimebackup.SourcePinObservation{NodePinnedBytes: remaining}, nil
}

// AdoptLease re-fences one local record without releasing a same-physical-Slot
// floor. A physical remap releases the exact old Slot before dropping it.
func (m *ClusterSourcePinManager) AdoptLease(
	ctx context.Context,
	hashSlot uint16,
	lease backupcontract.SlotCaptureLease,
) (runtimebackup.SourcePinObservation, error) {
	if m == nil || m.node == nil || lease.SlotID == 0 {
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrInvalidCapture
	}
	hashSlotLock := m.lockForHashSlot(hashSlot)
	hashSlotLock.Lock()
	defer hashSlotLock.Unlock()
	m.mu.Lock()
	record, found := m.pins[hashSlot]
	m.mu.Unlock()
	unlockPhysical := m.lockPhysicalSlots(record.slotID, lease.SlotID)
	defer unlockPhysical()
	if !found {
		m.mu.Lock()
		total := m.nodePinnedBytesLocked()
		m.mu.Unlock()
		return runtimebackup.SourcePinObservation{NodePinnedBytes: total}, nil
	}
	if record.slotID != lease.SlotID {
		if err := m.node.ReleaseBackupSourcePin(ctx, hashSlot, record.slotID); err != nil {
			return runtimebackup.SourcePinObservation{}, err
		}
		m.mu.Lock()
		delete(m.pins, hashSlot)
		m.mu.Unlock()
		if err := m.refreshPhysicalFloor(ctx, record.slotID, false, 0, 0); err != nil {
			return runtimebackup.SourcePinObservation{}, err
		}
		m.mu.Lock()
		total := m.nodePinnedBytesLocked()
		m.mu.Unlock()
		return runtimebackup.SourcePinObservation{NodePinnedBytes: total}, nil
	}
	record.lease = lease
	m.mu.Lock()
	m.pins[hashSlot] = record
	total := m.nodePinnedBytesLocked()
	m.mu.Unlock()
	return runtimebackup.SourcePinObservation{
		PinnedBytes: record.pinnedBytes, NodePinnedBytes: total,
	}, nil
}

// ReleaseObsolete drops the recorded physical hold without consulting current
// routing or authority. It serializes against Observe so it cannot remove a
// replacement hold installed by a new lease in this process.
func (m *ClusterSourcePinManager) ReleaseObsolete(
	ctx context.Context,
	hashSlot uint16,
) (runtimebackup.SourcePinObservation, error) {
	if m == nil || m.node == nil {
		return runtimebackup.SourcePinObservation{}, runtimebackup.ErrInvalidCapture
	}
	hashSlotLock := m.lockForHashSlot(hashSlot)
	hashSlotLock.Lock()
	defer hashSlotLock.Unlock()
	m.mu.Lock()
	record, found := m.pins[hashSlot]
	m.mu.Unlock()
	unlockPhysical := m.lockPhysicalSlots(record.slotID)
	defer unlockPhysical()
	if found {
		if err := m.node.ReleaseBackupSourcePin(ctx, hashSlot, record.slotID); err != nil {
			return runtimebackup.SourcePinObservation{}, err
		}
		m.mu.Lock()
		delete(m.pins, hashSlot)
		m.mu.Unlock()
		if err := m.refreshPhysicalFloor(ctx, record.slotID, false, 0, 0); err != nil {
			return runtimebackup.SourcePinObservation{}, err
		}
	}
	m.mu.Lock()
	total := m.nodePinnedBytesLocked()
	m.mu.Unlock()
	return runtimebackup.SourcePinObservation{NodePinnedBytes: total}, nil
}

func (m *ClusterSourcePinManager) lockForHashSlot(hashSlot uint16) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	slotLock := m.hashSlotLocks[hashSlot]
	if slotLock == nil {
		slotLock = &sync.Mutex{}
		m.hashSlotLocks[hashSlot] = slotLock
	}
	return slotLock
}

func (m *ClusterSourcePinManager) lockPhysicalSlots(slotIDs ...uint32) func() {
	unique := make(map[uint32]struct{}, len(slotIDs))
	ordered := make([]uint32, 0, len(slotIDs))
	for _, slotID := range slotIDs {
		if slotID == 0 {
			continue
		}
		if _, exists := unique[slotID]; exists {
			continue
		}
		unique[slotID] = struct{}{}
		ordered = append(ordered, slotID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	locks := make([]*sync.Mutex, len(ordered))
	m.mu.Lock()
	for index, slotID := range ordered {
		slotLock := m.physicalSlotLocks[slotID]
		if slotLock == nil {
			slotLock = &sync.Mutex{}
			m.physicalSlotLocks[slotID] = slotLock
		}
		locks[index] = slotLock
	}
	m.mu.Unlock()
	for _, slotLock := range locks {
		slotLock.Lock()
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}
}

func (m *ClusterSourcePinManager) physicalFloorLocked(slotID uint32) (uint16, sourcePinRecord, bool) {
	var floorHashSlot uint16
	var floor sourcePinRecord
	found := false
	for hashSlot, candidate := range m.pins {
		if candidate.slotID != slotID {
			continue
		}
		if !found ||
			candidate.afterIndex < floor.afterIndex ||
			(candidate.afterIndex == floor.afterIndex &&
				candidate.pinStartedAtUnixMillis < floor.pinStartedAtUnixMillis) ||
			(candidate.afterIndex == floor.afterIndex &&
				candidate.pinStartedAtUnixMillis == floor.pinStartedAtUnixMillis &&
				hashSlot < floorHashSlot) {
			floorHashSlot = hashSlot
			floor = candidate
			found = true
		}
	}
	return floorHashSlot, floor, found
}

// refreshPhysicalFloor remeasures the exact current minimum after a member
// changes. The caller holds the physical Slot lock, so the selected record
// cannot move between selection and measurement.
func (m *ClusterSourcePinManager) refreshPhysicalFloor(
	ctx context.Context,
	slotID uint32,
	alreadyMeasured bool,
	alreadyMeasuredHashSlot uint16,
	alreadyMeasuredAfter uint64,
) error {
	m.mu.Lock()
	floorHashSlot, floor, found := m.physicalFloorLocked(slotID)
	m.mu.Unlock()
	if !found ||
		(alreadyMeasured &&
			floorHashSlot == alreadyMeasuredHashSlot &&
			floor.afterIndex == alreadyMeasuredAfter) {
		return nil
	}
	observation, err := m.node.HoldBackupSourcePin(
		ctx, floorHashSlot, floor.afterIndex,
	)
	if err != nil {
		if errors.Is(err, clusterpkg.ErrBackupSourceCompacted) {
			return runtimebackup.ErrCaptureSourceCompacted
		}
		return err
	}
	if observation.HashSlot != floorHashSlot || observation.SlotID != slotID {
		_ = m.node.ReleaseBackupSourcePin(
			context.Background(), floorHashSlot, observation.SlotID,
		)
		return runtimebackup.ErrCaptureLeaseFenced
	}
	m.mu.Lock()
	if current, ok := m.pins[floorHashSlot]; ok &&
		current.slotID == floor.slotID &&
		current.afterIndex == floor.afterIndex &&
		current.pinStartedAtUnixMillis == floor.pinStartedAtUnixMillis &&
		backupcontract.SlotCaptureLeasesEqual(current.lease, floor.lease) {
		current.pinnedBytes = observation.PinnedBytes
		m.pins[floorHashSlot] = current
	}
	m.mu.Unlock()
	return nil
}

func (m *ClusterSourcePinManager) nodePinnedBytesLocked() uint64 {
	total, _, _ := m.accountingLocked()
	return total
}

func (m *ClusterSourcePinManager) accountingLocked() (uint64, uint16, bool) {
	type physicalPin struct {
		floorHashSlot uint16
		floor         sourcePinRecord
		set           bool
	}
	physical := make(map[uint32]physicalPin)
	for hashSlot, candidate := range m.pins {
		group := physical[candidate.slotID]
		if !group.set ||
			candidate.afterIndex < group.floor.afterIndex ||
			(candidate.afterIndex == group.floor.afterIndex &&
				candidate.pinStartedAtUnixMillis < group.floor.pinStartedAtUnixMillis) ||
			(candidate.afterIndex == group.floor.afterIndex &&
				candidate.pinStartedAtUnixMillis == group.floor.pinStartedAtUnixMillis &&
				hashSlot < group.floorHashSlot) {
			group = physicalPin{floorHashSlot: hashSlot, floor: candidate, set: true}
			physical[candidate.slotID] = group
		}
	}
	var total uint64
	var victimHashSlot uint16
	var victim sourcePinRecord
	victimSet := false
	for _, group := range physical {
		if total > math.MaxUint64-group.floor.pinnedBytes {
			total = math.MaxUint64
		} else {
			total += group.floor.pinnedBytes
		}
		if !victimSet ||
			group.floor.pinnedBytes > victim.pinnedBytes ||
			(group.floor.pinnedBytes == victim.pinnedBytes &&
				group.floor.pinStartedAtUnixMillis < victim.pinStartedAtUnixMillis) ||
			(group.floor.pinnedBytes == victim.pinnedBytes &&
				group.floor.pinStartedAtUnixMillis == victim.pinStartedAtUnixMillis &&
				group.floorHashSlot < victimHashSlot) {
			victimHashSlot = group.floorHashSlot
			victim = group.floor
			victimSet = true
		}
	}
	return total, victimHashSlot, victimSet
}

var _ runtimebackup.SourcePinManager = (*ClusterSourcePinManager)(nil)
var _ SourcePinNode = (*clusterpkg.Node)(nil)
