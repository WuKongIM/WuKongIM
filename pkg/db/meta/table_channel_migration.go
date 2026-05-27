package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	// ChannelMigrationBlockerNeedsSnapshotBootstrap records that snapshot bootstrap is required.
	ChannelMigrationBlockerNeedsSnapshotBootstrap = "NeedsSnapshotBootstrap"
)

const (
	channelMigrationPrimaryFamilyID uint16 = 0
	channelMigrationPrimaryIndexID  uint16 = 1
	channelMigrationActiveIndexID   uint16 = 2
	channelMigrationTerminalIndexID uint16 = 3

	channelMigrationColumnChannelID   uint16 = 1
	channelMigrationColumnChannelType uint16 = 2
	channelMigrationColumnTaskID      uint16 = 3
	channelMigrationColumnCompletedAt uint16 = 4
	channelMigrationColumnValue       uint16 = 5
)

var channelMigrationTable = registerMetaTable(TableSpec[ChannelMigrationTask]{
	ID:   TableIDChannelMigration,
	Name: "channel_migration",
	Columns: []schema.Column{
		{ID: channelMigrationColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: channelMigrationColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: channelMigrationColumnTaskID, Name: "task_id", Type: schema.TypeString, Required: true},
		{ID: channelMigrationColumnCompletedAt, Name: "completed_at", Type: schema.TypeInt64},
		{ID: channelMigrationColumnValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: channelMigrationPrimaryFamilyID, Name: "primary", Columns: []uint16{channelMigrationColumnValue}}},
	Primary: PrimarySpec[ChannelMigrationTask]{
		IndexID:  channelMigrationPrimaryIndexID,
		FamilyID: channelMigrationPrimaryFamilyID,
		Name:     "pk_channel_migration",
		Columns:  []uint16{channelMigrationColumnChannelID, channelMigrationColumnChannelType, channelMigrationColumnTaskID},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered, KeyString},
		Key: func(task ChannelMigrationTask) KeyParts {
			return channelMigrationTaskPrimaryKey(task.ChannelID, task.ChannelType, task.TaskID)
		},
	},
	Indexes: []IndexSpec[ChannelMigrationTask]{
		{
			ID:             channelMigrationActiveIndexID,
			Name:           "idx_channel_migration_active",
			Columns:        []uint16{channelMigrationColumnChannelID, channelMigrationColumnChannelType},
			Layout:         KeyLayout{KeyString, KeyInt64Ordered},
			DescriptorOnly: true,
		},
		channelMigrationTerminalIndexSpec(),
	},
	Validate: validateChannelMigrationTask,
	EncodeValue: func(task ChannelMigrationTask) ([]byte, error) {
		return encodeChannelMigrationTaskValue(task), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (ChannelMigrationTask, error) {
		return decodeChannelMigrationTaskValue(primary[0].S, primary[1].I64, primary[2].S, value)
	},
})

// ChannelMigrationTable describes the channel migration task table schema.
var ChannelMigrationTable = channelMigrationTable.Schema()

// ChannelMigrationKind selects the migration workflow for a task.
type ChannelMigrationKind uint8

