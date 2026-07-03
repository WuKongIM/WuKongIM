//go:build e2e && legacy_e2e

package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// SlotTopology describes the externally observed slot leader/follower layout.
type SlotTopology struct {
	SlotID          uint32
	LeaderNodeID    uint64
	FollowerNodeIDs []uint64
	RawBody         string
}

// ManagerSlotDetail mirrors the subset of the manager slot detail response used by e2e.
type ManagerSlotDetail struct {
	SlotID  uint32           `json:"slot_id"`
	State   managerSlotState `json:"state"`
	Runtime managerSlotRun   `json:"runtime"`
}

type managerSlotState struct {
	Quorum string `json:"quorum"`
	Sync   string `json:"sync"`
}

type managerSlotRun struct {
	LeaderID     uint64   `json:"leader_id"`
	CurrentPeers []uint64 `json:"current_peers"`
	HasQuorum    bool     `json:"has_quorum"`
}

type managerConnectionsResponse struct {
	Items []ManagerConnection `json:"items"`
}

// ManagerNodesResponse mirrors the manager node list response used by e2e.
type ManagerNodesResponse struct {
	Total int           `json:"total"`
	Items []ManagerNode `json:"items"`
}

// ManagerNode mirrors the manager node fields used by e2e assertions.
type ManagerNode struct {
	NodeID          uint64                `json:"node_id"`
	Addr            string                `json:"addr"`
	Status          string                `json:"status"`
	LastHeartbeatAt time.Time             `json:"last_heartbeat_at"`
	IsLocal         bool                  `json:"is_local"`
	CapacityWeight  int                   `json:"capacity_weight"`
	Controller      ManagerNodeController `json:"controller"`
	SlotStats       ManagerNodeSlotStats  `json:"slot_stats"`
}

// ManagerNodeController contains controller role fields from /manager/nodes.
type ManagerNodeController struct {
	Role string `json:"role"`
}

// ManagerNodeSlotStats contains slot hosting counts from /manager/nodes.
type ManagerNodeSlotStats struct {
	Count       int `json:"count"`
	LeaderCount int `json:"leader_count"`
}

// ManagerSlotRaftCompactionResponse mirrors the manager Slot compaction response.
type ManagerSlotRaftCompactionResponse struct {
	Total     int                             `json:"total"`
	Succeeded int                             `json:"succeeded"`
	Failed    int                             `json:"failed"`
	Items     []ManagerSlotRaftCompactionItem `json:"items"`
}

// ManagerSlotRaftCompactionItem mirrors one node-local Slot compaction result.
type ManagerSlotRaftCompactionItem struct {
	NodeID              uint64 `json:"node_id"`
	SlotID              uint32 `json:"slot_id"`
	Success             bool   `json:"success"`
	AppliedIndex        uint64 `json:"applied_index"`
	BeforeSnapshotIndex uint64 `json:"before_snapshot_index"`
	AfterSnapshotIndex  uint64 `json:"after_snapshot_index"`
	Compacted           bool   `json:"compacted"`
	SkippedReason       string `json:"skipped_reason"`
	Error               string `json:"error"`
}

// ManagerControllerRaftStatus mirrors the manager Controller Raft status response.
type ManagerControllerRaftStatus struct {
	NodeID        uint64                              `json:"node_id"`
	Role          string                              `json:"role"`
	LeaderID      uint64                              `json:"leader_id"`
	Term          uint64                              `json:"term"`
	Health        string                              `json:"health"`
	FirstIndex    uint64                              `json:"first_index"`
	LastIndex     uint64                              `json:"last_index"`
	CommitIndex   uint64                              `json:"commit_index"`
	AppliedIndex  uint64                              `json:"applied_index"`
	SnapshotIndex uint64                              `json:"snapshot_index"`
	SnapshotTerm  uint64                              `json:"snapshot_term"`
	Compaction    ManagerControllerRaftCompactionInfo `json:"compaction"`
	Restore       ManagerControllerRaftRestoreInfo    `json:"restore"`
	Peers         []ManagerControllerRaftPeer         `json:"peers"`
}

// ManagerControllerRaftCompactionInfo mirrors local Controller Raft compaction status.
type ManagerControllerRaftCompactionInfo struct {
	Enabled           bool      `json:"enabled"`
	TriggerEntries    uint64    `json:"trigger_entries"`
	CheckIntervalMs   int64     `json:"check_interval_ms"`
	LastSnapshotIndex uint64    `json:"last_snapshot_index"`
	LastSnapshotAt    time.Time `json:"last_snapshot_at"`
	LastCheckAt       time.Time `json:"last_check_at"`
	LastError         string    `json:"last_error"`
	LastErrorAt       time.Time `json:"last_error_at"`
	Degraded          bool      `json:"degraded"`
}

// ManagerControllerRaftRestoreInfo mirrors local Controller snapshot restore status.
type ManagerControllerRaftRestoreInfo struct {
	LastSnapshotIndex uint64    `json:"last_snapshot_index"`
	LastSnapshotTerm  uint64    `json:"last_snapshot_term"`
	LastRestoredAt    time.Time `json:"last_restored_at"`
	LastError         string    `json:"last_error"`
	LastErrorAt       time.Time `json:"last_error_at"`
	Failed            bool      `json:"failed"`
}

