package state

import backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"

// BackupErasureLedgerReference is the only bounded pending permanent-erasure
// ledger record stored in Controller state.
type BackupErasureLedgerReference struct {
	// HashSlot identifies the independently sequenced erasure stream.
	HashSlot uint16 `json:"hash_slot"`
	// Sequence is the next contiguous position within HashSlot.
	Sequence uint64 `json:"sequence"`
	// EventID is the deterministic permanent-erasure event identity.
	EventID string `json:"event_id"`
	// RecordKey points to the immutable signed event record.
	RecordKey string `json:"record_key"`
	// RecordSHA256 authenticates the exact signed record bytes.
	RecordSHA256 string `json:"record_sha256"`
}

// BackupErasureStreamState is the bounded coordination state for one Hash Slot
// permanent-erasure stream.
type BackupErasureStreamState struct {
	// HashSlot identifies the independently sequenced stream.
	HashSlot uint16 `json:"hash_slot"`
	// Head authenticates the latest dual-repository commit.
	Head *backupartifact.ErasureStreamHead `json:"head,omitempty"`
	// Pending is the one reserved record whose commit marker may need repair.
	Pending *BackupErasureLedgerReference `json:"pending,omitempty"`
	// LastCommitted preserves constant-space retry recognition.
	LastCommitted *BackupErasureLedgerReference `json:"last_committed,omitempty"`
}

// BackupSegmentReference binds a Controller frontier to one immutable dual-repository commit.
type BackupSegmentReference struct {
	// SegmentID is the canonical segment-header SHA-256.
	SegmentID string `json:"segment_id"`
	// CommitKey locates the immutable signed commit record.
	CommitKey string `json:"commit_key"`
	// CommitSHA256 authenticates the exact signed commit bytes.
	CommitSHA256 string `json:"commit_sha256"`
	// PlaintextBytes is the authenticated decompressed segment size.
	PlaintextBytes int64 `json:"plaintext_bytes"`
}

// BackupCatalogPageReference is the bounded Controller-visible head of the
// immutable checkpoint catalog.
type BackupCatalogPageReference struct {
	// Sequence is the monotonically increasing catalog page position.
	Sequence uint64 `json:"sequence"`
	// Key locates the immutable signed catalog page.
	Key string `json:"key"`
	// SHA256 authenticates the exact signed page bytes.
	SHA256 string `json:"sha256"`
	// Bytes is the exact signed page size.
	Bytes int64 `json:"bytes"`
	// LatestCheckpointID identifies the checkpoint state appended on this page.
	LatestCheckpointID string `json:"latest_checkpoint_id"`
}

// BackupStreamFrontier is the compact durable head of one continuous Slot stream.
type BackupStreamFrontier struct {
	// Sequence is the latest committed segment sequence in the current Generation.
	Sequence uint64 `json:"sequence"`
	// Head authenticates the latest segment; nil means the stream has emitted no data.
	Head *BackupSegmentReference `json:"head,omitempty"`
	// CursorHead authenticates the latest cursor-only message sidecar.
	CursorHead *BackupSegmentReference `json:"cursor_head,omitempty"`
	// BaselineCursorHead authenticates the complete materialized Channel index.
	BaselineCursorHead *BackupSegmentReference `json:"baseline_cursor_head,omitempty"`
	// SourceCursor is the bounded opaque reconciliation cursor.
	SourceCursor string `json:"source_cursor,omitempty"`
	// SourceHighWatermark is the greatest fully reconciled committed source position.
	SourceHighWatermark uint64 `json:"source_high_watermark"`
	// WatermarkAtUnixMillis is the UTC source time represented by the high watermark.
	WatermarkAtUnixMillis int64 `json:"watermark_at_unix_millis"`
	// CapturedPlaintextBytes is the cumulative post-baseline logical bytes in this Generation.
	CapturedPlaintextBytes uint64 `json:"captured_plaintext_bytes"`
}

// BackupSlotCaptureLease fences one Hash Slot capture worker to one Raft authority.
type BackupSlotCaptureLease struct {
	// SlotID identifies the logical Slot Raft Group that owns the Hash Slot.
	SlotID uint32 `json:"slot_id"`
	// LeaderTerm is the Slot Raft term observed with HolderNodeID.
	LeaderTerm uint64 `json:"leader_term"`
	// ConfigEpoch is the control-plane configuration epoch for SlotID.
	ConfigEpoch uint64 `json:"config_epoch"`
	// HolderNodeID is the only node allowed to advance this frontier.
	HolderNodeID uint64 `json:"holder_node_id"`
	// Generation binds the lease to one immutable Slot segment graph.
	Generation string `json:"generation"`
	// Sequence increases on every authority takeover.
	Sequence uint64 `json:"sequence"`
	// AcquiredAtUnixMillis is the UTC time of the latest durable takeover.
	AcquiredAtUnixMillis int64 `json:"acquired_at_unix_millis"`
}

