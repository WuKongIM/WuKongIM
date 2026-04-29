package fsm

import (
	"context"
	"errors"
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
	hashSlot    uint16
	sourceSlot  multiraft.SlotID
	sourceIndex uint64
}

type pendingForwardDelta struct {
	target multiraft.SlotID
	cmd    multiraft.Command
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
	var pendingForwardDeltas []pendingForwardDelta
	pendingDeltaRecords := make(map[metadb.AppliedHashSlotDelta]struct{})
	pendingMigrationStates := make(map[uint16]metadb.HashSlotMigrationState)
	for i, cmd := range cmds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if cmd.SlotID != multiraft.SlotID(m.slot) {
			return nil, metadb.ErrInvalidArgument
		}
		hashSlot, err := m.resolveHashSlot(cmd)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve hash slot slot=%d command_hash_slot=%d command_type=%d", err, m.slot, cmd.HashSlot, commandTypeForDiagnostics(cmd.Data))
		}

		decoded, err := decodeCommand(cmd.Data)
		if err != nil {
			return nil, err
		}
		if !isMigrationMaintenanceCommand(decoded) {
			fenced, err := m.isHashSlotFenced(ctx, hashSlot, pendingMigrationStates)
			if err != nil {
				return nil, err
			}
			if fenced {
				results[i] = []byte(ApplyResultHashSlotFenced)
				continue
			}
		}
		if ack, ok := decoded.(*ackMigrationOutboxCmd); ok {
			if err := m.applyMigrationOutboxAck(ctx, wb, hashSlot, ack, pendingMigrationStates); err != nil {
				return nil, err
			}
			results[i] = []byte(ApplyResultOK)
			continue
		}
		if cleanup, ok := decoded.(*cleanupMigrationOutboxCmd); ok {
			if err := m.applyMigrationOutboxCleanup(ctx, wb, hashSlot, cleanup, pendingMigrationStates); err != nil {
				return nil, err
			}
			results[i] = []byte(ApplyResultOK)
			continue
		}
		var (
			shouldMarkAppliedDelta bool
			appliedDeltaKey        deltaReplayKey
			appliedDeltaRecord     metadb.AppliedHashSlotDelta
		)
		if applyDelta, ok := decoded.(*applyDeltaCmd); ok {
			appliedDeltaKey = deltaReplayKey{
				hashSlot:    hashSlot,
				sourceSlot:  applyDelta.SourceSlotID,
				sourceIndex: applyDelta.SourceIndex,
			}
			appliedDeltaRecord = metadb.AppliedHashSlotDelta{
				HashSlot:    hashSlot,
				SourceSlot:  uint64(applyDelta.SourceSlotID),
				SourceIndex: applyDelta.SourceIndex,
			}
			if _, ok := pendingDeltaRecords[appliedDeltaRecord]; ok {
				results[i] = []byte(ApplyResultOK)
				continue
			}
			applied, err := m.db.HasAppliedHashSlotDelta(ctx, appliedDeltaRecord)
			if err != nil {
				return nil, err
			}
			if applied {
				results[i] = []byte(ApplyResultOK)
				continue
			}
			shouldMarkAppliedDelta = true
		}
		if err := decoded.apply(wb, hashSlot); err != nil {
			return nil, fmt.Errorf("%w: apply command slot=%d hash_slot=%d command_type=%d", err, m.slot, hashSlot, commandTypeForDiagnostics(cmd.Data))
		}
		if shouldMarkAppliedDelta {
			if err := wb.MarkAppliedHashSlotDelta(appliedDeltaRecord); err != nil {
				return nil, err
			}
			pendingDeltaRecords[appliedDeltaRecord] = struct{}{}
			pendingDeltaKeys = append(pendingDeltaKeys, appliedDeltaKey)
		}
		pendingForward, ok, err := m.stageMigrationMaintenance(ctx, wb, cmd, hashSlot, decoded, pendingMigrationStates)
		if err != nil {
			return nil, fmt.Errorf("%w: stage migration maintenance slot=%d hash_slot=%d command_type=%d", err, m.slot, hashSlot, commandTypeForDiagnostics(cmd.Data))
		}
		if ok {
			pendingForwardDeltas = append(pendingForwardDeltas, pendingForward)
		}
		results[i] = []byte(ApplyResultOK)
	}

	if err := wb.Commit(); err != nil {
		return nil, err
	}
	m.markAppliedDeltas(pendingDeltaKeys)
	m.forwardCommittedDeltas(ctx, pendingForwardDeltas)
	return results, nil
}

