package manager

import (
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/gin-gonic/gin"
)

// OverviewResponse is the manager homepage overview response body.
type OverviewResponse struct {
	// GeneratedAt is the time when the overview aggregation was produced.
	GeneratedAt time.Time `json:"generated_at"`
	// Cluster contains cluster-wide headline fields.
	Cluster OverviewClusterDTO `json:"cluster"`
	// Nodes contains node status counters.
	Nodes OverviewNodesDTO `json:"nodes"`
	// Slots contains slot status counters.
	Slots OverviewSlotsDTO `json:"slots"`
	// Tasks contains reconcile task counters.
	Tasks OverviewTasksDTO `json:"tasks"`
	// Anomalies contains capped slot and task anomaly samples.
	Anomalies OverviewAnomaliesDTO `json:"anomalies"`
}

// OverviewClusterDTO contains cluster-wide overview fields.
type OverviewClusterDTO struct {
	// ControllerLeaderID is the current controller leader node ID.
	ControllerLeaderID uint64 `json:"controller_leader_id"`
}

// OverviewNodesDTO contains node counters for the manager homepage.
type OverviewNodesDTO struct {
	// Total is the number of cluster nodes.
	Total int `json:"total"`
	// Alive is the count of alive nodes.
	Alive int `json:"alive"`
	// Suspect is the count of suspect nodes.
	Suspect int `json:"suspect"`
	// Dead is the count of dead nodes.
	Dead int `json:"dead"`
	// Draining is the count of draining nodes.
	Draining int `json:"draining"`
}

// OverviewSlotsDTO contains slot counters for the manager homepage.
type OverviewSlotsDTO struct {
	// Total is the number of configured physical slots.
	Total int `json:"total"`
	// Ready is the count of slots with quorum.
	Ready int `json:"ready"`
	// QuorumLost is the count of slots without quorum.
	QuorumLost int `json:"quorum_lost"`
	// LeaderMissing is the count of slots with a runtime view but no leader.
	LeaderMissing int `json:"leader_missing"`
	// Unreported is the count of slots without a runtime view.
	Unreported int `json:"unreported"`
	// PeerMismatch is the count of slots whose runtime peers differ from assignment.
	PeerMismatch int `json:"peer_mismatch"`
	// EpochLag is the count of slots whose observed config epoch lags assignment.
	EpochLag int `json:"epoch_lag"`
}

// OverviewTasksDTO contains reconcile task counters for the manager homepage.
type OverviewTasksDTO struct {
	// Total is the number of tracked reconcile tasks.
	Total int `json:"total"`
	// Pending is the count of pending tasks.
	Pending int `json:"pending"`
	// Retrying is the count of retrying tasks.
	Retrying int `json:"retrying"`
	// Failed is the count of failed tasks.
	Failed int `json:"failed"`
}

// OverviewAnomaliesDTO contains overview anomaly groups.
type OverviewAnomaliesDTO struct {
	// Slots contains slot anomaly groups.
	Slots OverviewSlotAnomaliesDTO `json:"slots"`
	// Tasks contains task anomaly groups.
	Tasks OverviewTaskAnomaliesDTO `json:"tasks"`
}

// OverviewSlotAnomaliesDTO contains slot anomaly groups for overview.
type OverviewSlotAnomaliesDTO struct {
	// QuorumLost contains slots that currently lack quorum.
	QuorumLost OverviewSlotAnomalyGroupDTO `json:"quorum_lost"`
	// LeaderMissing contains slots that currently report no leader.
	LeaderMissing OverviewSlotAnomalyGroupDTO `json:"leader_missing"`
	// SyncMismatch contains slots with peer mismatch or epoch lag.
	SyncMismatch OverviewSlotAnomalyGroupDTO `json:"sync_mismatch"`
}

// OverviewTaskAnomaliesDTO contains task anomaly groups for overview.
type OverviewTaskAnomaliesDTO struct {
	// Failed contains failed task samples.
	Failed OverviewTaskAnomalyGroupDTO `json:"failed"`
	// Retrying contains retrying task samples.
	Retrying OverviewTaskAnomalyGroupDTO `json:"retrying"`
}

// OverviewSlotAnomalyGroupDTO contains one slot anomaly group.
type OverviewSlotAnomalyGroupDTO struct {
	// Count is the full number of anomalies in the group.
	Count int `json:"count"`
	// Items contains the capped anomaly samples.
	Items []OverviewSlotAnomalyItemDTO `json:"items"`
}

// OverviewTaskAnomalyGroupDTO contains one task anomaly group.
type OverviewTaskAnomalyGroupDTO struct {
	// Count is the full number of anomalies in the group.
	Count int `json:"count"`
	// Items contains the capped anomaly samples.
	Items []OverviewTaskAnomalyItemDTO `json:"items"`
}

// OverviewSlotAnomalyItemDTO is one slot anomaly sample for overview.
type OverviewSlotAnomalyItemDTO struct {
	// SlotID is the physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// Quorum is the derived slot quorum state.
	Quorum string `json:"quorum"`
	// Sync is the derived slot sync state.
	Sync string `json:"sync"`
	// LeaderID is the observed slot leader ID.
	LeaderID uint64 `json:"leader_id"`
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64 `json:"desired_peers"`
	// CurrentPeers is the observed slot voter set.
	CurrentPeers []uint64 `json:"current_peers"`
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time `json:"last_report_at"`
}

