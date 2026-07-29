package backup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const (
	restoreCapacityReserve          uint64 = 1 << 30
	restoreHealthConvergenceTimeout        = 15 * time.Second
	restoreHealthConvergencePoll           = 100 * time.Millisecond
)

var errRestoreHealthNotConverged = errors.New(
	"backup restore preflight: node health has not converged",
)

// RestoreCluster exposes durable topology without relying on foreground
// routing APIs, which are intentionally disabled during maintenance.
type RestoreCluster interface {
	NodeID() uint64
	BackupControllerFence(context.Context) (uint64, uint64, error)
	LocalState(context.Context) (controller.ClusterState, error)
}

type restoreCoordinatorFence struct {
	nodeID uint64
	term   uint64
}

// Check completes every read-only safety check before Controller admits a
// restore and therefore before maintenance affects foreground traffic.
func (e *DistributedRestoreExecutor) Check(
	ctx context.Context,
	job backupcontract.RestoreJob,
	plan backupcontract.Plan,
	manifest backupartifact.ArchiveManifest,
) error {
	state, active, err := e.waitForRestorePreflightState(ctx, plan, manifest)
	if err != nil {
		return err
	}
	if manifest.LogicalBytes > (math.MaxUint64-restoreCapacityReserve)/2 {
		return fmt.Errorf("backup restore preflight: archive size overflows capacity check")
	}
	archiveStagingBytes := manifest.LogicalBytes*2 + restoreCapacityReserve
	coordinator, err := e.controllerFence(ctx, false)
	if err != nil {
		return err
	}
	command := restoreCommand(
		backupcontract.RestoreNodeActionPreflight, state, plan.Store, job,
		coordinator,
	)
	command.RequiredBytes = archiveStagingBytes
	for _, nodeID := range active {
		receipt, err := e.call(ctx, nodeID, command)
		if err != nil {
			return fmt.Errorf(
				"backup restore preflight: node %d repository/capacity check: %w",
				nodeID, err,
			)
		}
		if receipt.CurrentBusinessBytes >
			math.MaxUint64-archiveStagingBytes {
			return fmt.Errorf(
				"backup restore preflight: node %d current data size overflows capacity check",
				nodeID,
			)
		}
		requiredBytes := archiveStagingBytes + receipt.CurrentBusinessBytes
		if receipt.AvailableBytes < requiredBytes {
			return fmt.Errorf(
				"backup restore preflight: node %d has insufficient staging capacity",
				nodeID,
			)
		}
	}
	latest, latestActive, err := e.waitForRestorePreflightState(
		ctx, plan, manifest,
	)
	if err != nil {
		return err
	}
	if !slices.Equal(active, latestActive) {
		return fmt.Errorf("backup restore preflight: active topology changed")
	}
	for hashSlot := 0; hashSlot < backupcontract.HashSlotCount; hashSlot++ {
		before, beforeErr := restoreSlotPeers(state, uint16(hashSlot))
		after, afterErr := restoreSlotPeers(latest, uint16(hashSlot))
		if beforeErr != nil || afterErr != nil || !slices.Equal(before, after) {
			return fmt.Errorf(
				"backup restore preflight: Slot %d topology changed",
				hashSlot,
			)
		}
	}
	return nil
}

