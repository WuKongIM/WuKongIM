package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
)

const (
	// ChannelMigrationBlockerNeedsSnapshotBootstrap records that V1 cannot
	// continue until a durable channel snapshot bootstrap primitive exists.
	ChannelMigrationBlockerNeedsSnapshotBootstrap = "NeedsSnapshotBootstrap"
)

// ChannelMigrationKind selects the migration workflow for a task.
type ChannelMigrationKind uint8

const (
	ChannelMigrationKindLeaderTransfer ChannelMigrationKind = 1
	ChannelMigrationKindReplicaReplace ChannelMigrationKind = 2
)

// ChannelMigrationStatus is the durable lifecycle state for a task.
type ChannelMigrationStatus uint8

const (
	ChannelMigrationStatusPending   ChannelMigrationStatus = 1
	ChannelMigrationStatusRunning   ChannelMigrationStatus = 2
	ChannelMigrationStatusBlocked   ChannelMigrationStatus = 3
	ChannelMigrationStatusCompleted ChannelMigrationStatus = 4
	ChannelMigrationStatusFailed    ChannelMigrationStatus = 5
	ChannelMigrationStatusAborted   ChannelMigrationStatus = 6
)

// ChannelMigrationPhase is the resumable executor phase for a task.
type ChannelMigrationPhase uint8

const (
	ChannelMigrationPhaseValidate           ChannelMigrationPhase = 1
	ChannelMigrationPhaseProbeTarget        ChannelMigrationPhase = 2
	ChannelMigrationPhaseWriteFence         ChannelMigrationPhase = 3
	ChannelMigrationPhaseDrainLeader        ChannelMigrationPhase = 4
	ChannelMigrationPhaseFinalTargetCatchUp ChannelMigrationPhase = 5
	ChannelMigrationPhaseCommitLeaderMeta   ChannelMigrationPhase = 6
	ChannelMigrationPhaseVerifyNewLeader    ChannelMigrationPhase = 7
	ChannelMigrationPhaseAddLearner         ChannelMigrationPhase = 20
	ChannelMigrationPhaseBootstrapTarget    ChannelMigrationPhase = 21
	ChannelMigrationPhaseWarmCatchUp        ChannelMigrationPhase = 22
	ChannelMigrationPhaseCutoverFence       ChannelMigrationPhase = 23
	ChannelMigrationPhasePromoteAndRemove   ChannelMigrationPhase = 25
	ChannelMigrationPhaseVerifyMembership   ChannelMigrationPhase = 26
	ChannelMigrationPhaseClearFence         ChannelMigrationPhase = 27
)

// ChannelMigrationProgress stores lightweight executor observations that must
// survive owner changes and retries.
type ChannelMigrationProgress struct {
	// LeaderLEO is the latest observed leader log end offset.
	LeaderLEO uint64 `json:"leader_leo,omitempty"`
	// LeaderHW is the latest observed leader high watermark.
	LeaderHW uint64 `json:"leader_hw,omitempty"`
	// TargetLEO is the latest observed target log end offset.
	TargetLEO uint64 `json:"target_leo,omitempty"`
	// TargetCheckpointHW is the latest target checkpoint high watermark.
	TargetCheckpointHW uint64 `json:"target_checkpoint_hw,omitempty"`
	// LagRecords is the latest observed leader-to-target log gap.
	LagRecords uint64 `json:"lag_records,omitempty"`
	// StableSinceMS records when the current catch-up proof became stable.
	StableSinceMS int64 `json:"stable_since_ms,omitempty"`
}