// ManagerControllerRaftPeer mirrors one leader-side Controller follower progress row.
type ManagerControllerRaftPeer struct {
	NodeID               uint64 `json:"node_id"`
	Match                uint64 `json:"match"`
	Next                 uint64 `json:"next"`
	State                string `json:"state"`
	PendingSnapshot      uint64 `json:"pending_snapshot"`
	RecentActive         bool   `json:"recent_active"`
	NeedsSnapshot        bool   `json:"needs_snapshot"`
	SnapshotTransferring bool   `json:"snapshot_transferring"`
}

// ManagerControllerRaftCompactionResponse mirrors the manager Controller compaction response.
type ManagerControllerRaftCompactionResponse struct {
	Total     int                                   `json:"total"`
	Succeeded int                                   `json:"succeeded"`
	Failed    int                                   `json:"failed"`
	Items     []ManagerControllerRaftCompactionItem `json:"items"`
}

// ManagerControllerRaftCompactionItem mirrors one node-local Controller compaction result.
type ManagerControllerRaftCompactionItem struct {
	NodeID              uint64 `json:"node_id"`
	Success             bool   `json:"success"`
	AppliedIndex        uint64 `json:"applied_index"`
	BeforeSnapshotIndex uint64 `json:"before_snapshot_index"`
	AfterSnapshotIndex  uint64 `json:"after_snapshot_index"`
	Compacted           bool   `json:"compacted"`
	SkippedReason       string `json:"skipped_reason"`
	Error               string `json:"error"`
}

// ManagerNodeOnboardingCandidatesResponse mirrors the manager onboarding candidate list.
type ManagerNodeOnboardingCandidatesResponse struct {
	Total int                              `json:"total"`
	Items []ManagerNodeOnboardingCandidate `json:"items"`
}

