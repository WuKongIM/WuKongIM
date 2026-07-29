package backup

import "context"

const (
	// HashSlotCount is the fixed logical backup partition count.
	HashSlotCount = 256
	// MaxTaskHistory bounds durable terminal task records.
	MaxTaskHistory = 100
)

type coordinatorFenceContextKey struct{}

// CoordinatorFence identifies the exact Controller leader turn authorized to
// mutate durable background-job state.
type CoordinatorFence struct {
	NodeID uint64
	Term   uint64
}

// WithCoordinatorFence binds background work to one Controller leader turn.
func WithCoordinatorFence(
	ctx context.Context,
	nodeID uint64,
	term uint64,
) context.Context {
	return context.WithValue(
		ctx, coordinatorFenceContextKey{},
		CoordinatorFence{NodeID: nodeID, Term: term},
	)
}

// CoordinatorFenceFromContext returns the optional background coordinator
// fence. Foreground Manager operations intentionally have no such fence.
func CoordinatorFenceFromContext(ctx context.Context) (CoordinatorFence, bool) {
	if ctx == nil {
		return CoordinatorFence{}, false
	}
	fence, ok := ctx.Value(coordinatorFenceContextKey{}).(CoordinatorFence)
	return fence, ok && fence.NodeID != 0 && fence.Term != 0
}

// StoreKind identifies the single configured archive repository.
type StoreKind string

const (
	// StoreKindFile uses the fixed node data-dir repository mount.
	StoreKindFile StoreKind = "file"
	// StoreKindS3 uses one S3-compatible bucket prefix.
	StoreKindS3 StoreKind = "s3"
)

// StoreConfig is the durable repository configuration. CredentialCiphertext is
// write-only at access boundaries.
type StoreConfig struct {
	Kind                 StoreKind `json:"kind"`
	Endpoint             string    `json:"endpoint,omitempty"`
	Region               string    `json:"region,omitempty"`
	Bucket               string    `json:"bucket,omitempty"`
	Prefix               string    `json:"prefix,omitempty"`
	PathStyle            bool      `json:"path_style,omitempty"`
	CredentialCiphertext []byte    `json:"credential_ciphertext,omitempty"`
	CredentialRevision   uint64    `json:"credential_revision,omitempty"`
}

// Plan is the only cluster-scoped scheduled full-backup policy.
type Plan struct {
	Revision                 uint64      `json:"revision"`
	Enabled                  bool        `json:"enabled"`
	Store                    StoreConfig `json:"store"`
	Cron                     string      `json:"cron"`
	TimeZone                 string      `json:"time_zone"`
	RetentionCount           int         `json:"retention_count"`
	RateBytesPerSec          uint64      `json:"rate_bytes_per_sec"`
	WorkersPerNode           int         `json:"workers_per_node"`
	MaxDurationMillis        int64       `json:"max_duration_ms"`
	ScheduleCursorUnixMillis int64       `json:"schedule_cursor_unix_ms"`
	CreatedUnixMillis        int64       `json:"created_unix_ms"`
	UpdatedUnixMillis        int64       `json:"updated_unix_ms"`
}

// Trigger identifies why a backup job was admitted.
type Trigger string

const (
	TriggerInitial   Trigger = "initial"
	TriggerScheduled Trigger = "scheduled"
	TriggerManual    Trigger = "manual"
)

// JobStatus is a bounded operator-facing backup lifecycle phase.
type JobStatus string

const (
	JobStatusPreparing  JobStatus = "preparing"
	JobStatusExporting  JobStatus = "exporting"
	JobStatusVerifying  JobStatus = "verifying"
	JobStatusPublishing JobStatus = "publishing"
	JobStatusCleaning   JobStatus = "cleaning"
	JobStatusSucceeded  JobStatus = "succeeded"
	JobStatusFailed     JobStatus = "failed"
	JobStatusCanceled   JobStatus = "canceled"
	JobStatusSkipped    JobStatus = "skipped"
)

// SlotStatus is one fixed per-Hash-Slot job phase.
type SlotStatus string