// ChannelMigrationTask is the authoritative durable state for one channel
// leader-transfer or replica-replacement attempt.
type ChannelMigrationTask struct {
	// TaskID uniquely identifies one channel migration attempt.
	TaskID string
	// Kind selects leader transfer or one-replica replacement semantics.
	Kind ChannelMigrationKind
	// Status is the durable task lifecycle state.
	Status ChannelMigrationStatus
	// Phase is the resumable executor phase inside the current status.
	Phase ChannelMigrationPhase
	// ChannelID identifies the channel being migrated.
	ChannelID string
	// ChannelType identifies the channel namespace for ChannelID.
	ChannelType int64
	// SourceNode is the replica being replaced or the source leader.
	SourceNode uint64
	// TargetNode is the desired target replica or leader.
	TargetNode uint64
	// DesiredLeader is the leader expected after a standalone or embedded transfer.
	DesiredLeader uint64
	// BaseChannelEpoch is the channel epoch observed when the task was created.
	BaseChannelEpoch uint64
	// BaseLeaderEpoch is the leader epoch observed when the task was created.
	BaseLeaderEpoch uint64
	// FenceToken identifies the write fence owned by this task.
	FenceToken string
	// FenceVersion is the authoritative write-fence generation.
	FenceVersion uint64
	// FenceUntilMS is the current write-fence lease deadline in milliseconds.
	FenceUntilMS int64
	// EmbeddedLeaderTransfer records whether replica replacement includes a transfer.
	EmbeddedLeaderTransfer bool
	// EmbeddedDesiredLeader is the temporary leader selected before replica cutover.
	EmbeddedDesiredLeader uint64
	// OwnerNodeID is the slot-leader node currently executing local side effects.
	OwnerNodeID uint64
	// OwnerLeaseUntilMS is the owner lease deadline in milliseconds.
	OwnerLeaseUntilMS int64
	// CutoverLEO is the leader log end offset recorded at cutover drain.
	CutoverLEO uint64
	// CutoverHW is the leader high watermark recorded at cutover drain.
	CutoverHW uint64
	// DrainedLeaderNode identifies the leader that produced the drain proof.
	DrainedLeaderNode uint64
	// DrainedRuntimeGeneration is the leader runtime generation in the drain proof.
	DrainedRuntimeGeneration uint64
	// DrainedChannelEpoch is the channel epoch included in the drain proof.
	DrainedChannelEpoch uint64
	// DrainedLeaderEpoch is the leader epoch included in the drain proof.
	DrainedLeaderEpoch uint64
	// DrainedFenceVersion is the fence generation included in the drain proof.
	DrainedFenceVersion uint64
	// Attempt is the durable retry counter.
	Attempt uint32
	// NextRunAtMS is the earliest wall-clock time for the next executor attempt.
	NextRunAtMS int64
	// BlockerCode is a stable machine-readable blocker reason.
	BlockerCode string
	// BlockerMessage is a human-readable explanation for the blocker.
	BlockerMessage string
	// LastError stores the last retryable or terminal executor error.
	LastError string
	// CreatedAtMS is the task creation timestamp in milliseconds.
	CreatedAtMS int64
	// UpdatedAtMS is the last task update timestamp in milliseconds.
	UpdatedAtMS int64
	// CompletedAtMS is set when the task reaches a terminal status.
	CompletedAtMS int64
	// Progress stores durable executor observations for recovery and APIs.
	Progress ChannelMigrationProgress
}

// CreateChannelMigrationTask creates task if no active task exists for the
// channel and no task with the same primary key already exists.
func (s *ShardStore) CreateChannelMigrationTask(ctx context.Context, task ChannelMigrationTask) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	primaryKey := encodeChannelMigrationTaskPrimaryKey(s.slot, task.ChannelID, task.ChannelType, task.TaskID, channelMigrationTaskPrimaryFamilyID)
	exists, err := s.db.hasKey(primaryKey)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyExists
	}

	batch := s.db.db.NewBatch()
	defer batch.Close()
	if err := s.upsertChannelMigrationTaskLocked(batch, task); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

// GetChannelMigrationTask loads one migration task by its channel and task id.
func (s *ShardStore) GetChannelMigrationTask(ctx context.Context, channelID string, channelType int64, taskID string) (ChannelMigrationTask, error) {
	if err := s.validate(); err != nil {
		return ChannelMigrationTask{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return ChannelMigrationTask{}, err
	}
	if err := validateChannelMigrationTaskKey(channelID, taskID); err != nil {
		return ChannelMigrationTask{}, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	return s.getChannelMigrationTaskLocked(channelID, channelType, taskID)
}

// GetActiveChannelMigrationTask returns the active task for a channel, if one exists.
func (s *ShardStore) GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (ChannelMigrationTask, bool, error) {
	if err := s.validate(); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	if err := validateChannelRuntimeMetaChannelID(channelID); err != nil {
		return ChannelMigrationTask{}, false, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	taskID, err := s.getActiveChannelMigrationTaskIDLocked(channelID, channelType)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelMigrationTask{}, false, nil
		}
		return ChannelMigrationTask{}, false, err
	}
	task, err := s.getChannelMigrationTaskLocked(channelID, channelType, taskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelMigrationTask{}, false, nil
		}
		return ChannelMigrationTask{}, false, err
	}
	if !task.IsActive() {
		return ChannelMigrationTask{}, false, nil
	}
	return task, true, nil
}

// ListChannelMigrationTasks returns all migration tasks in this shard ordered by primary key.
func (s *ShardStore) ListChannelMigrationTasks(ctx context.Context) ([]ChannelMigrationTask, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeStatePrefix(s.slot, TableIDChannelMigrationTask)
	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	tasks := make([]ChannelMigrationTask, 0, 8)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, err
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, err
		}
		task, familyID, err := decodeChannelMigrationTaskRecord(iter.Key(), value)
		if err != nil {
			return nil, err
		}
		if familyID != channelMigrationTaskPrimaryFamilyID {
			continue
		}
		tasks = append(tasks, task)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return tasks, nil
}