// ManagerNodeOnboardingCandidate mirrors the manager onboarding candidate node DTO.
type ManagerNodeOnboardingCandidate struct {
	NodeID      uint64 `json:"node_id"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Role        string `json:"role"`
	JoinState   string `json:"join_state"`
	Status      string `json:"status"`
	SlotCount   int    `json:"slot_count"`
	LeaderCount int    `json:"leader_count"`
	Recommended bool   `json:"recommended"`
}

// ManagerNodeOnboardingJob mirrors the manager onboarding job DTO used by e2e.
type ManagerNodeOnboardingJob struct {
	JobID        string                      `json:"job_id"`
	TargetNodeID uint64                      `json:"target_node_id"`
	Status       string                      `json:"status"`
	Plan         ManagerNodeOnboardingPlan   `json:"plan"`
	Moves        []ManagerNodeOnboardingMove `json:"moves"`
	ResultCounts ManagerNodeOnboardingCounts `json:"result_counts"`
	LastError    string                      `json:"last_error"`
}

// ManagerNodeOnboardingPlan mirrors the reviewed onboarding plan.
type ManagerNodeOnboardingPlan struct {
	TargetNodeID   uint64                               `json:"target_node_id"`
	Summary        ManagerNodeOnboardingPlanSummary     `json:"summary"`
	Moves          []ManagerNodeOnboardingPlanMove      `json:"moves"`
	BlockedReasons []ManagerNodeOnboardingBlockedReason `json:"blocked_reasons"`
}

// ManagerNodeOnboardingPlanSummary mirrors aggregate plan load effects.
type ManagerNodeOnboardingPlanSummary struct {
	CurrentTargetSlotCount   int `json:"current_target_slot_count"`
	PlannedTargetSlotCount   int `json:"planned_target_slot_count"`
	CurrentTargetLeaderCount int `json:"current_target_leader_count"`
	PlannedLeaderGain        int `json:"planned_leader_gain"`
}

// ManagerNodeOnboardingPlanMove mirrors one planned Slot move.
type ManagerNodeOnboardingPlanMove struct {
	SlotID                 uint32   `json:"slot_id"`
	SourceNodeID           uint64   `json:"source_node_id"`
	TargetNodeID           uint64   `json:"target_node_id"`
	DesiredPeersBefore     []uint64 `json:"desired_peers_before"`
	DesiredPeersAfter      []uint64 `json:"desired_peers_after"`
	LeaderTransferRequired bool     `json:"leader_transfer_required"`
}

// ManagerNodeOnboardingBlockedReason mirrors one planner blocked reason.
type ManagerNodeOnboardingBlockedReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ManagerNodeOnboardingMove mirrors one durable execution move.
type ManagerNodeOnboardingMove struct {
	SlotID    uint32 `json:"slot_id"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

// ManagerNodeOnboardingCounts mirrors job result counters.
type ManagerNodeOnboardingCounts struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

// ManagerConnection mirrors the subset of the manager connections response used by e2e.
type ManagerConnection struct {
	UID string `json:"uid"`
}

// ManagerChannelRuntimeMetaDetail mirrors channel runtime metadata used by channel-cluster E2E assertions.
type ManagerChannelRuntimeMetaDetail struct {
	ChannelID     string   `json:"channel_id"`
	ChannelType   int64    `json:"channel_type"`
	SlotID        uint32   `json:"slot_id"`
	HashSlot      uint32   `json:"hash_slot"`
	ChannelEpoch  uint64   `json:"channel_epoch"`
	LeaderEpoch   uint64   `json:"leader_epoch"`
	Leader        uint64   `json:"leader"`
	Replicas      []uint64 `json:"replicas"`
	ISR           []uint64 `json:"isr"`
	MinISR        int      `json:"min_isr"`
	MaxMessageSeq uint64   `json:"max_message_seq"`
	Status        string   `json:"status"`
	Features      uint64   `json:"features"`
	LeaseUntilMS  int64    `json:"lease_until_ms"`
}

// ManagerChannelClusterReplicaDetail mirrors /manager/channel-cluster/:type/:id/replicas.
type ManagerChannelClusterReplicaDetail struct {
	Channel             ManagerChannelRuntimeMetaDetail      `json:"channel"`
	RuntimeReported     bool                                 `json:"runtime_reported"`
	CommitSeq           *uint64                              `json:"commit_seq"`
	MinAvailableSeq     *uint64                              `json:"min_available_seq"`
	RetentionThroughSeq *uint64                              `json:"retention_through_seq"`
	Replicas            []ManagerChannelClusterReplicaStatus `json:"replicas"`
}

// ManagerChannelClusterReplicaStatus mirrors one channel-cluster replica row.
type ManagerChannelClusterReplicaStatus struct {
	NodeID       uint64  `json:"node_id"`
	Role         string  `json:"role"`
	IsLeader     bool    `json:"is_leader"`
	InISR        bool    `json:"in_isr"`
	Reported     bool    `json:"reported"`
	CommitSeq    *uint64 `json:"commit_seq"`
	LEO          *uint64 `json:"leo"`
	CheckpointHW *uint64 `json:"checkpoint_hw"`
	Lag          *uint64 `json:"lag"`
}

// ManagerChannelLeaderTransferResponse mirrors the manager leader transfer response.
type ManagerChannelLeaderTransferResponse struct {
	Changed bool                            `json:"changed"`
	Channel ManagerChannelRuntimeMetaDetail `json:"channel"`
}

// ManagerChannelMigrationDetail mirrors the manager channel migration detail response.
type ManagerChannelMigrationDetail struct {
	// TaskID identifies one active channel migration task.
	TaskID string `json:"task_id"`
	// Kind is the stable migration kind.
	Kind string `json:"kind"`
	// Status is the stable task lifecycle status.
	Status string `json:"status"`
	// Phase is the current resumable executor phase.
	Phase string `json:"phase"`
	// ChannelID identifies the migrated channel.
	ChannelID string `json:"channel_id"`
	// ChannelType identifies the channel namespace.
	ChannelType int64 `json:"channel_type"`
	// SourceNode is the leader or replica being drained.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the target leader or replacement replica.
	TargetNode uint64 `json:"target_node"`
	// DesiredLeader is the requested leader after migration.
	DesiredLeader uint64 `json:"desired_leader"`
	// BaseChannelEpoch is the epoch captured when the task was created.
	BaseChannelEpoch uint64 `json:"base_channel_epoch"`
	// BaseLeaderEpoch is the leader epoch captured when the task was created.
	BaseLeaderEpoch uint64 `json:"base_leader_epoch"`
	// CurrentChannelEpoch is the latest authoritative channel epoch.
	CurrentChannelEpoch uint64 `json:"current_channel_epoch"`
	// CurrentLeaderEpoch is the latest authoritative leader epoch.
	CurrentLeaderEpoch uint64 `json:"current_leader_epoch"`
	// LeaderLEO is the latest observed leader log end offset.
	LeaderLEO uint64 `json:"leader_leo"`
	// LeaderHW is the latest observed leader high watermark.
	LeaderHW uint64 `json:"leader_hw"`
	// TargetLEO is the latest observed target log end offset.
	TargetLEO uint64 `json:"target_leo"`
	// TargetCheckpointHW is the latest target checkpoint high watermark.
	TargetCheckpointHW uint64 `json:"target_checkpoint_hw"`
	// LagRecords is the latest observed leader-to-target record gap.
	LagRecords uint64 `json:"lag_records"`
	// StableSinceMS records when the current catch-up proof became stable.
	StableSinceMS int64 `json:"stable_since_ms"`
	// FenceActive reports whether a migration write fence is active.
	FenceActive bool `json:"fence_active"`
	// FenceUntilMS is the write-fence lease deadline.
	FenceUntilMS int64 `json:"fence_until_ms"`
	// FenceReason is the raw runtime write-fence reason.
	FenceReason uint8 `json:"fence_reason"`
	// BlockerCode is a stable blocker code when the task is blocked.
	BlockerCode string `json:"blocker_code"`
	// BlockerMessage is the human-readable blocker detail.
	BlockerMessage string `json:"blocker_message"`
	// Attempt is the durable retry counter.
	Attempt uint32 `json:"attempt"`
	// NextRunAtMS is the next executor wake-up timestamp.
	NextRunAtMS int64 `json:"next_run_at_ms"`
	// LastError is the latest retryable or terminal error.
	LastError string `json:"last_error"`
	// CreatedAtMS is the task creation timestamp.
	CreatedAtMS int64 `json:"created_at_ms"`
	// UpdatedAtMS is the latest task update timestamp.
	UpdatedAtMS int64 `json:"updated_at_ms"`
	// CompletedAtMS is set for terminal tasks.
	CompletedAtMS int64 `json:"completed_at_ms"`
}

// ManagerNodeScaleInPlanRequest carries operator confirmation for scale-in plan/start APIs.
type ManagerNodeScaleInPlanRequest struct {
	// ConfirmStatefulSetTail confirms the target is the external StatefulSet tail node.
	ConfirmStatefulSetTail bool `json:"confirm_statefulset_tail"`
	// ExpectedTailNodeID is the node ID the operator expects to remove.
	ExpectedTailNodeID uint64 `json:"expected_tail_node_id"`
}

// ManagerAdvanceNodeScaleInRequest controls one bounded manager scale-in advance step.
type ManagerAdvanceNodeScaleInRequest struct {
	// MaxLeaderTransfers limits Slot leader transfer attempts in one call.
	MaxLeaderTransfers int `json:"max_leader_transfers"`
	// MaxChannelMigrations limits channel migration task creation in one call.
	MaxChannelMigrations int `json:"max_channel_migrations"`
	// ForceCloseConnections is reserved for explicit operator-driven session closure.
	ForceCloseConnections bool `json:"force_close_connections"`
}

// ManagerNodeScaleInReport mirrors the manager scale-in report used by e2e.
type ManagerNodeScaleInReport struct {
	// NodeID is the target data node being evaluated.
	NodeID uint64 `json:"node_id"`
	// Status is the manager-computed scale-in phase.
	Status string `json:"status"`
	// SafeToRemove is true only when the manager reports ready_to_remove.
	SafeToRemove bool `json:"safe_to_remove"`
	// CanStart reports whether the target can enter the draining state.
	CanStart bool `json:"can_start"`
	// CanAdvance reports whether a bounded advance call can progress the phase.
	CanAdvance bool `json:"can_advance"`
	// CanCancel reports whether the draining target can be resumed.
	CanCancel bool `json:"can_cancel"`
	// ConnectionSafetyVerified reports whether runtime session counters are known.
	ConnectionSafetyVerified bool `json:"connection_safety_verified"`
	// BlockedReasons lists stable manager blockers for diagnostics.
	BlockedReasons []ManagerNodeScaleInBlockedReason `json:"blocked_reasons"`
	// Checks exposes individual scale-in safety checks.
	Checks ManagerNodeScaleInChecks `json:"checks"`
	// Progress exposes live drain counters.
	Progress ManagerNodeScaleInProgress `json:"progress"`
	// Runtime exposes node-local runtime session counters.
	Runtime ManagerNodeScaleInRuntimeSummary `json:"runtime"`
	// NextAction describes the next operator or manager action.
	NextAction string `json:"next_action"`
}

// ManagerNodeScaleInChecks mirrors stable safety check booleans from the manager API.
type ManagerNodeScaleInChecks struct {
	TargetExists                             bool `json:"target_exists"`
	TargetIsDataNode                         bool `json:"target_is_data_node"`
	TargetIsActiveOrDraining                 bool `json:"target_is_active_or_draining"`
	TargetIsNotControllerVoter               bool `json:"target_is_not_controller_voter"`
	TailNodeMappingVerified                  bool `json:"tail_node_mapping_verified"`
	RemainingDataNodesEnough                 bool `json:"remaining_data_nodes_enough"`
	ControllerLeaderAvailable                bool `json:"controller_leader_available"`
	SlotReplicaCountKnown                    bool `json:"slot_replica_count_known"`
	NoOtherDrainingNode                      bool `json:"no_other_draining_node"`
	NoActiveHashslotMigrations               bool `json:"no_active_hashslot_migrations"`
	NoRunningOnboarding                      bool `json:"no_running_onboarding"`
	NoActiveReconcileTasksInvolvingTarget    bool `json:"no_active_reconcile_tasks_involving_target"`
	NoFailedReconcileTasks                   bool `json:"no_failed_reconcile_tasks"`
	RuntimeViewsCompleteAndFresh             bool `json:"runtime_views_complete_and_fresh"`
	AllSlotsHaveQuorum                       bool `json:"all_slots_have_quorum"`
	TargetNotUniqueHealthyReplica            bool `json:"target_not_unique_healthy_replica"`
	ChannelInventoryAvailable                bool `json:"channel_inventory_available"`
	NoActiveChannelMigrationsInvolvingTarget bool `json:"no_active_channel_migrations_involving_target"`
	NoChannelLeadersOnTarget                 bool `json:"no_channel_leaders_on_target"`
	NoChannelReplicasOnTarget                bool `json:"no_channel_replicas_on_target"`
}

// ManagerNodeScaleInProgress mirrors live drain counters from the manager API.
type ManagerNodeScaleInProgress struct {
	AssignedSlotReplicas                 int    `json:"assigned_slot_replicas"`
	ObservedSlotReplicas                 int    `json:"observed_slot_replicas"`
	SlotLeaders                          int    `json:"slot_leaders"`
	ActiveTasksInvolvingNode             int    `json:"active_tasks_involving_node"`
	ActiveMigrationsInvolvingNode        int    `json:"active_migrations_involving_node"`
	ChannelLeaders                       int    `json:"channel_leaders"`
	ChannelReplicas                      int    `json:"channel_replicas"`
	ActiveChannelMigrationsInvolvingNode int    `json:"active_channel_migrations_involving_node"`
	ActiveConnections                    int    `json:"active_connections"`
	ClosingConnections                   int    `json:"closing_connections"`
	GatewaySessions                      int    `json:"gateway_sessions"`
	ActiveConnectionsUnknown             bool   `json:"active_connections_unknown"`
	ChannelInventoryScanned              bool   `json:"channel_inventory_scanned"`
	ChannelInventoryPartial              bool   `json:"channel_inventory_partial"`
	ChannelInventoryError                string `json:"channel_inventory_error"`
}

// ManagerNodeScaleInRuntimeSummary mirrors runtime session counters used for scale-in safety.
type ManagerNodeScaleInRuntimeSummary struct {
	NodeID               uint64         `json:"node_id"`
	ActiveOnline         int            `json:"active_online"`
	ClosingOnline        int            `json:"closing_online"`
	TotalOnline          int            `json:"total_online"`
	GatewaySessions      int            `json:"gateway_sessions"`
	SessionsByListener   map[string]int `json:"sessions_by_listener"`
	AcceptingNewSessions bool           `json:"accepting_new_sessions"`
	Draining             bool           `json:"draining"`
	Unknown              bool           `json:"unknown"`
}

// ManagerNodeScaleInBlockedReason mirrors one stable scale-in blocker.
type ManagerNodeScaleInBlockedReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	SlotID  uint32 `json:"slot_id"`
	NodeID  uint64 `json:"node_id"`
}