func (e *DistributedRestoreExecutor) waitForRestorePreflightState(
	ctx context.Context,
	plan backupcontract.Plan,
	manifest backupartifact.ArchiveManifest,
) (controller.ClusterState, []uint64, error) {
	timer := time.NewTimer(restoreHealthConvergenceTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(restoreHealthConvergencePoll)
	defer ticker.Stop()

	var lastErr error
	for {
		state, err := e.cluster.LocalState(ctx)
		if err != nil {
			return controller.ClusterState{}, nil, err
		}
		active, err := validateRestorePreflightState(
			state, plan, manifest, time.Now().UTC(),
		)
		if err == nil {
			return state, active, nil
		}
		if !errors.Is(err, errRestoreHealthNotConverged) {
			return controller.ClusterState{}, nil, err
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return controller.ClusterState{}, nil, ctx.Err()
		case <-timer.C:
			return controller.ClusterState{}, nil, lastErr
		case <-ticker.C:
		}
	}
}

func validateRestorePreflightState(
	state controller.ClusterState,
	plan backupcontract.Plan,
	manifest backupartifact.ArchiveManifest,
	now time.Time,
) ([]uint64, error) {
	if state.ClusterID != manifest.SourceClusterID ||
		state.Config.HashSlotCount != backupcontract.HashSlotCount ||
		len(state.Tasks) != 0 ||
		state.ScheduledBackup == nil ||
		state.ScheduledBackup.ActiveBackup != nil ||
		state.ScheduledBackup.ActiveRestore != nil ||
		state.ScheduledBackup.Plan == nil {
		return nil, fmt.Errorf("backup restore preflight: cluster state is not quiescent")
	}
	system := scheduledStateFromController(*state.ScheduledBackup)
	if system.Plan == nil || !equalRestoreStore(system.Plan.Store, plan.Store) {
		return nil, fmt.Errorf("backup restore preflight: backup plan changed")
	}
	active := activeRestoreDataNodes(state)
	if len(active) == 0 {
		return nil, fmt.Errorf("backup restore preflight: no active data nodes")
	}
	for hashSlot := 0; hashSlot < backupcontract.HashSlotCount; hashSlot++ {
		peers, err := restoreSlotPeers(state, uint16(hashSlot))
		if err != nil {
			return nil, err
		}
		for _, nodeID := range peers {
			if err := validateRestoreNodeHealth(
				state, nodeID, now, true,
			); err != nil {
				return nil, err
			}
		}
	}
	for _, nodeID := range active {
		if err := validateRestoreNodeHealth(
			state, nodeID, now, true,
		); err != nil {
			return nil, err
		}
	}
	for _, voter := range state.Controllers {
		if err := validateRestoreNodeHealth(
			state, voter.NodeID, now, false,
		); err != nil {
			return nil, err
		}
	}
	return active, nil
}

// RemoteRestoreClient forwards bounded node-local restore commands.
type RemoteRestoreClient interface {
	RunBackupRestoreNode(
		context.Context,
		uint64,
		backupcontract.RestoreNodeCommand,
	) (backupcontract.RestoreNodeReceipt, error)
}

// DistributedRestoreExecutor coordinates every current physical Slot replica.
type DistributedRestoreExecutor struct {
	cluster RestoreCluster
	local   *StagedRestoreNodeService
	remote  RemoteRestoreClient
}

// NewDistributedRestoreExecutor creates the current-cluster restore executor.
func NewDistributedRestoreExecutor(
	cluster RestoreCluster,
	local *StagedRestoreNodeService,
	remote RemoteRestoreClient,
) (*DistributedRestoreExecutor, error) {
	if cluster == nil || cluster.NodeID() == 0 || local == nil || remote == nil {
		return nil, fmt.Errorf("backup distributed restore: dependencies are required")
	}
	return &DistributedRestoreExecutor{
		cluster: cluster, local: local, remote: remote,
	}, nil
}

// VerifyArchive streams and authenticates every Slot before maintenance
// changes foreground availability.
func (e *DistributedRestoreExecutor) VerifyArchive(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	_, storeConfig, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return err
	}
	store, err := e.local.repository.Open(ctx, storeConfig)
	if err != nil {
		return err
	}
	if _, err := backupartifact.VerifyPublishedArchive(
		ctx, store, job.BackupID,
	); err != nil {
		if backupusecase.IsArchiveIntegrityFailure(err) {
			return errors.Join(
				err,
				backupusecase.MarkArchiveCorrupt(
					ctx, store, job.BackupID, time.Now().UTC(),
				),
			)
		}
		return err
	}
	current, err := e.controllerFence(ctx, true)
	if err != nil {
		return err
	}
	if current != coordinator {
		return fmt.Errorf("backup distributed restore: coordinator changed during verification")
	}
	return nil
}

