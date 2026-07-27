package state

import (
	"encoding/hex"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// Validate checks whether the cluster state satisfies durable Controller invariants.
func (s ClusterState) Validate() error {
	s = s.Clone()
	s.Normalize()
	if s.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedSchema, s.SchemaVersion)
	}
	if s.ClusterID == "" {
		return invalid("cluster_id is required")
	}
	if s.Revision == 0 {
		return invalid("revision is required")
	}
	if s.Config.SlotCount == 0 || s.Config.HashSlotCount == 0 || s.Config.ReplicaCount == 0 {
		return invalid("slot_count, hash_slot_count, and replica_count must be positive")
	}
	if s.Config.SlotCount > uint32(s.Config.HashSlotCount) {
		return invalid("slot_count must not exceed hash_slot_count")
	}

	nodes, err := validateNodes(s.Nodes)
	if err != nil {
		return err
	}
	if err := validateControllers(s.Controllers, nodes); err != nil {
		return err
	}
	if err := validateNodeHealthReports(s.NodeHealthReports, nodes); err != nil {
		return err
	}
	assignments, err := validateSlots(s.Config, s.Slots, nodes)
	if err != nil {
		return err
	}
	if err := validateHashSlots(s.Config, s.HashSlots); err != nil {
		return err
	}
	if err := validateTasks(s.Tasks, assignments, nodes); err != nil {
		return err
	}
	if err := validateBackup(s.Backup, s.Config.HashSlotCount); err != nil {
		return err
	}
	if err := validateRestore(s.Restore, s.Config.HashSlotCount); err != nil {
		return err
	}
	if err := validateOpsMCP(s.OpsMCP, nodes); err != nil {
		return err
	}
	return nil
}

func validateOpsMCP(opsMCP *OpsMCPState, nodes map[uint64]Node) error {
	if opsMCP == nil {
		return nil
	}
	if len(opsMCP.Credentials) > MaxOpsMCPCredentials {
		return invalid("ops_mcp credentials exceed limit")
	}
	if opsMCP.ProfileFenceUntilUnixMillis < 0 {
		return invalid("ops_mcp profile fence is invalid")
	}
	if opsMCP.Enabled && opsMCP.OwnerNodeID == 0 {
		return invalid("enabled ops_mcp requires an owner")
	}
	if opsMCP.Enabled && len(opsMCP.Credentials) == 0 {
		return invalid("enabled ops_mcp requires a credential")
	}
	if opsMCP.OwnerNodeID != 0 {
		owner, ok := nodes[opsMCP.OwnerNodeID]
		if !ok || owner.JoinState != NodeJoinStateActive {
			return invalid("ops_mcp owner must be an active cluster node")
		}
	}
	credentialIDs := make(map[string]struct{}, len(opsMCP.Credentials))
	for _, credential := range opsMCP.Credentials {
		if !validOpsMCPCredentialID(credential.ID) || !validSHA256(credential.DigestSHA256) || credential.CreatedAtUnixMillis <= 0 {
			return invalid("ops_mcp credential is invalid")
		}
		if _, exists := credentialIDs[credential.ID]; exists {
			return invalid("duplicate ops_mcp credential id")
		}
		credentialIDs[credential.ID] = struct{}{}
	}
	return nil
}

