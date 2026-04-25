package fsm

import (
	"context"
	"fmt"
	"sort"
	"sync"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Compile-time interface assertion.
var _ multiraft.BatchStateMachine = (*stateMachine)(nil)

type migrationRuntimePhase uint8

const (
	migrationPhaseSnapshot migrationRuntimePhase = iota
	migrationPhaseDelta
	migrationPhaseSwitching
	migrationPhaseDone
)

type migrationRuntimeState struct {
	target multiraft.SlotID
	phase  migrationRuntimePhase
}

type deltaReplayKey struct {
	hashSlot   uint16
	sourceSlot multiraft.SlotID
	sourceIndex uint64
}

type stateMachine struct {
	db                 *metadb.DB
	slot               uint64
	ownershipMu        sync.RWMutex
	ownedHashSlotList  []uint16
	ownedHashSlots     map[uint16]struct{}
	incomingDeltaSlots map[uint16]struct{}
	legacyHashSlot     uint16
	allowLegacyDefault bool
	migrations         map[uint16]migrationRuntimeState
	forwardDelta       func(context.Context, multiraft.SlotID, multiraft.Command) error
	appliedDeltaMu     sync.Mutex
	appliedDelta       map[deltaReplayKey]struct{}
}

// NewStateMachine creates a state machine for the given slot.
// It returns an error if db is nil or slot is zero.
func NewStateMachine(db *metadb.DB, slot uint64) (multiraft.StateMachine, error) {
	return newStateMachine(db, slot, []uint16{uint16(slot)}, true)
}

func NewStateMachineWithHashSlots(db *metadb.DB, slot uint64, hashSlots []uint16) (multiraft.StateMachine, error) {
	return newStateMachine(db, slot, hashSlots, false)
}

func newStateMachine(db *metadb.DB, slot uint64, hashSlots []uint16, allowLegacyDefault bool) (multiraft.StateMachine, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db must not be nil", metadb.ErrInvalidArgument)
	}
	if slot == 0 {
		return nil, fmt.Errorf("%w: slot must not be zero", metadb.ErrInvalidArgument)
	}
	if len(hashSlots) == 0 {
		return nil, fmt.Errorf("%w: hash slots must not be empty", metadb.ErrInvalidArgument)
	}
	ownedHashSlotList, ownedHashSlots := normalizeOwnedHashSlots(hashSlots)
	return &stateMachine{
		db:                 db,
		slot:               slot,
		ownedHashSlotList:  ownedHashSlotList,
		ownedHashSlots:     ownedHashSlots,
		incomingDeltaSlots: make(map[uint16]struct{}),
		legacyHashSlot:     ownedHashSlotList[0],
		allowLegacyDefault: allowLegacyDefault,
		migrations:         make(map[uint16]migrationRuntimeState),
		appliedDelta:       make(map[deltaReplayKey]struct{}),
	}, nil
}

func normalizeOwnedHashSlots(hashSlots []uint16) ([]uint16, map[uint16]struct{}) {
	normalized := append([]uint16(nil), hashSlots...)
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i] < normalized[j]
	})
	ownedHashSlots := make(map[uint16]struct{}, len(normalized))
	deduped := normalized[:0]
	for _, hashSlot := range normalized {
		if _, exists := ownedHashSlots[hashSlot]; exists {
			continue
		}
		ownedHashSlots[hashSlot] = struct{}{}
		deduped = append(deduped, hashSlot)
	}
	return append([]uint16(nil), deduped...), ownedHashSlots
}

func (m *stateMachine) UpdateOwnedHashSlots(hashSlots []uint16) {
	if m == nil {
		return
	}
	ownedHashSlotList, ownedHashSlots := normalizeOwnedHashSlots(hashSlots)
	legacyHashSlot := uint16(0)
	if len(ownedHashSlotList) > 0 {
		legacyHashSlot = ownedHashSlotList[0]
	}

	m.ownershipMu.Lock()
	m.ownedHashSlotList = ownedHashSlotList
	m.ownedHashSlots = ownedHashSlots
	m.legacyHashSlot = legacyHashSlot
	m.ownershipMu.Unlock()
}