func commandTypeForDiagnostics(data []byte) uint8 {
	if len(data) < 2 || data[0] != commandVersion {
		return 0
	}
	return data[1]
}

func isMigrationMaintenanceCommand(decoded command) bool {
	switch decoded.(type) {
	case *applyDeltaCmd, *enterFenceCmd, *ackMigrationOutboxCmd, *cleanupMigrationOutboxCmd:
		return true
	default:
		return false
	}
}

func (m *stateMachine) resolveHashSlot(cmd multiraft.Command) (uint16, error) {
	if isApplyDeltaCommandData(cmd.Data) {
		decoded, err := decodeCommand(cmd.Data)
		if err != nil {
			return 0, err
		}
		applyDelta, ok := decoded.(*applyDeltaCmd)
		if !ok || applyDelta.HashSlot != cmd.HashSlot {
			return 0, metadb.ErrInvalidArgument
		}
		return applyDelta.HashSlot, nil
	}

	m.ownershipMu.RLock()
	defer m.ownershipMu.RUnlock()

	hashSlot := cmd.HashSlot
	if hashSlot == 0 && m.allowLegacyDefault {
		hashSlot = m.legacyHashSlot
	}
	if _, ok := m.ownedHashSlots[hashSlot]; ok {
		return hashSlot, nil
	}
	if isSourceMigrationMaintenanceCommandData(cmd.Data) {
		return hashSlot, nil
	}
	return 0, metadb.ErrInvalidArgument
}