func validOpsMCPCredentialID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func validateRestore(restore *RestoreCoordinationState, hashSlotCount uint16) error {
	if restore == nil || restore.Plan == nil {
		return nil
	}
	plan := restore.Plan
	if !validBackupIdentity(plan.ID) || !validBackupIdentity(plan.CheckpointID) || !validSHA256(plan.CheckpointSHA256) {
		return invalid("restore plan identity is invalid")
	}
	if plan.Repository != "primary" && plan.Repository != "secondary" {
		return invalid("restore repository selector is invalid")
	}
	if plan.CatalogProof == nil ||
		backupartifact.ValidateCheckpointCatalogProof(*plan.CatalogProof) != nil ||
		plan.CatalogProof.Checkpoint.ID != plan.CheckpointID ||
		plan.CatalogProof.Checkpoint.SHA256 != plan.CheckpointSHA256 ||
		plan.CatalogProof.Checkpoint.GenerationVector.HashSlotCount !=
			plan.HashSlotCount ||
		plan.CheckpointVersion != backupartifact.CheckpointVersion ||
		plan.CheckpointCreatedAtUnixMillis != plan.CatalogProof.Checkpoint.CreatedAtUnixMillis ||
		plan.CheckpointEffectiveAtUnixMillis != plan.CatalogProof.Checkpoint.EffectiveAtUnixMillis {
		return invalid("restore checkpoint catalog proof is invalid")
	}
	if plan.SourceClusterID == "" || plan.TargetClusterID == "" || plan.SourceClusterID == plan.TargetClusterID ||
		plan.SourceGeneration == "" || plan.TargetGeneration == "" || plan.SourceGeneration == plan.TargetGeneration {
		return invalid("restore source and target generations must differ")
	}
	if plan.HashSlotCount != hashSlotCount || len(plan.Partitions) != int(hashSlotCount) {
		return invalid("restore hash_slot_count or partition count is invalid")
	}
	if plan.ErasureLedgerVersion != backupartifact.ErasureLedgerSnapshotVersion || !validSHA256(plan.ErasureLedgerSHA256) {
		return invalid("restore erasure-ledger snapshot fence is invalid")
	}
	var erasureBoundary uint64
	for index, head := range plan.ErasureHeads {
		if head.HashSlot >= hashSlotCount || backupartifact.ValidateErasureStreamHead(head) != nil ||
			(index > 0 && plan.ErasureHeads[index-1].HashSlot >= head.HashSlot) ||
			head.Sequence > uint64(backupartifact.MaxErasureLedgerEvents)-erasureBoundary {
			return invalid("restore erasure stream heads are invalid")
		}
		erasureBoundary += head.Sequence
	}
	if erasureBoundary != plan.ErasureEventCount {
		return invalid("restore erasure stream boundary is invalid")
	}
	switch plan.Status {
	case RestoreStatusPlanned, RestoreStatusInstalling, RestoreStatusInstalled,
		RestoreStatusVerified, RestoreStatusActivating, RestoreStatusActivated,
		RestoreStatusAbandoned:
	default:
		return invalid("restore status is invalid")
	}
	if plan.CreatedAtUnixMillis <= 0 ||
		plan.UpdatedAtUnixMillis < plan.CreatedAtUnixMillis ||
		plan.VerifiedAtUnixMillis < 0 ||
		plan.ActivatedAtUnixMillis < 0 ||
		plan.StagingCleanupCompletedAtUnixMillis < 0 {
		return invalid("restore timestamps are invalid")
	}
	if (plan.Status == RestoreStatusVerified ||
		plan.Status == RestoreStatusActivating ||
		plan.Status == RestoreStatusActivated) &&
		plan.VerifiedAtUnixMillis <= 0 {
		return invalid("verified restore has no verification timestamp")
	}
	if plan.Status == RestoreStatusActivating {
		if plan.ActivatedAtUnixMillis != 0 ||
			plan.StagingCleanupCompletedAtUnixMillis != 0 ||
			plan.Activation == nil ||
			backupartifact.ValidateRestoreActivationEvidence(*plan.Activation) != nil {
			return invalid("activating restore has invalid fencing evidence")
		}
	} else if plan.Status == RestoreStatusActivated {
		if plan.ActivatedAtUnixMillis <= 0 ||
			plan.StagingCleanupCompletedAtUnixMillis <= 0 ||
			plan.StagingCleanupCompletedAtUnixMillis >
				plan.ActivatedAtUnixMillis ||
			plan.Activation == nil ||
			backupartifact.ValidateRestoreActivationEvidence(*plan.Activation) != nil ||
			plan.Activation.RecordedAtUnixMillis >
				plan.StagingCleanupCompletedAtUnixMillis {
			return invalid("activated restore has no fencing or cleanup evidence")
		}
	}
	if plan.Status != RestoreStatusActivating &&
		plan.Status != RestoreStatusActivated &&
		(plan.ActivatedAtUnixMillis != 0 ||
			plan.StagingCleanupCompletedAtUnixMillis != 0 ||
			plan.Activation != nil) {
		return invalid("inactive restore carries activation evidence")
	}
	if plan.Status != RestoreStatusVerified &&
		plan.Status != RestoreStatusActivating &&
		plan.Status != RestoreStatusActivated &&
		plan.VerifiedAtUnixMillis != 0 {
		return invalid("unverified restore carries verification evidence")
	}
	var pending, converged, verified int
	for index, partition := range plan.Partitions {
		if partition.HashSlot != uint16(index) || len(partition.FailureCategory) > 128 || partition.UpdatedAtUnixMillis < 0 {
			return invalid("restore partition progress is invalid")
		}
		if partition.Verified &&
			(!partition.Installed ||
				partition.Status != RestorePartitionConverged) {
			return invalid("restore partition verified before convergence")
		}
		if partition.Installed &&
			partition.EvidenceVersion != backupartifact.RestoreEvidenceVersion {
			return invalid("installed restore partition has no evidence version")
		}
		if partition.Installed && (partition.MessageCount == 0) != (partition.MaxMessageID == 0) {
			return invalid("installed restore partition message evidence is inconsistent")
		}
		if partition.Installed && !validSHA256(partition.MetadataSHA256) {
			return invalid("installed restore partition metadata digest is invalid")
		}
		switch partition.Status {
		case RestorePartitionPending:
			pending++
			empty := RestorePartition{
				HashSlot: partition.HashSlot,
				Status:   RestorePartitionPending,
			}
			if !reflect.DeepEqual(partition, empty) {
				return invalid("pending restore partition carries progress")
			}
			continue
		case RestorePartitionInstalling, RestorePartitionInstalled,
			RestorePartitionConverging, RestorePartitionConverged,
			RestorePartitionFailed:
		default:
			return invalid("restore partition status is invalid")
		}
		if partition.TargetSlotID == 0 || partition.LeaderNodeID == 0 ||
			partition.LeaderTerm == 0 || partition.ConfigEpoch == 0 ||
			partition.InstallAttempt == 0 || partition.StartedAtUnixMillis <= 0 ||
			partition.ReplicaCount == 0 {
			return invalid("restore partition leader fence is incomplete")
		}
		switch partition.Status {
		case RestorePartitionInstalling:
			if partition.Installed || partition.Verified ||
				partition.EvidenceVersion != 0 ||
				hasRestorePartitionInstallEvidence(partition) ||
				partition.InstalledAtUnixMillis != 0 ||
				partition.ConvergedReplicas != 0 ||
				partition.ReplicatedBytes != 0 ||
				partition.FailureCategory != "" {
				return invalid("installing restore partition carries completion evidence")
			}
		case RestorePartitionFailed:
			if partition.Installed || partition.Verified ||
				partition.FailureCategory == "" ||
				partition.EvidenceVersion != 0 ||
				hasRestorePartitionInstallEvidence(partition) ||
				partition.InstalledAtUnixMillis != 0 ||
				partition.ConvergedReplicas != 0 ||
				partition.ReplicatedBytes != 0 {
				return invalid("failed restore partition is inconsistent")
			}
		case RestorePartitionInstalled, RestorePartitionConverging,
			RestorePartitionConverged:
			if !partition.Installed ||
				partition.FailureCategory != "" ||
				!validSHA256(partition.ContentSHA256) ||
				!validSHA256(partition.MessageMerkleSHA256) ||
				partition.ConvergedReplicas > partition.ReplicaCount ||
				partition.InstalledAtUnixMillis < partition.StartedAtUnixMillis {
				return invalid("restore partition vNext evidence is invalid")
			}
			if partition.Status == RestorePartitionConverged {
				converged++
				if partition.ConvergedReplicas != partition.ReplicaCount {
					return invalid("restore partition convergence is incomplete")
				}
			} else if partition.Status == RestorePartitionInstalled {
				if partition.ConvergedReplicas != 1 ||
					partition.ReplicaCount <= 1 {
					return invalid("installed restore partition replica evidence is invalid")
				}
			} else if partition.ConvergedReplicas <= 1 ||
				partition.ConvergedReplicas >= partition.ReplicaCount {
				return invalid("converging restore partition replica evidence is invalid")
			}
		}
		if partition.Verified {
			verified++
		}
	}
	switch plan.Status {
	case RestoreStatusPlanned:
		if pending != len(plan.Partitions) {
			return invalid("planned restore contains active partition progress")
		}
	case RestoreStatusInstalling:
		if converged == len(plan.Partitions) || verified != 0 {
			return invalid("installing restore is already fully converged")
		}
	case RestoreStatusInstalled:
		if converged != len(plan.Partitions) || verified != 0 {
			return invalid("installed restore aggregate phase is inconsistent")
		}
	case RestoreStatusVerified, RestoreStatusActivating,
		RestoreStatusActivated:
		if converged != len(plan.Partitions) ||
			verified != len(plan.Partitions) {
			return invalid("verified restore aggregate phase is inconsistent")
		}
	case RestoreStatusAbandoned:
	}
	return nil
}