// DeleteTerminalChannelMigrationTasksBefore removes terminal tasks whose
// CompletedAtMS is older than beforeMS, scanning only the terminal index.
func (s *ShardStore) DeleteTerminalChannelMigrationTasksBefore(ctx context.Context, beforeMS int64, limit int) (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, nil
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	prefix := encodeChannelMigrationTaskTerminalIndexPrefix(s.slot)
	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	type deleteKeys struct {
		primary  []byte
		terminal []byte
		count    bool
	}
	deletions := make([]deleteKeys, 0, limit)
	for ok := iter.First(); ok && len(deletions) < limit; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return 0, err
		}
		key := append([]byte(nil), iter.Key()...)
		completedAtMS, channelID, channelType, taskID, err := decodeChannelMigrationTaskTerminalIndexKey(key)
		if err != nil {
			return 0, err
		}
		if completedAtMS >= beforeMS {
			break
		}

		primaryKey := encodeChannelMigrationTaskPrimaryKey(s.slot, channelID, channelType, taskID, channelMigrationTaskPrimaryFamilyID)
		task, err := s.getChannelMigrationTaskByPrimaryKeyLocked(primaryKey, channelID, channelType, taskID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				deletions = append(deletions, deleteKeys{terminal: key})
				continue
			}
			return 0, err
		}
		if !task.IsTerminal() || task.CompletedAtMS != completedAtMS {
			deletions = append(deletions, deleteKeys{terminal: key})
			continue
		}
		deletions = append(deletions, deleteKeys{
			primary:  primaryKey,
			terminal: key,
			count:    true,
		})
	}
	if err := iter.Error(); err != nil {
		return 0, err
	}
	if len(deletions) == 0 {
		return 0, nil
	}

	batch := s.db.db.NewBatch()
	defer batch.Close()
	deleted := 0
	for _, deletion := range deletions {
		if len(deletion.primary) > 0 {
			if err := batch.Delete(deletion.primary, nil); err != nil {
				return 0, err
			}
		}
		if err := batch.Delete(deletion.terminal, nil); err != nil {
			return 0, err
		}
		if deletion.count {
			deleted++
		}
	}
	return deleted, batch.Commit(pebble.Sync)
}

// IsActive reports whether the task should block another task for the same channel.
func (t ChannelMigrationTask) IsActive() bool {
	return !t.IsTerminal()
}

// IsTerminal reports whether the task has reached a retention-eligible state.
func (t ChannelMigrationTask) IsTerminal() bool {
	switch t.Status {
	case ChannelMigrationStatusCompleted, ChannelMigrationStatusFailed, ChannelMigrationStatusAborted:
		return true
	default:
		return false
	}
}

func (s *ShardStore) upsertChannelMigrationTaskLocked(batch *pebble.Batch, task ChannelMigrationTask) error {
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}

	primaryKey := encodeChannelMigrationTaskPrimaryKey(s.slot, task.ChannelID, task.ChannelType, task.TaskID, channelMigrationTaskPrimaryFamilyID)
	existing, err := s.getChannelMigrationTaskByPrimaryKeyLocked(primaryKey, task.ChannelID, task.ChannelType, task.TaskID)
	exists := true
	if errors.Is(err, ErrNotFound) {
		exists = false
	} else if err != nil {
		return err
	}

	activeIndexKey := encodeChannelMigrationTaskActiveIndexKey(s.slot, task.ChannelID, task.ChannelType)
	if task.IsActive() {
		if err := s.ensureChannelMigrationTaskActiveSlotAvailableLocked(activeIndexKey, task); err != nil {
			return err
		}
		if err := batch.Set(activeIndexKey, []byte(task.TaskID), nil); err != nil {
			return err
		}
	} else if exists && existing.IsActive() {
		if err := batch.Delete(activeIndexKey, nil); err != nil {
			return err
		}
	}

	if exists && existing.IsTerminal() {
		oldTerminalIndexKey := encodeChannelMigrationTaskTerminalIndexKey(s.slot, existing.CompletedAtMS, existing.ChannelID, existing.ChannelType, existing.TaskID)
		if !task.IsTerminal() || existing.CompletedAtMS != task.CompletedAtMS {
			if err := batch.Delete(oldTerminalIndexKey, nil); err != nil {
				return err
			}
		}
	}
	if task.IsTerminal() {
		terminalIndexKey := encodeChannelMigrationTaskTerminalIndexKey(s.slot, task.CompletedAtMS, task.ChannelID, task.ChannelType, task.TaskID)
		if err := batch.Set(terminalIndexKey, nil, nil); err != nil {
			return err
		}
	}

	return batch.Set(primaryKey, encodeChannelMigrationTaskFamilyValue(task, primaryKey), nil)
}