// BackupPartitionReference authenticates one immutable materialized Slot manifest.
type BackupPartitionReference struct {
	HashSlot        uint16                           `json:"hash_slot"`
	Key             string                           `json:"key"`
	SHA256          string                           `json:"sha256"`
	Bytes           int64                            `json:"bytes"`
	ObjectCount     uint64                           `json:"object_count"`
	CiphertextBytes uint64                           `json:"ciphertext_bytes"`
	Evidence        backupartifact.PartitionEvidence `json:"evidence"`
}

// BackupSlotBaselineReference authenticates the materialized root and cursor index.
type BackupSlotBaselineReference struct {
	// Partition authenticates the materialized Slot manifest.
	Partition BackupPartitionReference `json:"partition"`
	// PlaintextBytes is the logical baseline size used by compaction thresholds.
	PlaintextBytes uint64 `json:"plaintext_bytes"`
}

// BackupSlotRebase preserves a pending replacement while the current generation
// remains the published restore source.
type BackupSlotRebase struct {
	TargetGeneration    string `json:"target_generation"`
	Epoch               uint64 `json:"epoch"`
	Reason              string `json:"reason"`
	StartedAtUnixMillis int64  `json:"started_at_unix_millis"`
}

// BackupSlotGenerationPromotion records the latest completed replacement.
type BackupSlotGenerationPromotion struct {
	PreviousGeneration   string `json:"previous_generation"`
	Reason               string `json:"reason"`
	PromotedAtUnixMillis int64  `json:"promoted_at_unix_millis"`
}

// BackupSlotFrontier atomically binds metadata and message stream heads for one Hash Slot.
type BackupSlotFrontier struct {
	// Revision fences compare-and-swap updates to this Slot record.
	Revision uint64 `json:"revision"`
	// HashSlot identifies the logical cluster partition.
	HashSlot uint16 `json:"hash_slot"`
	// Generation identifies the independently replaceable immutable segment graph.
	Generation string `json:"generation"`
	// GenerationStartedAtUnixMillis is the durable age origin of Generation.
	GenerationStartedAtUnixMillis int64 `json:"generation_started_at_unix_millis"`
	// Lease fences frontier commits to one exact current Slot authority.
	Lease BackupSlotCaptureLease `json:"lease"`
	// SourceSlotID identifies the physical Slot index space used by the
	// metadata source cursor. It may differ from Lease only while rebasing.
	SourceSlotID uint32 `json:"source_slot_id"`
	// SourcePinStartedAtUnixMillis is the durable age origin of the retained
	// metadata log floor and survives lease takeover.
	SourcePinStartedAtUnixMillis int64 `json:"source_pin_started_at_unix_millis"`
	// Baseline is the optional materialized root of Generation.
	Baseline *BackupSlotBaselineReference `json:"baseline,omitempty"`
	// Rebase records a retryable pending generation replacement.
	Rebase *BackupSlotRebase `json:"rebase,omitempty"`
	// LastPromotion proves why the current Generation replaced its predecessor.
	LastPromotion *BackupSlotGenerationPromotion `json:"last_promotion,omitempty"`
	// Metadata and Messages are separate streams advanced through this one record.
	Metadata BackupStreamFrontier `json:"metadata"`
	Messages BackupStreamFrontier `json:"messages"`
	// WatermarkAtUnixMillis is the older fully reconciled stream time.
	WatermarkAtUnixMillis int64 `json:"watermark_at_unix_millis"`
	// UpdatedAtUnixMillis is the UTC time of the latest frontier commit.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
}

// BackupGenerationGCCursor is one bounded repository-local Generation sweep position.
type BackupGenerationGCCursor struct {
	// Repository identifies one explicit failure-domain copy.
	Repository string `json:"repository"`
	// Revision fences independent compare-and-swap updates.
	Revision uint64 `json:"revision"`
	// CycleID identifies one retryable protection and cutoff decision.
	CycleID string `json:"cycle_id"`
	// CatalogRetentionRevision is the hold/release revision fixed by this cycle.
	CatalogRetentionRevision uint64 `json:"catalog_retention_revision,omitempty"`
	// AfterKey is the last fully processed lexicographic object key.
	AfterKey string `json:"after_key,omitempty"`
	// CutoffUnixMillis freezes safety-window eligibility for the cycle.
	CutoffUnixMillis int64 `json:"cutoff_unix_millis"`
	// Complete prevents a healthy copy from rescanning while its peer retries.
	Complete bool `json:"complete"`
	// UpdatedAtUnixMillis is the latest durable progress time.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
}