func hasRestorePartitionInstallEvidence(
	partition RestorePartition,
) bool {
	return partition.PlainBytes != 0 ||
		partition.MetadataRecordCount != 0 ||
		partition.MessageCount != 0 ||
		partition.MaxMessageID != 0 ||
		partition.MetadataSHA256 != "" ||
		partition.ContentSHA256 != "" ||
		partition.MessageMerkleSHA256 != "" ||
		partition.ChannelBoundaryCount != 0
}

func validateBackup(backup *BackupCoordinationState, hashSlotCount uint16) error {
	if backup == nil {
		return nil
	}
	if backup.SourceFence != nil {
		if err := backupartifact.ValidateSourceFenceRecord(
			*backup.SourceFence, false,
		); err != nil {
			return invalid("backup source fence is invalid")
		}
	}
	if len(backup.SlotFrontiers) > int(hashSlotCount) {
		return invalid("backup Slot frontiers exceed hash_slot_count")
	}
	for index, frontier := range backup.SlotFrontiers {
		if index > 0 && backup.SlotFrontiers[index-1].HashSlot >= frontier.HashSlot {
			return invalid("backup Slot frontiers must be unique and sorted")
		}
		if frontier.HashSlot >= hashSlotCount || frontier.Revision == 0 ||
			!validBackupIdentity(frontier.Generation) || frontier.UpdatedAtUnixMillis <= 0 ||
			!validBackupSlotCaptureLease(frontier.Lease, frontier.Generation) ||
			frontier.SourceSlotID == 0 ||
			frontier.GenerationStartedAtUnixMillis <= 0 ||
			frontier.GenerationStartedAtUnixMillis > frontier.UpdatedAtUnixMillis ||
			frontier.SourcePinStartedAtUnixMillis <= 0 ||
			frontier.SourcePinStartedAtUnixMillis > frontier.UpdatedAtUnixMillis ||
			!validBackupSlotBaseline(frontier.Baseline, frontier.HashSlot) ||
			!validBackupSlotRebase(frontier.Rebase, frontier.Generation) ||
			!validBackupSlotPromotion(
				frontier.LastPromotion, frontier.Generation,
				frontier.GenerationStartedAtUnixMillis,
			) ||
			!validBackupStreamFrontier(frontier.Metadata) ||
			!validBackupStreamFrontier(frontier.Messages) ||
			frontier.Metadata.CursorHead != nil ||
			frontier.Metadata.BaselineCursorHead != nil ||
			(frontier.Baseline == nil) != (frontier.Messages.BaselineCursorHead == nil) ||
			(frontier.Messages.Sequence > 0 && frontier.Messages.CursorHead == nil) ||
			frontier.WatermarkAtUnixMillis != olderBackupWatermark(
				frontier.Metadata.WatermarkAtUnixMillis,
				frontier.Messages.WatermarkAtUnixMillis,
			) {
			return invalid("backup Slot frontier is invalid")
		}
	}
	if backup.CatalogHead != nil {
		head := backup.CatalogHead
		if head.Sequence == 0 || !validBackupIdentity(head.LatestCheckpointID) ||
			head.Key != backupartifact.CatalogPageObjectKey(head.Sequence, head.LatestCheckpointID) ||
			!validSHA256(head.SHA256) || head.Bytes <= 0 || head.Bytes > 1<<20 ||
			backup.CatalogAuditRootSequence == 0 ||
			backup.CatalogRetentionRevision == 0 ||
			backup.CatalogAuditRootSequence > head.Sequence {
			return invalid("backup checkpoint catalog head is invalid")
		}
	} else if backup.CatalogAuditRootSequence != 0 ||
		backup.CatalogRetentionRevision != 0 {
		return invalid("backup checkpoint catalog audit root has no head")
	}
	var erasureReservations uint64
	for index, stream := range backup.ErasureStreams {
		if stream.HashSlot >= hashSlotCount ||
			(index > 0 && backup.ErasureStreams[index-1].HashSlot >= stream.HashSlot) ||
			(stream.Head == nil && stream.Pending == nil) {
			return invalid("backup erasure streams must be non-empty, unique, and sorted")
		}
		var boundary uint64
		if stream.Head != nil {
			if stream.Head.HashSlot != stream.HashSlot || backupartifact.ValidateErasureStreamHead(*stream.Head) != nil {
				return invalid("backup erasure stream head is invalid")
			}
			boundary = stream.Head.Sequence
			if boundary > uint64(backupartifact.MaxErasureLedgerEvents)-erasureReservations {
				return invalid("backup erasure stream event capacity is exceeded")
			}
			erasureReservations += boundary
		}
		if stream.Pending != nil {
			pending := stream.Pending
			if boundary == ^uint64(0) || pending.HashSlot != stream.HashSlot || pending.Sequence != boundary+1 ||
				!validBackupErasureReference(*pending) {
				return invalid("backup pending erasure stream reference is invalid")
			}
			if erasureReservations >= backupartifact.MaxErasureLedgerEvents {
				return invalid("backup erasure stream event capacity is exceeded")
			}
			erasureReservations++
		}
		if stream.LastCommitted != nil {
			committed := stream.LastCommitted
			if stream.Head == nil || committed.HashSlot != stream.HashSlot ||
				committed.Sequence != boundary || !validBackupErasureReference(*committed) {
				return invalid("backup last committed erasure stream reference is invalid")
			}
		}
	}
	if len(backup.GenerationGCCursors) > 2 {
		return invalid("backup generation GC cursors exceed repository count")
	}
	for index, cursor := range backup.GenerationGCCursors {
		if !validBackupIdentity(cursor.Repository) || cursor.Revision == 0 ||
			!validBackupIdentity(cursor.CycleID) ||
			len(cursor.AfterKey) > 8<<10 || !utf8.ValidString(cursor.AfterKey) ||
			(cursor.AfterKey != "" && !validBackupObjectKey(cursor.AfterKey)) ||
			cursor.CutoffUnixMillis <= 0 || cursor.UpdatedAtUnixMillis <= 0 ||
			(index > 0 && backup.GenerationGCCursors[index-1].Repository >= cursor.Repository) {
			return invalid("backup generation GC cursor is invalid")
		}
	}
	if err := validateBackupIntegrityAudit(backup.IntegrityAudit, hashSlotCount); err != nil {
		return err
	}
	return nil
}