func (s *ShardStore) ensureChannelMigrationTaskActiveSlotAvailableLocked(activeIndexKey []byte, task ChannelMigrationTask) error {
	existingTaskID, err := decodeChannelMigrationTaskIDIndexValue(s.db.getValue(activeIndexKey))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if existingTaskID == task.TaskID {
		return nil
	}
	existing, err := s.getChannelMigrationTaskLocked(task.ChannelID, task.ChannelType, existingTaskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if existing.IsActive() {
		return ErrAlreadyExists
	}
	return nil
}

func (s *ShardStore) getActiveChannelMigrationTaskIDLocked(channelID string, channelType int64) (string, error) {
	key := encodeChannelMigrationTaskActiveIndexKey(s.slot, channelID, channelType)
	return decodeChannelMigrationTaskIDIndexValue(s.db.getValue(key))
}

func decodeChannelMigrationTaskIDIndexValue(value []byte, err error) (string, error) {
	if err != nil {
		return "", err
	}
	taskID := string(value)
	if err := validateChannelMigrationTaskID(taskID); err != nil {
		return "", ErrCorruptValue
	}
	return taskID, nil
}

func (s *ShardStore) getChannelMigrationTaskLocked(channelID string, channelType int64, taskID string) (ChannelMigrationTask, error) {
	key := encodeChannelMigrationTaskPrimaryKey(s.slot, channelID, channelType, taskID, channelMigrationTaskPrimaryFamilyID)
	return s.getChannelMigrationTaskByPrimaryKeyLocked(key, channelID, channelType, taskID)
}

func (s *ShardStore) getChannelMigrationTaskByPrimaryKeyLocked(key []byte, channelID string, channelType int64, taskID string) (ChannelMigrationTask, error) {
	value, err := s.db.getValue(key)
	if err != nil {
		return ChannelMigrationTask{}, err
	}
	task, err := decodeChannelMigrationTaskFamilyValue(key, value)
	if err != nil {
		return ChannelMigrationTask{}, err
	}
	task.ChannelID = channelID
	task.ChannelType = channelType
	task.TaskID = taskID
	return task, nil
}

func validateChannelMigrationTask(task ChannelMigrationTask) error {
	if err := validateChannelMigrationTaskKey(task.ChannelID, task.TaskID); err != nil {
		return err
	}
	if !isValidChannelMigrationKind(task.Kind) || !isValidChannelMigrationStatus(task.Status) || !isValidChannelMigrationPhase(task.Phase) {
		return ErrInvalidArgument
	}
	switch task.Kind {
	case ChannelMigrationKindLeaderTransfer:
		if task.SourceNode == 0 || task.TargetNode == 0 || task.DesiredLeader == 0 || task.SourceNode == task.TargetNode {
			return ErrInvalidArgument
		}
	case ChannelMigrationKindReplicaReplace:
		if task.SourceNode == 0 || task.TargetNode == 0 || task.SourceNode == task.TargetNode {
			return ErrInvalidArgument
		}
	}
	if task.FenceToken != "" && (task.FenceVersion == 0 || task.FenceUntilMS <= 0) {
		return ErrInvalidArgument
	}
	if task.EmbeddedLeaderTransfer && task.EmbeddedDesiredLeader == 0 {
		return ErrInvalidArgument
	}
	if task.OwnerNodeID == 0 && task.OwnerLeaseUntilMS != 0 {
		return ErrInvalidArgument
	}
	if task.OwnerNodeID != 0 && task.OwnerLeaseUntilMS <= 0 {
		return ErrInvalidArgument
	}
	if task.CreatedAtMS <= 0 || task.UpdatedAtMS <= 0 {
		return ErrInvalidArgument
	}
	if task.IsTerminal() {
		if task.CompletedAtMS <= 0 {
			return ErrInvalidArgument
		}
	} else if task.CompletedAtMS != 0 {
		return ErrInvalidArgument
	}
	if task.Status == ChannelMigrationStatusBlocked && task.BlockerCode == "" {
		return ErrInvalidArgument
	}
	if task.BlockerCode == ChannelMigrationBlockerNeedsSnapshotBootstrap && task.Phase == 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationTaskKey(channelID, taskID string) error {
	if err := validateChannelRuntimeMetaChannelID(channelID); err != nil {
		return err
	}
	return validateChannelMigrationTaskID(taskID)
}

func validateChannelMigrationTaskID(taskID string) error {
	if taskID == "" || len(taskID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func isValidChannelMigrationKind(kind ChannelMigrationKind) bool {
	switch kind {
	case ChannelMigrationKindLeaderTransfer, ChannelMigrationKindReplicaReplace:
		return true
	default:
		return false
	}
}

func isValidChannelMigrationStatus(status ChannelMigrationStatus) bool {
	switch status {
	case ChannelMigrationStatusPending,
		ChannelMigrationStatusRunning,
		ChannelMigrationStatusBlocked,
		ChannelMigrationStatusCompleted,
		ChannelMigrationStatusFailed,
		ChannelMigrationStatusAborted:
		return true
	default:
		return false
	}
}

func isValidChannelMigrationPhase(phase ChannelMigrationPhase) bool {
	switch phase {
	case ChannelMigrationPhaseValidate,
		ChannelMigrationPhaseProbeTarget,
		ChannelMigrationPhaseWriteFence,
		ChannelMigrationPhaseDrainLeader,
		ChannelMigrationPhaseFinalTargetCatchUp,
		ChannelMigrationPhaseCommitLeaderMeta,
		ChannelMigrationPhaseVerifyNewLeader,
		ChannelMigrationPhaseAddLearner,
		ChannelMigrationPhaseBootstrapTarget,
		ChannelMigrationPhaseWarmCatchUp,
		ChannelMigrationPhaseCutoverFence,
		ChannelMigrationPhasePromoteAndRemove,
		ChannelMigrationPhaseVerifyMembership,
		ChannelMigrationPhaseClearFence:
		return true
	default:
		return false
	}
}

func encodeChannelMigrationTaskPrimaryKey(hashSlot uint16, channelID string, channelType int64, taskID string, familyID uint16) []byte {
	key := make([]byte, 0, 80)
	key = encodeStatePrefix(hashSlot, ChannelMigrationTaskTable.ID)
	key = appendKeyString(key, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, taskID)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeChannelMigrationTaskActiveIndexKey(hashSlot uint16, channelID string, channelType int64) []byte {
	key := make([]byte, 0, 64)
	key = encodeIndexPrefix(hashSlot, ChannelMigrationTaskTable.ID, channelMigrationTaskActiveIndexID)
	key = appendKeyString(key, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	return key
}

func encodeChannelMigrationTaskTerminalIndexPrefix(hashSlot uint16) []byte {
	return encodeIndexPrefix(hashSlot, ChannelMigrationTaskTable.ID, channelMigrationTaskTerminalIndexID)
}

func encodeChannelMigrationTaskTerminalIndexKey(hashSlot uint16, completedAtMS int64, channelID string, channelType int64, taskID string) []byte {
	key := make([]byte, 0, 96)
	key = encodeChannelMigrationTaskTerminalIndexPrefix(hashSlot)
	key = appendKeyInt64Ordered(key, completedAtMS)
	key = appendKeyString(key, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, taskID)
	return key
}

func decodeChannelMigrationTaskTerminalIndexKey(key []byte) (int64, string, int64, string, error) {
	prefixLen := 1 + 2 + 4 + 2
	if len(key) < prefixLen || key[0] != keyspaceIndex {
		return 0, "", 0, "", ErrCorruptValue
	}
	if binary.BigEndian.Uint32(key[1+2:1+2+4]) != TableIDChannelMigrationTask {
		return 0, "", 0, "", ErrCorruptValue
	}
	if binary.BigEndian.Uint16(key[1+2+4:prefixLen]) != channelMigrationTaskTerminalIndexID {
		return 0, "", 0, "", ErrCorruptValue
	}
	rest := key[prefixLen:]
	completedAtMS, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return 0, "", 0, "", err
	}
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return 0, "", 0, "", err
	}
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return 0, "", 0, "", err
	}
	taskID, rest, err := decodeKeyString(rest)
	if err != nil {
		return 0, "", 0, "", err
	}
	if len(rest) != 0 {
		return 0, "", 0, "", ErrCorruptValue
	}
	return completedAtMS, channelID, channelType, taskID, nil
}

func decodeChannelMigrationTaskRecord(key, value []byte) (ChannelMigrationTask, uint16, error) {
	channelID, channelType, taskID, familyID, err := decodeChannelMigrationTaskPrimaryKey(key)
	if err != nil {
		return ChannelMigrationTask{}, 0, err
	}
	task, err := decodeChannelMigrationTaskFamilyValue(key, value)
	if err != nil {
		return ChannelMigrationTask{}, 0, err
	}
	task.ChannelID = channelID
	task.ChannelType = channelType
	task.TaskID = taskID
	return task, familyID, nil
}

func decodeChannelMigrationTaskPrimaryKey(key []byte) (string, int64, string, uint16, error) {
	prefixLen := 1 + 2 + 4
	if len(key) < prefixLen || key[0] != keyspaceState {
		return "", 0, "", 0, ErrCorruptValue
	}
	if binary.BigEndian.Uint32(key[1+2:prefixLen]) != TableIDChannelMigrationTask {
		return "", 0, "", 0, ErrCorruptValue
	}
	rest := key[prefixLen:]
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return "", 0, "", 0, err
	}
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return "", 0, "", 0, err
	}
	taskID, rest, err := decodeKeyString(rest)
	if err != nil {
		return "", 0, "", 0, err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 || len(rest[n:]) != 0 {
		return "", 0, "", 0, ErrCorruptValue
	}
	return channelID, channelType, taskID, uint16(familyID), nil
}

func encodeChannelMigrationTaskFamilyValue(task ChannelMigrationTask, key []byte) []byte {
	payload := make([]byte, 0, 320)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDKind, 0, uint64(task.Kind))
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDStatus, channelMigrationTaskColumnIDKind, uint64(task.Status))
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDPhase, channelMigrationTaskColumnIDStatus, uint64(task.Phase))
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDSourceNode, channelMigrationTaskColumnIDPhase, task.SourceNode)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDTargetNode, channelMigrationTaskColumnIDSourceNode, task.TargetNode)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDDesiredLeader, channelMigrationTaskColumnIDTargetNode, task.DesiredLeader)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDBaseChannelEpoch, channelMigrationTaskColumnIDDesiredLeader, task.BaseChannelEpoch)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDBaseLeaderEpoch, channelMigrationTaskColumnIDBaseChannelEpoch, task.BaseLeaderEpoch)
	previousColumnID := channelMigrationTaskColumnIDBaseLeaderEpoch
	if task.FenceToken != "" {
		payload = appendBytesValue(payload, channelMigrationTaskColumnIDFenceToken, previousColumnID, task.FenceToken)
		previousColumnID = channelMigrationTaskColumnIDFenceToken
	}
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDFenceVersion, previousColumnID, task.FenceVersion)
	payload = appendIntValue(payload, channelMigrationTaskColumnIDFenceUntilMS, channelMigrationTaskColumnIDFenceVersion, task.FenceUntilMS)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDEmbeddedLeaderTransfer, channelMigrationTaskColumnIDFenceUntilMS, encodeBoolUint64(task.EmbeddedLeaderTransfer))
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDEmbeddedDesiredLeader, channelMigrationTaskColumnIDEmbeddedLeaderTransfer, task.EmbeddedDesiredLeader)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDOwnerNodeID, channelMigrationTaskColumnIDEmbeddedDesiredLeader, task.OwnerNodeID)
	payload = appendIntValue(payload, channelMigrationTaskColumnIDOwnerLeaseUntilMS, channelMigrationTaskColumnIDOwnerNodeID, task.OwnerLeaseUntilMS)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDCutoverLEO, channelMigrationTaskColumnIDOwnerLeaseUntilMS, task.CutoverLEO)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDCutoverHW, channelMigrationTaskColumnIDCutoverLEO, task.CutoverHW)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDDrainedLeaderNode, channelMigrationTaskColumnIDCutoverHW, task.DrainedLeaderNode)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDDrainedRuntimeGeneration, channelMigrationTaskColumnIDDrainedLeaderNode, task.DrainedRuntimeGeneration)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDDrainedChannelEpoch, channelMigrationTaskColumnIDDrainedRuntimeGeneration, task.DrainedChannelEpoch)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDDrainedLeaderEpoch, channelMigrationTaskColumnIDDrainedChannelEpoch, task.DrainedLeaderEpoch)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDDrainedFenceVersion, channelMigrationTaskColumnIDDrainedLeaderEpoch, task.DrainedFenceVersion)
	payload = appendUint64Value(payload, channelMigrationTaskColumnIDAttempt, channelMigrationTaskColumnIDDrainedFenceVersion, uint64(task.Attempt))
	payload = appendIntValue(payload, channelMigrationTaskColumnIDNextRunAtMS, channelMigrationTaskColumnIDAttempt, task.NextRunAtMS)
	previousColumnID = channelMigrationTaskColumnIDNextRunAtMS
	if task.BlockerCode != "" {
		payload = appendBytesValue(payload, channelMigrationTaskColumnIDBlockerCode, previousColumnID, task.BlockerCode)
		previousColumnID = channelMigrationTaskColumnIDBlockerCode
	}
	if task.BlockerMessage != "" {
		payload = appendBytesValue(payload, channelMigrationTaskColumnIDBlockerMessage, previousColumnID, task.BlockerMessage)
		previousColumnID = channelMigrationTaskColumnIDBlockerMessage
	}
	if task.LastError != "" {
		payload = appendBytesValue(payload, channelMigrationTaskColumnIDLastError, previousColumnID, task.LastError)
		previousColumnID = channelMigrationTaskColumnIDLastError
	}
	payload = appendIntValue(payload, channelMigrationTaskColumnIDCreatedAtMS, previousColumnID, task.CreatedAtMS)
	payload = appendIntValue(payload, channelMigrationTaskColumnIDUpdatedAtMS, channelMigrationTaskColumnIDCreatedAtMS, task.UpdatedAtMS)
	payload = appendIntValue(payload, channelMigrationTaskColumnIDCompletedAtMS, channelMigrationTaskColumnIDUpdatedAtMS, task.CompletedAtMS)
	progress := encodeChannelMigrationProgress(task.Progress)
	if len(progress) > 0 {
		payload = appendRawBytesValue(payload, channelMigrationTaskColumnIDProgress, channelMigrationTaskColumnIDCompletedAtMS, progress)
	}
	return wrapFamilyValue(key, payload)
}