const (
	// ChannelMigrationKindLeaderTransfer moves leadership to an existing replica.
	ChannelMigrationKindLeaderTransfer ChannelMigrationKind = 1
	// ChannelMigrationKindReplicaReplace replaces one replica with another.
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

// ChannelMigrationProgress stores lightweight executor observations.
type ChannelMigrationProgress struct {
	LeaderLEO          uint64 `json:"leader_leo,omitempty"`
	LeaderHW           uint64 `json:"leader_hw,omitempty"`
	TargetLEO          uint64 `json:"target_leo,omitempty"`
	TargetCheckpointHW uint64 `json:"target_checkpoint_hw,omitempty"`
	LagRecords         uint64 `json:"lag_records,omitempty"`
	StableSinceMS      int64  `json:"stable_since_ms,omitempty"`
}

// ChannelMigrationTaskGuard fences a task mutation to one observed durable state.
type ChannelMigrationTaskGuard struct {
	ChannelID                 string
	ChannelType               int64
	TaskID                    string
	ExpectedStatus            ChannelMigrationStatus
	ExpectedPhase             ChannelMigrationPhase
	ExpectedOwnerNodeID       uint64
	ExpectedOwnerLeaseUntilMS int64
	ExpectedUpdatedAtMS       int64
}

// ChannelMigrationRuntimeGuard fences a task create to one runtime metadata state.
type ChannelMigrationRuntimeGuard struct {
	ChannelID               string
	ChannelType             int64
	ExpectedChannelEpoch    uint64
	ExpectedLeaderEpoch     uint64
	ExpectedLeader          uint64
	ExpectedFenceToken      string
	ExpectedFenceVersion    uint64
	ExpectedRouteGeneration uint64
}

// ChannelMigrationTaskCreate describes a runtime-guarded task create.
type ChannelMigrationTaskCreate struct {
	Task         ChannelMigrationTask
	RuntimeGuard ChannelMigrationRuntimeGuard
}

// ChannelMigrationCutoverProof stores the durable proof produced by a fenced drain.
type ChannelMigrationCutoverProof struct {
	CutoverLEO               uint64
	CutoverHW                uint64
	DrainedLeaderNode        uint64
	DrainedRuntimeGeneration uint64
	DrainedChannelEpoch      uint64
	DrainedLeaderEpoch       uint64
	DrainedFenceVersion      uint64
}

// ChannelMigrationTaskClaim describes a guarded owner claim or renewal.
type ChannelMigrationTaskClaim struct {
	Guard             ChannelMigrationTaskGuard
	Status            ChannelMigrationStatus
	Phase             ChannelMigrationPhase
	OwnerNodeID       uint64
	OwnerLeaseUntilMS int64
	UpdatedAtMS       int64
}

// ChannelMigrationTaskAdvance describes a guarded phase/progress update.
type ChannelMigrationTaskAdvance struct {
	Guard                 ChannelMigrationTaskGuard
	Status                ChannelMigrationStatus
	Phase                 ChannelMigrationPhase
	Attempt               uint32
	NextRunAtMS           int64
	BlockerCode           string
	BlockerMessage        string
	LastError             string
	UpdatedAtMS           int64
	CompletedAtMS         int64
	Progress              ChannelMigrationProgress
	CutoverProof          ChannelMigrationCutoverProof
	EmbeddedDesiredLeader uint64
}

// ChannelMigrationFenceRequest sets or renews a channel write fence.
type ChannelMigrationFenceRequest struct {
	Guard        ChannelMigrationTaskGuard
	RuntimeGuard ChannelMigrationRuntimeGuard
	Status       ChannelMigrationStatus
	Phase        ChannelMigrationPhase
	FenceReason  uint8
	FenceUntilMS int64
	UpdatedAtMS  int64
}

// ChannelMigrationResetFenceRequest clears an expired matching write fence.
type ChannelMigrationResetFenceRequest struct {
	Guard        ChannelMigrationTaskGuard
	RuntimeGuard ChannelMigrationRuntimeGuard
	Status       ChannelMigrationStatus
	Phase        ChannelMigrationPhase
	NowMS        int64
	UpdatedAtMS  int64
}

// ChannelMigrationLeaderTransferRequest commits a fenced leader metadata change.
type ChannelMigrationLeaderTransferRequest struct {
	Guard           ChannelMigrationTaskGuard
	RuntimeGuard    ChannelMigrationRuntimeGuard
	Status          ChannelMigrationStatus
	Phase           ChannelMigrationPhase
	DesiredLeader   uint64
	NextLeaderEpoch uint64
	LeaseUntilMS    int64
	NowMS           int64
	UpdatedAtMS     int64
}

// ChannelMigrationAddLearnerRequest adds a learner replica.
type ChannelMigrationAddLearnerRequest struct {
	Guard        ChannelMigrationTaskGuard
	RuntimeGuard ChannelMigrationRuntimeGuard
	Status       ChannelMigrationStatus
	Phase        ChannelMigrationPhase
	TargetNode   uint64
	UpdatedAtMS  int64
}

// ChannelMigrationPromoteLearnerRequest promotes a learner and removes source.
type ChannelMigrationPromoteLearnerRequest struct {
	Guard        ChannelMigrationTaskGuard
	RuntimeGuard ChannelMigrationRuntimeGuard
	Status       ChannelMigrationStatus
	Phase        ChannelMigrationPhase
	SourceNode   uint64
	TargetNode   uint64
	NowMS        int64
	UpdatedAtMS  int64
}

// ChannelMigrationClearFenceRequest clears a matching write fence.
type ChannelMigrationClearFenceRequest struct {
	Guard         ChannelMigrationTaskGuard
	RuntimeGuard  ChannelMigrationRuntimeGuard
	Status        ChannelMigrationStatus
	Phase         ChannelMigrationPhase
	UpdatedAtMS   int64
	CompletedAtMS int64
}

// ChannelMigrationAbortRequest marks a migration aborted.
type ChannelMigrationAbortRequest struct {
	Guard         ChannelMigrationTaskGuard
	RuntimeGuard  ChannelMigrationRuntimeGuard
	Status        ChannelMigrationStatus
	Phase         ChannelMigrationPhase
	UpdatedAtMS   int64
	CompletedAtMS int64
	LastError     string
}

// ChannelMigrationTaskGCRequest removes terminal tasks before a cutoff.
type ChannelMigrationTaskGCRequest struct {
	BeforeMS int64
	Limit    int
}

// ChannelMigrationTaskGCPlan describes terminal task cleanup work.
type ChannelMigrationTaskGCPlan struct {
	TaskCount  int
	EntryCount int
}

// ChannelMigrationTask is the authoritative durable state for one migration attempt.
type ChannelMigrationTask struct {
	TaskID                   string
	Kind                     ChannelMigrationKind
	Status                   ChannelMigrationStatus
	Phase                    ChannelMigrationPhase
	ChannelID                string
	ChannelType              int64
	SourceNode               uint64
	TargetNode               uint64
	DesiredLeader            uint64
	BaseChannelEpoch         uint64
	BaseLeaderEpoch          uint64
	FenceToken               string
	FenceVersion             uint64
	FenceUntilMS             int64
	EmbeddedLeaderTransfer   bool
	EmbeddedDesiredLeader    uint64
	OwnerNodeID              uint64
	OwnerLeaseUntilMS        int64
	CutoverLEO               uint64
	CutoverHW                uint64
	DrainedLeaderNode        uint64
	DrainedRuntimeGeneration uint64
	DrainedChannelEpoch      uint64
	DrainedLeaderEpoch       uint64
	DrainedFenceVersion      uint64
	Attempt                  uint32
	NextRunAtMS              int64
	BlockerCode              string
	BlockerMessage           string
	LastError                string
	CreatedAtMS              int64
	UpdatedAtMS              int64
	CompletedAtMS            int64
	Progress                 ChannelMigrationProgress
}

// IsActive reports whether the task still owns the active channel slot.
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

// CreateChannelMigrationTask creates a task when no active task exists.
func (s *Shard) CreateChannelMigrationTask(ctx context.Context, task ChannelMigrationTask) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageCreateChannelMigrationTask(ctx, batch, task); err != nil {
		return err
	}
	return batch.Commit(true)
}

