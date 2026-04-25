package cluster

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/slotmigration"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultHashSlotMigrationStableThreshold int64 = 100
	defaultHashSlotMigrationStableWindow          = time.Second
	hashSlotDeltaForwardRetryInterval             = 100 * time.Millisecond
)

type hashSlotMigrationWorker interface {
	StartMigration(hashSlot uint16, source, target multiraft.SlotID) error
	AbortMigration(hashSlot uint16) error
	MarkSnapshotComplete(hashSlot uint16, sourceApplyIndex uint64, bytesTransferred int64) error
	MarkSwitchComplete(hashSlot uint16) error
	ActiveMigrations() []slotmigration.Migration
	Tick() []slotmigration.Transition
}

type hashSlotSnapshotExporter interface {
	ExportHashSlotSnapshot(context.Context, uint16) (metadb.SlotSnapshot, error)
}

type hashSlotSnapshotImporter interface {
	ImportHashSlotSnapshot(context.Context, metadb.SlotSnapshot) error
}

func newHashSlotMigrationWorker() hashSlotMigrationWorker {
	return slotmigration.NewWorker(defaultHashSlotMigrationStableThreshold, defaultHashSlotMigrationStableWindow)
}

func deltaMigrationRuntimeForSlot(table *HashSlotTable, slotID multiraft.SlotID) (map[uint16]multiraft.SlotID, []uint16) {
	if table == nil || slotID == 0 {
		return nil, nil
	}

	var incoming []uint16
	outgoing := make(map[uint16]multiraft.SlotID)
	for _, migration := range table.ActiveMigrations() {
		if migration.Phase != PhaseDelta {
			continue
		}
		if migration.Source == slotID && migration.Target != 0 {
			outgoing[migration.HashSlot] = migration.Target
		}
		if migration.Target == slotID {
			incoming = append(incoming, migration.HashSlot)
		}
	}
	if len(outgoing) == 0 {
		outgoing = nil
	}
	return outgoing, incoming
}

func (c *Cluster) makeHashSlotDeltaForwarder() func(context.Context, multiraft.SlotID, multiraft.Command) error {
	return func(_ context.Context, target multiraft.SlotID, cmd multiraft.Command) error {
		if c == nil || target == 0 || cmd.SlotID == 0 || cmd.HashSlot == 0 || len(cmd.Data) == 0 {
			return nil
		}
		cloned := multiraft.Command{
			SlotID:   cmd.SlotID,
			HashSlot: cmd.HashSlot,
			Index:    cmd.Index,
			Term:     cmd.Term,
			Data:     append([]byte(nil), cmd.Data...),
		}
		go c.forwardHashSlotDelta(target, cloned)
		return nil
	}
}

