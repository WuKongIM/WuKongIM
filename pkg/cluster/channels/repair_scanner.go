package channels

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultRepairScannerPageLimit       = 100
	defaultRepairScannerMaxPagesPerTick = 1
	defaultRepairScannerMaxTasksPerTick = 1
)

// RepairScannerConfig bounds one Channel repair scanner tick.
type RepairScannerConfig struct {
	Enabled         bool
	PageLimit       int
	MaxPagesPerTick int
	MaxTasksPerTick int
	// Observer records low-cardinality scan and repair-decision events. Nil is allowed.
	Observer RepairObserver
	// TickInterval is the intended delay between scheduler ticks when a loop hosts this scanner.
	TickInterval time.Duration
}

// RepairObserver receives low-cardinality Channel repair scanner observations.
type RepairObserver interface {
	RepairScanPages(pages int)
	RepairScanBacklog(backlog int)
	FailoverResult(result string)
	ReplicaRepairResult(result string)
}

// RepairScannerRuntimeMeta carries one scanned runtime metadata row and the hash slot that owns it.
type RepairScannerRuntimeMeta struct {
	// HashSlot is the channel-owned hash slot shard that stored Meta.
	HashSlot uint16
	// Meta is the durable Channel runtime metadata row.
	Meta metadb.ChannelRuntimeMeta
}