func (m *stateMachine) isHashSlotFenced(ctx context.Context, hashSlot uint16, pendingStates map[uint16]metadb.HashSlotMigrationState) (bool, error) {
	if m == nil {
		return false, nil
	}
	state, ok := pendingStates[hashSlot]
	if !ok {
		var err error
		state, err = m.db.LoadHashSlotMigrationState(ctx, hashSlot)
		if errors.Is(err, metadb.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	if state.SourceSlot != m.slot || state.FenceIndex == 0 {
		return false, nil
	}
	m.ownershipMu.RLock()
	migration, ok := m.migrations[hashSlot]
	m.ownershipMu.RUnlock()
	if ok && migration.target != 0 && uint64(migration.target) != state.TargetSlot {
		return false, nil
	}
	return true, nil
}

func (m *stateMachine) applyMigrationOutboxAck(ctx context.Context, wb *metadb.WriteBatch, hashSlot uint16, ack *ackMigrationOutboxCmd, pendingStates map[uint16]metadb.HashSlotMigrationState) error {
	if m == nil || ack == nil {
		return nil
	}
	if ack.HashSlot != hashSlot || uint64(ack.SourceSlot) != m.slot || ack.TargetSlot == 0 || ack.SourceIndex == 0 {
		return metadb.ErrInvalidArgument
	}
	state, ok := pendingStates[hashSlot]
	if !ok {
		var err error
		state, err = m.db.LoadHashSlotMigrationState(ctx, hashSlot)
		if errors.Is(err, metadb.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if state.SourceSlot != uint64(ack.SourceSlot) || state.TargetSlot != uint64(ack.TargetSlot) || ack.SourceIndex > state.LastOutboxIndex {
		return nil
	}
	if ack.SourceIndex > state.LastAckedIndex {
		state.LastAckedIndex = ack.SourceIndex
	}
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		return err
	}
	if err := wb.DeleteHashSlotMigrationOutbox(hashSlot, uint64(ack.SourceSlot), uint64(ack.TargetSlot), ack.SourceIndex); err != nil {
		return err
	}
	pendingStates[hashSlot] = state
	return nil
}

func (m *stateMachine) applyMigrationOutboxCleanup(ctx context.Context, wb *metadb.WriteBatch, hashSlot uint16, cleanup *cleanupMigrationOutboxCmd, pendingStates map[uint16]metadb.HashSlotMigrationState) error {
	if m == nil || cleanup == nil {
		return nil
	}
	if cleanup.HashSlot != hashSlot || uint64(cleanup.SourceSlot) != m.slot || cleanup.TargetSlot == 0 || cleanup.ThroughIndex == 0 {
		return metadb.ErrInvalidArgument
	}
	state, ok := pendingStates[hashSlot]
	if !ok {
		var err error
		state, err = m.db.LoadHashSlotMigrationState(ctx, hashSlot)
		if err != nil && !errors.Is(err, metadb.ErrNotFound) {
			return err
		}
	}
	if state.SourceSlot == uint64(cleanup.SourceSlot) && state.TargetSlot == uint64(cleanup.TargetSlot) && state.LastOutboxIndex != 0 && state.LastOutboxIndex <= cleanup.ThroughIndex {
		if err := wb.DeleteHashSlotMigrationState(hashSlot); err != nil {
			return err
		}
		delete(pendingStates, hashSlot)
	}
	return wb.DeleteHashSlotMigrationOutboxThrough(hashSlot, uint64(cleanup.SourceSlot), uint64(cleanup.TargetSlot), cleanup.ThroughIndex)
}

func (m *stateMachine) stageMigrationMaintenance(ctx context.Context, wb *metadb.WriteBatch, cmd multiraft.Command, hashSlot uint16, decoded command, pendingStates map[uint16]metadb.HashSlotMigrationState) (pendingForwardDelta, bool, error) {
	if fence, ok := decoded.(*enterFenceCmd); ok {
		return m.stageMigrationFence(ctx, wb, cmd, hashSlot, fence, pendingStates)
	}
	return m.stageMigrationOutbox(ctx, wb, cmd, hashSlot, decoded, pendingStates)
}

func (m *stateMachine) stageMigrationFence(ctx context.Context, wb *metadb.WriteBatch, cmd multiraft.Command, hashSlot uint16, fence *enterFenceCmd, pendingStates map[uint16]metadb.HashSlotMigrationState) (pendingForwardDelta, bool, error) {
	if m == nil {
		return pendingForwardDelta{}, false, nil
	}
	migration, ok := m.migrationForFence(hashSlot, fence.Target)
	if !ok || migration.target == 0 {
		return pendingForwardDelta{}, false, metadb.ErrInvalidArgument
	}

	state, err := m.loadOrCreateMigrationState(ctx, wb, hashSlot, migration, pendingStates)
	if err != nil {
		return pendingForwardDelta{}, false, err
	}
	if state.FenceIndex != 0 {
		return pendingForwardDelta{}, false, nil
	}

	state.Phase = uint8(migrationPhaseSwitching)
	state.FenceIndex = cmd.Index
	if cmd.Index > state.LastOutboxIndex {
		state.LastOutboxIndex = cmd.Index
	}
	row := metadb.HashSlotMigrationOutboxRow{
		HashSlot:    hashSlot,
		SourceSlot:  uint64(m.slot),
		TargetSlot:  uint64(migration.target),
		SourceIndex: cmd.Index,
		Data:        append([]byte(nil), cmd.Data...),
	}
	if err := wb.UpsertHashSlotMigrationOutbox(row); err != nil {
		return pendingForwardDelta{}, false, err
	}
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		return pendingForwardDelta{}, false, err
	}
	pendingStates[hashSlot] = state

	if migration.phase < migrationPhaseDelta {
		return pendingForwardDelta{}, false, nil
	}
	forwardCmd := multiraft.Command{
		SlotID:   multiraft.SlotID(m.slot),
		HashSlot: hashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	}
	return pendingForwardDelta{target: migration.target, cmd: forwardCmd}, true, nil
}

func (m *stateMachine) migrationForFence(hashSlot uint16, target multiraft.SlotID) (migrationRuntimeState, bool) {
	m.ownershipMu.RLock()
	migration, ok := m.migrations[hashSlot]
	m.ownershipMu.RUnlock()
	if ok && migration.target != 0 && migration.phase >= migrationPhaseDelta {
		return migration, true
	}
	if target == 0 {
		return migrationRuntimeState{}, false
	}
	return migrationRuntimeState{target: target, phase: migrationPhaseSnapshot}, true
}

func (m *stateMachine) stageMigrationOutbox(ctx context.Context, wb *metadb.WriteBatch, cmd multiraft.Command, hashSlot uint16, decoded command, pendingStates map[uint16]metadb.HashSlotMigrationState) (pendingForwardDelta, bool, error) {
	if m == nil {
		return pendingForwardDelta{}, false, nil
	}
	if _, ok := decoded.(*applyDeltaCmd); ok {
		return pendingForwardDelta{}, false, nil
	}

	m.ownershipMu.RLock()
	migration, ok := m.migrations[hashSlot]
	m.ownershipMu.RUnlock()

	if !ok || (migration.phase != migrationPhaseDelta && migration.phase != migrationPhaseSwitching) {
		return pendingForwardDelta{}, false, nil
	}

	state, err := m.loadOrCreateMigrationState(ctx, wb, hashSlot, migration, pendingStates)
	if err != nil {
		return pendingForwardDelta{}, false, err
	}

	row := metadb.HashSlotMigrationOutboxRow{
		HashSlot:    hashSlot,
		SourceSlot:  uint64(m.slot),
		TargetSlot:  uint64(migration.target),
		SourceIndex: cmd.Index,
		Data:        append([]byte(nil), cmd.Data...),
	}
	if err := wb.UpsertHashSlotMigrationOutbox(row); err != nil {
		return pendingForwardDelta{}, false, err
	}
	state.Phase = uint8(migration.phase)
	if cmd.Index > state.LastOutboxIndex {
		state.LastOutboxIndex = cmd.Index
	}
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		return pendingForwardDelta{}, false, err
	}
	pendingStates[hashSlot] = state

	forwardCmd := multiraft.Command{
		SlotID:   cmd.SlotID,
		HashSlot: hashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	}
	return pendingForwardDelta{target: migration.target, cmd: forwardCmd}, true, nil
}

func (m *stateMachine) loadOrCreateMigrationState(ctx context.Context, wb *metadb.WriteBatch, hashSlot uint16, migration migrationRuntimeState, pendingStates map[uint16]metadb.HashSlotMigrationState) (metadb.HashSlotMigrationState, error) {
	state, ok := pendingStates[hashSlot]
	if !ok {
		var err error
		state, err = m.db.LoadHashSlotMigrationState(ctx, hashSlot)
		if errors.Is(err, metadb.ErrNotFound) {
			state = metadb.HashSlotMigrationState{
				HashSlot:   hashSlot,
				SourceSlot: uint64(m.slot),
				TargetSlot: uint64(migration.target),
			}
		} else if err != nil {
			return metadb.HashSlotMigrationState{}, err
		}
	}
	if state.SourceSlot != uint64(m.slot) || state.TargetSlot != uint64(migration.target) {
		if err := wb.DeleteHashSlotMigrationState(hashSlot); err != nil {
			return metadb.HashSlotMigrationState{}, err
		}
		if err := wb.DeleteAllHashSlotMigrationOutbox(hashSlot); err != nil {
			return metadb.HashSlotMigrationState{}, err
		}
		state = metadb.HashSlotMigrationState{
			HashSlot:   hashSlot,
			SourceSlot: uint64(m.slot),
			TargetSlot: uint64(migration.target),
		}
	}
	return state, nil
}

func (m *stateMachine) forwardCommittedDeltas(ctx context.Context, pending []pendingForwardDelta) {
	if m == nil || len(pending) == 0 {
		return
	}
	m.ownershipMu.RLock()
	forwardDelta := m.forwardDelta
	m.ownershipMu.RUnlock()
	if forwardDelta == nil {
		return
	}
	for _, delta := range pending {
		_ = forwardDelta(ctx, delta.target, delta.cmd)
	}
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
	if err := m.db.ImportHashSlotSnapshotPreservingMigrationMeta(ctx, snap); err != nil {
		return err
	}
	m.addIncomingDeltaHashSlots(snap.HashSlots)
	return nil
}

func (m *stateMachine) addIncomingDeltaHashSlots(hashSlots []uint16) {
	if m == nil || len(hashSlots) == 0 {
		return
	}
	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	if m.incomingDeltaSlots == nil {
		m.incomingDeltaSlots = make(map[uint16]struct{}, len(hashSlots))
	}
	for _, hashSlot := range hashSlots {
		m.incomingDeltaSlots[hashSlot] = struct{}{}
	}
}

func (m *stateMachine) ListHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, afterSourceIndex uint64, limit int) ([]metadb.HashSlotMigrationOutboxRow, error) {
	if m == nil {
		return nil, metadb.ErrInvalidArgument
	}
	return m.db.ListHashSlotMigrationOutbox(ctx, hashSlot, sourceSlot, targetSlot, afterSourceIndex, limit)
}