func (c *Cluster) forwardHashSlotDelta(target multiraft.SlotID, cmd multiraft.Command) {
	if c == nil || target == 0 || cmd.HashSlot == 0 || len(cmd.Data) == 0 {
		return
	}
	payload := metafsm.EncodeApplyDeltaCommand(cmd.SlotID, cmd.Index, cmd.HashSlot, cmd.Data)
	for c != nil && !c.stopped.Load() {
		timeout := c.cfg.ForwardTimeout
		if timeout <= 0 {
			timeout = defaultForwardTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := c.ProposeWithHashSlot(ctx, target, cmd.HashSlot, payload)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(hashSlotDeltaForwardRetryInterval)
	}
}

func (c *Cluster) observeHashSlotMigrations(ctx context.Context) error {
	if c == nil || c.migrationWorker == nil || c.router == nil {
		return nil
	}

	table := c.router.hashSlotTable.Load()
	if table == nil {
		return nil
	}

	desired := make(map[uint16]HashSlotMigration)
	desiredList := table.ActiveMigrations()
	for _, migration := range desiredList {
		desired[migration.HashSlot] = migration
	}
	pendingAbortErr := c.retryPendingHashSlotAborts(ctx, desired, table.Version())

	for _, active := range c.migrationWorker.ActiveMigrations() {
		want, ok := desired[active.HashSlot]
		if !ok || c.hasPendingHashSlotAbort(active.HashSlot, active.Source, active.Target) || want.Source != active.Source || want.Target != active.Target || !c.shouldExecuteHashSlotMigration(want) {
			if err := c.migrationWorker.AbortMigration(active.HashSlot); err != nil {
				return err
			}
		}
	}

	for _, migration := range orderHashSlotMigrationsForStart(desiredList) {
		if c.hasPendingHashSlotAbort(migration.HashSlot, migration.Source, migration.Target) {
			continue
		}
		if !c.shouldExecuteHashSlotMigration(migration) {
			continue
		}
		if err := c.migrationWorker.StartMigration(migration.HashSlot, migration.Source, migration.Target); err != nil {
			return err
		}
	}

	for _, migration := range desired {
		if migration.Phase != PhaseSwitching || !c.shouldExecuteHashSlotMigration(migration) {
			continue
		}
		if err := c.migrationWorker.MarkSwitchComplete(migration.HashSlot); err != nil {
			return err
		}
	}

	if err := c.completeActiveHashSlotSnapshots(ctx); err != nil {
		return err
	}

	for _, transition := range c.migrationWorker.Tick() {
		if transition.TimedOut {
			c.markPendingHashSlotAbort(transition.HashSlot, transition.Source, transition.Target)
			if err := c.AbortHashSlotMigration(ctx, transition.HashSlot); err != nil {
				return err
			}
			c.recordPendingHashSlotAbortAttempt(transition.HashSlot, table.Version())
			if err := c.migrationWorker.AbortMigration(transition.HashSlot); err != nil {
				return err
			}
			if hook := c.obs.OnHashSlotMigration; hook != nil {
				hook(transition.HashSlot, transition.Source, transition.Target, "abort")
			}
			continue
		}
		switch transition.To {
		case slotmigration.PhaseDelta, slotmigration.PhaseSwitching:
			if err := c.advanceHashSlotMigration(ctx, transition.HashSlot, transition.Source, transition.Target, uint8(transition.To)); err != nil {
				return err
			}
		case slotmigration.PhaseDone:
			if err := c.finalizeHashSlotMigration(ctx, transition.HashSlot, transition.Source, transition.Target); err != nil {
				return err
			}
			if hook := c.obs.OnHashSlotMigration; hook != nil {
				hook(transition.HashSlot, transition.Source, transition.Target, "ok")
			}
		}
	}

	if pendingAbortErr != nil {
		return pendingAbortErr
	}
	return nil
}

func orderHashSlotMigrationsForStart(migrations []HashSlotMigration) []HashSlotMigration {
	if len(migrations) <= 1 {
		return append([]HashSlotMigration(nil), migrations...)
	}

	bySource := make(map[multiraft.SlotID][]HashSlotMigration)
	sources := make([]multiraft.SlotID, 0, len(migrations))
	for _, migration := range migrations {
		if _, ok := bySource[migration.Source]; !ok {
			sources = append(sources, migration.Source)
		}
		bySource[migration.Source] = append(bySource[migration.Source], migration)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i] < sources[j]
	})

	ordered := make([]HashSlotMigration, 0, len(migrations))
	for {
		progressed := false
		for _, source := range sources {
			queue := bySource[source]
			if len(queue) == 0 {
				continue
			}
			ordered = append(ordered, queue[0])
			bySource[source] = queue[1:]
			progressed = true
		}
		if !progressed {
			return ordered
		}
	}
}

func (c *Cluster) retryPendingHashSlotAborts(ctx context.Context, desired map[uint16]HashSlotMigration, tableVersion uint64) error {
	if c == nil || len(c.pendingHashSlotAborts) == 0 {
		return nil
	}
	var firstErr error
	for hashSlot, pending := range c.pendingHashSlotAborts {
		current, ok := desired[hashSlot]
		if !ok {
			delete(c.pendingHashSlotAborts, hashSlot)
			continue
		}
		if current.Source != pending.migration.Source || current.Target != pending.migration.Target {
			delete(c.pendingHashSlotAborts, hashSlot)
			continue
		}
		if pending.lastAbortTableVersion == tableVersion {
			continue
		}
		if err := c.AbortHashSlotMigration(ctx, hashSlot); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pending.lastAbortTableVersion = tableVersion
		c.pendingHashSlotAborts[hashSlot] = pending
	}
	return firstErr
}

func (c *Cluster) hasPendingHashSlotAbort(hashSlot uint16, source, target multiraft.SlotID) bool {
	if c == nil || len(c.pendingHashSlotAborts) == 0 {
		return false
	}
	pending, ok := c.pendingHashSlotAborts[hashSlot]
	return ok && pending.migration.Source == source && pending.migration.Target == target
}

func (c *Cluster) markPendingHashSlotAbort(hashSlot uint16, source, target multiraft.SlotID) {
	if c == nil || hashSlot == 0 || source == 0 || target == 0 {
		return
	}
	if c.pendingHashSlotAborts == nil {
		c.pendingHashSlotAborts = make(map[uint16]pendingHashSlotAbort)
	}
	c.pendingHashSlotAborts[hashSlot] = pendingHashSlotAbort{
		migration: HashSlotMigration{
			HashSlot: hashSlot,
			Source:   source,
			Target:   target,
		},
	}
}

func (c *Cluster) recordPendingHashSlotAbortAttempt(hashSlot uint16, tableVersion uint64) {
	if c == nil || tableVersion == 0 || len(c.pendingHashSlotAborts) == 0 {
		return
	}
	pending, ok := c.pendingHashSlotAborts[hashSlot]
	if !ok {
		return
	}
	pending.lastAbortTableVersion = tableVersion
	c.pendingHashSlotAborts[hashSlot] = pending
}