// OverviewTaskAnomalyItemDTO is one task anomaly sample for overview.
type OverviewTaskAnomalyItemDTO struct {
	// SlotID is the physical slot identifier associated with the task.
	SlotID uint32 `json:"slot_id"`
	// Kind is the stringified task kind.
	Kind string `json:"kind"`
	// Step is the stringified task step.
	Step string `json:"step"`
	// Status is the stringified task status.
	Status string `json:"status"`
	// SourceNode is the task source node when applicable.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the task target node when applicable.
	TargetNode uint64 `json:"target_node"`
	// Attempt is the current task attempt count.
	Attempt uint32 `json:"attempt"`
	// NextRunAt is the next retry schedule when present.
	NextRunAt *time.Time `json:"next_run_at"`
	// LastError is the last recorded task error message.
	LastError string `json:"last_error"`
}

func (s *Server) handleOverview(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	item, err := s.management.GetOverview(c.Request.Context())
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, overviewResponse(item))
}

func overviewResponse(item managementusecase.Overview) OverviewResponse {
	return OverviewResponse{
		GeneratedAt: item.GeneratedAt,
		Cluster: OverviewClusterDTO{
			ControllerLeaderID: item.Cluster.ControllerLeaderID,
		},
		Nodes: OverviewNodesDTO{
			Total:    item.Nodes.Total,
			Alive:    item.Nodes.Alive,
			Suspect:  item.Nodes.Suspect,
			Dead:     item.Nodes.Dead,
			Draining: item.Nodes.Draining,
		},
		Slots: OverviewSlotsDTO{
			Total:         item.Slots.Total,
			Ready:         item.Slots.Ready,
			QuorumLost:    item.Slots.QuorumLost,
			LeaderMissing: item.Slots.LeaderMissing,
			Unreported:    item.Slots.Unreported,
			PeerMismatch:  item.Slots.PeerMismatch,
			EpochLag:      item.Slots.EpochLag,
		},
		Tasks: OverviewTasksDTO{
			Total:    item.Tasks.Total,
			Pending:  item.Tasks.Pending,
			Retrying: item.Tasks.Retrying,
			Failed:   item.Tasks.Failed,
		},
		Anomalies: OverviewAnomaliesDTO{
			Slots: OverviewSlotAnomaliesDTO{
				QuorumLost:    overviewSlotAnomalyGroupDTO(item.Anomalies.Slots.QuorumLost),
				LeaderMissing: overviewSlotAnomalyGroupDTO(item.Anomalies.Slots.LeaderMissing),
				SyncMismatch:  overviewSlotAnomalyGroupDTO(item.Anomalies.Slots.SyncMismatch),
			},
			Tasks: OverviewTaskAnomaliesDTO{
				Failed:   overviewTaskAnomalyGroupDTO(item.Anomalies.Tasks.Failed),
				Retrying: overviewTaskAnomalyGroupDTO(item.Anomalies.Tasks.Retrying),
			},
		},
	}
}

func overviewSlotAnomalyGroupDTO(group managementusecase.OverviewSlotAnomalyGroup) OverviewSlotAnomalyGroupDTO {
	return OverviewSlotAnomalyGroupDTO{
		Count: group.Count,
		Items: overviewSlotAnomalyItemDTOs(group.Items),
	}
}

func overviewSlotAnomalyItemDTOs(items []managementusecase.OverviewSlotAnomalyItem) []OverviewSlotAnomalyItemDTO {
	out := make([]OverviewSlotAnomalyItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, OverviewSlotAnomalyItemDTO{
			SlotID:       item.SlotID,
			Quorum:       item.Quorum,
			Sync:         item.Sync,
			LeaderID:     item.LeaderID,
			DesiredPeers: append([]uint64(nil), item.DesiredPeers...),
			CurrentPeers: append([]uint64(nil), item.CurrentPeers...),
			LastReportAt: item.LastReportAt,
		})
	}
	return out
}

func overviewTaskAnomalyGroupDTO(group managementusecase.OverviewTaskAnomalyGroup) OverviewTaskAnomalyGroupDTO {
	return OverviewTaskAnomalyGroupDTO{
		Count: group.Count,
		Items: overviewTaskAnomalyItemDTOs(group.Items),
	}
}

func overviewTaskAnomalyItemDTOs(items []managementusecase.OverviewTaskAnomalyItem) []OverviewTaskAnomalyItemDTO {
	out := make([]OverviewTaskAnomalyItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, OverviewTaskAnomalyItemDTO{
			SlotID:     item.SlotID,
			Kind:       item.Kind,
			Step:       item.Step,
			Status:     item.Status,
			SourceNode: item.SourceNode,
			TargetNode: item.TargetNode,
			Attempt:    item.Attempt,
			NextRunAt:  item.NextRunAt,
			LastError:  item.LastError,
		})
	}
	return out
}
