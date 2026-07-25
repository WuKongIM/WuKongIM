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
	return nil
}

func validateRestore(restore *RestoreCoordinationState, hashSlotCount uint16) error {
	if restore == nil || restore.Plan == nil {
		return nil
	}
	plan := restore.Plan
	if !validBackupIdentity(plan.ID) || !validBackupIdentity(plan.RestorePointID) || !validSHA256(plan.ManifestSHA256) {
		return invalid("restore plan identity is invalid")
	}
	if plan.Repository != "primary" && plan.Repository != "secondary" {
		return invalid("restore repository selector is invalid")
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
	case RestoreStatusPlanned, RestoreStatusInstalling, RestoreStatusInstalled, RestoreStatusVerified, RestoreStatusActivated, RestoreStatusAbandoned:
	default:
		return invalid("restore status is invalid")
	}
	if plan.CreatedAtUnixMillis <= 0 || plan.UpdatedAtUnixMillis < plan.CreatedAtUnixMillis || plan.VerifiedAtUnixMillis < 0 || plan.ActivatedAtUnixMillis < 0 {
		return invalid("restore timestamps are invalid")
	}
	if (plan.Status == RestoreStatusVerified || plan.Status == RestoreStatusActivated) && plan.VerifiedAtUnixMillis <= 0 {
		return invalid("verified restore has no verification timestamp")
	}
	if plan.Status == RestoreStatusActivated && (plan.ActivatedAtUnixMillis <= 0 || !validSHA256(plan.ActivationFenceDigest)) {
		return invalid("activated restore has no fencing evidence")
	}
	for index, partition := range plan.Partitions {
		if partition.HashSlot != uint16(index) || len(partition.FailureCategory) > 128 || partition.UpdatedAtUnixMillis < 0 {
			return invalid("restore partition progress is invalid")
		}
		if partition.Verified && !partition.Installed {
			return invalid("restore partition verified before installation")
		}
		if partition.Installed && partition.EvidenceVersion == 0 {
			return invalid("installed restore partition has no evidence version")
		}
		if partition.Installed && (partition.MessageCount == 0) != (partition.MaxMessageID == 0) {
			return invalid("installed restore partition message evidence is inconsistent")
		}
		if partition.Installed && !validSHA256(partition.MetadataSHA256) {
			return invalid("installed restore partition metadata digest is invalid")
		}
	}
	return nil
}

