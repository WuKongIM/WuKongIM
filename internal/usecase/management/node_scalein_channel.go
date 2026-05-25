package management

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	scaleInChannelScanPageLimit     = 128
	scaleInChannelTaskScanLimit     = 1024
	scaleInDefaultChannelMigrations = 1
	scaleInMaxChannelMigrations     = 5
)

type nodeScaleInChannelInventory struct {
	leaders          int
	replicas         int
	activeMigrations int
	scanned          bool
	partial          bool
	errText          string
}

type scaleInChannelDrainCandidate struct {
	slotID multiraft.SlotID
	meta   metadb.ChannelRuntimeMeta
}

type scaleInChannelMigrationCluster struct {
	ClusterReader
	source uint64
}

func (c scaleInChannelMigrationCluster) ListNodesStrict(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	nodes, err := c.ClusterReader.ListNodesStrict(ctx)
	if err != nil {
		return nil, err
	}
	out := append([]controllermeta.ClusterNode(nil), nodes...)
	for i := range out {
		if out[i].NodeID == c.source && out[i].Status == controllermeta.NodeStatusDraining {
			// Scale-in drains ownership from a Draining source while preserving normal target eligibility checks.
			out[i].Status = controllermeta.NodeStatusAlive
		}
	}
	return out, nil
}

func (a *App) loadNodeScaleInChannelInventory(ctx context.Context, nodeID uint64) nodeScaleInChannelInventory {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil || a.channelMigration == nil {
		return nodeScaleInChannelInventory{
			partial: true,
			errText: "channel inventory dependencies are not configured",
		}
	}

	inventory := nodeScaleInChannelInventory{}
	seenTasks := make(map[scaleInChannelMigrationTaskKey]struct{})
	slotIDs := append([]multiraft.SlotID(nil), a.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	for _, slotID := range slotIDs {
		after := metadb.ChannelRuntimeMetaCursor{}
		for {
			page, cursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, scaleInChannelScanPageLimit)
			if err != nil {
				inventory.partial = true
				inventory.errText = scaleInChannelInventoryError(err)
				return inventory
			}
			for _, meta := range page {
				inventory.addRuntimeMeta(ctx, a.channelMigration, nodeID, meta, seenTasks)
				if inventory.partial {
					return inventory
				}
			}
			if done {
				break
			}
			if !scaleInChannelInventoryCursorAfter(cursor, after) {
				inventory.partial = true
				inventory.errText = "channel inventory scan made no cursor progress"
				return inventory
			}
			after = cursor
		}
	}

	tasks, hasMore, err := a.channelMigration.ListActiveChannelMigrationTasksForNode(ctx, nodeID, scaleInChannelTaskScanLimit)
	if err != nil {
		inventory.partial = true
		inventory.errText = scaleInChannelInventoryError(err)
		return inventory
	}
	for _, task := range tasks {
		if task.SourceNode == nodeID || task.TargetNode == nodeID {
			inventory.addMigrationTask(task, seenTasks)
		}
	}
	if hasMore {
		inventory.partial = true
		inventory.errText = fmt.Sprintf("active channel migration scan exceeded limit %d", scaleInChannelTaskScanLimit)
		return inventory
	}
	inventory.scanned = true
	return inventory
}

func clampScaleInChannelMigrations(limit int) int {
	if limit <= 0 {
		return scaleInDefaultChannelMigrations
	}
	if limit > scaleInMaxChannelMigrations {
		return scaleInMaxChannelMigrations
	}
	return limit
}

func (a *App) advanceNodeScaleInChannels(ctx context.Context, nodeID uint64, limit int) (created int, blockers []string, race bool, err error) {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		return 0, nil, false, metadb.ErrInvalidArgument
	}
	limit = clampScaleInChannelMigrations(limit)
	nodes, err := a.cluster.ListNodesStrict(ctx)
	if err != nil {
		return 0, nil, false, err
	}

	leaderCandidates, replicaCandidates, err := a.loadNodeScaleInChannelDrainCandidates(ctx, nodeID)
	if err != nil {
		return 0, nil, false, err
	}
	sortScaleInChannelDrainCandidates(leaderCandidates)
	sortScaleInChannelDrainCandidates(replicaCandidates)

	missingTarget := false
	for _, candidates := range [][]scaleInChannelDrainCandidate{leaderCandidates, replicaCandidates} {
		for _, candidate := range candidates {
			ok, raced, validationBlockers, createErr := a.createScaleInChannelMigration(ctx, nodes, candidate.meta, nodeID)
			switch {
			case createErr != nil:
				if created > 0 {
					return created, nil, false, nil
				}
				return 0, nil, false, createErr
			case raced:
				return created, nil, true, nil
			case len(validationBlockers) > 0:
				if created > 0 {
					return created, nil, false, nil
				}
				return 0, validationBlockers, false, nil
			case !ok:
				if created > 0 {
					return created, nil, false, nil
				}
				missingTarget = true
				continue
			}
			created++
			if created >= limit {
				return created, nil, false, nil
			}
		}
	}
	if created > 0 {
		return created, nil, false, nil
	}
	if missingTarget || len(leaderCandidates) > 0 || len(replicaCandidates) > 0 {
		return 0, []string{"no_channel_migration_target"}, false, nil
	}
	return 0, []string{"no_channel_migration_target"}, false, nil
}