// GetChannelMigrationTask returns one migration task.
func (s *Shard) GetChannelMigrationTask(ctx context.Context, channelID string, channelType int64, taskID string) (ChannelMigrationTask, bool, error) {
	if err := s.check(ctx); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	if err := validateChannelMigrationIdentity(channelID, taskID); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	return channelMigrationTable.Get(ctx, s, channelMigrationTaskPrimaryKey(channelID, channelType, taskID))
}

// GetActiveChannelMigrationTask returns the active task for a channel, if any.
func (s *Shard) GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (ChannelMigrationTask, bool, error) {
	if err := s.check(ctx); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	if err := validateKeyString(channelID); err != nil {
		return ChannelMigrationTask{}, false, err
	}
	value, ok, err := s.db.get(encodeChannelMigrationActiveIndexKey(s.hashSlot, channelID, channelType))
	if err != nil || !ok {
		return ChannelMigrationTask{}, ok, err
	}
	taskID := string(value)
	if err := validateKeyString(taskID); err != nil {
		return ChannelMigrationTask{}, false, dberrors.ErrCorruptValue
	}
	task, ok, err := s.GetChannelMigrationTask(ctx, channelID, channelType, taskID)
	if err != nil || !ok || !task.IsActive() {
		return ChannelMigrationTask{}, false, err
	}
	return task, true, nil
}

// ListChannelMigrationTasks returns all migration tasks in primary-key order.
func (s *Shard) ListChannelMigrationTasks(ctx context.Context) ([]ChannelMigrationTask, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	tasks, _, _, err := channelMigrationTable.scanPrimary(ctx, s, nil, nil, 0, true, true, false)
	return tasks, err
}

// ClaimChannelMigrationTask applies a guarded owner claim or renewal.
func (s *Shard) ClaimChannelMigrationTask(ctx context.Context, req ChannelMigrationTaskClaim) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageClaimChannelMigrationTask(ctx, batch, req); err != nil {
		return err
	}
	return batch.Commit(true)
}

