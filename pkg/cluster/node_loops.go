package cluster

import (
	"context"
	"slices"
	"time"

	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/observe"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/tasks"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultTaskReconcileIdleInterval = time.Second
	maxTaskReconcileIdleInterval     = 5 * time.Second
	minHealthReportTimeout           = 10 * time.Millisecond
	// healthReportRenewalAttempts reserves repeated Controller write opportunities within one lease TTL.
	healthReportRenewalAttempts = 3
)

func (n *Node) startWatchLoop() {
	if n == nil || n.control == nil || n.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.watchCancel = cancel
	watch := n.control.Watch()
	n.watchWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterNodeControlWatch, func() {
		defer n.watchWG.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watch:
				if !ok {
					return
				}
				_ = n.applySnapshot(ctx, ev.Snapshot)
			}
		}
	})
}

func (n *Node) stopWatchLoop() {
	if n == nil || n.watchCancel == nil {
		return
	}
	n.watchCancel()
	n.watchWG.Wait()
	n.watchCancel = nil
}

func (n *Node) startHealthReportLoop() {
	if n == nil || n.control == nil || n.healthReportCancel != nil {
		return
	}
	reporter := observe.NewReporter(observe.ReporterConfig{
		NodeID:                  n.cfg.NodeID,
		Addr:                    n.cfg.ListenAddr,
		Controller:              n.control,
		RuntimeReady:            n.runtimeReadyForHealthReport,
		ObservedControlRevision: n.observedControlRevision,
		ObservedSlotRevision:    n.observedSlotRevision,
		SlotStatus:              n.localSlotStatuses,
	})
	n.healthReporter = reporter
	ctx, cancel := context.WithCancel(context.Background())
	n.healthReportCancel = cancel
	n.healthReportWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterHealthReport, func() {
		defer n.healthReportWG.Done()
		_ = n.reportNodeHealth(ctx, reporter)
		loop := observe.NewLoop(n.cfg.HealthReport.Interval, func(ctx context.Context) error {
			return n.reportNodeHealth(ctx, reporter)
		})
		loop.Start(ctx)
		<-ctx.Done()
		loop.Stop()
	})
}

func (n *Node) stopHealthReportLoop(ctx context.Context) {
	if n == nil || n.healthReportCancel == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reporter := n.healthReporter
	n.healthReportCancel()
	n.healthReportWG.Wait()
	_ = n.reportNodeHealth(ctx, reporter)
	n.healthReportCancel = nil
	n.healthReporter = nil
}

func (n *Node) reportNodeHealth(ctx context.Context, reporter *observe.Reporter) error {
	if reporter == nil {
		return nil
	}
	attemptStartedAt := time.Now()
	reportCtx, cancel := context.WithTimeout(ctx, healthReportTimeout(n.cfg.HealthReport.Interval, n.cfg.HealthReport.TTL))
	defer cancel()
	report, err := reporter.ReportNode(reportCtx)
	if err != nil {
		return err
	}
	if n.channelDataPlaneLease != nil && report.RuntimeReady {
		n.channelDataPlaneLease.MarkVisible(attemptStartedAt)
	}
	return nil
}

// healthReportTimeout reserves one scheduling interval and three bounded renewal attempts within a positive lease TTL.
func healthReportTimeout(interval, ttl time.Duration) time.Duration {
	timeout := interval
	if ttl > interval {
		renewalWindow := (ttl - interval) / healthReportRenewalAttempts
		if renewalWindow > timeout {
			timeout = renewalWindow
		}
	}
	if timeout < minHealthReportTimeout {
		timeout = minHealthReportTimeout
	}
	if ttl > 0 && timeout > ttl {
		timeout = ttl
	}
	return timeout
}

func (n *Node) runtimeReadyForHealthReport() bool {
	if n == nil || !n.started.Load() || n.stopping.Load() {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.snapshot.RoutesReady && n.snapshot.SlotsReady && n.snapshot.ChannelsReady
}

func (n *Node) observedControlRevision() uint64 {
	if n == nil {
		return 0
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.controlSnapshot.Revision
}

func (n *Node) observedSlotRevision() uint64 {
	if n == nil || n.defaultSlotRuntime == nil {
		return 0
	}
	var revision uint64
	for _, slotID := range n.defaultSlotRuntime.Slots() {
		status, err := n.defaultSlotRuntime.Status(slotID)
		if err != nil {
			continue
		}
		revision = max(revision, status.ConfigAppliedIndex, status.AppliedIndex, status.CommitIndex)
	}
	return revision
}

func (n *Node) localSlotStatuses() []observe.SlotStatus {
	if n == nil || n.defaultSlotRuntime == nil {
		return nil
	}
	slotIDs := n.defaultSlotRuntime.Slots()
	statuses := make([]observe.SlotStatus, 0, len(slotIDs))
	for _, slotID := range slotIDs {
		status, err := n.defaultSlotRuntime.Status(slotID)
		if err != nil {
			continue
		}
		statuses = append(statuses, observe.SlotStatus{SlotID: uint32(slotID), Leader: uint64(status.LeaderID)})
	}
	return statuses
}

func (n *Node) startTaskReconcileLoop() {
	if n == nil || n.control == nil || n.tasks == nil || n.taskReconcileCancel != nil {
		return
	}
	fastInterval := n.taskReconcileFastInterval()
	idleInterval := taskReconcileIdleInterval(fastInterval)
	ctx, cancel := context.WithCancel(context.Background())
	n.taskReconcileCancel = cancel
	n.taskReconcileWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterTaskReconcile, func() {
		defer n.taskReconcileWG.Done()
		timer := time.NewTimer(n.nextTaskReconcileInterval(fastInterval, idleInterval))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				nextInterval := idleInterval
				snapshot, err := n.control.LocalSnapshot(ctx)
				if err != nil {
					n.recordTaskReconcileError("snapshot", err)
					if n.hasCachedControlTasks() {
						nextInterval = fastInterval
					}
					timer.Reset(nextInterval)
					continue
				}
				if len(snapshot.Tasks) != 0 {
					nextInterval = fastInterval
				} else {
					n.clearTaskReconcileError()
					timer.Reset(nextInterval)
					continue
				}
				if err := n.reconcileTasks(ctx, snapshot); err != nil {
					n.recordTaskReconcileError("reconcile", err)
				} else {
					n.clearTaskReconcileError()
				}
				timer.Reset(nextInterval)
			}
		}
	})
}