// FetchNodeOnboardingCandidates fetches manager onboarding candidates from the started node.
func FetchNodeOnboardingCandidates(ctx context.Context, node StartedNode) (ManagerNodeOnboardingCandidatesResponse, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, "/manager/node-onboarding/candidates")
	if err != nil {
		return ManagerNodeOnboardingCandidatesResponse{}, nil, err
	}

	resp, err := decodeNodeOnboardingCandidatesResponse(body)
	if err != nil {
		return ManagerNodeOnboardingCandidatesResponse{}, body, err
	}
	return resp, body, nil
}

// CreateNodeOnboardingPlan creates a manager-reviewed onboarding plan for targetNodeID.
func CreateNodeOnboardingPlan(ctx context.Context, node StartedNode, targetNodeID uint64) (ManagerNodeOnboardingJob, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, "/manager/node-onboarding/plan", map[string]uint64{"target_node_id": targetNodeID})
	if err != nil {
		return ManagerNodeOnboardingJob{}, nil, err
	}

	job, err := decodeNodeOnboardingJobResponse(body)
	if err != nil {
		return ManagerNodeOnboardingJob{}, body, err
	}
	return job, body, nil
}

// StartNodeOnboardingJob starts a planned onboarding job through the manager API.
func StartNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string) (ManagerNodeOnboardingJob, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/node-onboarding/jobs/%s/start", jobID), nil)
	if err != nil {
		return ManagerNodeOnboardingJob{}, nil, err
	}

	job, err := decodeNodeOnboardingJobResponse(body)
	if err != nil {
		return ManagerNodeOnboardingJob{}, body, err
	}
	return job, body, nil
}