func (c *Cluster) completeActiveHashSlotSnapshots(ctx context.Context) error {
	for _, migration := range c.migrationWorker.ActiveMigrations() {
		if migration.Phase != slotmigration.PhaseSnapshot {
			continue
		}
		if err := c.completeHashSlotSnapshot(ctx, migration); err != nil {
			if errors.Is(err, ErrSlotNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

func (c *Cluster) completeHashSlotSnapshot(ctx context.Context, migration slotmigration.Migration) error {
	snap, sourceApplyIndex, err := c.exportHashSlotSnapshot(ctx, migration.Source, migration.HashSlot)
	if err != nil {
		return err
	}
	if err := c.importHashSlotSnapshot(ctx, migration.Target, snap); err != nil {
		return err
	}
	return c.migrationWorker.MarkSnapshotComplete(migration.HashSlot, sourceApplyIndex, int64(len(snap.Data)))
}

func (c *Cluster) shouldExecuteHashSlotMigration(migration HashSlotMigration) bool {
	if c == nil {
		return false
	}
	leaderID, err := c.currentManagedSlotLeader(migration.Source)
	if err != nil {
		return false
	}
	return c.IsLocal(leaderID)
}

func (c *Cluster) advanceHashSlotMigration(ctx context.Context, hashSlot uint16, source, target multiraft.SlotID, phase uint8) error {
	return c.submitHashSlotMigration(ctx, controllerRPCAdvanceMigration, slotcontroller.CommandKindAdvanceMigration, slotcontroller.MigrationRequest{
		HashSlot: hashSlot,
		Source:   uint64(source),
		Target:   uint64(target),
		Phase:    phase,
	})
}

func (c *Cluster) StartHashSlotMigration(ctx context.Context, hashSlot uint16, target multiraft.SlotID) error {
	if c == nil || c.router == nil {
		return ErrNotStarted
	}
	table := c.router.hashSlotTable.Load()
	if table == nil {
		return ErrNotStarted
	}
	source := table.Lookup(hashSlot)
	if source == 0 || target == 0 || source == target {
		return ErrInvalidConfig
	}
	return c.submitHashSlotMigration(ctx, controllerRPCStartMigration, slotcontroller.CommandKindStartMigration, slotcontroller.MigrationRequest{
		HashSlot: hashSlot,
		Source:   uint64(source),
		Target:   uint64(target),
	})
}

func (c *Cluster) finalizeHashSlotMigration(ctx context.Context, hashSlot uint16, source, target multiraft.SlotID) error {
	return c.submitHashSlotMigration(ctx, controllerRPCFinalizeMigration, slotcontroller.CommandKindFinalizeMigration, slotcontroller.MigrationRequest{
		HashSlot: hashSlot,
		Source:   uint64(source),
		Target:   uint64(target),
	})
}

func (c *Cluster) AbortHashSlotMigration(ctx context.Context, hashSlot uint16) error {
	req := slotcontroller.MigrationRequest{HashSlot: hashSlot}
	if c != nil {
		if pending, ok := c.pendingHashSlotAborts[hashSlot]; ok {
			req.Source = uint64(pending.migration.Source)
			req.Target = uint64(pending.migration.Target)
			return c.submitHashSlotMigration(ctx, controllerRPCAbortMigration, slotcontroller.CommandKindAbortMigration, req)
		}
	}
	if c != nil && c.router != nil {
		if table := c.router.hashSlotTable.Load(); table != nil {
			if migration := table.GetMigration(hashSlot); migration != nil {
				req.Source = uint64(migration.Source)
				req.Target = uint64(migration.Target)
			}
		}
	}
	return c.submitHashSlotMigration(ctx, controllerRPCAbortMigration, slotcontroller.CommandKindAbortMigration, req)
}

func (c *Cluster) submitHashSlotMigration(ctx context.Context, rpcKind string, commandKind slotcontroller.CommandKind, req slotcontroller.MigrationRequest) error {
	if c == nil {
		return ErrNotStarted
	}
	if c.controllerClient != nil {
		return c.retryControllerCommand(ctx, func(attemptCtx context.Context) error {
			switch rpcKind {
			case controllerRPCStartMigration:
				return c.controllerClient.StartMigration(attemptCtx, req)
			case controllerRPCAdvanceMigration:
				return c.controllerClient.AdvanceMigration(attemptCtx, req)
			case controllerRPCFinalizeMigration:
				return c.controllerClient.FinalizeMigration(attemptCtx, req)
			case controllerRPCAbortMigration:
				return c.controllerClient.AbortMigration(attemptCtx, req)
			default:
				return ErrInvalidConfig
			}
		})
	}
	if c.controller == nil {
		return ErrNotStarted
	}
	if c.controller.LeaderID() != uint64(c.cfg.NodeID) {
		return ErrNotLeader
	}
	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind:      commandKind,
		Migration: &req,
	}); err != nil {
		if errors.Is(err, controllerraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	return nil
}