func (n *Node) taskReconcileFastInterval() time.Duration {
	interval := n.cfg.Slots.TickInterval * 5
	if interval <= 0 {
		return 100 * time.Millisecond
	}
	return interval
}

func taskReconcileIdleInterval(fast time.Duration) time.Duration {
	interval := fast * 20
	if interval < defaultTaskReconcileIdleInterval {
		return defaultTaskReconcileIdleInterval
	}
	if interval > maxTaskReconcileIdleInterval {
		return maxTaskReconcileIdleInterval
	}
	return interval
}

func (n *Node) nextTaskReconcileInterval(fast, idle time.Duration) time.Duration {
	if n.hasCachedControlTasks() {
		return fast
	}
	return idle
}

func (n *Node) hasCachedControlTasks() bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.controlSnapshot.Tasks) > 0
}

func (n *Node) stopTaskReconcileLoop() {
	if n == nil || n.taskReconcileCancel == nil {
		return
	}
	n.taskReconcileCancel()
	n.taskReconcileWG.Wait()
	n.taskReconcileCancel = nil
}

func (n *Node) startPreferredLeaderReconcileLoop() {
	if n == nil || n.control == nil || n.preferredLeaderReconciler == nil || n.preferredLeaderCancel != nil {
		return
	}
	interval := n.preferredLeaderInterval
	if interval <= 0 {
		interval = taskReconcileIdleInterval(n.taskReconcileFastInterval())
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.preferredLeaderCancel = cancel
	n.preferredLeaderWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterPreferredLeader, func() {
		defer n.preferredLeaderWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snapshot, err := n.control.LocalSnapshot(ctx)
				if err != nil {
					continue
				}
				_ = n.preferredLeaderReconciler.Reconcile(ctx, snapshot)
			}
		}
	})
}

func (n *Node) stopPreferredLeaderReconcileLoop() {
	if n == nil {
		return
	}
	n.invalidatePreferredLeaderIntent()
	if n.preferredLeaderCancel == nil {
		return
	}
	n.preferredLeaderCancel()
	n.preferredLeaderWG.Wait()
	n.preferredLeaderCancel = nil
}

func (n *Node) beginPreferredLeaderIntentApply() {
	if n == nil {
		return
	}
	n.preferredLeaderIntentMu.Lock()
	generation := n.preferredLeaderIntentGeneration
	n.preferredLeaderIntentGeneration = nil
	n.preferredLeaderIntentMu.Unlock()
	if generation != nil {
		generation.invalidate()
	}
}

func (n *Node) publishPreferredLeaderIntent(snapshot control.Snapshot) {
	if n == nil {
		return
	}
	generation := newPreferredLeaderIntentGeneration(snapshot)
	n.preferredLeaderIntentMu.Lock()
	if n.stopping.Load() {
		n.preferredLeaderIntentMu.Unlock()
		generation.invalidate()
		return
	}
	previous := n.preferredLeaderIntentGeneration
	n.preferredLeaderIntentGeneration = generation
	n.preferredLeaderIntentMu.Unlock()
	if previous != nil {
		previous.invalidate()
	}
}

func (n *Node) invalidatePreferredLeaderIntent() {
	n.beginPreferredLeaderIntentApply()
}

func (n *Node) preferredLeaderIntentGuard(intent tasks.PreferredLeaderIntent) (multiraft.PreferredLeaderTransferGuard, bool) {
	if n == nil {
		return nil, false
	}
	n.preferredLeaderIntentMu.Lock()
	generation := n.preferredLeaderIntentGeneration
	n.preferredLeaderIntentMu.Unlock()
	if generation == nil || !generation.matches(intent) {
		return nil, false
	}
	return generation, true
}