// FetchNodeOnboardingJob fetches one onboarding job through the manager API.
func FetchNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string) (ManagerNodeOnboardingJob, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/node-onboarding/jobs/%s", jobID))
	if err != nil {
		return ManagerNodeOnboardingJob{}, nil, err
	}

	job, err := decodeNodeOnboardingJobResponse(body)
	if err != nil {
		return ManagerNodeOnboardingJob{}, body, err
	}
	return job, body, nil
}

// WaitForNodeOnboardingJob waits until a manager onboarding job satisfies accept.
func WaitForNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string, accept func(ManagerNodeOnboardingJob) bool) (ManagerNodeOnboardingJob, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastErr  error
		lastBody []byte
	)
	for {
		job, body, err := FetchNodeOnboardingJob(ctx, node, jobID)
		if err == nil {
			lastBody = body
			if accept == nil || accept(job) {
				return job, body, nil
			}
			lastErr = fmt.Errorf("onboarding job %s did not satisfy expected state", jobID)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return ManagerNodeOnboardingJob{}, lastBody, lastErr
			}
			return ManagerNodeOnboardingJob{}, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeNodeOnboardingCandidatesResponse(body []byte) (ManagerNodeOnboardingCandidatesResponse, error) {
	var resp ManagerNodeOnboardingCandidatesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerNodeOnboardingCandidatesResponse{}, err
	}
	return resp, nil
}

func decodeNodeOnboardingJobResponse(body []byte) (ManagerNodeOnboardingJob, error) {
	var job ManagerNodeOnboardingJob
	if err := json.Unmarshal(body, &job); err != nil {
		return ManagerNodeOnboardingJob{}, err
	}
	return job, nil
}

// CompactSlotRaftLog triggers node-local Slot Raft compaction through manager API.
func CompactSlotRaftLog(ctx context.Context, node StartedNode, targetNodeID uint64, slotID uint32) (ManagerSlotRaftCompactionResponse, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/slots/%d/compact", targetNodeID, slotID), nil)
	if err != nil {
		return ManagerSlotRaftCompactionResponse{}, nil, err
	}

	resp, err := decodeSlotRaftCompactionResponse(body)
	if err != nil {
		return ManagerSlotRaftCompactionResponse{}, body, err
	}
	return resp, body, nil
}

func decodeSlotRaftCompactionResponse(body []byte) (ManagerSlotRaftCompactionResponse, error) {
	var resp ManagerSlotRaftCompactionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerSlotRaftCompactionResponse{}, err
	}
	return resp, nil
}

// FetchControllerRaftStatus fetches one node-local Controller Raft status through manager API.
func FetchControllerRaftStatus(ctx context.Context, node StartedNode, targetNodeID uint64) (ManagerControllerRaftStatus, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/controller-raft", targetNodeID))
	if err != nil {
		return ManagerControllerRaftStatus{}, nil, err
	}

	resp, err := decodeControllerRaftStatusResponse(body)
	if err != nil {
		return ManagerControllerRaftStatus{}, body, err
	}
	return resp, body, nil
}

func decodeControllerRaftStatusResponse(body []byte) (ManagerControllerRaftStatus, error) {
	var resp ManagerControllerRaftStatus
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerControllerRaftStatus{}, err
	}
	return resp, nil
}