func validateBackup(backup *BackupCoordinationState, hashSlotCount uint16) error {
	if backup == nil {
		return nil
	}
	if len(backup.RestorePoints)+len(backup.PendingGarbage) > MaxBackupRestorePoints {
		return invalid("backup restore-point references exceed limit")
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
			!validSHA256(head.SHA256) || head.Bytes <= 0 || head.Bytes > 1<<20 {
			return invalid("backup checkpoint catalog head is invalid")
		}
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
	if backup.Active != nil {
		job := backup.Active
		if job.ID == "" || job.Epoch == 0 || job.Epoch > backup.LastEpoch {
			return invalid("backup active job identity or epoch is invalid")
		}
		if !validBackupKind(job.Kind) || !validBackupJobStatus(job.Status) {
			return invalid("backup active job kind or status is invalid")
		}
		if job.HashSlotCount != hashSlotCount {
			return invalid("backup active job hash_slot_count must match cluster config")
		}
		if !validSHA256(job.ConfigFingerprint) {
			return invalid("backup config fingerprint must be sha256 hex")
		}
		if !validBackupIdentity(job.RestorePointID) || (job.BaseRestorePointID != "" && !validBackupIdentity(job.BaseRestorePointID)) {
			return invalid("backup restore-point publication fence is invalid")
		}
		if job.StartedAtUnixMillis <= 0 || job.UpdatedAtUnixMillis < job.StartedAtUnixMillis {
			return invalid("backup active job timestamps are invalid")
		}
		if len(job.FailureCategory) > 128 {
			return invalid("backup failure category exceeds limit")
		}
		if len(job.Partitions) > int(hashSlotCount) {
			return invalid("backup partition reports exceed hash_slot_count")
		}
		for index, report := range job.Partitions {
			if index > 0 && job.Partitions[index-1].HashSlot >= report.HashSlot {
				return invalid("backup partition reports must be unique and sorted")
			}
			if report.JobID != job.ID || report.BackupEpoch != job.Epoch || report.HashSlot >= hashSlotCount {
				return invalid("backup partition report fence is invalid")
			}
			if report.RaftIndex == 0 || report.CommittedAtUnixMillis <= 0 || report.ObjectCount == 0 || report.CiphertextBytes == 0 {
				return invalid("backup partition report boundary is invalid")
			}
			if !validBackupObjectKey(report.ManifestKey) || !validSHA256(report.ManifestSHA256) {
				return invalid("backup partition manifest reference is invalid")
			}
		}
	}
	if backup.Verification != nil {
		task := backup.Verification
		if !validBackupIdentity(task.ID) || !validBackupIdentity(task.RestorePointID) || !validBackupVerificationEvidence(task.BackupVerificationEvidence) {
			return invalid("backup verification task is invalid")
		}
	}
	if backup.Active != nil && backup.Verification != nil &&
		(backup.Verification.Status == BackupVerificationTaskStatusPending ||
			backup.Verification.Status == BackupVerificationTaskStatusRunning) {
		return invalid("backup and verification tasks cannot both be active")
	}
	seenIDs := make(map[string]struct{}, len(backup.RestorePoints)+len(backup.PendingGarbage))
	verificationTargetFound := backup.Verification == nil
	for _, restorePoint := range append(cloneSlice(backup.RestorePoints), backup.PendingGarbage...) {
		if restorePoint.ID == "" || restorePoint.JobID == "" || restorePoint.BackupEpoch == 0 || restorePoint.BackupEpoch > backup.LastEpoch {
			return invalid("backup restore-point identity or epoch is invalid")
		}
		if _, exists := seenIDs[restorePoint.ID]; exists {
			return invalid("duplicate backup restore-point id")
		}
		seenIDs[restorePoint.ID] = struct{}{}
		if !validBackupKind(restorePoint.Kind) || restorePoint.EffectiveAtUnixMillis <= 0 || restorePoint.CreatedAtUnixMillis < restorePoint.EffectiveAtUnixMillis {
			return invalid("backup restore-point kind or timestamps are invalid")
		}
		if !validSHA256(restorePoint.ManifestSHA256) || !restorePoint.PrimaryVerified || !restorePoint.SecondaryVerified {
			return invalid("backup restore-point is not verified in both repositories")
		}
		if restorePoint.LastVerification != nil && !validBackupVerificationEvidence(*restorePoint.LastVerification) {
			return invalid("backup restore-point verification evidence is invalid")
		}
		if backup.Verification != nil && restorePoint.ID == backup.Verification.RestorePointID {
			verificationTargetFound = true
			if restorePoint.LastVerification == nil || *restorePoint.LastVerification != backup.Verification.BackupVerificationEvidence {
				return invalid("backup verification task evidence does not match restore point")
			}
		}
	}
	if !verificationTargetFound {
		return invalid("backup verification target is missing")
	}
	return nil
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
		"generation_bytes", "generation_segments", "generation_age":
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

func validBackupVerificationEvidence(evidence BackupVerificationEvidence) bool {
	if evidence.StartedAtUnixMillis <= 0 || len(evidence.FailureCategory) > 128 {
		return false
	}
	switch evidence.Status {
	case BackupVerificationTaskStatusPending, BackupVerificationTaskStatusRunning:
		return evidence.CompletedAtUnixMillis == 0 && evidence.ManifestSHA256 == "" && evidence.FailureCategory == ""
	case BackupVerificationTaskStatusSucceeded:
		return evidence.CompletedAtUnixMillis >= evidence.StartedAtUnixMillis &&
			evidence.PrimaryVerified && evidence.SecondaryVerified &&
			validSHA256(evidence.ManifestSHA256) && evidence.FailureCategory == ""
	case BackupVerificationTaskStatusFailed:
		return evidence.CompletedAtUnixMillis >= evidence.StartedAtUnixMillis &&
			(evidence.ManifestSHA256 == "" || validSHA256(evidence.ManifestSHA256)) &&
			evidence.FailureCategory != ""
	default:
		return false
	}
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

func validBackupKind(kind BackupRestorePointKind) bool {
	return kind == BackupRestorePointKindIncremental || kind == BackupRestorePointKindSyntheticFull || kind == BackupRestorePointKindMaterializedFull
}

func validBackupJobStatus(status BackupJobStatus) bool {
	return status == BackupJobStatusPreparing || status == BackupJobStatusCapturing || status == BackupJobStatusPublishing || status == BackupJobStatusDegraded || status == BackupJobStatusFailed
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