// AdvanceChannelMigrationTask applies a guarded phase/progress update.
func (s *Shard) AdvanceChannelMigrationTask(ctx context.Context, req ChannelMigrationTaskAdvance) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageAdvanceChannelMigrationTask(ctx, batch, req); err != nil {
		return err
	}
	return batch.Commit(true)
}

// CountTerminalChannelMigrationTasksBefore counts terminal-index entries before a cutoff.
func (s *Shard) CountTerminalChannelMigrationTasksBefore(ctx context.Context, beforeMS int64, limit int) (int, error) {
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, dberrors.ErrInvalidArgument
	}
	prefix := encodeChannelMigrationTerminalIndexPrefix(s.hashSlot)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	count := 0
	for ok := iter.First(); ok && count < limit; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		completedAt, _, _, _, ok := decodeChannelMigrationTerminalIndexKey(prefix, iter.Key())
		if !ok {
			return 0, dberrors.ErrCorruptValue
		}
		if completedAt >= beforeMS {
			break
		}
		count++
	}
	if err := iter.Error(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Shard) stageCreateChannelMigrationTask(ctx context.Context, batch *engine.Batch, task ChannelMigrationTask) error {
	primaryKey, err := channelMigrationTaskRowKey(s.hashSlot, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		return err
	}
	if _, exists, err := s.getChannelMigrationTaskByKey(ctx, primaryKey, task.ChannelID, task.ChannelType, task.TaskID); err != nil || exists {
		if err != nil {
			return err
		}
		return dberrors.ErrAlreadyExists
	}
	return s.stageUpsertChannelMigrationTask(ctx, batch, task)
}

func (s *Shard) stageClaimChannelMigrationTask(ctx context.Context, batch *engine.Batch, req ChannelMigrationTaskClaim) error {
	if err := validateChannelMigrationTaskGuard(req.Guard); err != nil {
		return err
	}
	task, ok, err := s.GetChannelMigrationTask(ctx, req.Guard.ChannelID, req.Guard.ChannelType, req.Guard.TaskID)
	if err != nil || !ok {
		return err
	}
	if !req.Guard.matches(task) {
		return dberrors.ErrConflict
	}
	task.Status = req.Status
	task.Phase = req.Phase
	task.OwnerNodeID = req.OwnerNodeID
	task.OwnerLeaseUntilMS = req.OwnerLeaseUntilMS
	task.UpdatedAtMS = req.UpdatedAtMS
	return s.stageUpsertChannelMigrationTask(ctx, batch, task)
}

func (s *Shard) stageAdvanceChannelMigrationTask(ctx context.Context, batch *engine.Batch, req ChannelMigrationTaskAdvance) error {
	if err := validateChannelMigrationTaskGuard(req.Guard); err != nil {
		return err
	}
	task, ok, err := s.GetChannelMigrationTask(ctx, req.Guard.ChannelID, req.Guard.ChannelType, req.Guard.TaskID)
	if err != nil || !ok {
		return err
	}
	if !req.Guard.matches(task) {
		return dberrors.ErrConflict
	}
	task.Status = req.Status
	task.Phase = req.Phase
	task.Attempt = req.Attempt
	task.NextRunAtMS = req.NextRunAtMS
	task.BlockerCode = req.BlockerCode
	task.BlockerMessage = req.BlockerMessage
	task.LastError = req.LastError
	task.UpdatedAtMS = req.UpdatedAtMS
	task.CompletedAtMS = req.CompletedAtMS
	task.Progress = req.Progress
	if req.CutoverProof != (ChannelMigrationCutoverProof{}) {
		task.CutoverLEO = req.CutoverProof.CutoverLEO
		task.CutoverHW = req.CutoverProof.CutoverHW
		task.DrainedLeaderNode = req.CutoverProof.DrainedLeaderNode
		task.DrainedRuntimeGeneration = req.CutoverProof.DrainedRuntimeGeneration
		task.DrainedChannelEpoch = req.CutoverProof.DrainedChannelEpoch
		task.DrainedLeaderEpoch = req.CutoverProof.DrainedLeaderEpoch
		task.DrainedFenceVersion = req.CutoverProof.DrainedFenceVersion
	}
	if req.EmbeddedDesiredLeader != 0 {
		task.EmbeddedLeaderTransfer = true
		task.EmbeddedDesiredLeader = req.EmbeddedDesiredLeader
	}
	return s.stageUpsertChannelMigrationTask(ctx, batch, task)
}