// CompactControllerRaftLog triggers node-local Controller Raft compaction through manager API.
func CompactControllerRaftLog(ctx context.Context, node StartedNode, targetNodeID uint64) (ManagerControllerRaftCompactionResponse, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/controller-raft/compact", targetNodeID), nil)
	if err != nil {
		return ManagerControllerRaftCompactionResponse{}, nil, err
	}

	resp, err := decodeControllerRaftCompactionResponse(body)
	if err != nil {
		return ManagerControllerRaftCompactionResponse{}, body, err
	}
	return resp, body, nil
}

func decodeControllerRaftCompactionResponse(body []byte) (ManagerControllerRaftCompactionResponse, error) {
	var resp ManagerControllerRaftCompactionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerControllerRaftCompactionResponse{}, err
	}
	return resp, nil
}

// FetchSlotDetail fetches one manager slot detail from the started node.
func FetchSlotDetail(ctx context.Context, node StartedNode, slotID uint32) (ManagerSlotDetail, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/slots/%d", slotID))
	if err != nil {
		return ManagerSlotDetail{}, nil, err
	}

	var detail ManagerSlotDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return ManagerSlotDetail{}, body, err
	}
	return detail, body, nil
}

// FetchChannelClusterReplicas fetches authoritative channel-cluster replica detail.
func FetchChannelClusterReplicas(ctx context.Context, node StartedNode, channelType uint8, channelID string) (ManagerChannelClusterReplicaDetail, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/channel-cluster/%d/%s/replicas", channelType, url.PathEscape(channelID)))
	if err != nil {
		return ManagerChannelClusterReplicaDetail{}, nil, err
	}

	detail, err := decodeChannelClusterReplicaDetail(body)
	if err != nil {
		return ManagerChannelClusterReplicaDetail{}, body, err
	}
	return detail, body, nil
}

// TransferChannelClusterLeader transfers one channel leader to a requested replica through manager APIs.
func TransferChannelClusterLeader(ctx context.Context, node StartedNode, channelType uint8, channelID string, targetNodeID uint64) (ManagerChannelLeaderTransferResponse, []byte, error) {
	body, err := postHTTPJSONBody(
		ctx,
		node.Spec.ManagerAddr,
		fmt.Sprintf("/manager/channel-cluster/%d/%s/leader/transfer", channelType, url.PathEscape(channelID)),
		map[string]uint64{"target_node_id": targetNodeID},
	)
	if err != nil {
		return ManagerChannelLeaderTransferResponse{}, nil, err
	}

	result, err := decodeChannelLeaderTransferResponse(body)
	if err != nil {
		return ManagerChannelLeaderTransferResponse{}, body, err
	}
	return result, body, nil
}

// FetchChannelMigration fetches active manager migration details for one channel.
func FetchChannelMigration(ctx context.Context, node StartedNode, channelType uint8, channelID string) (ManagerChannelMigrationDetail, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/channels/%d/%s/migration", channelType, url.PathEscape(channelID)))
	if err != nil {
		return ManagerChannelMigrationDetail{}, nil, err
	}

	detail, err := decodeChannelMigrationDetailResponse(body)
	if err != nil {
		return ManagerChannelMigrationDetail{}, body, err
	}
	return detail, body, nil
}

// WaitForChannelClusterReplicas waits until channel-cluster replica detail satisfies accept.
func WaitForChannelClusterReplicas(ctx context.Context, node StartedNode, channelType uint8, channelID string, accept func(ManagerChannelClusterReplicaDetail) bool) (ManagerChannelClusterReplicaDetail, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastDetail ManagerChannelClusterReplicaDetail
		lastErr    error
		lastBody   []byte
	)
	for {
		detail, body, err := FetchChannelClusterReplicas(ctx, node, channelType, channelID)
		if err == nil {
			lastDetail = detail
			lastBody = body
			if accept == nil || accept(detail) {
				return detail, body, nil
			}
			lastErr = fmt.Errorf("channel %d/%s replica detail did not satisfy expected state", channelType, channelID)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastDetail, lastBody, lastErr
			}
			return lastDetail, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeChannelClusterReplicaDetail(body []byte) (ManagerChannelClusterReplicaDetail, error) {
	var detail ManagerChannelClusterReplicaDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return ManagerChannelClusterReplicaDetail{}, err
	}
	return detail, nil
}

func decodeChannelLeaderTransferResponse(body []byte) (ManagerChannelLeaderTransferResponse, error) {
	var resp ManagerChannelLeaderTransferResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerChannelLeaderTransferResponse{}, err
	}
	return resp, nil
}

func decodeChannelMigrationDetailResponse(body []byte) (ManagerChannelMigrationDetail, error) {
	var detail ManagerChannelMigrationDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return ManagerChannelMigrationDetail{}, err
	}
	return detail, nil
}

// PlanNodeScaleIn fetches a side-effect-free manager scale-in report.
func PlanNodeScaleIn(ctx context.Context, node StartedNode, nodeID uint64, req ManagerNodeScaleInPlanRequest) (ManagerNodeScaleInReport, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/scale-in/plan", nodeID), req)
	if err != nil {
		return ManagerNodeScaleInReport{}, nil, err
	}

	report, err := decodeNodeScaleInReportResponse(body)
	if err != nil {
		return ManagerNodeScaleInReport{}, body, err
	}
	return report, body, nil
}

// StartNodeScaleIn marks a preflight-safe manager node as draining.
func StartNodeScaleIn(ctx context.Context, node StartedNode, nodeID uint64, req ManagerNodeScaleInPlanRequest) (ManagerNodeScaleInReport, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/scale-in/start", nodeID), req)
	if err != nil {
		return ManagerNodeScaleInReport{}, nil, err
	}

	report, err := decodeNodeScaleInReportResponse(body)
	if err != nil {
		return ManagerNodeScaleInReport{}, body, err
	}
	return report, body, nil
}

