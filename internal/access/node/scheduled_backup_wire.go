package node

import (
	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// Version-2 request DTOs deliberately enumerate wire fields. Durable StoreConfig
// and its encrypted credentials must never become part of these payloads.

type scheduledBackupPlan struct {
	Revision                 uint64                                 `json:"revision"`
	Enabled                  bool                                   `json:"enabled"`
	Store                    backupcontract.StoreReference          `json:"store"`
	RepositoryVerification   *backupcontract.RepositoryVerification `json:"repository_verification,omitempty"`
	Cron                     string                                 `json:"cron"`
	TimeZone                 string                                 `json:"time_zone"`
	RetentionCount           int                                    `json:"retention_count"`
	RateBytesPerSec          uint64                                 `json:"rate_bytes_per_sec"`
	WorkersPerNode           int                                    `json:"workers_per_node"`
	MaxDurationMillis        int64                                  `json:"max_duration_ms"`
	ScheduleCursorUnixMillis int64                                  `json:"schedule_cursor_unix_ms"`
	CreatedUnixMillis        int64                                  `json:"created_unix_ms"`
	UpdatedUnixMillis        int64                                  `json:"updated_unix_ms"`
}

func newScheduledBackupPlan(command backupcontract.Plan) scheduledBackupPlan {
	return scheduledBackupPlan{
		Revision:                 command.Revision,
		Enabled:                  command.Enabled,
		Store:                    command.Store.Reference(),
		RepositoryVerification:   command.RepositoryVerification,
		Cron:                     command.Cron,
		TimeZone:                 command.TimeZone,
		RetentionCount:           command.RetentionCount,
		RateBytesPerSec:          command.RateBytesPerSec,
		WorkersPerNode:           command.WorkersPerNode,
		MaxDurationMillis:        command.MaxDurationMillis,
		ScheduleCursorUnixMillis: command.ScheduleCursorUnixMillis,
		CreatedUnixMillis:        command.CreatedUnixMillis,
		UpdatedUnixMillis:        command.UpdatedUnixMillis,
	}
}

func (request scheduledBackupPlan) command(store backupcontract.StoreConfig) backupcontract.Plan {
	return backupcontract.Plan{
		Revision:                 request.Revision,
		Enabled:                  request.Enabled,
		Store:                    store,
		RepositoryVerification:   request.RepositoryVerification,
		Cron:                     request.Cron,
		TimeZone:                 request.TimeZone,
		RetentionCount:           request.RetentionCount,
		RateBytesPerSec:          request.RateBytesPerSec,
		WorkersPerNode:           request.WorkersPerNode,
		MaxDurationMillis:        request.MaxDurationMillis,
		ScheduleCursorUnixMillis: request.ScheduleCursorUnixMillis,
		CreatedUnixMillis:        request.CreatedUnixMillis,
		UpdatedUnixMillis:        request.UpdatedUnixMillis,
	}
}

type scheduledBackupSlotRequest struct {
	Plan              scheduledBackupPlan `json:"plan"`
	BackupID          string              `json:"backup_id"`
	HashSlot          uint16              `json:"hash_slot"`
	Attempt           uint32              `json:"attempt"`
	OwnerNodeID       uint64              `json:"owner_node_id"`
	OwnerTerm         uint64              `json:"owner_term"`
	CoordinatorNodeID uint64              `json:"coordinator_node_id"`
	CoordinatorTerm   uint64              `json:"coordinator_term"`
}

func newScheduledBackupSlotRequest(command backupcontract.SlotExportCommand) scheduledBackupSlotRequest {
	return scheduledBackupSlotRequest{
		Plan:              newScheduledBackupPlan(command.Plan),
		BackupID:          command.BackupID,
		HashSlot:          command.HashSlot,
		Attempt:           command.Attempt,
		OwnerNodeID:       command.OwnerNodeID,
		OwnerTerm:         command.OwnerTerm,
		CoordinatorNodeID: command.CoordinatorNodeID,
		CoordinatorTerm:   command.CoordinatorTerm,
	}
}

func (request scheduledBackupSlotRequest) command(store backupcontract.StoreConfig) backupcontract.SlotExportCommand {
	return backupcontract.SlotExportCommand{
		Plan:              request.Plan.command(store),
		BackupID:          request.BackupID,
		HashSlot:          request.HashSlot,
		Attempt:           request.Attempt,
		OwnerNodeID:       request.OwnerNodeID,
		OwnerTerm:         request.OwnerTerm,
		CoordinatorNodeID: request.CoordinatorNodeID,
		CoordinatorTerm:   request.CoordinatorTerm,
	}
}

type scheduledBackupMessageRequest struct {
	Store             backupcontract.StoreReference `json:"store"`
	BackupID          string                        `json:"backup_id"`
	HashSlot          uint16                        `json:"hash_slot"`
	ArtifactPrefix    string                        `json:"artifact_prefix"`
	Shard             backupcontract.MessageShard   `json:"shard"`
	FirstSequence     uint32                        `json:"first_sequence"`
	StreamNumber      uint32                        `json:"stream_number"`
	RateBytesPerSec   uint64                        `json:"rate_bytes_per_sec"`
	CoordinatorNodeID uint64                        `json:"coordinator_node_id"`
	CoordinatorTerm   uint64                        `json:"coordinator_term"`
}

func newScheduledBackupMessageRequest(command backupcontract.MessageExportCommand) scheduledBackupMessageRequest {
	return scheduledBackupMessageRequest{
		Store:             command.Store.Reference(),
		BackupID:          command.BackupID,
		HashSlot:          command.HashSlot,
		ArtifactPrefix:    command.ArtifactPrefix,
		Shard:             command.Shard,
		FirstSequence:     command.FirstSequence,
		StreamNumber:      command.StreamNumber,
		RateBytesPerSec:   command.RateBytesPerSec,
		CoordinatorNodeID: command.CoordinatorNodeID,
		CoordinatorTerm:   command.CoordinatorTerm,
	}
}

func (request scheduledBackupMessageRequest) command(store backupcontract.StoreConfig) backupcontract.MessageExportCommand {
	return backupcontract.MessageExportCommand{
		Store:             store,
		BackupID:          request.BackupID,
		HashSlot:          request.HashSlot,
		ArtifactPrefix:    request.ArtifactPrefix,
		Shard:             request.Shard,
		FirstSequence:     request.FirstSequence,
		StreamNumber:      request.StreamNumber,
		RateBytesPerSec:   request.RateBytesPerSec,
		CoordinatorNodeID: request.CoordinatorNodeID,
		CoordinatorTerm:   request.CoordinatorTerm,
	}
}

type scheduledBackupProbeRequest struct {
	Store          backupcontract.StoreReference `json:"store"`
	MarkerKey      string                        `json:"marker_key"`
	MarkerSHA256   string                        `json:"marker_sha256"`
	ReceiptKey     string                        `json:"receipt_key"`
	ReceiptContent string                        `json:"receipt_content"`
}

func newScheduledBackupProbeRequest(command backupcontract.RepositoryProbeCommand) scheduledBackupProbeRequest {
	return scheduledBackupProbeRequest{
		Store:          command.Store.Reference(),
		MarkerKey:      command.MarkerKey,
		MarkerSHA256:   command.MarkerSHA256,
		ReceiptKey:     command.ReceiptKey,
		ReceiptContent: command.ReceiptContent,
	}
}

func (request scheduledBackupProbeRequest) command(store backupcontract.StoreConfig) backupcontract.RepositoryProbeCommand {
	return backupcontract.RepositoryProbeCommand{
		Store:          store,
		MarkerKey:      request.MarkerKey,
		MarkerSHA256:   request.MarkerSHA256,
		ReceiptKey:     request.ReceiptKey,
		ReceiptContent: request.ReceiptContent,
	}
}

type scheduledBackupRestoreRequest struct {
	Action             backupcontract.RestoreNodeAction `json:"action"`
	Store              backupcontract.StoreReference    `json:"store"`
	JobID              string                           `json:"job_id"`
	BackupID           string                           `json:"backup_id"`
	HashSlot           uint16                           `json:"hash_slot"`
	Attempt            uint32                           `json:"attempt"`
	SlotReference      backupartifact.SlotReference     `json:"slot_reference"`
	ControllerRevision uint64                           `json:"controller_revision"`
	TargetActivation   string                           `json:"target_activation"`
	RequiredBytes      uint64                           `json:"required_bytes,omitempty"`
	MaxMessageID       uint64                           `json:"max_message_id,omitempty"`
	CoordinatorNodeID  uint64                           `json:"coordinator_node_id"`
	CoordinatorTerm    uint64                           `json:"coordinator_term"`
}

func newScheduledBackupRestoreRequest(command backupcontract.RestoreNodeCommand) scheduledBackupRestoreRequest {
	return scheduledBackupRestoreRequest{
		Action:             command.Action,
		Store:              command.Store.Reference(),
		JobID:              command.JobID,
		BackupID:           command.BackupID,
		HashSlot:           command.HashSlot,
		Attempt:            command.Attempt,
		SlotReference:      command.SlotReference,
		ControllerRevision: command.ControllerRevision,
		TargetActivation:   command.TargetActivation,
		RequiredBytes:      command.RequiredBytes,
		MaxMessageID:       command.MaxMessageID,
		CoordinatorNodeID:  command.CoordinatorNodeID,
		CoordinatorTerm:    command.CoordinatorTerm,
	}
}

func (request scheduledBackupRestoreRequest) command(store backupcontract.StoreConfig) backupcontract.RestoreNodeCommand {
	return backupcontract.RestoreNodeCommand{
		Action:             request.Action,
		Store:              store,
		JobID:              request.JobID,
		BackupID:           request.BackupID,
		HashSlot:           request.HashSlot,
		Attempt:            request.Attempt,
		SlotReference:      request.SlotReference,
		ControllerRevision: request.ControllerRevision,
		TargetActivation:   request.TargetActivation,
		RequiredBytes:      request.RequiredBytes,
		MaxMessageID:       request.MaxMessageID,
		CoordinatorNodeID:  request.CoordinatorNodeID,
		CoordinatorTerm:    request.CoordinatorTerm,
	}
}
