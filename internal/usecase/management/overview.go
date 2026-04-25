package management

import (
	"context"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const overviewAnomalyItemLimit = 5

// Overview is the manager-facing homepage overview DTO.
type Overview struct {
	// GeneratedAt is the time when the overview aggregation was produced.
	GeneratedAt time.Time
	// Cluster contains cluster-wide headline fields.
	Cluster OverviewCluster
	// Nodes contains node status counters.
	Nodes OverviewNodes
	// Slots contains slot status counters.
	Slots OverviewSlots
	// Tasks contains reconcile task counters.
	Tasks OverviewTasks
	// Anomalies contains capped slot and task anomaly samples.
	Anomalies OverviewAnomalies
}

// OverviewCluster contains cluster-wide headline fields for overview.
type OverviewCluster struct {
	// ControllerLeaderID is the current controller leader node ID.
	ControllerLeaderID uint64
}

// OverviewNodes contains manager homepage node counters.
type OverviewNodes struct {
	// Total is the number of cluster nodes.
	Total int
	// Alive is the count of alive nodes.
	Alive int
	// Suspect is the count of suspect nodes.
	Suspect int
	// Dead is the count of dead nodes.
	Dead int
	// Draining is the count of draining nodes.
	Draining int
}

// OverviewSlots contains manager homepage slot counters.
type OverviewSlots struct {
	// Total is the number of configured physical slots.
	Total int
	// Ready is the count of slots with quorum.
	Ready int
	// QuorumLost is the count of slots without quorum.
	QuorumLost int
	// LeaderMissing is the count of slots with a runtime view but no leader.
	LeaderMissing int
	// Unreported is the count of slots without a runtime view.
	Unreported int
	// PeerMismatch is the count of slots whose runtime peers differ from assignment.
	PeerMismatch int
	// EpochLag is the count of slots whose observed config epoch lags assignment.
	EpochLag int
}

// OverviewTasks contains manager homepage task counters.
type OverviewTasks struct {
	// Total is the number of tracked reconcile tasks.
	Total int
	// Pending is the count of pending tasks.
	Pending int
	// Retrying is the count of retrying tasks.
	Retrying int
	// Failed is the count of failed tasks.
	Failed int
}

// OverviewAnomalies contains overview anomaly groups.
type OverviewAnomalies struct {
	// Slots contains slot anomaly groups.
	Slots OverviewSlotAnomalies
	// Tasks contains task anomaly groups.
	Tasks OverviewTaskAnomalies
}

// OverviewSlotAnomalies contains slot anomaly groups for overview.
type OverviewSlotAnomalies struct {
	// QuorumLost contains slots that currently lack quorum.
	QuorumLost OverviewSlotAnomalyGroup
	// LeaderMissing contains slots that currently report no leader.
	LeaderMissing OverviewSlotAnomalyGroup
	// SyncMismatch contains slots with peer mismatch or epoch lag.
	SyncMismatch OverviewSlotAnomalyGroup
}

// OverviewTaskAnomalies contains task anomaly groups for overview.
type OverviewTaskAnomalies struct {
	// Failed contains failed task samples.
	Failed OverviewTaskAnomalyGroup
	// Retrying contains retrying task samples.
	Retrying OverviewTaskAnomalyGroup
}

// OverviewSlotAnomalyGroup contains one slot anomaly group.
type OverviewSlotAnomalyGroup struct {
	// Count is the full number of anomalies in the group.
	Count int
	// Items contains the capped anomaly samples.
	Items []OverviewSlotAnomalyItem
}

// OverviewTaskAnomalyGroup contains one task anomaly group.
type OverviewTaskAnomalyGroup struct {
	// Count is the full number of anomalies in the group.
	Count int
	// Items contains the capped anomaly samples.
	Items []OverviewTaskAnomalyItem
}

// OverviewSlotAnomalyItem is one slot anomaly sample for overview.
type OverviewSlotAnomalyItem struct {
	// SlotID is the physical slot identifier.
	SlotID uint32
	// Quorum is the derived slot quorum state.
	Quorum string
	// Sync is the derived slot sync state.
	Sync string
	// LeaderID is the observed slot leader ID.
	LeaderID uint64
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64
	// CurrentPeers is the observed slot voter set.
	CurrentPeers []uint64
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time
}

// OverviewTaskAnomalyItem is one task anomaly sample for overview.
type OverviewTaskAnomalyItem struct {
	// SlotID is the physical slot identifier associated with the task.
	SlotID uint32
	// Kind is the stringified task kind.
	Kind string
	// Step is the stringified task step.
	Step string
	// Status is the stringified task status.
	Status string
	// SourceNode is the task source node when applicable.
	SourceNode uint64
	// TargetNode is the task target node when applicable.
	TargetNode uint64
	// Attempt is the current task attempt count.
	Attempt uint32
	// NextRunAt is the next retry schedule when present.
	NextRunAt *time.Time
	// LastError is the last recorded task error message.
	LastError string
}

// GetOverview returns the manager homepage overview DTO.
func (a *App) GetOverview(ctx context.Context) (Overview, error) {
	if a == nil || a.cluster == nil {
		return Overview{}, nil
	}

	nodes, err := a.cluster.ListNodesStrict(ctx)
	if err != nil {
		return Overview{}, err
	}
	assignments, err := a.cluster.ListSlotAssignmentsStrict(ctx)
	if err != nil {
		return Overview{}, err
	}
	views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
	if err != nil {
		return Overview{}, err
	}
	tasks, err := a.cluster.ListTasksStrict(ctx)
	if err != nil {
		return Overview{}, err
	}

	slotIDs := append([]multiraft.SlotID(nil), a.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	overview := Overview{
		GeneratedAt: a.now().UTC(),
		Cluster: OverviewCluster{
			ControllerLeaderID: a.cluster.ControllerLeaderID(),
		},
		Nodes: summarizeOverviewNodes(nodes),
		Slots: OverviewSlots{Total: len(slotIDs)},
		Tasks: OverviewTasks{Total: len(tasks)},
	}

	assignmentBySlot := slotAssignmentsBySlot(assignments)
	viewBySlot := runtimeViewsBySlot(views)
	for _, slotID := range slotIDs {
		slot, hasView := overviewSlot(multiraft.SlotID(slotID), assignmentBySlot, viewBySlot)
		accumulateOverviewSlot(&overview, slot, hasView)
	}

	overview.Tasks = summarizeOverviewTasks(tasks)
	collectOverviewTaskAnomalies(&overview, tasks)
	return overview, nil
}

func summarizeOverviewNodes(nodes []controllermeta.ClusterNode) OverviewNodes {
	out := OverviewNodes{Total: len(nodes)}
	for _, node := range nodes {
		switch managerNodeStatus(node.Status) {
		case "alive":
			out.Alive++
		case "suspect":
			out.Suspect++
		case "dead":
			out.Dead++
		case "draining":
			out.Draining++
		}
	}
	return out
}

func overviewSlot(slotID multiraft.SlotID, assignments map[uint32]controllermeta.SlotAssignment, views map[uint32]controllermeta.SlotRuntimeView) (Slot, bool) {
	slotKey := uint32(slotID)
	assignment, hasAssignment := assignments[slotKey]
	view, hasView := views[slotKey]
	switch {
	case hasAssignment:
		return slotFromAssignmentView(assignment, view, hasView), hasView
	case hasView:
		return slotFromRuntimeView(slotKey, view), true
	default:
		return slotWithoutObservation(slotKey), false
	}
}

func accumulateOverviewSlot(overview *Overview, slot Slot, hasView bool) {
	switch slot.State.Quorum {
	case "ready":
		overview.Slots.Ready++
	case "lost":
		overview.Slots.QuorumLost++
		appendOverviewSlotAnomaly(&overview.Anomalies.Slots.QuorumLost, overviewSlotAnomalyItem(slot))
	}

	if hasView && slot.Runtime.LeaderID == 0 {
		overview.Slots.LeaderMissing++
		appendOverviewSlotAnomaly(&overview.Anomalies.Slots.LeaderMissing, overviewSlotAnomalyItem(slot))
	}

	switch slot.State.Sync {
	case "unreported":
		overview.Slots.Unreported++
	case "peer_mismatch":
		overview.Slots.PeerMismatch++
		appendOverviewSlotAnomaly(&overview.Anomalies.Slots.SyncMismatch, overviewSlotAnomalyItem(slot))
	case "epoch_lag":
		overview.Slots.EpochLag++
		appendOverviewSlotAnomaly(&overview.Anomalies.Slots.SyncMismatch, overviewSlotAnomalyItem(slot))
	}
}

func overviewSlotAnomalyItem(slot Slot) OverviewSlotAnomalyItem {
	return OverviewSlotAnomalyItem{
		SlotID:       slot.SlotID,
		Quorum:       slot.State.Quorum,
		Sync:         slot.State.Sync,
		LeaderID:     slot.Runtime.LeaderID,
		DesiredPeers: append([]uint64(nil), slot.Assignment.DesiredPeers...),
		CurrentPeers: append([]uint64(nil), slot.Runtime.CurrentPeers...),
		LastReportAt: slot.Runtime.LastReportAt,
	}
}

func appendOverviewSlotAnomaly(group *OverviewSlotAnomalyGroup, item OverviewSlotAnomalyItem) {
	group.Count++
	if len(group.Items) < overviewAnomalyItemLimit {
		group.Items = append(group.Items, item)
	}
}

func summarizeOverviewTasks(tasks []controllermeta.ReconcileTask) OverviewTasks {
	out := OverviewTasks{Total: len(tasks)}
	for _, task := range tasks {
		switch managerTaskStatus(task.Status) {
		case "pending":
			out.Pending++
		case "retrying":
			out.Retrying++
		case "failed":
			out.Failed++
		}
	}
	return out
}

func collectOverviewTaskAnomalies(overview *Overview, tasks []controllermeta.ReconcileTask) {
	taskItems := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		taskItems = append(taskItems, managerTask(task))
	}
	sort.Slice(taskItems, func(i, j int) bool { return taskItems[i].SlotID < taskItems[j].SlotID })
	for _, task := range taskItems {
		switch task.Status {
		case "failed":
			appendOverviewTaskAnomaly(&overview.Anomalies.Tasks.Failed, overviewTaskAnomalyItem(task))
		case "retrying":
			appendOverviewTaskAnomaly(&overview.Anomalies.Tasks.Retrying, overviewTaskAnomalyItem(task))
		}
	}
}

func overviewTaskAnomalyItem(task Task) OverviewTaskAnomalyItem {
	return OverviewTaskAnomalyItem{
		SlotID:     task.SlotID,
		Kind:       task.Kind,
		Step:       task.Step,
		Status:     task.Status,
		SourceNode: task.SourceNode,
		TargetNode: task.TargetNode,
		Attempt:    task.Attempt,
		NextRunAt:  task.NextRunAt,
		LastError:  task.LastError,
	}
}

func appendOverviewTaskAnomaly(group *OverviewTaskAnomalyGroup, item OverviewTaskAnomalyItem) {
	group.Count++
	if len(group.Items) < overviewAnomalyItemLimit {
		group.Items = append(group.Items, item)
	}
}
