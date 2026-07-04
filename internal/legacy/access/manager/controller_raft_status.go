package manager

import (
	"errors"
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"github.com/gin-gonic/gin"
)

// ControllerRaftStatusDTO is the manager Controller Raft status response body.
type ControllerRaftStatusDTO struct {
	// NodeID is the node whose local Controller Raft status was read.
	NodeID uint64 `json:"node_id"`
	// Role is leader, follower, candidate, or unknown.
	Role string `json:"role"`
	// LeaderID is the Controller Raft leader known to the queried node.
	LeaderID uint64 `json:"leader_id"`
	// Term is the queried node's current Raft term.
	Term uint64 `json:"term"`
	// Health is the derived manager-facing status bucket.
	Health string `json:"health"`
	// FirstIndex is the first available local Controller Raft log index.
	FirstIndex uint64 `json:"first_index"`
	// LastIndex is the last available local Controller Raft log index.
	LastIndex uint64 `json:"last_index"`
	// CommitIndex is the queried node's committed Controller Raft index watermark.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is the queried node's applied Controller Raft index watermark.
	AppliedIndex uint64 `json:"applied_index"`
	// SnapshotIndex is the latest persisted Controller Raft snapshot index.
	SnapshotIndex uint64 `json:"snapshot_index"`
	// SnapshotTerm is the latest persisted Controller Raft snapshot term.
	SnapshotTerm uint64 `json:"snapshot_term"`
	// Compaction describes local Controller Raft log compaction state.
	Compaction ControllerRaftCompactionDTO `json:"compaction"`
	// Restore describes local Controller metadata snapshot restore state.
	Restore ControllerRaftRestoreDTO `json:"restore"`
	// Peers contains leader-side follower progress.
	Peers []ControllerRaftPeerDTO `json:"peers"`
}

// ControllerRaftCompactionDTO describes local Controller Raft log compaction state.
type ControllerRaftCompactionDTO struct {
	// Enabled reports whether local Controller Raft snapshot compaction is enabled.
	Enabled bool `json:"enabled"`
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64 `json:"trigger_entries"`
	// CheckIntervalMs is the minimum interval between compaction checks in milliseconds.
	CheckIntervalMs int64 `json:"check_interval_ms"`
	// LastSnapshotIndex is the latest local snapshot index created by compaction.
	LastSnapshotIndex uint64 `json:"last_snapshot_index"`
	// LastSnapshotAt records when the latest local snapshot was created.
	LastSnapshotAt time.Time `json:"last_snapshot_at"`
	// LastCheckAt records the latest compaction check attempt.
	LastCheckAt time.Time `json:"last_check_at"`
	// LastError is the latest compaction error, if any.
	LastError string `json:"last_error"`
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time `json:"last_error_at"`
	// Degraded reports whether the latest compaction attempt failed and has not yet been cleared.
	Degraded bool `json:"degraded"`
}

// ControllerRaftRestoreDTO describes Controller metadata snapshot restore state.
type ControllerRaftRestoreDTO struct {
	// LastSnapshotIndex is the index of the latest restored snapshot.
	LastSnapshotIndex uint64 `json:"last_snapshot_index"`
	// LastSnapshotTerm is the term of the latest restored snapshot.
	LastSnapshotTerm uint64 `json:"last_snapshot_term"`
	// LastRestoredAt records when the latest snapshot restore succeeded.
	LastRestoredAt time.Time `json:"last_restored_at"`
	// LastError is the latest snapshot restore error, if any.
	LastError string `json:"last_error"`
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time `json:"last_error_at"`
	// Failed reports whether the latest restore attempt failed.
	Failed bool `json:"failed"`
}

// ControllerRaftPeerDTO describes one follower from the leader's view.
type ControllerRaftPeerDTO struct {
	// NodeID is the follower Controller Raft node ID.
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

func (s *Server) handleNodeControllerRaft(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	status, err := s.management.GetControllerRaftStatus(c.Request.Context(), nodeID)
	if err != nil {
		if controllerRaftStatusUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller raft status unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, controllerRaftStatusDTO(status))
}

func controllerRaftStatusUnavailable(err error) bool {
	return leaderConsistentReadUnavailable(err) ||
		errors.Is(err, transport.ErrNodeNotFound) ||
		errors.Is(err, transport.ErrStopped)
}

func controllerRaftStatusDTO(status managementusecase.ControllerRaftStatusResponse) ControllerRaftStatusDTO {
	return ControllerRaftStatusDTO{
		NodeID:        status.NodeID,
		Role:          status.Role,
		LeaderID:      status.LeaderID,
		Term:          status.Term,
		Health:        status.Health,
		FirstIndex:    status.FirstIndex,
		LastIndex:     status.LastIndex,
		CommitIndex:   status.CommitIndex,
		AppliedIndex:  status.AppliedIndex,
		SnapshotIndex: status.SnapshotIndex,
		SnapshotTerm:  status.SnapshotTerm,
		Compaction:    controllerRaftCompactionDTO(status.Compaction),
		Restore:       controllerRaftRestoreDTO(status.Restore),
		Peers:         controllerRaftPeerDTOs(status.Peers),
	}
}

func controllerRaftCompactionDTO(status managementusecase.ControllerRaftCompactionStatus) ControllerRaftCompactionDTO {
	return ControllerRaftCompactionDTO{
		Enabled:           status.Enabled,
		TriggerEntries:    status.TriggerEntries,
		CheckIntervalMs:   durationMillis(status.CheckInterval),
		LastSnapshotIndex: status.LastSnapshotIndex,
		LastSnapshotAt:    status.LastSnapshotAt,
		LastCheckAt:       status.LastCheckAt,
		LastError:         status.LastError,
		LastErrorAt:       status.LastErrorAt,
		Degraded:          status.Degraded,
	}
}

func controllerRaftRestoreDTO(status managementusecase.ControllerRaftRestoreStatus) ControllerRaftRestoreDTO {
	return ControllerRaftRestoreDTO{
		LastSnapshotIndex: status.LastSnapshotIndex,
		LastSnapshotTerm:  status.LastSnapshotTerm,
		LastRestoredAt:    status.LastRestoredAt,
		LastError:         status.LastError,
		LastErrorAt:       status.LastErrorAt,
		Failed:            status.Failed,
	}
}

func controllerRaftPeerDTOs(items []managementusecase.ControllerRaftPeerProgress) []ControllerRaftPeerDTO {
	out := make([]ControllerRaftPeerDTO, 0, len(items))
	for _, item := range items {
		out = append(out, ControllerRaftPeerDTO{
			NodeID:               item.NodeID,
			Match:                item.Match,
			Next:                 item.Next,
			State:                item.State,
			PendingSnapshot:      item.PendingSnapshot,
			RecentActive:         item.RecentActive,
			NeedsSnapshot:        item.NeedsSnapshot,
			SnapshotTransferring: item.SnapshotTransferring,
		})
	}
	return out
}