// EnterMaintenance proves that every active data node has observed the
// Controller-owned maintenance fence.
func (e *DistributedRestoreExecutor) EnterMaintenance(
	ctx context.Context,
	job backupcontract.RestoreJob,
) (string, error) {
	state, store, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return "", err
	}
	command := restoreCommand(
		backupcontract.RestoreNodeActionPrepare, state, store, job, coordinator,
	)
	for _, nodeID := range activeRestoreDataNodes(state) {
		if _, err := e.call(ctx, nodeID, command); err != nil {
			return "", fmt.Errorf(
				"backup distributed restore: node %d maintenance: %w",
				nodeID, err,
			)
		}
	}
	return "controller-revision-" + strconv.FormatUint(state.Revision, 10), nil
}

// StageSlot captures rollback data and stages the target archive on every
// current physical Slot replica.
func (e *DistributedRestoreExecutor) StageSlot(
	ctx context.Context,
	job backupcontract.RestoreJob,
	hashSlot uint16,
	attempt uint32,
) (backupusecase.RestoreStageResult, error) {
	state, store, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return backupusecase.RestoreStageResult{}, err
	}
	peers, err := restoreSlotPeers(state, hashSlot)
	if err != nil {
		return backupusecase.RestoreStageResult{}, err
	}
	command := restoreCommand(
		backupcontract.RestoreNodeActionStage, state, store, job, coordinator,
	)
	command.HashSlot = hashSlot
	command.Attempt = attempt
	archiveStore, err := e.local.repository.Open(ctx, store)
	if err != nil {
		return backupusecase.RestoreStageResult{}, err
	}
	manifest, err := backupartifact.LoadPublishedArchiveMetadata(
		ctx, archiveStore, job.BackupID,
	)
	if err != nil {
		return backupusecase.RestoreStageResult{}, err
	}
	if int(hashSlot) >= len(manifest.Slots) ||
		manifest.Slots[hashSlot].HashSlot != hashSlot {
		return backupusecase.RestoreStageResult{},
			fmt.Errorf("backup distributed restore: archive Slot reference missing")
	}
	command.SlotReference = manifest.Slots[hashSlot]
	var logicalBytes uint64
	for _, nodeID := range peers {
		receipt, err := e.call(ctx, nodeID, command)
		if err != nil {
			return backupusecase.RestoreStageResult{}, fmt.Errorf(
				"backup distributed restore: stage Slot %d on node %d: %w",
				hashSlot, nodeID, err,
			)
		}
		if logicalBytes == 0 {
			logicalBytes = receipt.LogicalBytes
		} else if receipt.LogicalBytes != logicalBytes {
			return backupusecase.RestoreStageResult{},
				fmt.Errorf("backup distributed restore: staged byte evidence differs")
		}
	}
	return backupusecase.RestoreStageResult{
		ReplicaNodeIDs: peers, LogicalBytes: logicalBytes,
	}, nil
}

// VerifySlot replays every staged portable stream without mutating live data.
func (e *DistributedRestoreExecutor) VerifySlot(
	ctx context.Context,
	job backupcontract.RestoreJob,
	hashSlot uint16,
	attempt uint32,
) error {
	state, store, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return err
	}
	slot, err := restoreJobSlot(job, hashSlot, attempt)
	if err != nil {
		return err
	}
	command := restoreCommand(
		backupcontract.RestoreNodeActionVerify, state, store, job, coordinator,
	)
	command.HashSlot = hashSlot
	command.Attempt = attempt
	for _, nodeID := range slot.ReplicaNodeIDs {
		receipt, err := e.call(ctx, nodeID, command)
		if err != nil {
			return fmt.Errorf(
				"backup distributed restore: verify Slot %d on node %d: %w",
				hashSlot, nodeID, err,
			)
		}
		if receipt.LogicalBytes != slot.LogicalBytes {
			return fmt.Errorf(
				"backup distributed restore: Slot %d verification evidence differs",
				hashSlot,
			)
		}
	}
	return nil
}