func validateBackupIntegrityAudit(audit BackupIntegrityAuditState, hashSlotCount uint16) error {
	if audit.Revision == 0 {
		if audit.Cursor != nil || len(audit.Slots) != 0 || len(audit.GCGuards) != 0 ||
			audit.DebtObjects != 0 ||
			audit.LastSuccessAtUnixMillis != 0 || audit.UpdatedAtUnixMillis != 0 {
			return invalid("backup integrity audit zero state is inconsistent")
		}
		return nil
	}
	if audit.UpdatedAtUnixMillis <= 0 ||
		(audit.LastSuccessAtUnixMillis < 0 ||
			audit.LastSuccessAtUnixMillis > audit.UpdatedAtUnixMillis) ||
		len(audit.Slots) > int(hashSlotCount) ||
		len(audit.GCGuards) > int(hashSlotCount) {
		return invalid("backup integrity audit state is invalid")
	}
	if audit.Cursor != nil {
		cursor := audit.Cursor
		if !validBackupIdentity(cursor.CycleID) ||
			(strings.HasPrefix(cursor.CycleID, "catalog-segments-") &&
				(cursor.ScrubEpoch == 0 ||
					(cursor.CatalogSequence > 0 &&
						(cursor.CatalogRootSequence == 0 ||
							cursor.CatalogRootSequence >
								cursor.CatalogSequence)))) ||
			cursor.HashSlot >= hashSlotCount ||
			!validBackupIdentity(cursor.Generation) ||
			len(cursor.Position) == 0 || len(cursor.Position) > 8<<10 ||
			!utf8.ValidString(cursor.Position) ||
			len(cursor.ResumePosition) > 8<<10 || !utf8.ValidString(cursor.ResumePosition) ||
			len(cursor.ResumeGeneration) > 128 || !utf8.ValidString(cursor.ResumeGeneration) ||
			(cursor.ResumePhase != "" && !validBackupAuditResumePhase(cursor.ResumePhase)) ||
			!validBackupAuditPhase(cursor.Phase) ||
			len(cursor.Repository) > 128 || !utf8.ValidString(cursor.Repository) ||
			!validBackupAuditCategory(cursor.Category, cursor.Phase) ||
			cursor.UpdatedAtUnixMillis <= 0 ||
			cursor.UpdatedAtUnixMillis > audit.UpdatedAtUnixMillis {
			return invalid("backup integrity audit cursor is invalid")
		}
		if cursor.Phase == "repair" &&
			(cursor.Repository == "" || cursor.Category == "" || cursor.ResumePosition == "" ||
				cursor.ResumeHashSlot >= hashSlotCount ||
				!validBackupIdentity(cursor.ResumeGeneration) ||
				!validBackupAuditResumePhase(cursor.ResumePhase)) {
			return invalid("backup integrity audit repair cursor is incomplete")
		}
		if cursor.Phase == "rebase" &&
			(cursor.ResumePosition == "" || cursor.ResumeHashSlot >= hashSlotCount ||
				!validBackupIdentity(cursor.ResumeGeneration) ||
				!validBackupAuditResumePhase(cursor.ResumePhase)) {
			return invalid("backup integrity audit rebase cursor is incomplete")
		}
	}
	for index, slot := range audit.Slots {
		if slot.HashSlot >= hashSlotCount ||
			(index > 0 && audit.Slots[index-1].HashSlot >= slot.HashSlot) ||
			!validBackupIdentity(slot.Generation) ||
			!validBackupAuditHealth(slot.Health) ||
			len(slot.Repository) > 128 || !utf8.ValidString(slot.Repository) ||
			!validBackupAuditCategory(slot.Category, "") ||
			slot.LastSuccessAtUnixMillis < 0 ||
			slot.LastSuccessAtUnixMillis > slot.UpdatedAtUnixMillis ||
			slot.UpdatedAtUnixMillis <= 0 ||
			slot.UpdatedAtUnixMillis > audit.UpdatedAtUnixMillis {
			return invalid("backup Slot integrity audit state is invalid")
		}
		if slot.Health == "healthy" && (slot.Repository != "" || slot.Category != "") {
			return invalid("healthy backup Slot carries corruption state")
		}
		if slot.Health != "healthy" && slot.Category == "" {
			return invalid("unhealthy backup Slot is missing corruption category")
		}
		if slot.Health == "degraded" && slot.Repository == "" {
			return invalid("degraded backup Slot is missing repair repository")
		}
		if (slot.Health == "rebase_required" || slot.Health == "failed") &&
			slot.Repository != "" {
			return invalid("dual-copy backup Slot names a repair repository")
		}
	}
	for index, guard := range audit.GCGuards {
		if guard.HashSlot >= hashSlotCount ||
			(index > 0 && audit.GCGuards[index-1].HashSlot >= guard.HashSlot) ||
			!validBackupIdentity(guard.Token) ||
			guard.AcquiredAtUnixMillis <= 0 ||
			guard.AcquiredAtUnixMillis > audit.UpdatedAtUnixMillis ||
			guard.ExpiresAtUnixMillis <= guard.AcquiredAtUnixMillis {
			return invalid("backup integrity audit GC guard is invalid")
		}
	}
	return nil
}

