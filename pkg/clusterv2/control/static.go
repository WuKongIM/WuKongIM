package control

import (
	"context"
	"sync"
)

// StaticController is a deterministic in-memory Controller used by tests and smoke harnesses.
type StaticController struct {
	mu         sync.RWMutex
	snapshot   Snapshot
	watch      chan SnapshotEvent
	nodeReport NodeReport
	slotReport SlotRuntimeReport
	// CompletedTasks records fenced completion writes for tests.
	CompletedTasks []TaskResult
	// FailedTasks records fenced failure writes for tests.
	FailedTasks []TaskResult
	// ProgressReports records participant progress writes for tests.
	ProgressReports []TaskProgress
	// LeaderTransfers records Slot leader transfer intents for tests.
	LeaderTransfers []SlotLeaderTransferRequest
	started         bool
}

// NewStaticController creates a StaticController seeded with snapshot.
func NewStaticController(snapshot Snapshot) *StaticController {
	return &StaticController{snapshot: snapshot.Clone(), watch: make(chan SnapshotEvent, 16)}
}

// Start validates the initial snapshot and marks the controller started.
func (c *StaticController) Start(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.snapshot.Validate(); err != nil {
		return err
	}
	c.started = true
	return nil
}

// Stop marks the controller stopped.
func (c *StaticController) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.started = false
	c.mu.Unlock()
	return nil
}

// LocalSnapshot returns a deep copy of the latest snapshot.
func (c *StaticController) LocalSnapshot(ctx context.Context) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot.Clone(), nil
}

// LeaderID returns the snapshot ControllerID.
func (c *StaticController) LeaderID() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot.ControllerID
}

// ReportNode records the last node report.
func (c *StaticController) ReportNode(ctx context.Context, report NodeReport) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.nodeReport = report
	c.mu.Unlock()
	return nil
}

// ReportSlots records the last Slot runtime report.
func (c *StaticController) ReportSlots(ctx context.Context, report SlotRuntimeReport) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.slotReport = cloneSlotRuntimeReport(report)
	c.mu.Unlock()
	return nil
}

// CompleteTask records a fenced global task completion result.
func (c *StaticController) CompleteTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.CompletedTasks = append(c.CompletedTasks, result)
	c.mu.Unlock()
	return nil
}

// FailTask records a fenced global task failure result.
func (c *StaticController) FailTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.FailedTasks = append(c.FailedTasks, result)
	c.mu.Unlock()
	return nil
}

// ReportTaskProgress records one participant's fenced progress report.
func (c *StaticController) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.ProgressReports = append(c.ProgressReports, progress)
	c.mu.Unlock()
	return nil
}

// RequestSlotLeaderTransfer records a Controller-backed Slot leader transfer intent.
func (c *StaticController) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferResult{}, err
	}
	task := leaderTransferTaskFromRequest(req)
	c.mu.Lock()
	c.LeaderTransfers = append(c.LeaderTransfers, req)
	c.mu.Unlock()
	return SlotLeaderTransferResult{Created: true, Task: &task}, nil
}

// Watch returns snapshot update events.
func (c *StaticController) Watch() <-chan SnapshotEvent { return c.watch }

// Publish replaces the current snapshot and emits a non-blocking update event.
func (c *StaticController) Publish(snapshot Snapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	clone := snapshot.Clone()
	c.mu.Lock()
	c.snapshot = clone
	c.mu.Unlock()
	select {
	case c.watch <- SnapshotEvent{Snapshot: clone.Clone()}:
	default:
	}
	return nil
}

// LastNodeReport returns the last recorded node report.
func (c *StaticController) LastNodeReport() NodeReport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nodeReport
}

// LastSlotReport returns the last recorded Slot runtime report.
func (c *StaticController) LastSlotReport() SlotRuntimeReport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSlotRuntimeReport(c.slotReport)
}

func cloneSlotRuntimeReport(in SlotRuntimeReport) SlotRuntimeReport {
	out := in
	out.Slots = append([]SlotRuntimeView(nil), in.Slots...)
	for i := range out.Slots {
		out.Slots[i].Peers = append([]uint64(nil), in.Slots[i].Peers...)
	}
	return out
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
