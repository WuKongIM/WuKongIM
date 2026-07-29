package state

import (
	"fmt"
	"strings"
)

const (
	// BackupHashSlotCount is the fixed logical partition count of every full backup.
	BackupHashSlotCount = 256
	// MaxBackupTaskHistory bounds terminal backup and restore records in Controller state.
	MaxBackupTaskHistory = 100
)

// BackupStoreKind identifies the single archive repository selected by the backup plan.
type BackupStoreKind string

const (
	// BackupStoreKindFile uses the fixed repository path below the node data directory.
	BackupStoreKindFile BackupStoreKind = "file"
	// BackupStoreKindS3 uses one S3-compatible bucket prefix.
	BackupStoreKindS3 BackupStoreKind = "s3"
)

// BackupStoreConfig stores repository settings. Credentials remain encrypted in Controller state.
type BackupStoreConfig struct {
	Kind                 BackupStoreKind `json:"kind"`
	Endpoint             string          `json:"endpoint,omitempty"`
	Region               string          `json:"region,omitempty"`
	Bucket               string          `json:"bucket,omitempty"`
	Prefix               string          `json:"prefix,omitempty"`
	PathStyle            bool            `json:"path_style,omitempty"`
	CredentialCiphertext []byte          `json:"credential_ciphertext,omitempty"`
	CredentialRevision   uint64          `json:"credential_revision,omitempty"`
}

// BackupPlan is the only cluster-scoped scheduled full-backup policy.
type BackupPlan struct {
	Revision                 uint64            `json:"revision"`
	Enabled                  bool              `json:"enabled"`
	Store                    BackupStoreConfig `json:"store"`
	Cron                     string            `json:"cron"`
	TimeZone                 string            `json:"time_zone"`
	RetentionCount           int               `json:"retention_count"`
	RateBytesPerSec          uint64            `json:"rate_bytes_per_sec"`
	WorkersPerNode           int               `json:"workers_per_node"`
	MaxDurationMillis        int64             `json:"max_duration_ms"`
	ScheduleCursorUnixMillis int64             `json:"schedule_cursor_unix_ms"`
	CreatedUnixMillis        int64             `json:"created_unix_ms"`
	UpdatedUnixMillis        int64             `json:"updated_unix_ms"`
}

// BackupTrigger identifies why a full-backup job was admitted.
type BackupTrigger string

const (
	BackupTriggerInitial   BackupTrigger = "initial"
	BackupTriggerScheduled BackupTrigger = "scheduled"
	BackupTriggerManual    BackupTrigger = "manual"
)

// BackupJobStatus is a bounded operator-facing full-backup lifecycle phase.
type BackupJobStatus string

const (
	BackupJobStatusPreparing  BackupJobStatus = "preparing"
	BackupJobStatusExporting  BackupJobStatus = "exporting"
	BackupJobStatusVerifying  BackupJobStatus = "verifying"
	BackupJobStatusPublishing BackupJobStatus = "publishing"
	BackupJobStatusCleaning   BackupJobStatus = "cleaning"
	BackupJobStatusSucceeded  BackupJobStatus = "succeeded"
	BackupJobStatusFailed     BackupJobStatus = "failed"
	BackupJobStatusCanceled   BackupJobStatus = "canceled"
	BackupJobStatusSkipped    BackupJobStatus = "skipped"
)

// BackupSlotStatus describes one Hash Slot's bounded export progress.
type BackupSlotStatus string

const (
	BackupSlotStatusPending  BackupSlotStatus = "pending"
	BackupSlotStatusRunning  BackupSlotStatus = "running"
	BackupSlotStatusComplete BackupSlotStatus = "complete"
	BackupSlotStatusFailed   BackupSlotStatus = "failed"
)

// BackupSlotProgress stores durable progress and the authority fence for one Hash Slot.
type BackupSlotProgress struct {
	HashSlot          uint16           `json:"hash_slot"`
	Status            BackupSlotStatus `json:"status"`
	Attempt           uint32           `json:"attempt"`
	OwnerNodeID       uint64           `json:"owner_node_id,omitempty"`
	OwnerTerm         uint64           `json:"owner_term,omitempty"`
	ManifestKey       string           `json:"manifest_key,omitempty"`
	ManifestSHA256    string           `json:"manifest_sha256,omitempty"`
	LogicalBytes      uint64           `json:"logical_bytes,omitempty"`
	StoredBytes       uint64           `json:"stored_bytes,omitempty"`
	Records           uint64           `json:"records,omitempty"`
	MaxMessageID      uint64           `json:"max_message_id,omitempty"`
	UpdatedUnixMillis int64            `json:"updated_unix_ms,omitempty"`
	ErrorCode         string           `json:"error_code,omitempty"`
}