func decodeChannelMigrationTaskFamilyValue(key, value []byte) (ChannelMigrationTask, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return ChannelMigrationTask{}, err
	}

	var (
		task        ChannelMigrationTask
		colID       uint16
		haveKind    bool
		haveStatus  bool
		havePhase   bool
		haveCreated bool
		haveUpdated bool
	)
	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return ChannelMigrationTask{}, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta
		if expectedType, ok := channelMigrationTaskValueColumnType(colID); ok && expectedType != valueType {
			return ChannelMigrationTask{}, fmt.Errorf("%w: invalid column %d type %d", ErrCorruptValue, colID, valueType)
		}

		switch valueType {
		case valueTypeBytes:
			raw, err := consumeBytesValue(&payload)
			if err != nil {
				return ChannelMigrationTask{}, err
			}
			switch colID {
			case channelMigrationTaskColumnIDFenceToken:
				task.FenceToken = string(raw)
			case channelMigrationTaskColumnIDBlockerCode:
				task.BlockerCode = string(raw)
			case channelMigrationTaskColumnIDBlockerMessage:
				task.BlockerMessage = string(raw)
			case channelMigrationTaskColumnIDLastError:
				task.LastError = string(raw)
			case channelMigrationTaskColumnIDProgress:
				if err := decodeChannelMigrationProgress(raw, &task.Progress); err != nil {
					return ChannelMigrationTask{}, err
				}
			default:
				// Unknown bytes columns are skipped for forward compatibility.
			}
		case valueTypeInt:
			raw, err := consumeUvarintValue(&payload, "int")
			if err != nil {
				return ChannelMigrationTask{}, err
			}
			value := decodeZigZagInt64(raw)
			switch colID {
			case channelMigrationTaskColumnIDFenceUntilMS:
				task.FenceUntilMS = value
			case channelMigrationTaskColumnIDOwnerLeaseUntilMS:
				task.OwnerLeaseUntilMS = value
			case channelMigrationTaskColumnIDNextRunAtMS:
				task.NextRunAtMS = value
			case channelMigrationTaskColumnIDCreatedAtMS:
				task.CreatedAtMS = value
				haveCreated = true
			case channelMigrationTaskColumnIDUpdatedAtMS:
				task.UpdatedAtMS = value
				haveUpdated = true
			case channelMigrationTaskColumnIDCompletedAtMS:
				task.CompletedAtMS = value
			default:
				// Unknown int columns are skipped for forward compatibility.
			}
		case valueTypeUint:
			raw, err := consumeUvarintValue(&payload, "uint")
			if err != nil {
				return ChannelMigrationTask{}, err
			}
			if err := assignChannelMigrationTaskUintColumn(&task, colID, raw, &haveKind, &haveStatus, &havePhase); err != nil {
				return ChannelMigrationTask{}, err
			}
		default:
			return ChannelMigrationTask{}, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}
	if !haveKind || !haveStatus || !havePhase || !haveCreated || !haveUpdated {
		return ChannelMigrationTask{}, fmt.Errorf("%w: missing required channel migration task column", ErrCorruptValue)
	}
	return task, nil
}

