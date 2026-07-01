package manager

import (
	"errors"
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerControllerRaftCompaction describes Controller Raft log compaction state on one node.
type ManagerControllerRaftCompaction struct {
	// Enabled reports whether Controller Raft snapshot compaction is enabled.
	Enabled bool `json:"enabled"`
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64 `json:"trigger_entries"`
	// CheckIntervalMS is the minimum interval between compaction checks in milliseconds.
	CheckIntervalMS int64 `json:"check_interval_ms"`
	// LastSnapshotIndex is the latest snapshot index created by compaction.
	LastSnapshotIndex uint64 `json:"last_snapshot_index"`
	// LastSnapshotAt records when the latest snapshot was created.
	LastSnapshotAt string `json:"last_snapshot_at"`
	// LastCheckAt records the latest compaction check attempt.
	LastCheckAt string `json:"last_check_at"`
	// LastError is the latest compaction error, when present.
	LastError string `json:"last_error"`
	// LastErrorAt records when LastError was observed.
	LastErrorAt string `json:"last_error_at"`
	// Degraded reports whether the latest compaction attempt failed.
	Degraded bool `json:"degraded"`
}

// ManagerControllerRaftRestore describes Controller metadata snapshot restore state.
type ManagerControllerRaftRestore struct {
	// LastSnapshotIndex is the index of the latest restored snapshot.
	LastSnapshotIndex uint64 `json:"last_snapshot_index"`
	// LastSnapshotTerm is the term of the latest restored snapshot.
	LastSnapshotTerm uint64 `json:"last_snapshot_term"`
	// LastRestoredAt records when the latest snapshot restore succeeded.
	LastRestoredAt string `json:"last_restored_at"`
	// LastError is the latest restore error, when present.
	LastError string `json:"last_error"`
	// LastErrorAt records when LastError was observed.
	LastErrorAt string `json:"last_error_at"`
	// Failed reports whether the latest restore attempt failed.
	Failed bool `json:"failed"`
}

// ManagerControllerRaftPeer describes one follower from the Controller Raft leader's view.
type ManagerControllerRaftPeer struct {
	// NodeID is the follower node ID.
	NodeID uint64 `json:"node_id"`
	// Match is the highest log index known to match on the follower.
	Match uint64 `json:"match"`
	// Next is the next log index the leader will send to the follower.
	Next uint64 `json:"next"`
	// State is the raft progress state.
	State string `json:"state"`
	// PendingSnapshot is the snapshot index currently pending for the follower.
	PendingSnapshot uint64 `json:"pending_snapshot"`
	// RecentActive reports whether the follower was recently active.
	RecentActive bool `json:"recent_active"`
	// NeedsSnapshot reports whether the follower has fallen behind the local first index.
	NeedsSnapshot bool `json:"needs_snapshot"`
	// SnapshotTransferring reports whether raft is currently transferring a snapshot.
	SnapshotTransferring bool `json:"snapshot_transferring"`
}

// ManagerControllerRaftStatusResponse is a node-scoped Controller Raft status snapshot.
type ManagerControllerRaftStatusResponse struct {
	// NodeID is the node whose Controller Raft status was read.
	NodeID uint64 `json:"node_id"`
	// Role is leader, follower, candidate, or unknown.
	Role string `json:"role"`
	// LeaderID is the Controller Raft leader known to the queried node.
	LeaderID uint64 `json:"leader_id"`
	// Term is the queried node's current Controller Raft term.
	Term uint64 `json:"term"`
	// Health is the derived manager-facing status bucket.
	Health string `json:"health"`
	// FirstIndex is the first available Controller Raft log index.
	FirstIndex uint64 `json:"first_index"`
	// LastIndex is the last available Controller Raft log index.
	LastIndex uint64 `json:"last_index"`
	// CommitIndex is the queried node's committed Controller Raft watermark.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is the queried node's applied Controller Raft watermark.
	AppliedIndex uint64 `json:"applied_index"`
	// Voters is the Controller Raft voter set observed by the queried node.
	Voters []uint64 `json:"voters"`
	// Learners is the Controller Raft learner set observed by the queried node.
	Learners []uint64 `json:"learners"`
	// SnapshotIndex is the latest persisted Controller Raft snapshot index.
	SnapshotIndex uint64 `json:"snapshot_index"`
	// SnapshotTerm is the latest persisted Controller Raft snapshot term.
	SnapshotTerm uint64 `json:"snapshot_term"`
	// Compaction describes Controller Raft log compaction state.
	Compaction ManagerControllerRaftCompaction `json:"compaction"`
	// Restore describes Controller metadata snapshot restore state.
	Restore ManagerControllerRaftRestore `json:"restore"`
	// Peers contains leader-side follower progress.
	Peers []ManagerControllerRaftPeer `json:"peers"`
}

// ManagerControllerRaftCompactNodeResult describes one Controller voter node compaction attempt.
type ManagerControllerRaftCompactNodeResult struct {
	// NodeID is the Controller voter node that handled the attempt.
	NodeID uint64 `json:"node_id"`
	// Success reports whether the node accepted and completed the local attempt.
	Success bool `json:"success"`
	// AppliedIndex is the node-local applied index used as the compaction target.
	AppliedIndex uint64 `json:"applied_index"`
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64 `json:"before_snapshot_index"`
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64 `json:"after_snapshot_index"`
	// Compacted reports whether this attempt created a new snapshot and compacted entries.
	Compacted bool `json:"compacted"`
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string `json:"skipped_reason"`
	// Error is the per-node failure message when Success is false.
	Error string `json:"error"`
}

// ManagerControllerRaftCompactResponse is returned after triggering Controller Raft compaction.
type ManagerControllerRaftCompactResponse struct {
	// GeneratedAt records when the compaction response was assembled.
	GeneratedAt string `json:"generated_at"`
	// Total is the number of Controller voter nodes targeted.
	Total int `json:"total"`
	// Succeeded is the number of nodes that completed the attempt.
	Succeeded int `json:"succeeded"`
	// Failed is the number of nodes that returned an error.
	Failed int `json:"failed"`
	// Items contains per-node results ordered by node id.
	Items []ManagerControllerRaftCompactNodeResult `json:"items"`
}

func (s *Server) handleControllerRaftStatus(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	status, err := s.management.ControllerRaftStatus(c.Request.Context(), nodeID)
	if err != nil {
		writeControllerRaftError(c, err)
		return
	}
	if status.NodeID == 0 {
		status.NodeID = nodeID
	}
	c.JSON(http.StatusOK, controllerRaftStatusDTO(status))
}

func (s *Server) handleCompactControllerRaftLog(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	result, err := s.management.CompactControllerRaftLog(c.Request.Context(), nodeID)
	if err != nil {
		writeControllerRaftError(c, err)
		return
	}
	if result.NodeID == 0 {
		result.NodeID = nodeID
	}
	c.JSON(http.StatusOK, controllerRaftSingleCompactDTO(result, time.Now().UTC()))
}

func (s *Server) handleCompactControllerRaftLogs(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	summary, err := s.management.CompactControllerRaftLogs(c.Request.Context())
	if err != nil {
		writeControllerRaftError(c, err)
		return
	}
	c.JSON(http.StatusOK, controllerRaftCompactSummaryDTO(summary))
}

func controllerRaftStatusDTO(status managementusecase.ControllerRaftStatus) ManagerControllerRaftStatusResponse {
	return ManagerControllerRaftStatusResponse{
		NodeID:        status.NodeID,
		Role:          status.Role,
		LeaderID:      status.LeaderID,
		Term:          status.Term,
		Health:        status.Health,
		FirstIndex:    status.FirstIndex,
		LastIndex:     status.LastIndex,
		CommitIndex:   status.CommitIndex,
		AppliedIndex:  status.AppliedIndex,
		Voters:        append([]uint64(nil), status.Voters...),
		Learners:      append([]uint64(nil), status.Learners...),
		SnapshotIndex: status.SnapshotIndex,
		SnapshotTerm:  status.SnapshotTerm,
		Compaction:    controllerRaftCompactionDTO(status.Compaction),
		Restore:       controllerRaftRestoreDTO(status.Restore),
		Peers:         controllerRaftPeerDTOs(status.Peers),
	}
}

func controllerRaftCompactionDTO(compaction managementusecase.ControllerRaftCompaction) ManagerControllerRaftCompaction {
	return ManagerControllerRaftCompaction{
		Enabled:           compaction.Enabled,
		TriggerEntries:    compaction.TriggerEntries,
		CheckIntervalMS:   int64(compaction.CheckInterval / time.Millisecond),
		LastSnapshotIndex: compaction.LastSnapshotIndex,
		LastSnapshotAt:    managerTimeString(compaction.LastSnapshotAt),
		LastCheckAt:       managerTimeString(compaction.LastCheckAt),
		LastError:         compaction.LastError,
		LastErrorAt:       managerTimeString(compaction.LastErrorAt),
		Degraded:          compaction.Degraded,
	}
}

func controllerRaftRestoreDTO(restore managementusecase.ControllerRaftRestore) ManagerControllerRaftRestore {
	return ManagerControllerRaftRestore{
		LastSnapshotIndex: restore.LastSnapshotIndex,
		LastSnapshotTerm:  restore.LastSnapshotTerm,
		LastRestoredAt:    managerTimeString(restore.LastRestoredAt),
		LastError:         restore.LastError,
		LastErrorAt:       managerTimeString(restore.LastErrorAt),
		Failed:            restore.Failed,
	}
}

func controllerRaftPeerDTOs(peers []managementusecase.ControllerRaftPeer) []ManagerControllerRaftPeer {
	out := make([]ManagerControllerRaftPeer, 0, len(peers))
	for _, peer := range peers {
		out = append(out, ManagerControllerRaftPeer{
			NodeID:               peer.NodeID,
			Match:                peer.Match,
			Next:                 peer.Next,
			State:                peer.State,
			PendingSnapshot:      peer.PendingSnapshot,
			RecentActive:         peer.RecentActive,
			NeedsSnapshot:        peer.NeedsSnapshot,
			SnapshotTransferring: peer.SnapshotTransferring,
		})
	}
	return out
}

func controllerRaftSingleCompactDTO(result managementusecase.ControllerRaftCompactionResult, generatedAt time.Time) ManagerControllerRaftCompactResponse {
	return ManagerControllerRaftCompactResponse{
		GeneratedAt: managerTimeString(generatedAt),
		Total:       1,
		Succeeded:   1,
		Items: []ManagerControllerRaftCompactNodeResult{controllerRaftCompactNodeDTO(managementusecase.ControllerRaftCompactNodeResult{
			NodeID:              result.NodeID,
			Success:             true,
			AppliedIndex:        result.AppliedIndex,
			BeforeSnapshotIndex: result.BeforeSnapshotIndex,
			AfterSnapshotIndex:  result.AfterSnapshotIndex,
			Compacted:           result.Compacted,
			SkippedReason:       result.SkippedReason,
			Error:               result.Error,
		})},
	}
}

func controllerRaftCompactSummaryDTO(summary managementusecase.ControllerRaftCompactionSummary) ManagerControllerRaftCompactResponse {
	items := make([]ManagerControllerRaftCompactNodeResult, 0, len(summary.Items))
	for _, item := range summary.Items {
		items = append(items, controllerRaftCompactNodeDTO(item))
	}
	return ManagerControllerRaftCompactResponse{
		GeneratedAt: managerTimeString(summary.GeneratedAt),
		Total:       summary.Total,
		Succeeded:   summary.Succeeded,
		Failed:      summary.Failed,
		Items:       items,
	}
}

func controllerRaftCompactNodeDTO(item managementusecase.ControllerRaftCompactNodeResult) ManagerControllerRaftCompactNodeResult {
	return ManagerControllerRaftCompactNodeResult{
		NodeID:              item.NodeID,
		Success:             item.Success,
		AppliedIndex:        item.AppliedIndex,
		BeforeSnapshotIndex: item.BeforeSnapshotIndex,
		AfterSnapshotIndex:  item.AfterSnapshotIndex,
		Compacted:           item.Compacted,
		SkippedReason:       item.SkippedReason,
		Error:               item.Error,
	}
}

func managerTimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func writeControllerRaftError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller raft request")
	case errors.Is(err, managementusecase.ErrControllerRaftOperatorUnavailable), controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller raft unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