func (m *stateMachine) UpdateOutgoingDeltaTargets(targets map[uint16]multiraft.SlotID) {
	if m == nil {
		return
	}
	next := make(map[uint16]migrationRuntimeState, len(targets))
	for hashSlot, target := range targets {
		if target == 0 {
			continue
		}
		next[hashSlot] = migrationRuntimeState{
			target: target,
			phase:  migrationPhaseDelta,
		}
	}

	m.ownershipMu.Lock()
	m.migrations = next
	m.ownershipMu.Unlock()
}

func (m *stateMachine) UpdateIncomingDeltaHashSlots(hashSlots []uint16) {
	if m == nil {
		return
	}
	_, incoming := normalizeOwnedHashSlots(hashSlots)
	m.ownershipMu.Lock()
	m.incomingDeltaSlots = incoming
	m.ownershipMu.Unlock()
}

func (m *stateMachine) SetDeltaForwarder(fn func(context.Context, multiraft.SlotID, multiraft.Command) error) {
	if m == nil {
		return
	}
	m.ownershipMu.Lock()
	m.forwardDelta = fn
	m.ownershipMu.Unlock()
}

// Apply delegates to ApplyBatch with a single-element slice.
func (m *stateMachine) Apply(ctx context.Context, cmd multiraft.Command) ([]byte, error) {
	results, err := m.ApplyBatch(ctx, []multiraft.Command{cmd})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

func (m *stateMachine) ApplyBatch(ctx context.Context, cmds []multiraft.Command) ([][]byte, error) {
	wb := m.db.NewWriteBatch()
	defer wb.Close()

	results := make([][]byte, len(cmds))
	var pendingDeltaKeys []deltaReplayKey
	for i, cmd := range cmds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if cmd.SlotID != multiraft.SlotID(m.slot) {
			return nil, metadb.ErrInvalidArgument
		}
		hashSlot, err := m.resolveHashSlot(cmd)
		if err != nil {
			return nil, err
		}

		decoded, err := decodeCommand(cmd.Data)
		if err != nil {
			return nil, err
		}
		if applyDelta, ok := decoded.(*applyDeltaCmd); ok {
			key := deltaReplayKey{
				hashSlot:    hashSlot,
				sourceSlot:  applyDelta.SourceSlotID,
				sourceIndex: applyDelta.SourceIndex,
			}
			if m.hasAppliedDelta(key) {
				results[i] = []byte(ApplyResultOK)
				continue
			}
			pendingDeltaKeys = append(pendingDeltaKeys, key)
		}
		if err := decoded.apply(wb, hashSlot); err != nil {
			return nil, err
		}
		if err := m.maybeForwardDelta(ctx, cmd, hashSlot, decoded); err != nil {
			return nil, err
		}
		results[i] = []byte(ApplyResultOK)
	}

	if err := wb.Commit(); err != nil {
		return nil, err
	}
	m.markAppliedDeltas(pendingDeltaKeys)
	return results, nil
}

func (m *stateMachine) resolveHashSlot(cmd multiraft.Command) (uint16, error) {
	m.ownershipMu.RLock()
	defer m.ownershipMu.RUnlock()

	hashSlot := cmd.HashSlot
	if hashSlot == 0 && m.allowLegacyDefault {
		hashSlot = m.legacyHashSlot
	}
	if _, ok := m.ownedHashSlots[hashSlot]; ok {
		return hashSlot, nil
	}
	if isApplyDeltaCommandData(cmd.Data) {
		if _, ok := m.incomingDeltaSlots[hashSlot]; ok {
			return hashSlot, nil
		}
		if migration, ok := m.migrations[hashSlot]; ok && migration.phase >= migrationPhaseDelta {
			return hashSlot, nil
		}
	}
	return 0, metadb.ErrInvalidArgument
}