const (
	SlotStatusPending  SlotStatus = "pending"
	SlotStatusRunning  SlotStatus = "running"
	SlotStatusComplete SlotStatus = "complete"
	SlotStatusFailed   SlotStatus = "failed"
)

// SlotProgress is the bounded durable progress for one logical Hash Slot.
type SlotProgress struct {
	HashSlot          uint16     `json:"hash_slot"`
	Status            SlotStatus `json:"status"`
	Attempt           uint32     `json:"attempt"`
	OwnerNodeID       uint64     `json:"owner_node_id,omitempty"`
	OwnerTerm         uint64     `json:"owner_term,omitempty"`
	ManifestKey       string     `json:"manifest_key,omitempty"`
	ManifestSHA256    string     `json:"manifest_sha256,omitempty"`
	LogicalBytes      uint64     `json:"logical_bytes,omitempty"`
	StoredBytes       uint64     `json:"stored_bytes,omitempty"`
	Records           uint64     `json:"records,omitempty"`
	MaxMessageID      uint64     `json:"max_message_id,omitempty"`
	UpdatedUnixMillis int64      `json:"updated_unix_ms,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
}

// BackupJob is the only active full-backup job.
type BackupJob struct {
	ID                    string         `json:"id"`
	Trigger               Trigger        `json:"trigger"`
	Status                JobStatus      `json:"status"`
	PlanRevision          uint64         `json:"plan_revision"`
	ScheduledAtUnixMillis int64          `json:"scheduled_at_unix_ms,omitempty"`
	StartedAtUnixMillis   int64          `json:"started_at_unix_ms"`
	DeadlineUnixMillis    int64          `json:"deadline_unix_ms"`
	UpdatedUnixMillis     int64          `json:"updated_unix_ms"`
	CancelRequested       bool           `json:"cancel_requested,omitempty"`
	Slots                 []SlotProgress `json:"slots"`
	LogicalBytes          uint64         `json:"logical_bytes,omitempty"`
	StoredBytes           uint64         `json:"stored_bytes,omitempty"`
	Records               uint64         `json:"records,omitempty"`
	ErrorCode             string         `json:"error_code,omitempty"`
}

// RestoreStatus is the operator-facing maintenance restore lifecycle.
type RestoreStatus string

const (
	RestoreStatusPreparing   RestoreStatus = "preparing"
	RestoreStatusValidated   RestoreStatus = "validated"
	RestoreStatusMaintenance RestoreStatus = "maintenance"
	RestoreStatusStaging     RestoreStatus = "staging"
	RestoreStatusVerifying   RestoreStatus = "verifying"
	RestoreStatusSwitching   RestoreStatus = "switching"
	RestoreStatusFinalizing  RestoreStatus = "finalizing"
	RestoreStatusRollingBack RestoreStatus = "rolling_back"
	RestoreStatusSucceeded   RestoreStatus = "succeeded"
	RestoreStatusFailed      RestoreStatus = "failed"
	RestoreStatusCanceled    RestoreStatus = "canceled"
)

// RestoreSlotStatus is one Hash Slot's restore progress across all replicas.
type RestoreSlotStatus string

const (
	RestoreSlotStatusPending  RestoreSlotStatus = "pending"
	RestoreSlotStatusStaging  RestoreSlotStatus = "staging"
	RestoreSlotStatusStaged   RestoreSlotStatus = "staged"
	RestoreSlotStatusVerified RestoreSlotStatus = "verified"
	RestoreSlotStatusFailed   RestoreSlotStatus = "failed"
)

// RestoreSlotProgress stores bounded all-replica staging evidence.
type RestoreSlotProgress struct {
	HashSlot          uint16            `json:"hash_slot"`
	Status            RestoreSlotStatus `json:"status"`
	Attempt           uint32            `json:"attempt"`
	ReplicaNodeIDs    []uint64          `json:"replica_node_ids,omitempty"`
	LogicalBytes      uint64            `json:"logical_bytes,omitempty"`
	UpdatedUnixMillis int64             `json:"updated_unix_ms,omitempty"`
	ErrorCode         string            `json:"error_code,omitempty"`
}

// RestoreJob is the only active maintenance-mode restore job.
type RestoreJob struct {
	ID                 string                `json:"id"`
	BackupID           string                `json:"backup_id"`
	Initiator          string                `json:"initiator"`
	Status             RestoreStatus         `json:"status"`
	StartedUnixMillis  int64                 `json:"started_at_unix_ms"`
	DeadlineUnixMillis int64                 `json:"deadline_unix_ms"`
	UpdatedUnixMillis  int64                 `json:"updated_unix_ms"`
	CancelRequested    bool                  `json:"cancel_requested,omitempty"`
	MaintenanceEntered bool                  `json:"maintenance_entered,omitempty"`
	PreviousActivation string                `json:"previous_activation,omitempty"`
	TargetActivation   string                `json:"target_activation"`
	Slots              []RestoreSlotProgress `json:"slots"`
	LogicalBytes       uint64                `json:"logical_bytes,omitempty"`
	MaxMessageID       uint64                `json:"max_message_id,omitempty"`
	ErrorCode          string                `json:"error_code,omitempty"`
}

// TaskRecord is one bounded terminal backup, restore, verification, or
// retention observation.
type TaskRecord struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	Initiator           string  `json:"initiator,omitempty"`
	Trigger             Trigger `json:"trigger,omitempty"`
	Status              string  `json:"status"`
	StartedUnixMillis   int64   `json:"started_at_unix_ms"`
	CompletedUnixMillis int64   `json:"completed_at_unix_ms"`
	ScheduledUnixMillis int64   `json:"scheduled_at_unix_ms,omitempty"`
	ErrorCode           string  `json:"error_code,omitempty"`
}

// ArchiveOperation is one durable cross-node repository mutation/read lease.
// It serializes hold, verify, delete, retention, and restore admission so
// repository marker checks cannot race destructive operations.
type ArchiveOperation struct {
	Token             string `json:"token"`
	Kind              string `json:"kind"`
	ArchiveID         string `json:"archive_id,omitempty"`
	StartedUnixMillis int64  `json:"started_unix_ms"`
	ExpiresUnixMillis int64  `json:"expires_unix_ms"`
}

// SystemState is the complete bounded backup state stored in Controller Raft.
type SystemState struct {
	Revision               uint64            `json:"revision"`
	ManagerSessionEpoch    uint64            `json:"manager_session_epoch"`
	Plan                   *Plan             `json:"plan,omitempty"`
	ActiveBackup           *BackupJob        `json:"active_backup,omitempty"`
	ActiveRestore          *RestoreJob       `json:"active_restore,omitempty"`
	ActiveArchiveOperation *ArchiveOperation `json:"active_archive_operation,omitempty"`
	History                []TaskRecord      `json:"history,omitempty"`
}

// Clone returns a deep copy safe for mutation outside Controller state.
func (s SystemState) Clone() SystemState {
	clone := s
	if s.Plan != nil {
		plan := *s.Plan
		plan.Store.CredentialCiphertext = append([]byte(nil), s.Plan.Store.CredentialCiphertext...)
		clone.Plan = &plan
	}
	if s.ActiveBackup != nil {
		job := *s.ActiveBackup
		job.Slots = append([]SlotProgress(nil), s.ActiveBackup.Slots...)
		clone.ActiveBackup = &job
	}
	if s.ActiveRestore != nil {
		restore := *s.ActiveRestore
		restore.Slots = make([]RestoreSlotProgress, len(s.ActiveRestore.Slots))
		for index, slot := range s.ActiveRestore.Slots {
			restore.Slots[index] = slot
			restore.Slots[index].ReplicaNodeIDs = append(
				[]uint64(nil), slot.ReplicaNodeIDs...,
			)
		}
		clone.ActiveRestore = &restore
	}
	if s.ActiveArchiveOperation != nil {
		operation := *s.ActiveArchiveOperation
		clone.ActiveArchiveOperation = &operation
	}
	clone.History = append([]TaskRecord(nil), s.History...)
	return clone
}
