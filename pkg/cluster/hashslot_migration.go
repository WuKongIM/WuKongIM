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
	hashSlotDeltaOutboxReplayLimit                = 64
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

type hashSlotDeltaOutboxStore interface {
	LoadHashSlotMigrationState(context.Context, uint16) (metadb.HashSlotMigrationState, error)
	ListHashSlotMigrationStates(context.Context) ([]metadb.HashSlotMigrationState, error)
	ListHashSlotMigrationOutbox(context.Context, uint16, uint64, uint64, uint64, int) ([]metadb.HashSlotMigrationOutboxRow, error)
	AckHashSlotMigrationOutbox(context.Context, uint16, uint64, uint64, uint64) error
	CleanupHashSlotMigrationOutbox(context.Context, uint16, uint64, uint64, uint64) error
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
		if migration.Target == slotID {
			incoming = append(incoming, migration.HashSlot)
		}
		if migration.Phase != PhaseDelta && migration.Phase != PhaseSwitching {
			continue
		}
		if migration.Source == slotID && migration.Target != 0 {
			outgoing[migration.HashSlot] = migration.Target
		}
	}
	if len(outgoing) == 0 {
		outgoing = nil
	}
	return outgoing, incoming
}

func (c *Cluster) makeHashSlotDeltaForwarder() func(context.Context, multiraft.SlotID, multiraft.Command) error {
	return func(_ context.Context, target multiraft.SlotID, cmd multiraft.Command) error {
		if c == nil || target == 0 || cmd.SlotID == 0 || len(cmd.Data) == 0 {
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
	if c == nil || target == 0 || len(cmd.Data) == 0 {
		return
	}
	payload := metafsm.EncodeApplyDeltaCommand(cmd.SlotID, cmd.Index, cmd.HashSlot, cmd.Data)
	for c != nil && !c.stopped.Load() {
		timeout := c.cfg.ForwardTimeout
		if timeout <= 0 {
			timeout = defaultForwardTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := c.proposeHashSlotDelta(ctx, target, cmd.HashSlot, payload)
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
	if err := c.retryPendingHashSlotDeltaCleanups(ctx); err != nil {
		return err
	}
	if err := c.reconcileOrphanedHashSlotMigrationStates(ctx, desired); err != nil {
		return err
	}
	pendingAbortErr := c.retryPendingHashSlotAborts(ctx, desired, table.Version())

	for _, active := range c.migrationWorker.ActiveMigrations() {
		want, ok := desired[active.HashSlot]
		abortLocal := false
		cleanupSourceOutbox := false
		switch {
		case !ok:
			abortLocal = true
			cleanupSourceOutbox = true
		case c.hasPendingHashSlotAbort(active.HashSlot, active.Source, active.Target):
			abortLocal = true
		case want.Source != active.Source || want.Target != active.Target:
			abortLocal = true
			cleanupSourceOutbox = true
		case !c.shouldExecuteHashSlotMigration(want):
			abortLocal = true
		}
		if abortLocal {
			if err := c.migrationWorker.AbortMigration(active.HashSlot); err != nil {
				return err
			}
			if cleanupSourceOutbox {
				if err := c.completeHashSlotDeltaOutboxCleanup(ctx, HashSlotMigration{HashSlot: active.HashSlot, Source: active.Source, Target: active.Target}); err != nil {
					return err
				}
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

	if err := c.completeActiveHashSlotSnapshots(ctx); err != nil {
		return err
	}
	outboxDrained, err := c.replayActiveHashSlotDeltaOutboxes(ctx, desiredList)
	if err != nil {
		return err
	}
	fenceReady, err := c.ensureSwitchingHashSlotMigrationFences(ctx, desired, outboxDrained)
	if err != nil {
		return err
	}

	for _, migration := range desired {
		if migration.Phase != PhaseSwitching || !c.shouldExecuteHashSlotMigration(migration) {
			continue
		}
		if !outboxDrained[migration.HashSlot] || !fenceReady[migration.HashSlot] {
			continue
		}
		if err := c.migrationWorker.MarkSwitchComplete(migration.HashSlot); err != nil {
			return err
		}
	}
	if err := c.advanceSwitchingHashSlotMigrations(ctx, desired, outboxDrained); err != nil {
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
			if transition.To == slotmigration.PhaseSwitching {
				if !outboxDrained[transition.HashSlot] {
					continue
				}
				ready, err := c.ensureHashSlotMigrationFence(ctx, HashSlotMigration{HashSlot: transition.HashSlot, Source: transition.Source, Target: transition.Target, Phase: PhaseDelta})
				if err != nil {
					return err
				}
				if !ready {
					continue
				}
			}
			if err := c.advanceHashSlotMigration(ctx, transition.HashSlot, transition.Source, transition.Target, uint8(transition.To)); err != nil {
				return err
			}
		case slotmigration.PhaseDone:
			if !outboxDrained[transition.HashSlot] || !fenceReady[transition.HashSlot] {
				continue
			}
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

func (c *Cluster) advanceSwitchingHashSlotMigrations(ctx context.Context, desired map[uint16]HashSlotMigration, outboxDrained map[uint16]bool) error {
	if c == nil || c.migrationWorker == nil {
		return nil
	}
	for _, active := range c.migrationWorker.ActiveMigrations() {
		if active.Phase != slotmigration.PhaseSwitching {
			continue
		}
		want, ok := desired[active.HashSlot]
		if !ok || want.Phase != PhaseDelta || want.Source != active.Source || want.Target != active.Target {
			continue
		}
		if !outboxDrained[active.HashSlot] {
			continue
		}
		ready, err := c.ensureHashSlotMigrationFence(ctx, want)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		if err := c.advanceHashSlotMigration(ctx, active.HashSlot, active.Source, active.Target, uint8(slotmigration.PhaseSwitching)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cluster) ensureSwitchingHashSlotMigrationFences(ctx context.Context, desired map[uint16]HashSlotMigration, outboxDrained map[uint16]bool) (map[uint16]bool, error) {
	ready := make(map[uint16]bool)
	if c == nil {
		return ready, nil
	}
	for _, migration := range desired {
		if migration.Phase != PhaseSwitching {
			continue
		}
		if !c.shouldExecuteHashSlotMigration(migration) || !outboxDrained[migration.HashSlot] {
			continue
		}
		ok, err := c.ensureHashSlotMigrationFence(ctx, migration)
		if err != nil {
			return nil, err
		}
		ready[migration.HashSlot] = ok
	}
	return ready, nil
}

func (c *Cluster) ensureHashSlotMigrationFence(ctx context.Context, migration HashSlotMigration) (bool, error) {
	store, ok := c.hashSlotMigrationStore(migration.Source)
	if !ok {
		return false, nil
	}
	ready, err := hashSlotMigrationFenceReady(ctx, store, migration)
	if err != nil || ready {
		return ready, err
	}
	if err := c.proposeHashSlotFence(ctx, migration.Source, migration.HashSlot, migration.Target); err != nil {
		return false, err
	}
	return hashSlotMigrationFenceReady(ctx, store, migration)
}

func (c *Cluster) ensureHashSlotMigrationFenceApplied(ctx context.Context, migration HashSlotMigration) (bool, error) {
	store, ok := c.hashSlotMigrationStore(migration.Source)
	if !ok {
		return false, nil
	}
	applied, err := hashSlotMigrationFenceApplied(ctx, store, migration)
	if err != nil || applied {
		return applied, err
	}
	if err := c.proposeHashSlotFence(ctx, migration.Source, migration.HashSlot, migration.Target); err != nil {
		return false, err
	}
	return hashSlotMigrationFenceApplied(ctx, store, migration)
}

func (c *Cluster) hashSlotMigrationStore(source multiraft.SlotID) (hashSlotDeltaOutboxStore, bool) {
	sm, ok := c.runtimeStateMachine(source)
	if !ok {
		return nil, false
	}
	store, ok := sm.(hashSlotDeltaOutboxStore)
	if !ok {
		return nil, false
	}
	return store, true
}

func (c *Cluster) hashSlotMigrationStores() map[multiraft.SlotID]hashSlotDeltaOutboxStore {
	if c == nil {
		return nil
	}
	c.runtimeStateMachinesMu.RLock()
	defer c.runtimeStateMachinesMu.RUnlock()

	stores := make(map[multiraft.SlotID]hashSlotDeltaOutboxStore)
	for slotID, sm := range c.runtimeStateMachines {
		store, ok := sm.(hashSlotDeltaOutboxStore)
		if !ok {
			continue
		}
		stores[slotID] = store
	}
	return stores
}

func hashSlotMigrationFenceApplied(ctx context.Context, store hashSlotDeltaOutboxStore, migration HashSlotMigration) (bool, error) {
	state, err := store.LoadHashSlotMigrationState(ctx, migration.HashSlot)
	if errors.Is(err, metadb.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.SourceSlot == uint64(migration.Source) && state.TargetSlot == uint64(migration.Target) && state.FenceIndex != 0, nil
}

func hashSlotMigrationFenceReady(ctx context.Context, store hashSlotDeltaOutboxStore, migration HashSlotMigration) (bool, error) {
	state, err := store.LoadHashSlotMigrationState(ctx, migration.HashSlot)
	if errors.Is(err, metadb.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state.SourceSlot != uint64(migration.Source) || state.TargetSlot != uint64(migration.Target) || state.FenceIndex == 0 {
		return false, nil
	}
	return state.LastAckedIndex >= state.FenceIndex && state.LastAckedIndex >= state.LastOutboxIndex, nil
}

func (c *Cluster) replayActiveHashSlotDeltaOutboxes(ctx context.Context, migrations []HashSlotMigration) (map[uint16]bool, error) {
	drained := make(map[uint16]bool)
	for _, migration := range migrations {
		if migration.Phase != PhaseDelta && migration.Phase != PhaseSwitching {
			continue
		}
		if !c.shouldExecuteHashSlotMigration(migration) {
			continue
		}
		ok, err := c.replayHashSlotDeltaOutbox(ctx, migration)
		if err != nil {
			return nil, err
		}
		drained[migration.HashSlot] = ok
	}
	return drained, nil
}

func (c *Cluster) replayHashSlotDeltaOutbox(ctx context.Context, migration HashSlotMigration) (bool, error) {
	sm, ok := c.runtimeStateMachine(migration.Source)
	if !ok {
		return false, nil
	}
	store, ok := sm.(hashSlotDeltaOutboxStore)
	if !ok {
		return false, nil
	}

	rows, err := store.ListHashSlotMigrationOutbox(ctx, migration.HashSlot, uint64(migration.Source), uint64(migration.Target), 0, hashSlotDeltaOutboxReplayLimit)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		payload := metafsm.EncodeApplyDeltaCommand(migration.Source, row.SourceIndex, row.HashSlot, row.Data)
		if err := c.proposeHashSlotDelta(ctx, migration.Target, row.HashSlot, payload); err != nil {
			return false, err
		}
		if err := c.proposeHashSlotOutboxAck(ctx, migration.Source, row.HashSlot, migration.Target, row.SourceIndex); err != nil {
			return false, err
		}
	}
	return len(rows) < hashSlotDeltaOutboxReplayLimit, nil
}

func (c *Cluster) proposeHashSlotDelta(ctx context.Context, target multiraft.SlotID, hashSlot uint16, payload []byte) error {
	if c != nil && c.proposeHashSlotDeltaTestHook != nil {
		return c.proposeHashSlotDeltaTestHook(ctx, target, hashSlot, payload)
	}
	return c.ProposeWithHashSlot(ctx, target, hashSlot, payload)
}

func (c *Cluster) proposeHashSlotFence(ctx context.Context, source multiraft.SlotID, hashSlot uint16, target multiraft.SlotID) error {
	payload := metafsm.EncodeEnterFenceCommandForTarget(hashSlot, target)
	if c != nil && c.proposeHashSlotFenceTestHook != nil {
		return c.proposeHashSlotFenceTestHook(ctx, source, hashSlot, payload)
	}
	return c.ProposeWithHashSlot(ctx, source, hashSlot, payload)
}

func (c *Cluster) proposeHashSlotOutboxAck(ctx context.Context, source multiraft.SlotID, hashSlot uint16, target multiraft.SlotID, sourceIndex uint64) error {
	payload := metafsm.EncodeAckHashSlotMigrationOutboxCommand(hashSlot, source, target, sourceIndex)
	if c != nil && c.proposeHashSlotAckTestHook != nil {
		return c.proposeHashSlotAckTestHook(ctx, source, hashSlot, target, sourceIndex, payload)
	}
	return c.ProposeWithHashSlot(ctx, source, hashSlot, payload)
}

func (c *Cluster) proposeHashSlotOutboxCleanup(ctx context.Context, source multiraft.SlotID, hashSlot uint16, target multiraft.SlotID, throughIndex uint64) error {
	payload := metafsm.EncodeCleanupHashSlotMigrationOutboxCommand(hashSlot, source, target, throughIndex)
	if c != nil && c.proposeHashSlotCleanupTestHook != nil {
		return c.proposeHashSlotCleanupTestHook(ctx, source, hashSlot, target, payload)
	}
	return c.ProposeWithHashSlot(ctx, source, hashSlot, payload)
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

func (c *Cluster) retryPendingHashSlotDeltaCleanups(ctx context.Context) error {
	if c == nil || len(c.pendingHashSlotDeltaCleanups) == 0 {
		return nil
	}
	for _, migration := range c.pendingHashSlotDeltaCleanups {
		if err := c.completeHashSlotDeltaOutboxCleanup(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cluster) reconcileOrphanedHashSlotMigrationStates(ctx context.Context, desired map[uint16]HashSlotMigration) error {
	if c == nil {
		return nil
	}
	stores := c.hashSlotMigrationStores()
	seen := make(map[hashSlotDeltaCleanupKey]struct{})
	for _, store := range stores {
		states, err := store.ListHashSlotMigrationStates(ctx)
		if err != nil {
			return err
		}
		for _, state := range states {
			if state.SourceSlot == 0 || state.TargetSlot == 0 || state.LastOutboxIndex == 0 {
				continue
			}
			key := hashSlotDeltaCleanupKey{
				hashSlot: state.HashSlot,
				source:   multiraft.SlotID(state.SourceSlot),
				target:   multiraft.SlotID(state.TargetSlot),
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if active, ok := desired[state.HashSlot]; ok && uint64(active.Source) == state.SourceSlot && uint64(active.Target) == state.TargetSlot {
				continue
			}
			migration := HashSlotMigration{
				HashSlot: state.HashSlot,
				Source:   multiraft.SlotID(state.SourceSlot),
				Target:   multiraft.SlotID(state.TargetSlot),
			}
			if _, ok := stores[migration.Source]; ok {
				if err := c.completeHashSlotDeltaOutboxCleanup(ctx, migration); err != nil {
					return err
				}
				continue
			}
			if err := store.CleanupHashSlotMigrationOutbox(ctx, state.HashSlot, state.SourceSlot, state.TargetSlot, state.LastOutboxIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Cluster) hasPendingHashSlotAbort(hashSlot uint16, source, target multiraft.SlotID) bool {
	if c == nil || len(c.pendingHashSlotAborts) == 0 {
		return false
	}
	pending, ok := c.pendingHashSlotAborts[hashSlot]
	return ok && pending.migration.Source == source && pending.migration.Target == target
}

func (c *Cluster) markPendingHashSlotAbort(hashSlot uint16, source, target multiraft.SlotID) {
	if c == nil || source == 0 || target == 0 {
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
	if _, err := c.currentManagedSlotLeader(migration.Target); err != nil {
		return ErrSlotNotFound
	}
	fenced, err := c.ensureHashSlotMigrationFenceApplied(ctx, HashSlotMigration{HashSlot: migration.HashSlot, Source: migration.Source, Target: migration.Target, Phase: PhaseSnapshot})
	if err != nil {
		return err
	}
	if !fenced {
		return ErrSlotNotFound
	}
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
	if !c.cfg.EnableHashSlotMigration {
		return ErrInvalidConfig
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
	if err := c.submitHashSlotMigration(ctx, controllerRPCFinalizeMigration, slotcontroller.CommandKindFinalizeMigration, slotcontroller.MigrationRequest{
		HashSlot: hashSlot,
		Source:   uint64(source),
		Target:   uint64(target),
	}); err != nil {
		return err
	}
	return c.completeHashSlotDeltaOutboxCleanup(ctx, HashSlotMigration{HashSlot: hashSlot, Source: source, Target: target})
}

func (c *Cluster) AbortHashSlotMigration(ctx context.Context, hashSlot uint16) error {
	req := slotcontroller.MigrationRequest{HashSlot: hashSlot}
	var cleanup HashSlotMigration
	if c != nil {
		if pending, ok := c.pendingHashSlotAborts[hashSlot]; ok {
			req.Source = uint64(pending.migration.Source)
			req.Target = uint64(pending.migration.Target)
			cleanup = pending.migration
			if err := c.submitHashSlotMigration(ctx, controllerRPCAbortMigration, slotcontroller.CommandKindAbortMigration, req); err != nil {
				return err
			}
			return c.completeHashSlotDeltaOutboxCleanup(ctx, cleanup)
		}
	}
	if c != nil && c.router != nil {
		if table := c.router.hashSlotTable.Load(); table != nil {
			if migration := table.GetMigration(hashSlot); migration != nil {
				req.Source = uint64(migration.Source)
				req.Target = uint64(migration.Target)
				cleanup = *migration
			}
		}
	}
	if err := c.submitHashSlotMigration(ctx, controllerRPCAbortMigration, slotcontroller.CommandKindAbortMigration, req); err != nil {
		return err
	}
	if cleanup.Source == 0 || cleanup.Target == 0 {
		return nil
	}
	return c.completeHashSlotDeltaOutboxCleanup(ctx, cleanup)
}

func (c *Cluster) completeHashSlotDeltaOutboxCleanup(ctx context.Context, migration HashSlotMigration) error {
	if migration.Source == 0 || migration.Target == 0 {
		return nil
	}
	key := hashSlotDeltaCleanupKey{hashSlot: migration.HashSlot, source: migration.Source, target: migration.Target}
	if c.pendingHashSlotDeltaCleanups == nil {
		c.pendingHashSlotDeltaCleanups = make(map[hashSlotDeltaCleanupKey]HashSlotMigration)
	}
	c.pendingHashSlotDeltaCleanups[key] = migration
	if err := c.cleanupHashSlotDeltaOutbox(ctx, migration); err != nil {
		if errors.Is(err, ErrSlotNotFound) || errors.Is(err, ErrInvalidConfig) {
			return nil
		}
		return err
	}
	delete(c.pendingHashSlotDeltaCleanups, key)
	return nil
}

func (c *Cluster) cleanupHashSlotDeltaOutbox(ctx context.Context, migration HashSlotMigration) error {
	if migration.Source == 0 || migration.Target == 0 {
		return nil
	}
	store, ok := c.hashSlotMigrationStore(migration.Source)
	if !ok {
		return ErrSlotNotFound
	}
	state, err := store.LoadHashSlotMigrationState(ctx, migration.HashSlot)
	if errors.Is(err, metadb.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.SourceSlot != uint64(migration.Source) || state.TargetSlot != uint64(migration.Target) {
		return nil
	}
	if state.LastOutboxIndex == 0 {
		return nil
	}
	return c.proposeHashSlotOutboxCleanup(ctx, migration.Source, migration.HashSlot, migration.Target, state.LastOutboxIndex)
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