func (s *Shard) stageUpsertChannelMigrationTask(ctx context.Context, batch *engine.Batch, task ChannelMigrationTask) error {
	if err := validateChannelMigrationTask(task); err != nil {
		return err
	}
	pk := channelMigrationTaskPrimaryKey(task.ChannelID, task.ChannelType, task.TaskID)
	primaryKey, err := channelMigrationTable.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	existing, exists, err := s.getChannelMigrationTaskByKey(ctx, primaryKey, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil {
		return err
	}
	activeIndexKey := encodeChannelMigrationActiveIndexKey(s.hashSlot, task.ChannelID, task.ChannelType)
	if task.IsActive() {
		if err := s.ensureChannelMigrationActiveAvailable(ctx, activeIndexKey, task); err != nil {
			return err
		}
		if err := batch.Set(activeIndexKey, []byte(task.TaskID)); err != nil {
			return err
		}
	} else if exists && existing.IsActive() {
		if err := batch.Delete(activeIndexKey); err != nil {
			return err
		}
	}
	if exists {
		if err := channelMigrationTable.stageDeleteIndexEntries(batch, s.hashSlot, existing, pk); err != nil {
			return err
		}
	}
	value, err := channelMigrationTable.encodeValue(primaryKey, task)
	if err != nil {
		return err
	}
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	return channelMigrationTable.stagePutIndexEntries(batch, s.hashSlot, task, pk, value)
}

func (s *Shard) ensureChannelMigrationActiveAvailable(ctx context.Context, activeIndexKey []byte, task ChannelMigrationTask) error {
	value, ok, err := s.db.get(activeIndexKey)
	if err != nil || !ok {
		return err
	}
	existingTaskID := string(value)
	if existingTaskID == task.TaskID {
		return nil
	}
	existing, ok, err := s.GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, existingTaskID)
	if err != nil || !ok {
		return err
	}
	if existing.IsActive() {
		return dberrors.ErrAlreadyExists
	}
	return nil
}

func (s *Shard) getChannelMigrationTaskByKey(ctx context.Context, key []byte, channelID string, channelType int64, taskID string) (ChannelMigrationTask, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return ChannelMigrationTask{}, false, err
		}
	}
	value, ok, err := s.db.get(key)
	if err != nil || !ok {
		return ChannelMigrationTask{}, ok, err
	}
	task, err := channelMigrationTable.decodeValue(key, channelMigrationTaskPrimaryKey(channelID, channelType, taskID), value)
	if err != nil {
		return ChannelMigrationTask{}, false, err
	}
	return task, true, nil
}

func channelMigrationTaskPrimaryKey(channelID string, channelType int64, taskID string) KeyParts {
	return KeyParts{String(channelID), Int64Ordered(channelType), String(taskID)}
}

func channelMigrationTaskRowKey(hashSlot HashSlot, channelID string, channelType int64, taskID string) ([]byte, error) {
	return channelMigrationTable.primaryRowKey(hashSlot, channelMigrationTaskPrimaryKey(channelID, channelType, taskID))
}

func channelMigrationTerminalIndexSpec() IndexSpec[ChannelMigrationTask] {
	return IndexSpec[ChannelMigrationTask]{
		ID:      channelMigrationTerminalIndexID,
		Name:    "idx_channel_migration_terminal",
		Columns: []uint16{channelMigrationColumnCompletedAt, channelMigrationColumnChannelID, channelMigrationColumnChannelType, channelMigrationColumnTaskID},
		Layout:  KeyLayout{KeyInt64Ordered, KeyString, KeyInt64Ordered, KeyString},
		Key: func(task ChannelMigrationTask) (KeyParts, bool) {
			if !task.IsTerminal() {
				return nil, false
			}
			return channelMigrationTerminalIndexParts(task), true
		},
		PrimaryKeyFromIndexParts: channelMigrationPrimaryFromTerminalIndexParts,
		CorruptIndexKeyIsError:   true,
	}
}

func channelMigrationTerminalIndexParts(task ChannelMigrationTask) KeyParts {
	return KeyParts{Int64Ordered(task.CompletedAtMS), String(task.ChannelID), Int64Ordered(task.ChannelType), String(task.TaskID)}
}

func channelMigrationPrimaryFromTerminalIndexParts(parts KeyParts) (KeyParts, bool) {
	if len(parts) != 4 {
		return nil, false
	}
	return KeyParts{parts[1], parts[2], parts[3]}, true
}