func (m *stateMachine) LoadHashSlotMigrationState(ctx context.Context, hashSlot uint16) (metadb.HashSlotMigrationState, error) {
	if m == nil {
		return metadb.HashSlotMigrationState{}, metadb.ErrInvalidArgument
	}
	return m.db.LoadHashSlotMigrationState(ctx, hashSlot)
}

func (m *stateMachine) ListHashSlotMigrationStates(ctx context.Context) ([]metadb.HashSlotMigrationState, error) {
	if m == nil {
		return nil, metadb.ErrInvalidArgument
	}
	return m.db.ListHashSlotMigrationStates(ctx)
}

func (m *stateMachine) AckHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	if m == nil {
		return metadb.ErrInvalidArgument
	}
	state, err := m.db.LoadHashSlotMigrationState(ctx, hashSlot)
	if err != nil {
		return err
	}
	if state.SourceSlot != sourceSlot || state.TargetSlot != targetSlot || sourceIndex == 0 || sourceIndex > state.LastOutboxIndex {
		return metadb.ErrInvalidArgument
	}
	if sourceIndex > state.LastAckedIndex {
		state.LastAckedIndex = sourceIndex
	}

	wb := m.db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		return err
	}
	if err := wb.DeleteHashSlotMigrationOutbox(hashSlot, sourceSlot, targetSlot, sourceIndex); err != nil {
		return err
	}
	return wb.Commit()
}