// GetNodeScaleInStatus fetches the latest manager scale-in report.
func GetNodeScaleInStatus(ctx context.Context, node StartedNode, nodeID uint64) (ManagerNodeScaleInReport, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/scale-in/status", nodeID))
	if err != nil {
		return ManagerNodeScaleInReport{}, nil, err
	}

	report, err := decodeNodeScaleInReportResponse(body)
	if err != nil {
		return ManagerNodeScaleInReport{}, body, err
	}
	return report, body, nil
}

// AdvanceNodeScaleIn performs one bounded manager scale-in advance step.
func AdvanceNodeScaleIn(ctx context.Context, node StartedNode, nodeID uint64, req ManagerAdvanceNodeScaleInRequest) (ManagerNodeScaleInReport, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/nodes/%d/scale-in/advance", nodeID), req)
	if err != nil {
		return ManagerNodeScaleInReport{}, nil, err
	}

	report, err := decodeNodeScaleInReportResponse(body)
	if err != nil {
		return ManagerNodeScaleInReport{}, body, err
	}
	return report, body, nil
}

func decodeNodeScaleInReportResponse(body []byte) (ManagerNodeScaleInReport, error) {
	var report ManagerNodeScaleInReport
	if err := json.Unmarshal(body, &report); err != nil {
		return ManagerNodeScaleInReport{}, err
	}
	return report, nil
}

// FetchConnections fetches local manager connections from the started node.
func FetchConnections(ctx context.Context, node StartedNode) ([]ManagerConnection, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, "/manager/connections")
	if err != nil {
		return nil, nil, err
	}

	var resp managerConnectionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, body, err
	}
	return resp.Items, body, nil
}

// FetchNodes fetches the manager node list from the started node.
func FetchNodes(ctx context.Context, node StartedNode) ([]ManagerNode, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, "/manager/nodes")
	if err != nil {
		return nil, nil, err
	}

	resp, err := decodeManagerNodesResponse(body)
	if err != nil {
		return nil, body, err
	}
	return resp.Items, body, nil
}

func decodeManagerNodesResponse(body []byte) (ManagerNodesResponse, error) {
	var resp ManagerNodesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerNodesResponse{}, err
	}
	return resp, nil
}

func managerNodeByID(items []ManagerNode, nodeID uint64) (ManagerNode, bool) {
	for _, item := range items {
		if item.NodeID == nodeID {
			return item, true
		}
	}
	return ManagerNode{}, false
}

// WaitForManagerNode waits until /manager/nodes contains a node accepted by the predicate.
func WaitForManagerNode(ctx context.Context, node StartedNode, nodeID uint64, accept func(ManagerNode) bool) (ManagerNode, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastErr  error
		lastBody []byte
	)
	for {
		items, body, err := FetchNodes(ctx, node)
		if err == nil {
			lastBody = body
			managerNode, ok := managerNodeByID(items, nodeID)
			if ok && (accept == nil || accept(managerNode)) {
				return managerNode, body, nil
			}
			if ok {
				lastErr = fmt.Errorf("manager node %d did not satisfy expected state", nodeID)
			} else {
				lastErr = fmt.Errorf("manager node %d not present", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return ManagerNode{}, lastBody, lastErr
			}
			return ManagerNode{}, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitForControllerRaftLeader waits until one of the target nodes reports itself as Controller Raft leader.
func WaitForControllerRaftLeader(ctx context.Context, node StartedNode, nodeIDs []uint64) (ManagerControllerRaftStatus, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastErr  error
		lastBody []byte
	)
	for {
		for _, nodeID := range nodeIDs {
			status, body, err := FetchControllerRaftStatus(ctx, node, nodeID)
			if err != nil {
				lastErr = err
				continue
			}
			lastBody = body
			if status.Role == "leader" && status.NodeID == nodeID && status.LeaderID == nodeID {
				return status, body, nil
			}
			lastErr = fmt.Errorf("controller node %d role=%s leader=%d", nodeID, status.Role, status.LeaderID)
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return ManagerControllerRaftStatus{}, lastBody, lastErr
			}
			return ManagerControllerRaftStatus{}, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitForControllerRaftStatus waits until one node-local Controller Raft status satisfies accept.
func WaitForControllerRaftStatus(ctx context.Context, node StartedNode, targetNodeID uint64, accept func(ManagerControllerRaftStatus) bool) (ManagerControllerRaftStatus, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastErr  error
		lastBody []byte
	)
	for {
		status, body, err := FetchControllerRaftStatus(ctx, node, targetNodeID)
		if err == nil {
			lastBody = body
			if accept == nil || accept(status) {
				return status, body, nil
			}
			lastErr = fmt.Errorf("controller node %d did not satisfy expected state", targetNodeID)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return ManagerControllerRaftStatus{}, lastBody, lastErr
			}
			return ManagerControllerRaftStatus{}, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ResolveSlotTopology resolves the externally observed slot topology for one managed slot.
func (c *StartedCluster) ResolveSlotTopology(ctx context.Context, slotID uint32) (SlotTopology, error) {
	if c == nil {
		return SlotTopology{}, fmt.Errorf("started cluster is nil")
	}

	expectedNodeIDs := make([]uint64, 0, len(c.Nodes))
	for _, node := range c.Nodes {
		expectedNodeIDs = append(expectedNodeIDs, node.Spec.ID)
	}

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		for _, node := range c.Nodes {
			_, body, err := FetchSlotDetail(ctx, node, slotID)
			if err != nil {
				lastErr = err
				continue
			}
			c.lastSlotBodies[slotID] = string(body)
			topology, err := parseSlotTopology(slotID, expectedNodeIDs, body)
			if err == nil {
				return topology, nil
			}
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = fmt.Errorf("slot %d topology unavailable", slotID)
			}
			return SlotTopology{}, lastErr
		case <-ticker.C:
		}
	}
}

// WaitForSlotLeaderChange waits until the slot reports a new leader with quorum.
func (c *StartedCluster) WaitForSlotLeaderChange(ctx context.Context, slotID uint32, previousLeaderID uint64) (SlotTopology, error) {
	if c == nil {
		return SlotTopology{}, fmt.Errorf("started cluster is nil")
	}

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		for _, node := range c.Nodes {
			if node.Process == nil {
				continue
			}

			detail, body, err := FetchSlotDetail(ctx, node, slotID)
			if err != nil {
				lastErr = err
				continue
			}
			c.lastSlotBodies[slotID] = string(body)
			if detail.SlotID != slotID {
				lastErr = fmt.Errorf("slot %d leader change: got slot %d", slotID, detail.SlotID)
				continue
			}
			if detail.Runtime.LeaderID == 0 {
				lastErr = fmt.Errorf("slot %d leader change: missing leader", slotID)
				continue
			}
			if detail.Runtime.LeaderID == previousLeaderID {
				lastErr = fmt.Errorf("slot %d leader change: leader still %d", slotID, previousLeaderID)
				continue
			}
			if !detail.Runtime.HasQuorum {
				lastErr = fmt.Errorf("slot %d leader change: quorum not ready", slotID)
				continue
			}

			followers := make([]uint64, 0, len(detail.Runtime.CurrentPeers))
			for _, nodeID := range detail.Runtime.CurrentPeers {
				if nodeID != detail.Runtime.LeaderID {
					followers = append(followers, nodeID)
				}
			}
			return SlotTopology{
				SlotID:          detail.SlotID,
				LeaderNodeID:    detail.Runtime.LeaderID,
				FollowerNodeIDs: followers,
				RawBody:         string(body),
			}, nil
		}

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = fmt.Errorf("slot %d leader change unavailable", slotID)
			}
			return SlotTopology{}, lastErr
		case <-ticker.C:
		}
	}
}