func (a *App) loadNodeScaleInChannelDrainCandidates(ctx context.Context, nodeID uint64) ([]scaleInChannelDrainCandidate, []scaleInChannelDrainCandidate, error) {
	slotIDs := append([]multiraft.SlotID(nil), a.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	var leaderCandidates []scaleInChannelDrainCandidate
	var replicaCandidates []scaleInChannelDrainCandidate
	for _, slotID := range slotIDs {
		after := metadb.ChannelRuntimeMetaCursor{}
		for {
			page, cursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, scaleInChannelScanPageLimit)
			if err != nil {
				return nil, nil, err
			}
			for _, meta := range page {
				if meta.Leader == nodeID {
					leaderCandidates = append(leaderCandidates, scaleInChannelDrainCandidate{slotID: slotID, meta: meta})
					continue
				}
				if scaleInUint64sContain(meta.Replicas, nodeID) || scaleInUint64sContain(meta.ISR, nodeID) {
					replicaCandidates = append(replicaCandidates, scaleInChannelDrainCandidate{slotID: slotID, meta: meta})
				}
			}
			if done {
				break
			}
			if !scaleInChannelInventoryCursorAfter(cursor, after) {
				return nil, nil, fmt.Errorf("channel inventory scan made no cursor progress")
			}
			after = cursor
		}
	}
	return leaderCandidates, replicaCandidates, nil
}

func sortScaleInChannelDrainCandidates(candidates []scaleInChannelDrainCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].slotID != candidates[j].slotID {
			return candidates[i].slotID < candidates[j].slotID
		}
		if candidates[i].meta.ChannelID != candidates[j].meta.ChannelID {
			return candidates[i].meta.ChannelID < candidates[j].meta.ChannelID
		}
		return candidates[i].meta.ChannelType < candidates[j].meta.ChannelType
	})
}

func (a *App) createScaleInChannelMigration(ctx context.Context, nodes []controllermeta.ClusterNode, meta metadb.ChannelRuntimeMeta, source uint64) (created bool, race bool, blockers []string, err error) {
	id := channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	migrationApp := *a
	migrationApp.cluster = scaleInChannelMigrationCluster{ClusterReader: a.cluster, source: source}
	if meta.Leader == source {
		if target, ok := selectScaleInChannelReplicaTarget(nodes, meta, source); ok && hasEligibleEmbeddedLeader(nodes, meta, source, target) {
			result, err := migrationApp.MigrateChannelReplica(ctx, id, MigrateChannelReplicaRequest{SourceNodeID: source, TargetNodeID: target})
			if scaleInChannelMigrationRace(result, err) {
				return false, true, nil, nil
			}
			if err != nil && len(result.Blockers) > 0 {
				return false, false, append([]string(nil), result.Blockers...), nil
			}
			return err == nil, false, nil, err
		}
		target, ok := selectScaleInChannelLeaderTarget(nodes, meta, source)
		if !ok {
			return false, false, nil, nil
		}
		result, err := migrationApp.TransferChannelLeader(ctx, id, TransferChannelLeaderRequest{TargetNodeID: target})
		if scaleInChannelMigrationRace(result, err) {
			return false, true, nil, nil
		}
		if err != nil && len(result.Blockers) > 0 {
			return false, false, append([]string(nil), result.Blockers...), nil
		}
		return err == nil, false, nil, err
	}

	target, ok := selectScaleInChannelReplicaTarget(nodes, meta, source)
	if !ok {
		return false, false, nil, nil
	}
	result, err := migrationApp.MigrateChannelReplica(ctx, id, MigrateChannelReplicaRequest{SourceNodeID: source, TargetNodeID: target})
	if scaleInChannelMigrationRace(result, err) {
		return false, true, nil, nil
	}
	if err != nil && len(result.Blockers) > 0 {
		return false, false, append([]string(nil), result.Blockers...), nil
	}
	return err == nil, false, nil, err
}