// BackupIntegrityAuditCursor is one opaque bounded full-audit continuation.
type BackupIntegrityAuditCursor struct {
	// CycleID identifies one fixed catalog/frontier decision.
	CycleID string `json:"cycle_id"`
	// ScrubEpoch identifies the periodic latent-damage pass.
	ScrubEpoch uint64 `json:"scrub_epoch"`
	// CatalogSequence is the immutable catalog head included by CycleID.
	CatalogSequence uint64 `json:"catalog_sequence"`
	// CatalogRootSequence is the oldest retained page fixed by this cycle.
	CatalogRootSequence uint64 `json:"catalog_root_sequence"`
	// HashSlot and Generation identify the isolated artifact graph.
	HashSlot   uint16 `json:"hash_slot"`
	Generation string `json:"generation"`
	// Position is the backend continuation; Resume* survives repair/rebase.
	Position         string `json:"position"`
	ResumeHashSlot   uint16 `json:"resume_hash_slot,omitempty"`
	ResumeGeneration string `json:"resume_generation,omitempty"`
	ResumePosition   string `json:"resume_position,omitempty"`
	ResumePhase      string `json:"resume_phase,omitempty"`
	// Phase selects inspect, repair, revalidate, rebase, or complete.
	Phase string `json:"phase"`
	// Repository and Category describe a pending single-copy repair.
	Repository string `json:"repository,omitempty"`
	Category   string `json:"category,omitempty"`
	// UpdatedAtUnixMillis is the latest durable transition.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
}

// BackupSlotIntegrityAuditState is one compact per-Slot health projection.
type BackupSlotIntegrityAuditState struct {
	// HashSlot and Generation identify the independently frozen graph.
	HashSlot   uint16 `json:"hash_slot"`
	Generation string `json:"generation"`
	// Health is healthy, degraded, rebase_required, or failed.
	Health string `json:"health"`
	// Repository and Category describe the bounded current failure.
	Repository string `json:"repository,omitempty"`
	Category   string `json:"category,omitempty"`
	// LastSuccessAtUnixMillis is the latest complete artifact validation.
	LastSuccessAtUnixMillis int64 `json:"last_success_at_unix_millis,omitempty"`
	// UpdatedAtUnixMillis is the latest health transition.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
}

// BackupIntegrityAuditGCGuard durably excludes a same-Slot audit freeze from
// one in-flight external Generation delete across Controller Leader changes.
type BackupIntegrityAuditGCGuard struct {
	HashSlot             uint16 `json:"hash_slot"`
	Token                string `json:"token"`
	AcquiredAtUnixMillis int64  `json:"acquired_at_unix_millis"`
	ExpiresAtUnixMillis  int64  `json:"expires_at_unix_millis"`
}

// BackupIntegrityAuditState is bounded Controller coordination for one auditor.
type BackupIntegrityAuditState struct {
	// Revision fences audit transitions independently from global Controller revision.
	Revision uint64 `json:"revision"`
	// Cursor is nil before the first audit cycle.
	Cursor *BackupIntegrityAuditCursor `json:"cursor,omitempty"`
	// Slots contains at most one sorted health projection per Hash Slot.
	Slots []BackupSlotIntegrityAuditState `json:"slots,omitempty"`
	// GCGuards contains at most one sorted in-flight delete per Hash Slot.
	GCGuards []BackupIntegrityAuditGCGuard `json:"gc_guards,omitempty"`
	// DebtObjects is the latest bounded remaining-artifact estimate.
	DebtObjects uint64 `json:"debt_objects"`
	// LastSuccessAtUnixMillis is the latest successful full artifact validation.
	LastSuccessAtUnixMillis int64 `json:"last_success_at_unix_millis,omitempty"`
	// UpdatedAtUnixMillis is the latest durable auditor progress.
	UpdatedAtUnixMillis int64 `json:"updated_at_unix_millis"`
}

