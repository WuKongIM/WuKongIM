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

func TestRegistryWaitReturnsBoundedTaskEvidence(t *testing.T) {
	r := New()
	blocker := make(chan struct{})
	r.Go(TaskAppLifecycle, func() { <-blocker })
	waitForTaskActive(t, r, TaskAppLifecycle, 1)

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
	if waitErr.Tasks[0].Task != TaskAppLifecycle || waitErr.Tasks[0].Active != 1 {
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
	if task, _ := findTaskSnapshot(r.Snapshot(), TaskAppTopCollector); task.Active != 1 {
		t.Fatalf("preexisting task active = %d, want 1", task.Active)
	}
	close(preexisting)
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
	if !ok || task.PanicCount != 1 {
		t.Fatalf("task snapshot = %+v, %v; want one panic", task, ok)
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
