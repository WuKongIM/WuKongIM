package goroutine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestRegistryManagedTaskSnapshot(t *testing.T) {
	r := New()
	blocker := make(chan struct{})

	r.GoN(TaskGatewayAsyncDispatch, 3, func(_ int) {
		<-blocker
	})

	waitForTaskActive(t, r, TaskGatewayAsyncDispatch, 3)
	snapshot := r.Snapshot()
	if snapshot.ManagedTotal != 3 {
		t.Fatalf("ManagedTotal = %d, want 3", snapshot.ManagedTotal)
	}
	if snapshot.ProcessTotal < snapshot.ManagedTotal {
		t.Fatalf("ProcessTotal = %d, want >= managed %d", snapshot.ProcessTotal, snapshot.ManagedTotal)
	}
	if snapshot.BootID == "" || snapshot.ProcessStartedAt.IsZero() {
		t.Fatalf("process identity = (%q, %v), want populated", snapshot.BootID, snapshot.ProcessStartedAt)
	}

	task, ok := findTaskSnapshot(snapshot, TaskGatewayAsyncDispatch)
	if !ok {
		t.Fatalf("task %q missing from snapshot", TaskGatewayAsyncDispatch)
	}
	if task.Active != 3 || task.Peak != 3 || task.TotalStarted != 3 {
		t.Fatalf("task snapshot = %+v, want active/peak/started 3", task)
	}

	close(blocker)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Group(ModuleGateway).Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestRegistryBootIdentitySeparatesCounterAndPeakResets(t *testing.T) {
	first := New()
	done := make(chan struct{})
	first.Go(TaskManagerSnapshotFanout, func() { close(done) })
	<-done
	waitForTaskActive(t, first, TaskManagerSnapshotFanout, 0)
	firstSnapshot := first.Snapshot()
	firstTask, _ := findTaskSnapshot(firstSnapshot, TaskManagerSnapshotFanout)
	if firstTask.TotalStarted != 1 || firstTask.Peak != 1 {
		t.Fatalf("first boot task = %+v, want started/peak 1", firstTask)
	}

	secondSnapshot := New().Snapshot()
	secondTask, _ := findTaskSnapshot(secondSnapshot, TaskManagerSnapshotFanout)
	if secondSnapshot.BootID == firstSnapshot.BootID {
		t.Fatalf("second boot id = %q, want distinct from first boot", secondSnapshot.BootID)
	}
	if secondTask.TotalStarted != 0 || secondTask.Peak != 0 {
		t.Fatalf("second boot task = %+v, want reset counters and peak", secondTask)
	}
}

func TestRegistryWaitReturnsBoundedTaskEvidence(t *testing.T) {
	r := New()
	blocker := make(chan struct{})
	r.Go(TaskAppConversationDrain, func() { <-blocker })
	waitForTaskActive(t, r, TaskAppConversationDrain, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := r.Group(ModuleApp).Wait(ctx)
	var waitErr *WaitError
	if !errors.As(err, &waitErr) {
		t.Fatalf("Wait() error = %v, want *WaitError", err)
	}
	if waitErr.Module != ModuleApp || len(waitErr.Tasks) != 1 {
		t.Fatalf("wait evidence = %+v, want app task", waitErr)
	}
	if waitErr.Tasks[0].Task != TaskAppConversationDrain || waitErr.Tasks[0].Active != 1 {
		t.Fatalf("task evidence = %+v, want lifecycle active=1", waitErr.Tasks[0])
	}
	if waitErr.Tasks[0].RunningFor <= 0 {
		t.Fatalf("RunningFor = %v, want > 0", waitErr.Tasks[0].RunningFor)
	}

	close(blocker)
}

func TestRegistryWaitFromExcludesPreexistingProcessTasks(t *testing.T) {
	r := New()
	preexisting := make(chan struct{})
	r.Go(TaskAppTopCollector, func() { <-preexisting })
	waitForTaskActive(t, r, TaskAppTopCollector, 1)
	baseline := r.Baseline()

	owned := make(chan struct{})
	r.Go(TaskAppTopCollector, func() { <-owned })
	waitForTaskActive(t, r, TaskAppTopCollector, 2)
	close(owned)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Group(ModuleApp).WaitFrom(ctx, baseline); err != nil {
		t.Fatalf("WaitFrom() error = %v, want preexisting task excluded", err)
	}
	if r.hasLifecycleFence.Load() {
		t.Fatal("lifecycle fence remained enabled after WaitFrom")
	}
	if task, _ := findTaskSnapshot(r.Snapshot(), TaskAppTopCollector); task.Active != 1 {
		t.Fatalf("preexisting task active = %d, want 1", task.Active)
	}
	close(preexisting)
}

func TestRegistryEmptyBaselineKeepsLifecycleFastPath(t *testing.T) {
	r := New()
	r.Baseline()
	if r.hasLifecycleFence.Load() {
		t.Fatal("empty baseline enabled lifecycle fence accounting")
	}
}

func TestRegistryWaitFromDoesNotLetPreexistingExitMaskOwnedTask(t *testing.T) {
	r := New()
	preexisting := make(chan struct{})
	r.Go(TaskAppTopCollector, func() { <-preexisting })
	waitForTaskActive(t, r, TaskAppTopCollector, 1)
	baseline := r.Baseline()

	owned := make(chan struct{})
	r.Go(TaskAppTopCollector, func() { <-owned })
	waitForTaskActive(t, r, TaskAppTopCollector, 2)
	close(preexisting)
	waitForTaskActive(t, r, TaskAppTopCollector, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := r.Group(ModuleApp).WaitFrom(ctx, baseline)
	var waitErr *WaitError
	if !errors.As(err, &waitErr) {
		t.Fatalf("WaitFrom() error = %v, want owned task evidence after preexisting exit", err)
	}
	if len(waitErr.Tasks) != 1 || waitErr.Tasks[0].Task != TaskAppTopCollector || waitErr.Tasks[0].Active != 1 {
		t.Fatalf("WaitFrom() evidence = %+v, want top collector active=1", waitErr.Tasks)
	}
	close(owned)
}

func TestRegistryIsolatedTaskRecoversAndReportsPanic(t *testing.T) {
	panicEvents := make(chan PanicEvent, 1)
	r := New(WithPanicObserver(func(event PanicEvent) {
		panicEvents <- event
	}))

	done := make(chan struct{})
	r.Go(TaskManagerSnapshotFanout, func() {
		defer close(done)
		panic("isolated")
	})
	<-done

	select {
	case event := <-panicEvents:
		if event.Task != TaskManagerSnapshotFanout || event.Recovered != "isolated" {
			t.Fatalf("panic event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("panic observer was not called")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Group(ModuleManager).Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	task, ok := findTaskSnapshot(r.Snapshot(), TaskManagerSnapshotFanout)
	if !ok || task.PanicCount != 1 || task.Health != HealthWarning || task.HealthReason != "panic" {
		t.Fatalf("task snapshot = %+v, %v; want one reported warning/panic failure", task, ok)
	}
}

func TestRegistryPoolPanicUsesCatalogPolicy(t *testing.T) {
	var observed PanicEvent
	r := New(WithPanicObserver(func(event PanicEvent) { observed = event }))
	r.ReportPoolPanic(TaskGatewayAsyncAuth, "isolated pool")
	task, _ := findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if task.PanicCount != 1 || observed.Task != TaskGatewayAsyncAuth {
		t.Fatalf("recovering pool panic task=%+v event=%+v", task, observed)
	}

	defer func() {
		if recovered := recover(); recovered != "critical pool" {
			t.Fatalf("critical pool recovered = %v, want repanic", recovered)
		}
	}()
	r.ReportPoolPanic(TaskTransportRPCExecutor, "critical pool")
}

func TestRegistryCriticalTaskRepanics(t *testing.T) {
	if os.Getenv("WK_GOROUTINE_CRITICAL_PANIC_HELPER") == "1" {
		r := New()
		r.Go(TaskControllerRaftRun, func() {
			panic("critical")
		})
		time.Sleep(time.Second)
		os.Exit(0)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRegistryCriticalTaskRepanics$")
	command.Env = append(os.Environ(), "WK_GOROUTINE_CRITICAL_PANIC_HELPER=1")
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.Success() {
		t.Fatalf("critical panic command error = %v, want non-zero exit", err)
	}
}

func TestRegistryIncludesOwnedPoolWorkersWithoutCountingBusyTasksAsGoroutines(t *testing.T) {
	r := New()
	stats := PoolStats{
		Goroutines:    5,
		BusyTasks:     2,
		Capacity:      8,
		QueueDepth:    4,
		RejectedTotal: 3,
	}
	unregister, err := r.RegisterPool(TaskGatewayAsyncAuth, func() PoolStats {
		return stats
	})
	if err != nil {
		t.Fatalf("RegisterPool() error = %v", err)
	}
	defer unregister()

	task, ok := findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if !ok {
		t.Fatalf("pool task %q missing", TaskGatewayAsyncAuth)
	}
	if task.Active != 5 || task.BusyTasks != 2 || task.PoolCapacity != 8 || task.QueueDepth != 4 || task.RejectedTotal != 3 {
		t.Fatalf("pool task snapshot = %+v", task)
	}
}

func TestRegistryModulePeakTracksConcurrentModuleActivity(t *testing.T) {
	r := New()
	first := make(chan struct{})
	r.Go(TaskAppTopCollector, func() { <-first })
	waitForTaskActive(t, r, TaskAppTopCollector, 1)
	if module := findModuleSnapshot(t, r.Snapshot(), ModuleApp); module.Peak != 1 {
		t.Fatalf("first module peak = %d, want 1", module.Peak)
	}
	close(first)
	waitForTaskActive(t, r, TaskAppTopCollector, 0)

	second := make(chan struct{})
	r.Go(TaskAppSeedJoin, func() { <-second })
	waitForTaskActive(t, r, TaskAppSeedJoin, 1)
	if module := findModuleSnapshot(t, r.Snapshot(), ModuleApp); module.Peak != 1 {
		t.Fatalf("non-overlapping module peak = %d, want 1", module.Peak)
	}
	close(second)
}

func TestRegistryPreservesRetiredPoolRejectedCounter(t *testing.T) {
	r := New()
	unregister, err := r.RegisterPool(TaskGatewayAsyncAuth, func() PoolStats {
		return PoolStats{RejectedTotal: 3}
	})
	if err != nil {
		t.Fatalf("RegisterPool() error = %v", err)
	}
	unregister()

	task, ok := findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if !ok || task.RejectedTotal != 3 {
		t.Fatalf("retired pool task = %+v, found=%v, want rejected_total=3", task, ok)
	}
}

func TestRegistryDerivesFixedAndPoolHealth(t *testing.T) {
	r := New()
	release := make(chan struct{})
	r.GoN(TaskAppTopCollector, 2, func(int) { <-release })
	waitForTaskActive(t, r, TaskAppTopCollector, 2)
	task, _ := findTaskSnapshot(r.Snapshot(), TaskAppTopCollector)
	if task.Health != HealthCritical || task.HealthReason != "over_declared" {
		t.Fatalf("over-declared health = %s/%s, want critical/over_declared", task.Health, task.HealthReason)
	}
	close(release)
	waitForTaskActive(t, r, TaskAppTopCollector, 0)
	r.SetReady(true)
	task, _ = findTaskSnapshot(r.Snapshot(), TaskAppTopCollector)
	if task.Health != HealthCritical || task.HealthReason != "missing" {
		t.Fatalf("missing health = %s/%s, want critical/missing", task.Health, task.HealthReason)
	}

	unregister, err := r.RegisterPool(TaskGatewayAsyncAuth, func() PoolStats {
		return PoolStats{Goroutines: 2, BusyTasks: 2, Capacity: 2, QueueDepth: 1, RejectedTotal: 1}
	})
	if err != nil {
		t.Fatalf("RegisterPool() error = %v", err)
	}
	defer unregister()
	task, _ = findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if task.Health != HealthCritical || task.HealthReason != "saturated" {
		t.Fatalf("pool health = %s/%s, want critical/saturated", task.Health, task.HealthReason)
	}
}

func TestCatalogRejectsFixedTaskWithoutDeclaredCount(t *testing.T) {
	_, err := buildCatalog([]TaskSpec{{
		ID:          TaskID("app/undeclared"),
		Module:      ModuleApp,
		Name:        "undeclared",
		Kind:        TaskKindFixed,
		PanicPolicy: PanicPolicyRepanic,
	}})
	if err == nil {
		t.Fatal("buildCatalog() error = nil, want undeclared fixed task rejected")
	}
}

func TestRegistryMarksRequiredNeverStartedTaskMissingAfterReadiness(t *testing.T) {
	r := New()
	r.SetReady(true)

	task, ok := findTaskSnapshot(r.Snapshot(), TaskClusterNodeControlWatch)
	if !ok {
		t.Fatalf("task %q missing from snapshot", TaskClusterNodeControlWatch)
	}
	if task.Health != HealthCritical || task.HealthReason != "missing" {
		t.Fatalf("required never-started health = %s/%s, want critical/missing", task.Health, task.HealthReason)
	}
}

func TestRegistryTreatsCompletedDatabaseMigrationAsHealthyBurst(t *testing.T) {
	r := New()
	done := make(chan struct{})
	r.Go(TaskDatabaseLatestMigration, func() { close(done) })
	<-done
	waitForTaskActive(t, r, TaskDatabaseLatestMigration, 0)
	r.SetReady(true)

	task, _ := findTaskSnapshot(r.Snapshot(), TaskDatabaseLatestMigration)
	if task.Kind != TaskKindBurst {
		t.Fatalf("migration kind = %s, want burst", task.Kind)
	}
	if task.Health != HealthNormal || task.HealthReason != "" {
		t.Fatalf("completed migration health = %s/%s, want normal", task.Health, task.HealthReason)
	}
}

func TestRegistryDistinguishesTransientPressureAndFullQueue(t *testing.T) {
	r := New()
	stats := PoolStats{
		Goroutines:    3,
		BusyTasks:     2,
		Capacity:      2,
		QueueDepth:    1,
		QueueCapacity: 4,
	}
	unregister, err := r.RegisterPool(TaskGatewayAsyncAuth, func() PoolStats { return stats })
	if err != nil {
		t.Fatalf("RegisterPool() error = %v", err)
	}
	defer unregister()

	task, _ := findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if task.Health != HealthNormal {
		t.Fatalf("transient pressure health = %s/%s, want normal", task.Health, task.HealthReason)
	}

	now := time.Now()
	r.poolsMu.RLock()
	for _, registration := range r.pools {
		if registration.task != TaskGatewayAsyncAuth {
			continue
		}
		registration.pressureMu.Lock()
		registration.pressureSince = now.Add(-poolPressureGrace - time.Second)
		registration.pressureObserved = now
		registration.pressureMu.Unlock()
	}
	r.poolsMu.RUnlock()
	task, _ = findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if task.Health != HealthWarning || task.HealthReason != "pressure" {
		t.Fatalf("sustained pressure health = %s/%s, want warning/pressure", task.Health, task.HealthReason)
	}

	stats.BusyTasks = 1
	stats.QueueDepth = stats.QueueCapacity
	task, _ = findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if task.Health != HealthCritical || task.HealthReason != "saturated" {
		t.Fatalf("full queue health = %s/%s, want critical/saturated", task.Health, task.HealthReason)
	}
}

func TestRegistryPoolHealthUsesWorstRegistrationBeforeAggregation(t *testing.T) {
	r := New()
	full := PoolStats{
		Goroutines:    2,
		BusyTasks:     2,
		Capacity:      2,
		QueueDepth:    4,
		QueueCapacity: 4,
	}
	idle := PoolStats{
		Goroutines:    100,
		Capacity:      100,
		QueueCapacity: 1000,
	}
	unregisterFull, err := r.RegisterPool(TaskChannelWorkerPool, func() PoolStats { return full })
	if err != nil {
		t.Fatalf("RegisterPool(full) error = %v", err)
	}
	defer unregisterFull()
	unregisterIdle, err := r.RegisterPool(TaskChannelWorkerPool, func() PoolStats { return idle })
	if err != nil {
		t.Fatalf("RegisterPool(idle) error = %v", err)
	}
	defer unregisterIdle()

	task, _ := findTaskSnapshot(r.Snapshot(), TaskChannelWorkerPool)
	if task.QueueDepth >= task.QueueCapacity {
		t.Fatalf("aggregate queue = %d/%d, want diluted below capacity for regression proof", task.QueueDepth, task.QueueCapacity)
	}
	if task.Health != HealthCritical || task.HealthReason != "saturated" {
		t.Fatalf("multi-pool health = %s/%s, want critical/saturated", task.Health, task.HealthReason)
	}

	full.QueueDepth = 1
	r.Snapshot()
	now := time.Now()
	r.poolsMu.RLock()
	for _, registration := range r.pools {
		if registration.task != TaskChannelWorkerPool || registration.snapshot().QueueCapacity != 4 {
			continue
		}
		registration.pressureMu.Lock()
		registration.pressureSince = now.Add(-poolPressureGrace - time.Second)
		registration.pressureObserved = now
		registration.pressureMu.Unlock()
	}
	r.poolsMu.RUnlock()
	task, _ = findTaskSnapshot(r.Snapshot(), TaskChannelWorkerPool)
	if task.BusyTasks*100 >= task.PoolCapacity*80 {
		t.Fatalf("aggregate utilization = %d/%d, want diluted below pressure threshold for regression proof", task.BusyTasks, task.PoolCapacity)
	}
	if task.Health != HealthWarning || task.HealthReason != "pressure" {
		t.Fatalf("multi-pool pressure = %s/%s, want warning/pressure", task.Health, task.HealthReason)
	}
}

func TestRegistryPoolPressureRequiresRecentObservations(t *testing.T) {
	r := New()
	stats := PoolStats{
		Goroutines:    2,
		BusyTasks:     2,
		Capacity:      2,
		QueueDepth:    1,
		QueueCapacity: 4,
	}
	unregister, err := r.RegisterPool(TaskGatewayAsyncAuth, func() PoolStats { return stats })
	if err != nil {
		t.Fatalf("RegisterPool() error = %v", err)
	}
	defer unregister()

	r.Snapshot()
	now := time.Now()
	r.poolsMu.RLock()
	for _, registration := range r.pools {
		if registration.task != TaskGatewayAsyncAuth {
			continue
		}
		registration.pressureMu.Lock()
		registration.pressureSince = now.Add(-poolPressureGrace - time.Second)
		registration.pressureObserved = now.Add(-poolPressureMaxObservationGap - time.Second)
		registration.pressureMu.Unlock()
	}
	r.poolsMu.RUnlock()

	task, _ := findTaskSnapshot(r.Snapshot(), TaskGatewayAsyncAuth)
	if task.Health != HealthNormal {
		t.Fatalf("stale pressure observation health = %s/%s, want normal", task.Health, task.HealthReason)
	}
}

func TestRegistrySupportsConcurrentLifecycleAndSnapshots(t *testing.T) {
	r := New()
	release := make(chan struct{})
	var starters sync.WaitGroup
	for i := 0; i < 16; i++ {
		starters.Add(1)
		go func() {
			defer starters.Done()
			r.Go(TaskManagerSnapshotFanout, func() { <-release })
		}()
	}
	starters.Wait()
	waitForTaskActive(t, r, TaskManagerSnapshotFanout, 16)

	var readers sync.WaitGroup
	for i := 0; i < 16; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 50; j++ {
				snapshot := r.Snapshot()
				if snapshot.ManagedTotal < 16 {
					t.Errorf("ManagedTotal = %d, want >= 16", snapshot.ManagedTotal)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(release)
}

func BenchmarkRegistryLifecycleAccounting(b *testing.B) {
	r := New()
	_, state := r.mustTask(TaskChannelAppendRouter)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			generation := r.start(state)
			r.done(state, generation)
		}
	})
}

func TestRegistrySnapshotAverageStaysBelowOneMillisecond(t *testing.T) {
	r := New()
	const samples = 500
	started := time.Now()
	for i := 0; i < samples; i++ {
		_ = r.Snapshot()
	}
	average := time.Since(started) / samples
	if average >= time.Millisecond {
		t.Fatalf("Snapshot() average = %v, want < 1ms", average)
	}
}

func BenchmarkRegistrySnapshot(b *testing.B) {
	r := New()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Snapshot()
	}
}

func waitForTaskActive(t *testing.T, registry *Registry, task TaskID, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := findTaskSnapshot(registry.Snapshot(), task)
		if ok && snapshot.Active == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %q did not reach active=%d", task, want)
}

func findTaskSnapshot(snapshot Snapshot, task TaskID) (TaskSnapshot, bool) {
	for _, module := range snapshot.Modules {
		for _, item := range module.Tasks {
			if item.Task == task {
				return item, true
			}
		}
	}
	return TaskSnapshot{}, false
}

func findModuleSnapshot(t *testing.T, snapshot Snapshot, module Module) ModuleSnapshot {
	t.Helper()
	for _, item := range snapshot.Modules {
		if item.Module == module {
			return item
		}
	}
	t.Fatalf("module %q missing from snapshot", module)
	return ModuleSnapshot{}
}