func newPreferredLeaderIntentGeneration(snapshot control.Snapshot) *preferredLeaderIntentGeneration {
	ctx, cancel := context.WithCancel(context.Background())
	return &preferredLeaderIntentGeneration{
		ctx:      ctx,
		cancel:   cancel,
		current:  true,
		snapshot: snapshot.Clone(),
	}
}

func (g *preferredLeaderIntentGeneration) Context() context.Context {
	if g == nil {
		return nil
	}
	return g.ctx
}

func (g *preferredLeaderIntentGeneration) ExecuteIfCurrent(action func()) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.current {
		return false
	}
	if action != nil {
		action()
	}
	return true
}

func (g *preferredLeaderIntentGeneration) matches(intent tasks.PreferredLeaderIntent) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.current || g.ctx == nil || g.ctx.Err() != nil {
		return false
	}
	snapshot := g.snapshot
	if snapshot.Revision != intent.Revision || len(snapshot.Tasks) != 0 {
		return false
	}
	for _, assignment := range snapshot.Slots {
		if assignment.SlotID != intent.SlotID {
			continue
		}
		current := assignment.ConfigEpoch == intent.ConfigEpoch &&
			assignment.PreferredLeader == intent.PreferredLeader &&
			slices.Equal(assignment.DesiredPeers, intent.DesiredPeers)
		return current
	}
	return false
}

func (g *preferredLeaderIntentGeneration) invalidate() {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.current {
		g.current = false
		g.cancel()
	}
	g.mu.Unlock()
}

func (n *Node) startChannelTickLoop() {
	if n == nil || n.channels == nil || n.channelTickCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.channelTickCancel = cancel
	n.channelTickWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterChannelTick, func() {
		defer n.channelTickWG.Done()
		ticker := time.NewTicker(n.cfg.Channel.TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = n.channels.Tick(ctx)
			}
		}
	})
}

func (n *Node) stopChannelTickLoop() {
	if n == nil || n.channelTickCancel == nil {
		return
	}
	n.channelTickCancel()
	n.channelTickWG.Wait()
	n.channelTickCancel = nil
}

func (n *Node) startChannelRetentionGCLoop() {
	if n == nil || n.channelRetentionCancel != nil || !n.cfg.ChannelRetention.PhysicalGCEnabled {
		return
	}
	if n.channels == nil || n.defaultChannelStore == nil || n.defaultSlotMetaDB == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.channelRetentionCancel = cancel
	n.channelRetentionWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterChannelRetention, func() {
		defer n.channelRetentionWG.Done()
		ticker := time.NewTicker(n.cfg.ChannelRetention.ScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = n.RunChannelRetentionGCOnce(ctx)
			}
		}
	})
}

func (n *Node) stopChannelRetentionGCLoop() {
	if n == nil || n.channelRetentionCancel == nil {
		return
	}
	n.channelRetentionCancel()
	n.channelRetentionWG.Wait()
	n.channelRetentionCancel = nil
}

func (n *Node) startChannelMigrationLoop() {
	if n == nil || n.channelMigrationCancel != nil || !n.cfg.ChannelMigration.Enabled {
		return
	}
	store := n.ChannelMigrationStore()
	if n.channels == nil || store == nil {
		return
	}
	interval := n.cfg.ChannelMigration.ScanInterval
	if interval <= 0 {
		return
	}
	metaReader := defaultChannelRuntimeMetaStore{node: n}
	var migrationObserver channelwrapper.MigrationObserver
	if observer, ok := n.cfg.Channel.Observer.(channelwrapper.MigrationObserver); ok {
		migrationObserver = observer
	}
	var repairObserver channelwrapper.RepairObserver
	if observer, ok := n.cfg.Channel.Observer.(channelwrapper.RepairObserver); ok {
		repairObserver = observer
	}
	executor := channelwrapper.NewMigrationExecutor(channelwrapper.MigrationExecutorConfig{
		LocalNode: n.cfg.NodeID,
		Source:    n,
		Store:     store,
		Runtime:   n,
		Meta:      metaReader,
		Observer:  migrationObserver,
		TaskLimit: n.cfg.ChannelMigration.TaskLimit,
	})
	scanner := channelwrapper.NewRepairScanner(channelwrapper.RepairScannerConfig{
		Enabled:         true,
		PageLimit:       n.cfg.ChannelMigration.ScanLimit,
		MaxPagesPerTick: n.cfg.ChannelMigration.MaxPagesPerTick,
		MaxTasksPerTick: n.cfg.ChannelMigration.MaxTasksPerTick,
		TickInterval:    interval,
		Observer:        repairObserver,
	}, n, store)

	ctx, cancel := context.WithCancel(context.Background())
	n.channelMigrationCancel = cancel
	n.channelMigrationWG.Add(1)
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskClusterChannelMigration, func() {
		defer n.channelMigrationWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = executor.RunOnce(ctx)
				_, _ = scanner.RunOnce(ctx)
			}
		}
	})
}

func (n *Node) stopChannelMigrationLoop() {
	if n == nil || n.channelMigrationCancel == nil {
		return
	}
	n.channelMigrationCancel()
	n.channelMigrationWG.Wait()
	n.channelMigrationCancel = nil
}