func validBackupAuditResumePhase(phase string) bool {
	return phase == "inspect" || phase == "complete"
}

func validBackupAuditPhase(phase string) bool {
	switch phase {
	case "inspect", "repair", "revalidate", "rebase", "complete":
		return true
	default:
		return false
	}
}

func validBackupAuditHealth(health string) bool {
	switch health {
	case "healthy", "degraded", "rebase_required", "failed":
		return true
	default:
		return false
	}
}

func validBackupAuditCategory(category string, phase string) bool {
	if category == "" {
		return phase != "repair"
	}
	switch category {
	case "missing", "checksum", "ciphertext", "commit_proof":
		return true
	default:
		return false
	}
}

func validBackupErasureReference(reference BackupErasureLedgerReference) bool {
	return reference.Sequence > 0 && validSHA256(reference.EventID) &&
		validSHA256(reference.RecordSHA256) &&
		backupartifact.ValidateErasureLedgerRecordKey(reference.RecordKey, reference.EventID) == nil &&
		strings.HasPrefix(reference.RecordKey, fmt.Sprintf("erasure-ledger/events/%04x/", reference.HashSlot))
}

func validBackupSlotBaseline(reference *BackupSlotBaselineReference, hashSlot uint16) bool {
	if reference == nil {
		return true
	}
	partition := reference.Partition
	evidence := partition.Evidence
	if reference.PlaintextBytes == 0 || partition.HashSlot != hashSlot || partition.Bytes <= 0 ||
		partition.ObjectCount == 0 || partition.CiphertextBytes == 0 ||
		!strings.HasPrefix(partition.Key, "partition-manifests/") ||
		!strings.HasSuffix(partition.Key, fmt.Sprintf("/%05d.json", hashSlot)) ||
		!validSHA256(partition.SHA256) ||
		evidence.Version != backupartifact.PartitionEvidenceVersion ||
		(evidence.MessageRecords == 0) != (evidence.MaxMessageID == 0) {
		return false
	}
	return true
}

func validBackupSlotRebase(rebase *BackupSlotRebase, currentGeneration string) bool {
	if rebase == nil {
		return true
	}
	if !validBackupIdentity(rebase.TargetGeneration) ||
		rebase.TargetGeneration == currentGeneration ||
		rebase.Epoch == 0 ||
		rebase.StartedAtUnixMillis <= 0 {
		return false
	}
	switch rebase.Reason {
	case "pin_age", "node_byte_budget", "source_compacted", "source_remapped",
		"generation_bytes", "generation_segments", "generation_age",
		"audit_corruption":
		return true
	default:
		return false
	}
}