// BackupCoordinationState stores only bounded backup coordination metadata in Controller Raft.
type BackupCoordinationState struct {
	// SourceFence is the irreversible generation-level write fence used to
	// authorize one exact successor restore plan.
	SourceFence *backupartifact.SourceFenceRecord `json:"source_fence,omitempty"`
	// SlotFrontiers contains at most one compact sorted record per configured Hash Slot.
	SlotFrontiers []BackupSlotFrontier `json:"slot_frontiers,omitempty"`
	// CatalogHead is the only Controller-resident pointer into immutable checkpoint history.
	CatalogHead *BackupCatalogPageReference `json:"catalog_head,omitempty"`
	// CatalogAuditRootSequence is the oldest page that may contain a checkpoint
	// in the sparse retention decision. Exact references stay in repositories.
	CatalogAuditRootSequence uint64 `json:"catalog_audit_root_sequence,omitempty"`
	// CatalogRetentionRevision fences Generation deletion against concurrent
	// checkpoint hold or release commits.
	CatalogRetentionRevision uint64 `json:"catalog_retention_revision,omitempty"`
	// ErasureStreams contains at most one sorted bounded state per Hash Slot.
	ErasureStreams []BackupErasureStreamState `json:"erasure_streams,omitempty"`
	// GenerationGCCursors contains at most one bounded cursor per explicit repository.
	GenerationGCCursors []BackupGenerationGCCursor `json:"generation_gc_cursors,omitempty"`
	// IntegrityAudit contains one durable full-scan cursor and bounded per-Slot health.
	IntegrityAudit BackupIntegrityAuditState `json:"integrity_audit,omitempty"`
}

// Clone returns a deep copy safe for normalization and mutation.
func (s BackupCoordinationState) Clone() BackupCoordinationState {
	out := s
	if s.SourceFence != nil {
		sourceFence := *s.SourceFence
		out.SourceFence = &sourceFence
	}
	out.SlotFrontiers = cloneBackupSlotFrontiers(s.SlotFrontiers)
	if s.CatalogHead != nil {
		head := *s.CatalogHead
		out.CatalogHead = &head
	}
	out.ErasureStreams = cloneBackupErasureStreams(s.ErasureStreams)
	out.GenerationGCCursors = cloneSlice(s.GenerationGCCursors)
	out.IntegrityAudit = cloneBackupIntegrityAuditState(s.IntegrityAudit)
	return out
}

func cloneBackupIntegrityAuditState(state BackupIntegrityAuditState) BackupIntegrityAuditState {
	out := state
	if state.Cursor != nil {
		cursor := *state.Cursor
		out.Cursor = &cursor
	}
	out.Slots = cloneSlice(state.Slots)
	out.GCGuards = cloneSlice(state.GCGuards)
	return out
}

func cloneBackupErasureStreams(streams []BackupErasureStreamState) []BackupErasureStreamState {
	out := cloneSlice(streams)
	for index, stream := range streams {
		if stream.Head != nil {
			head := *stream.Head
			out[index].Head = &head
		}
		if stream.Pending != nil {
			pending := *stream.Pending
			out[index].Pending = &pending
		}
		if stream.LastCommitted != nil {
			committed := *stream.LastCommitted
			out[index].LastCommitted = &committed
		}
	}
	return out
}

func cloneBackupSlotFrontiers(frontiers []BackupSlotFrontier) []BackupSlotFrontier {
	out := cloneSlice(frontiers)
	for index := range out {
		if frontiers[index].Baseline != nil {
			baseline := *frontiers[index].Baseline
			out[index].Baseline = &baseline
		}
		if frontiers[index].Rebase != nil {
			rebase := *frontiers[index].Rebase
			out[index].Rebase = &rebase
		}
		if frontiers[index].LastPromotion != nil {
			promotion := *frontiers[index].LastPromotion
			out[index].LastPromotion = &promotion
		}
		if frontiers[index].Metadata.Head != nil {
			head := *frontiers[index].Metadata.Head
			out[index].Metadata.Head = &head
		}
		if frontiers[index].Metadata.CursorHead != nil {
			head := *frontiers[index].Metadata.CursorHead
			out[index].Metadata.CursorHead = &head
		}
		if frontiers[index].Metadata.BaselineCursorHead != nil {
			head := *frontiers[index].Metadata.BaselineCursorHead
			out[index].Metadata.BaselineCursorHead = &head
		}
		if frontiers[index].Messages.Head != nil {
			head := *frontiers[index].Messages.Head
			out[index].Messages.Head = &head
		}
		if frontiers[index].Messages.CursorHead != nil {
			head := *frontiers[index].Messages.CursorHead
			out[index].Messages.CursorHead = &head
		}
		if frontiers[index].Messages.BaselineCursorHead != nil {
			head := *frontiers[index].Messages.BaselineCursorHead
			out[index].Messages.BaselineCursorHead = &head
		}
	}
	return out
}