func validateChannelMigrationTask(task ChannelMigrationTask) error {
	if err := validateChannelMigrationIdentity(task.ChannelID, task.TaskID); err != nil {
		return err
	}
	if task.Kind != ChannelMigrationKindLeaderTransfer && task.Kind != ChannelMigrationKindReplicaReplace {
		return dberrors.ErrInvalidArgument
	}
	if task.Status < ChannelMigrationStatusPending || task.Status > ChannelMigrationStatusAborted || task.Phase == 0 {
		return dberrors.ErrInvalidArgument
	}
	if task.Kind == ChannelMigrationKindLeaderTransfer && task.DesiredLeader != 0 && task.DesiredLeader != task.TargetNode {
		return dberrors.ErrInvalidArgument
	}
	if task.IsTerminal() && task.CompletedAtMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationIdentity(channelID string, taskID string) error {
	if err := validateKeyString(channelID); err != nil {
		return err
	}
	return validateKeyString(taskID)
}

func validateChannelMigrationTaskGuard(guard ChannelMigrationTaskGuard) error {
	if err := validateChannelMigrationIdentity(guard.ChannelID, guard.TaskID); err != nil {
		return err
	}
	if guard.ExpectedStatus == 0 || guard.ExpectedPhase == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateChannelMigrationRuntimeGuard(guard ChannelMigrationRuntimeGuard) error {
	return validateKeyString(guard.ChannelID)
}

func (guard ChannelMigrationTaskGuard) matches(task ChannelMigrationTask) bool {
	return task.ChannelID == guard.ChannelID &&
		task.ChannelType == guard.ChannelType &&
		task.TaskID == guard.TaskID &&
		task.Status == guard.ExpectedStatus &&
		task.Phase == guard.ExpectedPhase &&
		task.OwnerNodeID == guard.ExpectedOwnerNodeID &&
		task.OwnerLeaseUntilMS == guard.ExpectedOwnerLeaseUntilMS &&
		task.UpdatedAtMS == guard.ExpectedUpdatedAtMS
}

func (guard ChannelMigrationRuntimeGuard) matches(meta ChannelRuntimeMeta) bool {
	return meta.ChannelID == guard.ChannelID &&
		meta.ChannelType == guard.ChannelType &&
		meta.ChannelEpoch == guard.ExpectedChannelEpoch &&
		meta.LeaderEpoch == guard.ExpectedLeaderEpoch &&
		meta.Leader == guard.ExpectedLeader &&
		meta.WriteFenceToken == guard.ExpectedFenceToken &&
		meta.WriteFenceVersion == guard.ExpectedFenceVersion &&
		(guard.ExpectedRouteGeneration == 0 || meta.RouteGeneration == guard.ExpectedRouteGeneration)
}

func encodeChannelMigrationTaskValue(task ChannelMigrationTask) []byte {
	value, err := json.Marshal(task)
	if err != nil {
		panic(err)
	}
	return value
}

func decodeChannelMigrationTaskValue(channelID string, channelType int64, taskID string, value []byte) (ChannelMigrationTask, error) {
	var task ChannelMigrationTask
	if err := json.Unmarshal(value, &task); err != nil {
		return ChannelMigrationTask{}, dberrors.ErrCorruptValue
	}
	task.ChannelID = channelID
	task.ChannelType = channelType
	task.TaskID = taskID
	return task, nil
}

func decodeChannelMigrationTaskRowKey(prefix []byte, key []byte) (string, int64, string, uint16, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return "", 0, "", 0, false
	}
	channelID, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil {
		return "", 0, "", 0, false
	}
	channelTypeValue, rest, err := readKeyInt64Ordered(rest)
	if err != nil {
		return "", 0, "", 0, false
	}
	taskID, rest, err := keycodec.ReadString(rest)
	if err != nil || len(rest) != 2 {
		return "", 0, "", 0, false
	}
	return channelID, channelTypeValue, taskID, binary.BigEndian.Uint16(rest), true
}

func decodeChannelMigrationTerminalIndexKey(prefix []byte, key []byte) (int64, string, int64, string, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return 0, "", 0, "", false
	}
	completedAt, rest, err := readKeyInt64Ordered(key[len(prefix):])
	if err != nil {
		return 0, "", 0, "", false
	}
	channelID, rest, err := keycodec.ReadString(rest)
	if err != nil {
		return 0, "", 0, "", false
	}
	channelTypeValue, rest, err := readKeyInt64Ordered(rest)
	if err != nil {
		return 0, "", 0, "", false
	}
	taskID, rest, err := keycodec.ReadString(rest)
	if err != nil || len(rest) != 0 {
		return 0, "", 0, "", false
	}
	return completedAt, channelID, channelTypeValue, taskID, true
}