func validBackupSlotPromotion(
	promotion *BackupSlotGenerationPromotion,
	currentGeneration string,
	generationStartedAtUnixMillis int64,
) bool {
	if promotion == nil {
		return true
	}
	if !validBackupIdentity(promotion.PreviousGeneration) ||
		promotion.PreviousGeneration == currentGeneration ||
		promotion.PromotedAtUnixMillis <= 0 ||
		promotion.PromotedAtUnixMillis != generationStartedAtUnixMillis {
		return false
	}
	switch promotion.Reason {
	case "pin_age", "node_byte_budget", "source_compacted", "source_remapped",
		"generation_bytes", "generation_segments", "generation_age",
		"audit_corruption":
		return true
	default:
		return false
	}
}

func validBackupSegmentReference(reference BackupSegmentReference) bool {
	return validSHA256(reference.SegmentID) &&
		validSHA256(reference.CommitSHA256) &&
		reference.PlaintextBytes > 0 && reference.PlaintextBytes <= 256<<20 &&
		reference.CommitKey == "segments/"+reference.SegmentID+"/commit.json"
}

func validBackupSlotCaptureLease(lease BackupSlotCaptureLease, generation string) bool {
	return lease.SlotID > 0 && lease.LeaderTerm > 0 && lease.ConfigEpoch > 0 &&
		lease.HolderNodeID > 0 && lease.Sequence > 0 &&
		lease.AcquiredAtUnixMillis > 0 && lease.Generation == generation &&
		validBackupIdentity(lease.Generation)
}

func validBackupStreamFrontier(frontier BackupStreamFrontier) bool {
	if len(frontier.SourceCursor) > 8<<10 || !utf8.ValidString(frontier.SourceCursor) ||
		frontier.WatermarkAtUnixMillis < 0 {
		return false
	}
	if frontier.SourceHighWatermark > 0 && frontier.WatermarkAtUnixMillis <= 0 {
		return false
	}
	if frontier.Sequence == 0 {
		return frontier.Head == nil && frontier.CursorHead == nil &&
			(frontier.BaselineCursorHead == nil || validBackupSegmentReference(*frontier.BaselineCursorHead))
	}
	if frontier.Head == nil || !validBackupSegmentReference(*frontier.Head) {
		return false
	}
	return (frontier.CursorHead == nil || validBackupSegmentReference(*frontier.CursorHead)) &&
		(frontier.BaselineCursorHead == nil || validBackupSegmentReference(*frontier.BaselineCursorHead))
}

func olderBackupWatermark(left, right int64) int64 {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}