func (m *stateMachine) CleanupHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, throughIndex uint64) error {
	if m == nil {
		return metadb.ErrInvalidArgument
	}
	if throughIndex == 0 {
		return metadb.ErrInvalidArgument
	}

	wb := m.db.NewWriteBatch()
	defer wb.Close()

	state, err := m.db.LoadHashSlotMigrationState(ctx, hashSlot)
	if err == nil && state.SourceSlot == sourceSlot && state.TargetSlot == targetSlot && state.LastOutboxIndex != 0 && state.LastOutboxIndex <= throughIndex {
		if err := wb.DeleteHashSlotMigrationState(hashSlot); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return err
	}
	if err := wb.DeleteHashSlotMigrationOutboxThrough(hashSlot, sourceSlot, targetSlot, throughIndex); err != nil {
		return err
	}
	return wb.Commit()
}

func isApplyDeltaCommandData(data []byte) bool {
	return len(data) >= 2 && data[0] == commandVersion && data[1] == cmdTypeApplyDelta
}

func isSourceMigrationMaintenanceCommandData(data []byte) bool {
	if len(data) < 2 || data[0] != commandVersion {
		return false
	}
	return data[1] == cmdTypeEnterFence || data[1] == cmdTypeAckMigrationOutbox || data[1] == cmdTypeCleanupMigrationOutbox
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