func (m *stateMachine) maybeForwardDelta(ctx context.Context, cmd multiraft.Command, hashSlot uint16, decoded command) error {
	if m == nil {
		return nil
	}
	if _, ok := decoded.(*applyDeltaCmd); ok {
		return nil
	}

	m.ownershipMu.RLock()
	forwardDelta := m.forwardDelta
	migration, ok := m.migrations[hashSlot]
	m.ownershipMu.RUnlock()

	if forwardDelta == nil {
		return nil
	}
	if !ok || migration.phase > migrationPhaseDelta {
		return nil
	}
	cmd.HashSlot = hashSlot
	return forwardDelta(ctx, migration.target, cmd)
}

func (m *stateMachine) Restore(ctx context.Context, snap multiraft.Snapshot) error {
	m.ownershipMu.RLock()
	hashSlots := m.runtimeSnapshotHashSlotsLocked()
	m.ownershipMu.RUnlock()
	return m.db.ImportHashSlotSnapshot(ctx, metadb.SlotSnapshot{
		HashSlots: hashSlots,
		Data:      append([]byte(nil), snap.Data...),
	})
}

func (m *stateMachine) Snapshot(ctx context.Context) (multiraft.Snapshot, error) {
	m.ownershipMu.RLock()
	hashSlots := m.runtimeSnapshotHashSlotsLocked()
	m.ownershipMu.RUnlock()

	snap, err := m.db.ExportHashSlotSnapshot(ctx, hashSlots)
	if err != nil {
		return multiraft.Snapshot{}, err
	}
	return multiraft.Snapshot{
		Data: append([]byte(nil), snap.Data...),
	}, nil
}

func (m *stateMachine) ExportHashSlotSnapshot(ctx context.Context, hashSlot uint16) (metadb.SlotSnapshot, error) {
	return m.db.ExportHashSlotSnapshot(ctx, []uint16{hashSlot})
}

func (m *stateMachine) ImportHashSlotSnapshot(ctx context.Context, snap metadb.SlotSnapshot) error {
	return m.db.ImportHashSlotSnapshot(ctx, snap)
}

func isApplyDeltaCommandData(data []byte) bool {
	return len(data) >= 2 && data[0] == commandVersion && data[1] == cmdTypeApplyDelta
}

func (m *stateMachine) runtimeSnapshotHashSlotsLocked() []uint16 {
	if m == nil {
		return nil
	}
	hashSlots := append([]uint16(nil), m.ownedHashSlotList...)
	for hashSlot := range m.incomingDeltaSlots {
		hashSlots = append(hashSlots, hashSlot)
	}
	normalized, _ := normalizeOwnedHashSlots(hashSlots)
	return normalized
}

func (m *stateMachine) hasAppliedDelta(key deltaReplayKey) bool {
	if m == nil {
		return false
	}
	m.appliedDeltaMu.Lock()
	defer m.appliedDeltaMu.Unlock()
	_, ok := m.appliedDelta[key]
	return ok
}

func (m *stateMachine) markAppliedDeltas(keys []deltaReplayKey) {
	if m == nil || len(keys) == 0 {
		return
	}
	m.appliedDeltaMu.Lock()
	defer m.appliedDeltaMu.Unlock()
	for _, key := range keys {
		m.appliedDelta[key] = struct{}{}
	}
}

// NewStateMachineFactory returns a factory function suitable for raftcluster.Config.NewStateMachine.
func NewStateMachineFactory(db *metadb.DB) func(slotID multiraft.SlotID) (multiraft.StateMachine, error) {
	return func(slotID multiraft.SlotID) (multiraft.StateMachine, error) {
		return NewStateMachine(db, uint64(slotID))
	}
}

func NewHashSlotStateMachineFactory(db *metadb.DB) func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error) {
	return func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error) {
		return NewStateMachineWithHashSlots(db, uint64(slotID), hashSlots)
	}
}