// ActivateRestore installs every verified Slot while maintenance keeps the
// partially switched storage invisible. Runner transitions to rollback on any
// error, so a partial activation is never exposed.
func (e *DistributedRestoreExecutor) ActivateRestore(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	state, store, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return err
	}
	for _, slot := range job.Slots {
		current, err := restoreSlotPeers(state, slot.HashSlot)
		if err != nil {
			return err
		}
		if !slices.Equal(current, slot.ReplicaNodeIDs) {
			return fmt.Errorf(
				"backup distributed restore: Slot %d topology changed after staging",
				slot.HashSlot,
			)
		}
		command := restoreCommand(
			backupcontract.RestoreNodeActionSwitch, state, store, job,
			coordinator,
		)
		command.HashSlot = slot.HashSlot
		command.Attempt = slot.Attempt
		for _, nodeID := range slot.ReplicaNodeIDs {
			if _, err := e.call(ctx, nodeID, command); err != nil {
				return fmt.Errorf(
					"backup distributed restore: switch Slot %d on node %d: %w",
					slot.HashSlot, nodeID, err,
				)
			}
		}
	}
	activate := restoreCommand(
		backupcontract.RestoreNodeActionActivate, state, store, job, coordinator,
	)
	for _, nodeID := range activeRestoreDataNodes(state) {
		if _, err := e.call(ctx, nodeID, activate); err != nil {
			return fmt.Errorf(
				"backup distributed restore: activate node %d: %w",
				nodeID, err,
			)
		}
	}
	return e.checkActivatedNodes(ctx, state, store, job, coordinator)
}

// Rollback reinstalls captured pre-restore data only on replicas that reached
// the local SWITCHED marker.
func (e *DistributedRestoreExecutor) Rollback(
	ctx context.Context,
	job backupcontract.RestoreJob,
) error {
	state, store, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return err
	}
	for _, slot := range job.Slots {
		if slot.Attempt == 0 || len(slot.ReplicaNodeIDs) == 0 {
			continue
		}
		command := restoreCommand(
			backupcontract.RestoreNodeActionRollback, state, store, job,
			coordinator,
		)
		command.HashSlot = slot.HashSlot
		command.Attempt = slot.Attempt
		for _, nodeID := range slot.ReplicaNodeIDs {
			if _, err := e.call(ctx, nodeID, command); err != nil {
				return fmt.Errorf(
					"backup distributed restore: rollback Slot %d on node %d: %w",
					slot.HashSlot, nodeID, err,
				)
			}
		}
	}
	activate := restoreCommand(
		backupcontract.RestoreNodeActionActivate, state, store, job, coordinator,
	)
	for _, nodeID := range activeRestoreDataNodes(state) {
		if _, err := e.call(ctx, nodeID, activate); err != nil {
			return fmt.Errorf(
				"backup distributed restore: reactivate rollback on node %d: %w",
				nodeID, err,
			)
		}
	}
	return e.checkActivatedNodes(ctx, state, store, job, coordinator)
}