func assignChannelMigrationTaskUintColumn(task *ChannelMigrationTask, colID uint16, raw uint64, haveKind, haveStatus, havePhase *bool) error {
	switch colID {
	case channelMigrationTaskColumnIDKind:
		if raw > uint64(^uint8(0)) {
			return fmt.Errorf("%w: invalid migration kind %d", ErrCorruptValue, raw)
		}
		task.Kind = ChannelMigrationKind(raw)
		*haveKind = true
	case channelMigrationTaskColumnIDStatus:
		if raw > uint64(^uint8(0)) {
			return fmt.Errorf("%w: invalid migration status %d", ErrCorruptValue, raw)
		}
		task.Status = ChannelMigrationStatus(raw)
		*haveStatus = true
	case channelMigrationTaskColumnIDPhase:
		if raw > uint64(^uint8(0)) {
			return fmt.Errorf("%w: invalid migration phase %d", ErrCorruptValue, raw)
		}
		task.Phase = ChannelMigrationPhase(raw)
		*havePhase = true
	case channelMigrationTaskColumnIDSourceNode:
		task.SourceNode = raw
	case channelMigrationTaskColumnIDTargetNode:
		task.TargetNode = raw
	case channelMigrationTaskColumnIDDesiredLeader:
		task.DesiredLeader = raw
	case channelMigrationTaskColumnIDBaseChannelEpoch:
		task.BaseChannelEpoch = raw
	case channelMigrationTaskColumnIDBaseLeaderEpoch:
		task.BaseLeaderEpoch = raw
	case channelMigrationTaskColumnIDFenceVersion:
		task.FenceVersion = raw
	case channelMigrationTaskColumnIDEmbeddedLeaderTransfer:
		if raw > 1 {
			return fmt.Errorf("%w: invalid embedded leader transfer %d", ErrCorruptValue, raw)
		}
		task.EmbeddedLeaderTransfer = raw == 1
	case channelMigrationTaskColumnIDEmbeddedDesiredLeader:
		task.EmbeddedDesiredLeader = raw
	case channelMigrationTaskColumnIDOwnerNodeID:
		task.OwnerNodeID = raw
	case channelMigrationTaskColumnIDCutoverLEO:
		task.CutoverLEO = raw
	case channelMigrationTaskColumnIDCutoverHW:
		task.CutoverHW = raw
	case channelMigrationTaskColumnIDDrainedLeaderNode:
		task.DrainedLeaderNode = raw
	case channelMigrationTaskColumnIDDrainedRuntimeGeneration:
		task.DrainedRuntimeGeneration = raw
	case channelMigrationTaskColumnIDDrainedChannelEpoch:
		task.DrainedChannelEpoch = raw
	case channelMigrationTaskColumnIDDrainedLeaderEpoch:
		task.DrainedLeaderEpoch = raw
	case channelMigrationTaskColumnIDDrainedFenceVersion:
		task.DrainedFenceVersion = raw
	case channelMigrationTaskColumnIDAttempt:
		if raw > uint64(^uint32(0)) {
			return fmt.Errorf("%w: invalid attempt %d", ErrCorruptValue, raw)
		}
		task.Attempt = uint32(raw)
	default:
		// Unknown uint columns are skipped for forward compatibility.
	}
	return nil
}