func fetchHTTPBody(ctx context.Context, addr, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manager endpoint %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func postHTTPJSONBody(ctx context.Context, addr, path string, payload any) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manager endpoint %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func parseSlotTopology(slotID uint32, expectedNodeIDs []uint64, body []byte) (SlotTopology, error) {
	var detail ManagerSlotDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return SlotTopology{}, err
	}
	if detail.SlotID != slotID {
		return SlotTopology{}, fmt.Errorf("slot topology: got slot %d, want %d", detail.SlotID, slotID)
	}
	if detail.State.Quorum != "ready" {
		return SlotTopology{}, fmt.Errorf("slot topology: quorum=%s", detail.State.Quorum)
	}
	if detail.State.Sync != "matched" {
		return SlotTopology{}, fmt.Errorf("slot topology: sync=%s", detail.State.Sync)
	}
	if detail.Runtime.LeaderID == 0 {
		return SlotTopology{}, fmt.Errorf("slot topology: missing leader")
	}
	if !sameNodeSet(detail.Runtime.CurrentPeers, expectedNodeIDs) {
		return SlotTopology{}, fmt.Errorf("slot topology: peers=%v want=%v", detail.Runtime.CurrentPeers, expectedNodeIDs)
	}
	if !slices.Contains(detail.Runtime.CurrentPeers, detail.Runtime.LeaderID) {
		return SlotTopology{}, fmt.Errorf("slot topology: leader %d not in peers", detail.Runtime.LeaderID)
	}

	followers := make([]uint64, 0, len(detail.Runtime.CurrentPeers)-1)
	for _, nodeID := range detail.Runtime.CurrentPeers {
		if nodeID != detail.Runtime.LeaderID {
			followers = append(followers, nodeID)
		}
	}

	return SlotTopology{
		SlotID:          detail.SlotID,
		LeaderNodeID:    detail.Runtime.LeaderID,
		FollowerNodeIDs: followers,
		RawBody:         string(body),
	}, nil
}

func connectionsContainUID(items []ManagerConnection, uid string) bool {
	for _, item := range items {
		if item.UID == uid {
			return true
		}
	}
	return false
}

// ConnectionsContainUID waits until the node reports a local manager connection for the UID.
func ConnectionsContainUID(ctx context.Context, node StartedNode, uid string) (bool, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		items, _, err := FetchConnections(ctx, node)
		if err == nil {
			if connectionsContainUID(items, uid) {
				return true, nil
			}
			lastErr = fmt.Errorf("uid %s not present in manager connections", uid)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return false, lastErr
			}
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

// PersonChannelID returns the canonical runtime channel ID for two person-channel UIDs.
func PersonChannelID(leftUID, rightUID string) string {
	leftHash := crc32.ChecksumIEEE([]byte(leftUID))
	rightHash := crc32.ChecksumIEEE([]byte(rightUID))
	if leftHash > rightHash {
		return leftUID + "@" + rightUID
	}
	if leftHash == rightHash && leftUID > rightUID {
		return leftUID + "@" + rightUID
	}
	return rightUID + "@" + leftUID
}

func sameNodeSet(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}

	leftCopy := append([]uint64(nil), left...)
	rightCopy := append([]uint64(nil), right...)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}