func validBackupIdentity(value string) bool {
	if value == "" || len(value) > 128 || strings.Contains(value, "..") {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return false
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validBackupObjectKey(key string) bool {
	return key != "" && len(key) <= 1024 && !strings.HasPrefix(key, "/") && !strings.Contains(key, "\\") && path.Clean(key) == key && key != "."
}

func validateNodes(nodes []Node) (map[uint64]Node, error) {
	byID := make(map[uint64]Node, len(nodes))
	for _, node := range nodes {
		if node.NodeID == 0 {
			return nil, invalid("node_id must be non-zero")
		}
		if _, exists := byID[node.NodeID]; exists {
			return nil, invalid("duplicate node_id")
		}
		if node.Addr == "" {
			return nil, invalid("node addr is required")
		}
		if node.JoinState == "" {
			return nil, invalid("node join_state is required")
		}
		if node.JoinState != NodeJoinStateActive &&
			node.JoinState != NodeJoinStateJoining &&
			node.JoinState != NodeJoinStateLeaving &&
			node.JoinState != NodeJoinStateRemoved {
			return nil, invalid("unknown node join_state")
		}
		if node.Status == "" {
			return nil, invalid("node status is required")
		}
		if !validNodeStatus(node.Status) {
			return nil, invalid("unknown node status")
		}
		seenRoles := make(map[NodeRole]struct{}, len(node.Roles))
		for _, role := range node.Roles {
			if role != NodeRoleControllerVoter && role != NodeRoleData {
				return nil, invalid("unknown node role")
			}
			if _, exists := seenRoles[role]; exists {
				return nil, invalid("duplicate node role")
			}
			seenRoles[role] = struct{}{}
		}
		if len(seenRoles) == 0 {
			return nil, invalid("node roles are required")
		}
		byID[node.NodeID] = node
	}
	return byID, nil
}

func validateControllers(controllers []ControllerVoter, nodes map[uint64]Node) error {
	if len(controllers) == 0 {
		return invalid("controller voters are required")
	}
	seen := make(map[uint64]struct{}, len(controllers))
	for _, controller := range controllers {
		if controller.NodeID == 0 {
			return invalid("controller node_id must be non-zero")
		}
		if _, exists := seen[controller.NodeID]; exists {
			return invalid("duplicate controller node_id")
		}
		seen[controller.NodeID] = struct{}{}
		if controller.Addr == "" {
			return invalid("controller addr is required")
		}
		if controller.Role != ControllerRoleVoter {
			return invalid("controller role must be voter")
		}
		node, ok := nodes[controller.NodeID]
		if !ok || !node.HasRole(NodeRoleControllerVoter) || node.JoinState != NodeJoinStateActive {
			return invalid("controller voter must reference active controller_voter node")
		}
	}
	return nil
}

func validateSlots(config ClusterConfig, slots []SlotAssignment, nodes map[uint64]Node) (map[uint32]SlotAssignment, error) {
	byID := make(map[uint32]SlotAssignment, len(slots))
	for _, slot := range slots {
		if slot.SlotID == 0 || slot.SlotID > config.SlotCount {
			return nil, invalid("slot_id out of range")
		}
		if _, exists := byID[slot.SlotID]; exists {
			return nil, invalid("duplicate slot_id")
		}
		if slot.ConfigEpoch == 0 {
			return nil, invalid("slot config_epoch is required")
		}
		if len(slot.DesiredPeers) != int(config.ReplicaCount) {
			return nil, invalid("slot desired_peers must match replica_count")
		}
		seenPeers := make(map[uint64]struct{}, len(slot.DesiredPeers))
		for _, peerID := range slot.DesiredPeers {
			if peerID == 0 {
				return nil, invalid("slot peer must be non-zero")
			}
			if _, exists := seenPeers[peerID]; exists {
				return nil, invalid("duplicate slot peer")
			}
			seenPeers[peerID] = struct{}{}
			node, ok := nodes[peerID]
			if !ok || !slotDesiredPeerNodeAllowed(node) {
				return nil, invalid("slot peer must be an active or leaving data node")
			}
		}
		if slot.PreferredLeader != 0 {
			if _, ok := seenPeers[slot.PreferredLeader]; !ok {
				return nil, invalid("preferred leader must be a desired peer")
			}
		}
		byID[slot.SlotID] = slot
	}
	return byID, nil
}

func slotDesiredPeerNodeAllowed(node Node) bool {
	return node.HasRole(NodeRoleData) &&
		(node.JoinState == NodeJoinStateActive || node.JoinState == NodeJoinStateLeaving)
}

func validateHashSlots(config ClusterConfig, table HashSlotTable) error {
	if table.Version != CurrentHashSlotTableVersion {
		return invalid("unsupported hash slot table version")
	}
	if table.SlotCount != config.HashSlotCount {
		return invalid("hash slot table slot_count must match config")
	}
	if len(table.Ranges) == 0 {
		return invalid("hash slot ranges are required")
	}
	expectedFrom := uint32(0)
	last := uint32(config.HashSlotCount) - 1
	for _, r := range table.Ranges {
		if r.SlotID == 0 || r.SlotID > config.SlotCount {
			return invalid("hash slot range target out of range")
		}
		if r.From > r.To {
			return invalid("hash slot range from must not exceed to")
		}
		if uint32(r.From) != expectedFrom {
			return invalid("hash slot ranges must be contiguous")
		}
		if uint32(r.To) > last {
			return invalid("hash slot range exceeds hash_slot_count")
		}
		expectedFrom = uint32(r.To) + 1
	}
	if expectedFrom != uint32(config.HashSlotCount) {
		return invalid("hash slot ranges must cover full hash_slot_count")
	}
	return nil
}

func validateTasks(tasks []ReconcileTask, assignments map[uint32]SlotAssignment, nodes map[uint64]Node) error {
	seenTaskIDs := make(map[string]struct{}, len(tasks))
	seenSlots := make(map[uint32]struct{}, len(tasks))
	for _, task := range tasks {
		if task.TaskID == "" {
			return invalid("task_id is required")
		}
		if _, exists := seenTaskIDs[task.TaskID]; exists {
			return invalid("duplicate task_id")
		}
		seenTaskIDs[task.TaskID] = struct{}{}
		if task.SlotID == 0 {
			return invalid("task slot_id is required")
		}
		if _, exists := seenSlots[task.SlotID]; exists {
			return invalid("only one active task per slot is allowed")
		}
		seenSlots[task.SlotID] = struct{}{}
		if task.Status != TaskStatusPending && task.Status != TaskStatusRunning && task.Status != TaskStatusFailed {
			return invalid("unknown task status")
		}
		if task.CompletionPolicy != TaskCompletionPolicySingleObserver && task.CompletionPolicy != TaskCompletionPolicyAllTargetPeers {
			return invalid("unknown task completion_policy")
		}
		if err := validateParticipantProgress(task); err != nil {
			return err
		}
		switch task.Kind {
		case TaskKindBootstrap:
			if task.Step != TaskStepCreateSlot {
				return invalid("bootstrap task step must be create_slot")
			}
			assignment, ok := assignments[task.SlotID]
			if !ok {
				return invalid("bootstrap task requires slot assignment")
			}
			if !reflect.DeepEqual(task.TargetPeers, assignment.DesiredPeers) {
				return invalid("bootstrap target peers must match assignment")
			}
			if task.ConfigEpoch != assignment.ConfigEpoch {
				return invalid("bootstrap config_epoch must match assignment")
			}
			if task.TargetNode != assignment.PreferredLeader {
				return invalid("bootstrap target node must match preferred leader")
			}
		case TaskKindLeaderTransfer:
			if task.Step != TaskStepTransferLeader {
				return invalid("leader transfer task step must be transfer_leader")
			}
			assignment, ok := assignments[task.SlotID]
			if !ok {
				return invalid("leader transfer task requires slot assignment")
			}
			if task.SourceNode == 0 || task.TargetNode == 0 {
				return invalid("leader transfer source and target must be non-zero")
			}
			if task.SourceNode == task.TargetNode {
				return invalid("leader transfer source and target must differ")
			}
			if !containsUint64(assignment.DesiredPeers, task.SourceNode) {
				return invalid("leader transfer source must be a desired peer")
			}
			if !containsUint64(assignment.DesiredPeers, task.TargetNode) {
				return invalid("leader transfer target must be a desired peer")
			}
			if !reflect.DeepEqual(task.TargetPeers, assignment.DesiredPeers) {
				return invalid("leader transfer target peers must match assignment")
			}
			if task.ConfigEpoch != assignment.ConfigEpoch {
				return invalid("leader transfer config_epoch must match assignment")
			}
			if task.TargetNode != assignment.PreferredLeader {
				return invalid("leader transfer target node must match preferred leader")
			}
			if task.CompletionPolicy != TaskCompletionPolicySingleObserver {
				return invalid("leader transfer completion_policy must be single_observer")
			}
			if len(task.ParticipantProgress) != 0 {
				return invalid("leader transfer task must not have participant progress")
			}
		case TaskKindSlotReplicaMove:
			if task.Step != TaskStepOpenLearner &&
				task.Step != TaskStepAddLearner &&
				task.Step != TaskStepPromoteLearner &&
				task.Step != TaskStepRemoveVoter &&
				task.Step != TaskStepCommitAssignment {
				return invalid("slot replica move task step is invalid")
			}
			assignment, ok := assignments[task.SlotID]
			if !ok {
				return invalid("slot replica move task requires slot assignment")
			}
			if task.SourceNode == 0 || task.TargetNode == 0 {
				return invalid("slot replica move source and target must be non-zero")
			}
			if task.SourceNode == task.TargetNode {
				return invalid("slot replica move source and target must differ")
			}
			if task.ConfigEpoch != assignment.ConfigEpoch {
				return invalid("slot replica move config_epoch must match assignment")
			}
			if !containsUint64(assignment.DesiredPeers, task.SourceNode) {
				return invalid("slot replica move source must be a desired peer")
			}
			if containsUint64(assignment.DesiredPeers, task.TargetNode) {
				return invalid("slot replica move target must not already be a desired peer")
			}
			target, ok := nodes[task.TargetNode]
			if !ok || target.JoinState != NodeJoinStateActive || !target.HasRole(NodeRoleData) {
				return invalid("slot replica move target must be an active data node")
			}
			if !reflect.DeepEqual(task.TargetPeers, replacePeer(assignment.DesiredPeers, task.SourceNode, task.TargetNode)) {
				return invalid("slot replica move target peers must replace source with target")
			}
			if task.CompletionPolicy != TaskCompletionPolicySingleObserver {
				return invalid("slot replica move completion_policy must be single_observer")
			}
			if len(task.ParticipantProgress) != 0 {
				return invalid("slot replica move task must not have participant progress")
			}
			if hasDuplicateUint64(task.ObservedVoters) || hasDuplicateUint64(task.ObservedLearners) {
				return invalid("slot replica move observed sets must be unique")
			}
		default:
			return invalid("unknown task kind")
		}
	}
	return nil
}

func validateNodeHealthReports(reports []NodeHealthReport, nodes map[uint64]Node) error {
	seen := make(map[uint64]struct{}, len(reports))
	for _, report := range reports {
		if report.NodeID == 0 {
			return invalid("node health report node_id must be non-zero")
		}
		if _, exists := seen[report.NodeID]; exists {
			return invalid("duplicate node health report")
		}
		seen[report.NodeID] = struct{}{}
		if _, ok := nodes[report.NodeID]; !ok {
			return invalid("node health report must reference a node")
		}
		if !validNodeStatus(report.Status) {
			return invalid("unknown node health report status")
		}
		if report.ReportedAtUnixMilli < 0 {
			return invalid("node health report reported_at_unix_milli must not be negative")
		}
		if len(report.ErrorCode) > 128 {
			return invalid("node health report error_code is too long")
		}
	}
	return nil
}

func validNodeStatus(status NodeStatus) bool {
	return status == NodeStatusAlive || status == NodeStatusSuspect || status == NodeStatusDown
}

func containsUint64(items []uint64, want uint64) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func replacePeer(peers []uint64, source uint64, target uint64) []uint64 {
	out := append([]uint64(nil), peers...)
	for i, peer := range out {
		if peer == source {
			out[i] = target
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func hasDuplicateUint64(items []uint64) bool {
	seen := make(map[uint64]struct{}, len(items))
	for _, item := range items {
		if item == 0 {
			return true
		}
		if _, exists := seen[item]; exists {
			return true
		}
		seen[item] = struct{}{}
	}
	return false
}

func validateParticipantProgress(task ReconcileTask) error {
	if task.CompletionPolicy == TaskCompletionPolicySingleObserver {
		if len(task.ParticipantProgress) != 0 {
			return invalid("single_observer task must not have participant progress")
		}
		return nil
	}
	if task.CompletionPolicy != TaskCompletionPolicyAllTargetPeers {
		return invalid("unknown task completion_policy")
	}
	if len(task.ParticipantProgress) != len(task.TargetPeers) {
		return invalid("all_target_peers task progress must match target peers")
	}
	targets := make(map[uint64]struct{}, len(task.TargetPeers))
	for _, peerID := range task.TargetPeers {
		targets[peerID] = struct{}{}
	}
	seen := make(map[uint64]struct{}, len(task.ParticipantProgress))
	for _, progress := range task.ParticipantProgress {
		if progress.NodeID == 0 {
			return invalid("task participant node_id must be non-zero")
		}
		if _, ok := targets[progress.NodeID]; !ok {
			return invalid("task participant must be a target peer")
		}
		if _, exists := seen[progress.NodeID]; exists {
			return invalid("duplicate task participant")
		}
		seen[progress.NodeID] = struct{}{}
		switch progress.Status {
		case TaskParticipantStatusPending, TaskParticipantStatusDone, TaskParticipantStatusFailed:
		default:
			return invalid("unknown task participant status")
		}
	}
	return nil
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, reason)
}