// RepairScannerSource supplies repair scanner reads from cluster-owned state.
type RepairScannerSource interface {
	LocalLeaderSlotIDs(context.Context) ([]uint32, error)
	ListRepairScannerRuntimeMetaPage(context.Context, uint32, metadb.ChannelRuntimeMetaCursor, int) ([]RepairScannerRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
	ActiveChannelMigrationInHashSlot(context.Context, uint16, ch.ChannelID) (bool, error)
	ProbeChannel(ctx context.Context, nodeID uint64, channelID string, channelType uint8) (ch.RuntimeProbeChannel, error)
	ControlSnapshot(context.Context) (control.Snapshot, error)
}

// RepairScannerStore creates repair tasks selected by the scanner.
type RepairScannerStore interface {
	CreateLeaderFailover(context.Context, CreateLeaderFailoverRequest) (metadb.ChannelMigrationTask, error)
	CreateReplicaReplace(context.Context, CreateReplicaReplaceRequest) (metadb.ChannelMigrationTask, error)
}

// RepairScannerBlocked records one channel that could not be repaired this tick.
type RepairScannerBlocked struct {
	ChannelID   ch.ChannelID
	Reason      string
	ObservedHW  uint64
	TargetNode  uint64
	LeaderNode  uint64
	LeaderEpoch uint64
}

// RepairScannerResult summarizes one bounded scanner tick.
type RepairScannerResult struct {
	PagesScanned    int
	ChannelsScanned int
	TasksCreated    int
	// TasksAborted counts guarded pre-promotion replacements released for failover.
	TasksAborted int
	// TasksCompletedForFailover counts promoted replacements completed so a
	// dead leader can be failed over on the next tick (Issue #1077).
	TasksCompletedForFailover int
	Blocked                   []RepairScannerBlocked
}

// mutations returns the Slot task writes this tick, bounded by MaxTasksPerTick.
func (r RepairScannerResult) mutations() int {
	return r.TasksCreated + r.TasksAborted + r.TasksCompletedForFailover
}

// RepairScanner scans Slot-owned channel metadata and creates bounded repair work.
type RepairScanner struct {
	cfg     RepairScannerConfig
	source  RepairScannerSource
	store   RepairScannerStore
	planner FailoverPlanner
	repair  ReplicaRepairPlanner
	// Per-owned-Slot cursors retain bounded progress; dropped leadership removes
	// its cursor. RunOnce is called serially by the node-owned worker.
	cursors map[uint32]metadb.ChannelRuntimeMetaCursor
	// lastSlot rotates page admission so a large or failing Slot cannot starve peers.
	lastSlot     uint32
	haveLastSlot bool
	// healthyNodes invalidates old cursor positions when a leader becomes ineligible.
	healthyNodes map[uint64]bool
}

// NewRepairScanner creates a bounded Channel repair scanner.
func NewRepairScanner(cfg RepairScannerConfig, source RepairScannerSource, store RepairScannerStore) *RepairScanner {
	if cfg.PageLimit <= 0 {
		cfg.PageLimit = defaultRepairScannerPageLimit
	}
	if cfg.MaxPagesPerTick <= 0 {
		cfg.MaxPagesPerTick = defaultRepairScannerMaxPagesPerTick
	}
	if cfg.MaxTasksPerTick <= 0 {
		cfg.MaxTasksPerTick = defaultRepairScannerMaxTasksPerTick
	}
	return &RepairScanner{cfg: cfg, source: source, store: store, planner: NewFailoverPlanner(), repair: NewReplicaRepairPlanner()}
}

// RunOnce scans bounded local Slot-leader pages and creates repair tasks.
func (s *RepairScanner) RunOnce(ctx context.Context) (RepairScannerResult, error) {
	var result RepairScannerResult
	if err := ctxErr(ctx); err != nil {
		return result, err
	}
	if s == nil || !s.cfg.Enabled {
		return result, nil
	}
	if s.source == nil || s.store == nil {
		return result, fmt.Errorf("%w: repair scanner is not fully configured", ch.ErrInvalidConfig)
	}
	snapshot, err := s.source.ControlSnapshot(ctx)
	if err != nil {
		return result, err
	}
	healthy := failoverHealthyNodeSet(snapshot.Nodes)
	for nodeID := range s.healthyNodes {
		if !healthy[nodeID] {
			clear(s.cursors)
			s.lastSlot = 0
			s.haveLastSlot = false
			break
		}
	}
	s.healthyNodes = healthy
	slotIDs, err := s.source.LocalLeaderSlotIDs(ctx)
	if err != nil {
		return result, err
	}
	slices.Sort(slotIDs)
	slotIDs = slices.Compact(slotIDs)
	if s.cursors == nil {
		s.cursors = make(map[uint32]metadb.ChannelRuntimeMetaCursor)
	}
	for slotID := range s.cursors {
		if _, owned := slices.BinarySearch(slotIDs, slotID); !owned {
			delete(s.cursors, slotID)
		}
	}
	if len(slotIDs) == 0 {
		return s.observeRepairScan(result), nil
	}
	index := 0
	if s.haveLastSlot {
		index = sort.Search(len(slotIDs), func(i int) bool { return slotIDs[i] > s.lastSlot }) % len(slotIDs)
	}
	finished := make(map[uint32]bool, len(slotIDs))
	for result.PagesScanned < s.cfg.MaxPagesPerTick && result.mutations() < s.cfg.MaxTasksPerTick && len(finished) < len(slotIDs) {
		slotID := slotIDs[index]
		index = (index + 1) % len(slotIDs)
		if finished[slotID] {
			continue
		}
		s.lastSlot, s.haveLastSlot = slotID, true
		page, next, done, err := s.source.ListRepairScannerRuntimeMetaPage(ctx, slotID, s.cursors[slotID], s.cfg.PageLimit)
		if err != nil {
			return s.observeRepairScan(result), err
		}
		result.PagesScanned++
		processed := 0
		for _, item := range page {
			if result.mutations() >= s.cfg.MaxTasksPerTick {
				break
			}
			if err := s.scanMeta(ctx, snapshot, item, &result); err != nil {
				return s.observeRepairScan(result), err
			}
			// A task limit can stop inside a page. Resume after only the last
			// inspected row, never after unseen rows from the returned page.
			s.cursors[slotID] = metadb.ChannelRuntimeMetaCursor{
				ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType,
			}
			processed++
		}
		if processed == len(page) {
			if done {
				s.cursors[slotID] = metadb.ChannelRuntimeMetaCursor{}
				finished[slotID] = true
			} else {
				s.cursors[slotID] = next
			}
		}
	}
	return s.observeRepairScan(result), nil
}

func (s *RepairScanner) scanMeta(ctx context.Context, snapshot control.Snapshot, item RepairScannerRuntimeMeta, result *RepairScannerResult) error {
	meta := metadb.NormalizeChannelRuntimeMeta(item.Meta)
	if meta.ChannelID == "" || meta.ChannelType < 0 || meta.ChannelType > maxMigrationChannelType || meta.Status != uint8(ch.StatusActive) {
		return nil
	}
	id := ch.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	if repairScannerLeaderSuspect(snapshot.Nodes, meta.Leader) {
		result.ChannelsScanned++
		active, err := s.source.ActiveChannelMigrationInHashSlot(ctx, item.HashSlot, id)
		if err != nil {
			return err
		}
		if active {
			if err := s.preemptReplicaReplacementForFailover(ctx, meta, id, result); err != nil {
				return err
			}
			// Abort can advance the Channel epoch and remove a learner; both
			// abort and completion change the active task. Elect only from a
			// new authoritative snapshot on a later tick.
			return nil
		}
		return s.scanLeaderFailover(ctx, snapshot, meta, id, result)
	}
	decision := s.repair.Plan(ReplicaRepairPlanInput{Meta: meta, Nodes: snapshot.Nodes})
	if decision.Action == ReplicaRepairActionNone && !decision.Degraded {
		return nil
	}
	result.ChannelsScanned++
	active, err := s.source.ActiveChannelMigrationInHashSlot(ctx, item.HashSlot, id)
	if err != nil {
		return err
	}
	if active {
		return nil
	}
	return s.scanReplicaRepair(ctx, meta, id, decision, result)
}

// preemptReplicaReplacementForFailover releases an active replacement that
// would otherwise hide a dead leader from failover. Unpromoted tasks abort and
// roll back their learner; promoted tasks (VerifyMembership) only complete,
// because their membership change is already committed.
func (s *RepairScanner) preemptReplicaReplacementForFailover(ctx context.Context, meta metadb.ChannelRuntimeMeta, id ch.ChannelID, result *RepairScannerResult) error {
	if preemptor, ok := s.store.(interface {
		AbortReplicaReplacementForFailover(context.Context, ch.ChannelID, uint64, uint64) (bool, error)
	}); ok {
		aborted, err := preemptor.AbortReplicaReplacementForFailover(ctx, id, meta.Leader, meta.LeaderEpoch)
		if err != nil || aborted {
			if aborted {
				result.TasksAborted++
			}
			return err
		}
	}
	if completer, ok := s.store.(interface {
		CompletePromotedReplicaReplacementForFailover(context.Context, ch.ChannelID, uint64, uint64) (bool, error)
	}); ok {
		completed, err := completer.CompletePromotedReplicaReplacementForFailover(ctx, id, meta.Leader, meta.LeaderEpoch)
		if err != nil {
			return err
		}
		if completed {
			result.TasksCompletedForFailover++
		}
	}
	return nil
}

func (s *RepairScanner) scanLeaderFailover(ctx context.Context, snapshot control.Snapshot, meta metadb.ChannelRuntimeMeta, id ch.ChannelID, result *RepairScannerResult) error {
	probes := make([]FailoverCandidateProbe, 0, len(meta.ISR))
	for _, nodeID := range meta.ISR {
		if nodeID == 0 || nodeID == meta.Leader {
			continue
		}
		probe, err := s.source.ProbeChannel(ctx, nodeID, id.ID, id.Type)
		if err != nil {
			continue
		}
		probes = append(probes, FailoverCandidateProbe{NodeID: nodeID, Probe: probe})
	}
	decision := s.planner.Plan(FailoverPlanInput{
		Meta:          meta,
		Nodes:         snapshot.Nodes,
		Probes:        probes,
		LeaderSuspect: true,
	})
	if decision.Action == FailoverActionCreateLeaderTransfer {
		_, err := s.store.CreateLeaderFailover(ctx, CreateLeaderFailoverRequest{
			ChannelID:           id,
			DesiredLeader:       ch.NodeID(decision.TargetNode),
			ObservedHW:          decision.ObservedHW,
			ObservedLeaderEpoch: decision.ObservedEpoch,
		})
		if err != nil {
			return err
		}
		result.TasksCreated++
		s.observeFailoverResult("created")
		return nil
	}
	if decision.Action == FailoverActionBlocked {
		result.Blocked = append(result.Blocked, RepairScannerBlocked{
			ChannelID:   id,
			Reason:      decision.BlockReason,
			ObservedHW:  decision.ObservedHW,
			TargetNode:  decision.TargetNode,
			LeaderNode:  meta.Leader,
			LeaderEpoch: meta.LeaderEpoch,
		})
		s.observeFailoverResult("blocked")
	}
	return nil
}

func (s *RepairScanner) scanReplicaRepair(ctx context.Context, meta metadb.ChannelRuntimeMeta, id ch.ChannelID, decision ReplicaRepairDecision, result *RepairScannerResult) error {
	switch decision.Action {
	case ReplicaRepairActionCreateReplicaReplace:
		_, err := s.store.CreateReplicaReplace(ctx, CreateReplicaReplaceRequest{
			ChannelID:  id,
			SourceNode: ch.NodeID(decision.SourceNode),
			TargetNode: ch.NodeID(decision.TargetNode),
		})
		if err != nil {
			return err
		}
		result.TasksCreated++
		s.observeReplicaRepairResult("created")
	case ReplicaRepairActionBlocked:
		result.Blocked = append(result.Blocked, RepairScannerBlocked{
			ChannelID:   id,
			Reason:      decision.BlockReason,
			TargetNode:  decision.TargetNode,
			LeaderNode:  meta.Leader,
			LeaderEpoch: meta.LeaderEpoch,
		})
		s.observeReplicaRepairResult("blocked")
	case ReplicaRepairActionWaitForLeaderFailover:
		result.Blocked = append(result.Blocked, RepairScannerBlocked{
			ChannelID:   id,
			Reason:      "waiting_leader_failover",
			TargetNode:  decision.TargetNode,
			LeaderNode:  meta.Leader,
			LeaderEpoch: meta.LeaderEpoch,
		})
		s.observeReplicaRepairResult("waiting_leader_failover")
	}
	return nil
}

func (s *RepairScanner) observeRepairScan(result RepairScannerResult) RepairScannerResult {
	if s == nil || s.cfg.Observer == nil {
		return result
	}
	s.cfg.Observer.RepairScanPages(result.PagesScanned)
	s.cfg.Observer.RepairScanBacklog(len(result.Blocked))
	return result
}

func (s *RepairScanner) observeFailoverResult(result string) {
	if s != nil && s.cfg.Observer != nil {
		s.cfg.Observer.FailoverResult(result)
	}
}

func (s *RepairScanner) observeReplicaRepairResult(result string) {
	if s != nil && s.cfg.Observer != nil {
		s.cfg.Observer.ReplicaRepairResult(result)
	}
}

func repairScannerLeaderSuspect(nodes []control.Node, leader uint64) bool {
	if leader == 0 {
		return true
	}
	for _, node := range nodes {
		if node.NodeID != leader {
			continue
		}
		return !control.NodeSchedulableForPlacement(node)
	}
	return true
}