func channelMigrationTaskValueColumnType(columnID uint16) (byte, bool) {
	switch columnID {
	case channelMigrationTaskColumnIDFenceToken,
		channelMigrationTaskColumnIDBlockerCode,
		channelMigrationTaskColumnIDBlockerMessage,
		channelMigrationTaskColumnIDLastError,
		channelMigrationTaskColumnIDProgress:
		return valueTypeBytes, true
	case channelMigrationTaskColumnIDFenceUntilMS,
		channelMigrationTaskColumnIDOwnerLeaseUntilMS,
		channelMigrationTaskColumnIDNextRunAtMS,
		channelMigrationTaskColumnIDCreatedAtMS,
		channelMigrationTaskColumnIDUpdatedAtMS,
		channelMigrationTaskColumnIDCompletedAtMS:
		return valueTypeInt, true
	case channelMigrationTaskColumnIDKind,
		channelMigrationTaskColumnIDStatus,
		channelMigrationTaskColumnIDPhase,
		channelMigrationTaskColumnIDSourceNode,
		channelMigrationTaskColumnIDTargetNode,
		channelMigrationTaskColumnIDDesiredLeader,
		channelMigrationTaskColumnIDBaseChannelEpoch,
		channelMigrationTaskColumnIDBaseLeaderEpoch,
		channelMigrationTaskColumnIDFenceVersion,
		channelMigrationTaskColumnIDEmbeddedLeaderTransfer,
		channelMigrationTaskColumnIDEmbeddedDesiredLeader,
		channelMigrationTaskColumnIDOwnerNodeID,
		channelMigrationTaskColumnIDCutoverLEO,
		channelMigrationTaskColumnIDCutoverHW,
		channelMigrationTaskColumnIDDrainedLeaderNode,
		channelMigrationTaskColumnIDDrainedRuntimeGeneration,
		channelMigrationTaskColumnIDDrainedChannelEpoch,
		channelMigrationTaskColumnIDDrainedLeaderEpoch,
		channelMigrationTaskColumnIDDrainedFenceVersion,
		channelMigrationTaskColumnIDAttempt:
		return valueTypeUint, true
	default:
		return 0, false
	}
}