func scaleInChannelMigrationRace(result ChannelMigrationResult, err error) bool {
	if errors.Is(err, metadb.ErrStaleMeta) || errors.Is(err, metadb.ErrAlreadyExists) {
		return true
	}
	if err == nil {
		return false
	}
	for _, blocker := range result.Blockers {
		if blocker == "active_task_exists" {
			return true
		}
	}
	return false
}

func selectScaleInChannelLeaderTarget(nodes []controllermeta.ClusterNode, meta metadb.ChannelRuntimeMeta, source uint64) (uint64, bool) {
	for _, node := range scaleInSortedAliveChannelTargets(nodes, source) {
		if scaleInUint64sContain(meta.ISR, node.NodeID) {
			return node.NodeID, true
		}
	}
	return 0, false
}

func selectScaleInChannelReplicaTarget(nodes []controllermeta.ClusterNode, meta metadb.ChannelRuntimeMeta, source uint64) (uint64, bool) {
	for _, node := range scaleInSortedAliveChannelTargets(nodes, source) {
		if scaleInUint64sContain(meta.Replicas, node.NodeID) || scaleInUint64sContain(meta.ISR, node.NodeID) {
			continue
		}
		return node.NodeID, true
	}
	return 0, false
}

func scaleInSortedAliveChannelTargets(nodes []controllermeta.ClusterNode, source uint64) []controllermeta.ClusterNode {
	out := make([]controllermeta.ClusterNode, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeID == source {
			continue
		}
		if node.Role != controllermeta.NodeRoleData || node.JoinState != controllermeta.NodeJoinStateActive || node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func (i *nodeScaleInChannelInventory) addRuntimeMeta(ctx context.Context, store ChannelMigrationStore, nodeID uint64, meta metadb.ChannelRuntimeMeta, seenTasks map[scaleInChannelMigrationTaskKey]struct{}) {
	referencesTarget := false
	if meta.Leader == nodeID {
		i.leaders++
		referencesTarget = true
	}
	if referencesTarget || scaleInUint64sContain(meta.Replicas, nodeID) || scaleInUint64sContain(meta.ISR, nodeID) {
		i.replicas++
		referencesTarget = true
	}
	if !referencesTarget {
		return
	}

	task, ok, err := store.GetActiveChannelMigrationTask(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		i.partial = true
		i.errText = scaleInChannelInventoryError(err)
		return
	}
	if ok {
		i.addMigrationTask(task, seenTasks)
	}
}

func (i *nodeScaleInChannelInventory) addMigrationTask(task metadb.ChannelMigrationTask, seenTasks map[scaleInChannelMigrationTaskKey]struct{}) {
	key := newScaleInChannelMigrationTaskKey(task)
	if _, ok := seenTasks[key]; ok {
		return
	}
	seenTasks[key] = struct{}{}
	i.activeMigrations++
}

func scaleInUint64sContain(values []uint64, needle uint64) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

type scaleInChannelMigrationTaskKey struct {
	channelID   string
	channelType int64
	taskID      string
	kind        metadb.ChannelMigrationKind
	sourceNode  uint64
	targetNode  uint64
}

func newScaleInChannelMigrationTaskKey(task metadb.ChannelMigrationTask) scaleInChannelMigrationTaskKey {
	if task.TaskID != "" {
		return scaleInChannelMigrationTaskKey{
			channelID:   task.ChannelID,
			channelType: task.ChannelType,
			taskID:      task.TaskID,
		}
	}
	return scaleInChannelMigrationTaskKey{
		channelID:   task.ChannelID,
		channelType: task.ChannelType,
		kind:        task.Kind,
		sourceNode:  task.SourceNode,
		targetNode:  task.TargetNode,
	}
}

func scaleInChannelInventoryCursorAfter(cursor, after metadb.ChannelRuntimeMetaCursor) bool {
	if len(cursor.ChannelID) != len(after.ChannelID) {
		return len(cursor.ChannelID) > len(after.ChannelID)
	}
	if cursor.ChannelID != after.ChannelID {
		return cursor.ChannelID > after.ChannelID
	}
	return cursor.ChannelType > after.ChannelType
}

func scaleInChannelInventoryError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) <= 512 {
		return text
	}
	return text[:512]
}