// ScheduledBackupJob is the only active full-backup job in the cluster.
type ScheduledBackupJob struct {
	ID                    string               `json:"id"`
	Trigger               BackupTrigger        `json:"trigger"`
	Status                BackupJobStatus      `json:"status"`
	PlanRevision          uint64               `json:"plan_revision"`
	ScheduledAtUnixMillis int64                `json:"scheduled_at_unix_ms,omitempty"`
	StartedAtUnixMillis   int64                `json:"started_at_unix_ms"`
	DeadlineUnixMillis    int64                `json:"deadline_unix_ms"`
	UpdatedUnixMillis     int64                `json:"updated_unix_ms"`
	CancelRequested       bool                 `json:"cancel_requested,omitempty"`
	Slots                 []BackupSlotProgress `json:"slots"`
	LogicalBytes          uint64               `json:"logical_bytes,omitempty"`
	StoredBytes           uint64               `json:"stored_bytes,omitempty"`
	Records               uint64               `json:"records,omitempty"`
	ErrorCode             string               `json:"error_code,omitempty"`
}

// ScheduledRestoreJob is the only active maintenance-mode restore job.
type ScheduledRestoreJob struct {
	ID                 string                `json:"id"`
	BackupID           string                `json:"backup_id"`
	Initiator          string                `json:"initiator"`
	Status             string                `json:"status"`
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

// RestoreSlotProgress stores one Hash Slot's all-replica staging evidence.
type RestoreSlotProgress struct {
	HashSlot          uint16   `json:"hash_slot"`
	Status            string   `json:"status"`
	Attempt           uint32   `json:"attempt"`
	ReplicaNodeIDs    []uint64 `json:"replica_node_ids,omitempty"`
	LogicalBytes      uint64   `json:"logical_bytes,omitempty"`
	UpdatedUnixMillis int64    `json:"updated_unix_ms,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
}

// BackupTaskRecord is one bounded terminal backup, restore, verification, or
// retention observation.
type BackupTaskRecord struct {
	ID                  string        `json:"id"`
	Kind                string        `json:"kind"`
	Initiator           string        `json:"initiator,omitempty"`
	Trigger             BackupTrigger `json:"trigger,omitempty"`
	Status              string        `json:"status"`
	StartedUnixMillis   int64         `json:"started_at_unix_ms"`
	CompletedUnixMillis int64         `json:"completed_at_unix_ms"`
	ScheduledUnixMillis int64         `json:"scheduled_at_unix_ms,omitempty"`
	ErrorCode           string        `json:"error_code,omitempty"`
}

// BackupArchiveOperation is one bounded repository-operation lease.
type BackupArchiveOperation struct {
	Token             string `json:"token"`
	Kind              string `json:"kind"`
	ArchiveID         string `json:"archive_id,omitempty"`
	CoordinatorNodeID uint64 `json:"coordinator_node_id,omitempty"`
	CoordinatorTerm   uint64 `json:"coordinator_term,omitempty"`
	StartedUnixMillis int64  `json:"started_unix_ms"`
	ExpiresUnixMillis int64  `json:"expires_unix_ms"`
}

// ScheduledBackupState is the complete bounded backup subsystem state replicated by Controller.
type ScheduledBackupState struct {
	Revision               uint64                  `json:"revision"`
	ManagerSessionEpoch    uint64                  `json:"manager_session_epoch"`
	Plan                   *BackupPlan             `json:"plan,omitempty"`
	ActiveBackup           *ScheduledBackupJob     `json:"active_backup,omitempty"`
	ActiveRestore          *ScheduledRestoreJob    `json:"active_restore,omitempty"`
	ActiveArchiveOperation *BackupArchiveOperation `json:"active_archive_operation,omitempty"`
	History                []BackupTaskRecord      `json:"history,omitempty"`
}

// Clone returns a deep copy safe for mutation outside Controller state.
func (s ScheduledBackupState) Clone() ScheduledBackupState {
	out := s
	if s.Plan != nil {
		plan := *s.Plan
		plan.Store.CredentialCiphertext = cloneSlice(s.Plan.Store.CredentialCiphertext)
		out.Plan = &plan
	}
	if s.ActiveBackup != nil {
		job := *s.ActiveBackup
		job.Slots = cloneSlice(s.ActiveBackup.Slots)
		out.ActiveBackup = &job
	}
	if s.ActiveRestore != nil {
		restore := *s.ActiveRestore
		restore.Slots = cloneSlice(s.ActiveRestore.Slots)
		for index := range restore.Slots {
			restore.Slots[index].ReplicaNodeIDs = cloneSlice(
				s.ActiveRestore.Slots[index].ReplicaNodeIDs,
			)
		}
		out.ActiveRestore = &restore
	}
	if s.ActiveArchiveOperation != nil {
		operation := *s.ActiveArchiveOperation
		out.ActiveArchiveOperation = &operation
	}
	out.History = cloneSlice(s.History)
	return out
}

func validateScheduledBackup(value *ScheduledBackupState) error {
	if value == nil {
		return nil
	}
	if value.Revision == 0 {
		return invalid("scheduled_backup.revision is required")
	}
	if len(value.History) > MaxBackupTaskHistory {
		return invalid("scheduled_backup.history exceeds limit")
	}
	if value.ActiveBackup != nil && value.ActiveRestore != nil {
		return invalid("backup and restore jobs cannot be active together")
	}
	if value.ActiveArchiveOperation != nil {
		operation := value.ActiveArchiveOperation
		switch operation.Kind {
		case "verify", "hold", "delete", "retention", "restore":
		default:
			return invalid("scheduled backup archive operation kind is invalid")
		}
		if operation.Token == "" || len(operation.Token) > 128 ||
			len(operation.ArchiveID) > 128 ||
			(operation.CoordinatorNodeID == 0) !=
				(operation.CoordinatorTerm == 0) ||
			operation.StartedUnixMillis <= 0 ||
			operation.ExpiresUnixMillis <= operation.StartedUnixMillis {
			return invalid("scheduled backup archive operation is invalid")
		}
	}
	if value.Plan != nil {
		if err := validateBackupPlan(*value.Plan); err != nil {
			return err
		}
	}
	if value.ActiveBackup != nil {
		if value.Plan == nil {
			return invalid("active backup requires a plan")
		}
		if err := validateScheduledBackupJob(*value.ActiveBackup); err != nil {
			return err
		}
	}
	if value.ActiveRestore != nil {
		if value.Plan == nil {
			return invalid("active restore requires a plan")
		}
		if err := validateScheduledRestoreJob(*value.ActiveRestore); err != nil {
			return invalid("active restore is incomplete")
		}
	}
	for i, record := range value.History {
		if record.ID == "" ||
			(record.Kind != "backup" && record.Kind != "restore" &&
				record.Kind != "verification" && record.Kind != "retention") ||
			len(record.Initiator) > 128 ||
			record.Kind == "restore" && strings.TrimSpace(record.Initiator) == "" ||
			record.StartedUnixMillis <= 0 || record.CompletedUnixMillis < record.StartedUnixMillis {
			return invalid(fmt.Sprintf("scheduled_backup.history[%d] is invalid", i))
		}
	}
	return nil
}

func validateScheduledRestoreJob(job ScheduledRestoreJob) error {
	if job.ID == "" || job.BackupID == "" || job.Status == "" ||
		strings.TrimSpace(job.Initiator) == "" || len(job.Initiator) > 128 ||
		job.StartedUnixMillis <= 0 ||
		job.DeadlineUnixMillis <= job.StartedUnixMillis ||
		job.UpdatedUnixMillis < job.StartedUnixMillis ||
		job.TargetActivation == "" ||
		len(job.Slots) != BackupHashSlotCount ||
		len(job.ErrorCode) > 128 {
		return invalid("active restore is invalid")
	}
	switch job.Status {
	case "preparing", "validated", "maintenance", "staging", "verifying",
		"switching", "finalizing", "rolling_back":
	default:
		return invalid("active restore status must be non-terminal")
	}
	for hashSlot, slot := range job.Slots {
		if slot.HashSlot != uint16(hashSlot) || len(slot.ErrorCode) > 128 {
			return invalid("active restore slots must cover hash slots 0 through 255")
		}
		switch slot.Status {
		case "pending", "staging", "staged", "verified", "failed":
		default:
			return invalid("active restore slot status is invalid")
		}
		if slot.Status == "staging" && slot.Attempt == 0 {
			return invalid("staging restore slot requires an attempt")
		}
		seen := make(map[uint64]struct{}, len(slot.ReplicaNodeIDs))
		for _, nodeID := range slot.ReplicaNodeIDs {
			if nodeID == 0 {
				return invalid("restore replica node ID is invalid")
			}
			if _, exists := seen[nodeID]; exists {
				return invalid("restore replica node IDs must be unique")
			}
			seen[nodeID] = struct{}{}
		}
	}
	return nil
}

func validateBackupPlan(plan BackupPlan) error {
	if plan.Revision == 0 || plan.Cron == "" || plan.TimeZone == "" ||
		plan.RetentionCount < 1 || plan.RetentionCount > 1000 ||
		plan.RateBytesPerSec == 0 || plan.WorkersPerNode < 1 || plan.WorkersPerNode > 4 ||
		plan.MaxDurationMillis < 60*60*1000 || plan.MaxDurationMillis > 48*60*60*1000 ||
		plan.CreatedUnixMillis <= 0 || plan.UpdatedUnixMillis < plan.CreatedUnixMillis {
		return invalid("scheduled backup plan is invalid")
	}
	if plan.ScheduleCursorUnixMillis < plan.CreatedUnixMillis {
		return invalid("scheduled backup plan cursor is invalid")
	}
	switch plan.Store.Kind {
	case BackupStoreKindFile:
		if plan.Store.Endpoint != "" || plan.Store.Bucket != "" || plan.Store.Prefix != "" {
			return invalid("file backup store must use the fixed repository path")
		}
	case BackupStoreKindS3:
		if strings.TrimSpace(plan.Store.Endpoint) == "" || strings.TrimSpace(plan.Store.Bucket) == "" {
			return invalid("S3 backup store requires endpoint and bucket")
		}
	default:
		return invalid("backup store kind is invalid")
	}
	return nil
}

func validateScheduledBackupJob(job ScheduledBackupJob) error {
	if job.ID == "" || job.PlanRevision == 0 || job.StartedAtUnixMillis <= 0 ||
		job.DeadlineUnixMillis <= job.StartedAtUnixMillis ||
		job.UpdatedUnixMillis < job.StartedAtUnixMillis ||
		len(job.Slots) != BackupHashSlotCount {
		return invalid("active backup job is invalid")
	}
	switch job.Trigger {
	case BackupTriggerInitial, BackupTriggerScheduled, BackupTriggerManual:
	default:
		return invalid("active backup trigger is invalid")
	}
	switch job.Status {
	case BackupJobStatusPreparing, BackupJobStatusExporting, BackupJobStatusVerifying,
		BackupJobStatusPublishing, BackupJobStatusCleaning:
	default:
		return invalid("active backup status must be non-terminal")
	}
	for hashSlot, slot := range job.Slots {
		if slot.HashSlot != uint16(hashSlot) {
			return invalid("active backup slots must cover hash slots 0 through 255")
		}
		switch slot.Status {
		case BackupSlotStatusPending, BackupSlotStatusRunning, BackupSlotStatusComplete, BackupSlotStatusFailed:
		default:
			return invalid("active backup slot status is invalid")
		}
		if slot.Status == BackupSlotStatusRunning &&
			(slot.Attempt == 0 || slot.OwnerNodeID == 0 || slot.OwnerTerm == 0) {
			return invalid("running backup slot requires an authority fence")
		}
		if slot.Status == BackupSlotStatusComplete &&
			(slot.Attempt == 0 || slot.OwnerNodeID == 0 || slot.OwnerTerm == 0 ||
				len(slot.ManifestKey) == 0 || len(slot.ManifestKey) > 512 ||
				len(slot.ManifestSHA256) != 64) {
			return invalid("complete backup slot requires an artifact fence")
		}
	}
	return nil
}