func consumeBytesValue(payload *[]byte) ([]byte, error) {
	length, n := binary.Uvarint(*payload)
	if n <= 0 {
		return nil, fmt.Errorf("metadb: invalid bytes length")
	}
	*payload = (*payload)[n:]
	if uint64(len(*payload)) < length {
		return nil, fmt.Errorf("metadb: bytes payload truncated")
	}
	raw := append([]byte(nil), (*payload)[:length]...)
	*payload = (*payload)[length:]
	return raw, nil
}

func consumeUvarintValue(payload *[]byte, name string) (uint64, error) {
	raw, n := binary.Uvarint(*payload)
	if n <= 0 {
		return 0, fmt.Errorf("metadb: invalid %s payload", name)
	}
	*payload = (*payload)[n:]
	return raw, nil
}

func encodeBoolUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func encodeChannelMigrationProgress(progress ChannelMigrationProgress) []byte {
	if progress == (ChannelMigrationProgress{}) {
		return nil
	}
	raw, err := json.Marshal(progress)
	if err != nil {
		return nil
	}
	return raw
}

func decodeChannelMigrationProgress(raw []byte, progress *ChannelMigrationProgress) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, progress); err != nil {
		return fmt.Errorf("%w: invalid channel migration progress", ErrCorruptValue)
	}
	return nil
}