func (e *DistributedRestoreExecutor) checkActivatedNodes(
	ctx context.Context,
	state controller.ClusterState,
	store backupcontract.StoreConfig,
	job backupcontract.RestoreJob,
	coordinator restoreCoordinatorFence,
) error {
	health := restoreCommand(
		backupcontract.RestoreNodeActionHealth, state, store, job, coordinator,
	)
	deadline := time.Now().Add(30 * time.Second)
	if job.DeadlineUnixMillis > 0 {
		jobDeadline := time.UnixMilli(job.DeadlineUnixMillis)
		if jobDeadline.Before(deadline) {
			deadline = jobDeadline
		}
	}
	var lastErr error
	for {
		lastErr = nil
		for _, nodeID := range activeRestoreDataNodes(state) {
			if _, err := e.call(ctx, nodeID, health); err != nil {
				lastErr = fmt.Errorf(
					"backup distributed restore: node %d post-activation health: %w",
					nodeID, err,
				)
				break
			}
		}
		if lastErr == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return lastErr
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// ExitMaintenance removes staging files. Controller maintenance remains active
// until RestoreService atomically clears the active restore state.
func (e *DistributedRestoreExecutor) ExitMaintenance(
	ctx context.Context,
	job backupcontract.RestoreJob,
	_ bool,
) error {
	state, store, coordinator, err := e.restoreState(ctx, job.ID)
	if err != nil {
		return err
	}
	nodes := activeRestoreDataNodes(state)
	resume := restoreCommand(
		backupcontract.RestoreNodeActionResume, state, store, job, coordinator,
	)
	for _, nodeID := range nodes {
		if _, err := e.call(ctx, nodeID, resume); err != nil {
			return fmt.Errorf(
				"backup distributed restore: resume node %d: %w", nodeID, err,
			)
		}
	}
	cleanup := restoreCommand(
		backupcontract.RestoreNodeActionCleanup, state, store, job, coordinator,
	)
	for _, nodeID := range nodes {
		if _, err := e.call(ctx, nodeID, cleanup); err != nil {
			return fmt.Errorf(
				"backup distributed restore: cleanup node %d: %w", nodeID, err,
			)
		}
	}
	return nil
}

func (e *DistributedRestoreExecutor) restoreState(
	ctx context.Context,
	jobID string,
) (
	controller.ClusterState,
	backupcontract.StoreConfig,
	restoreCoordinatorFence,
	error,
) {
	state, err := e.cluster.LocalState(ctx)
	if err != nil {
		return controller.ClusterState{}, backupcontract.StoreConfig{},
			restoreCoordinatorFence{}, err
	}
	if state.ScheduledBackup == nil ||
		state.ScheduledBackup.ActiveRestore == nil ||
		state.ScheduledBackup.ActiveRestore.ID != jobID ||
		state.ScheduledBackup.Plan == nil {
		return controller.ClusterState{}, backupcontract.StoreConfig{},
			restoreCoordinatorFence{},
			fmt.Errorf("backup distributed restore: Controller state changed")
	}
	system := scheduledStateFromController(*state.ScheduledBackup)
	if system.Plan == nil {
		return controller.ClusterState{}, backupcontract.StoreConfig{},
			restoreCoordinatorFence{},
			fmt.Errorf("backup distributed restore: plan is unavailable")
	}
	coordinator, err := e.controllerFence(ctx, true)
	if err != nil {
		return controller.ClusterState{}, backupcontract.StoreConfig{},
			restoreCoordinatorFence{}, err
	}
	return state, system.Plan.Store, coordinator, nil
}

func (e *DistributedRestoreExecutor) controllerFence(
	ctx context.Context,
	requireLocalLeader bool,
) (restoreCoordinatorFence, error) {
	nodeID, term, err := e.cluster.BackupControllerFence(ctx)
	if err != nil {
		return restoreCoordinatorFence{}, err
	}
	if nodeID == 0 || term == 0 ||
		requireLocalLeader && nodeID != e.cluster.NodeID() {
		return restoreCoordinatorFence{},
			fmt.Errorf("backup distributed restore: local node is not coordinator")
	}
	return restoreCoordinatorFence{nodeID: nodeID, term: term}, nil
}

func (e *DistributedRestoreExecutor) call(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	var receipt backupcontract.RestoreNodeReceipt
	var err error
	retryDeadline := time.Now().Add(30 * time.Second)
	for attempt := 0; ; attempt++ {
		if nodeID == e.cluster.NodeID() {
			receipt, err = e.local.Run(ctx, command)
		} else {
			receipt, err = e.remote.RunBackupRestoreNode(ctx, nodeID, command)
		}
		if err == nil {
			return receipt, nil
		}
		if !time.Now().Before(retryDeadline) {
			break
		}
		delay := min(
			time.Duration(attempt+1)*100*time.Millisecond,
			time.Second,
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return backupcontract.RestoreNodeReceipt{}, ctx.Err()
		case <-timer.C:
		}
	}
	return backupcontract.RestoreNodeReceipt{}, err
}

func restoreCommand(
	action backupcontract.RestoreNodeAction,
	state controller.ClusterState,
	store backupcontract.StoreConfig,
	job backupcontract.RestoreJob,
	coordinator restoreCoordinatorFence,
) backupcontract.RestoreNodeCommand {
	return backupcontract.RestoreNodeCommand{
		Action: action, Store: store, JobID: job.ID, BackupID: job.BackupID,
		ControllerRevision: state.Revision,
		TargetActivation:   job.TargetActivation,
		MaxMessageID:       job.MaxMessageID,
		CoordinatorNodeID:  coordinator.nodeID,
		CoordinatorTerm:    coordinator.term,
	}
}

func activeRestoreDataNodes(state controller.ClusterState) []uint64 {
	nodes := make([]uint64, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.JoinState == controller.NodeJoinStateActive &&
			node.HasRole(controller.NodeRoleData) {
			nodes = append(nodes, node.NodeID)
		}
	}
	slices.Sort(nodes)
	return nodes
}

func validateRestoreNodeHealth(
	state controller.ClusterState,
	nodeID uint64,
	now time.Time,
	requireDataRole bool,
) error {
	found := false
	for _, node := range state.Nodes {
		if node.NodeID != nodeID {
			continue
		}
		found = true
		if node.JoinState != controller.NodeJoinStateActive ||
			requireDataRole && !node.HasRole(controller.NodeRoleData) ||
			node.Status != controller.NodeStatusAlive {
			return fmt.Errorf(
				"backup restore preflight: node %d is not healthy and active",
				nodeID,
			)
		}
		break
	}
	if !found {
		return fmt.Errorf("backup restore preflight: node %d is missing", nodeID)
	}
	for _, report := range state.NodeHealthReports {
		if report.NodeID != nodeID {
			continue
		}
		if report.Status != controller.NodeStatusAlive ||
			!report.RuntimeReady ||
			report.ObservedControlRevision < state.Revision ||
			report.ReportedAtUnixMilli <= 0 ||
			now.Sub(time.UnixMilli(report.ReportedAtUnixMilli)) > time.Minute {
			return fmt.Errorf(
				"%w: node %d health report is stale",
				errRestoreHealthNotConverged, nodeID,
			)
		}
		return nil
	}
	return fmt.Errorf(
		"%w: node %d health report is missing",
		errRestoreHealthNotConverged, nodeID,
	)
}

func restoreSlotPeers(
	state controller.ClusterState,
	hashSlot uint16,
) ([]uint64, error) {
	var slotID uint32
	for _, item := range state.HashSlots.Ranges {
		if hashSlot >= item.From && hashSlot <= item.To {
			slotID = item.SlotID
			break
		}
	}
	if slotID == 0 {
		return nil, fmt.Errorf(
			"backup distributed restore: Hash Slot %d is unassigned", hashSlot,
		)
	}
	for _, slot := range state.Slots {
		if slot.SlotID != slotID {
			continue
		}
		peers := append([]uint64(nil), slot.DesiredPeers...)
		slices.Sort(peers)
		peers = slices.Compact(peers)
		if len(peers) == 0 || peers[0] == 0 {
			return nil, fmt.Errorf(
				"backup distributed restore: physical Slot %d has no replicas",
				slotID,
			)
		}
		return peers, nil
	}
	return nil, fmt.Errorf(
		"backup distributed restore: physical Slot %d is missing", slotID,
	)
}

func restoreJobSlot(
	job backupcontract.RestoreJob,
	hashSlot uint16,
	attempt uint32,
) (backupcontract.RestoreSlotProgress, error) {
	if int(hashSlot) >= len(job.Slots) {
		return backupcontract.RestoreSlotProgress{},
			fmt.Errorf("backup distributed restore: Slot progress is missing")
	}
	slot := job.Slots[hashSlot]
	if slot.HashSlot != hashSlot || slot.Attempt != attempt ||
		len(slot.ReplicaNodeIDs) == 0 {
		return backupcontract.RestoreSlotProgress{},
			fmt.Errorf("backup distributed restore: Slot staging fence changed")
	}
	return slot, nil
}

var _ backupusecase.RestoreExecutor = (*DistributedRestoreExecutor)(nil)
