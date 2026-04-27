package plane

import (
	"context"
	"errors"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

type Controller struct {
	store    *controllermeta.Store
	planner  *Planner
	now      func() time.Time
	isLeader func() bool
	propose  func(context.Context, Command) error
}

type ControllerConfig struct {
	Planner  PlannerConfig
	Now      func() time.Time
	IsLeader func() bool
	// Propose submits planner mutations through the replicated controller log.
	// When nil, Tick falls back to direct store writes for local tests and tools.
	Propose func(context.Context, Command) error
}

func NewController(store *controllermeta.Store, cfg ControllerConfig) *Controller {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	isLeader := cfg.IsLeader
	if isLeader == nil {
		isLeader = func() bool { return true }
	}
	return &Controller{
		store:    store,
		planner:  NewPlanner(cfg.Planner),
		now:      now,
		isLeader: isLeader,
		propose:  cfg.Propose,
	}
}

func (c *Controller) Tick(ctx context.Context) error {
	if c == nil || c.store == nil {
		return controllermeta.ErrClosed
	}
	if c != nil && c.isLeader != nil && !c.isLeader() {
		return nil
	}

	state, err := c.snapshot(ctx)
	if err != nil {
		return err
	}

	decision, err := c.planner.NextDecision(ctx, state)
	if err != nil {
		return err
	}
	if decision.SlotID == 0 {
		return nil
	}
	return c.persistDecision(ctx, state, decision)
}

func (c *Controller) persistDecision(ctx context.Context, state PlannerState, decision Decision) error {
	assignment := decision.Assignment
	var assignmentPtr *controllermeta.SlotAssignment
	if assignment.SlotID != 0 {
		assignmentPtr = &assignment
	}

	var taskPtr *controllermeta.ReconcileTask
	if decision.Task == nil {
		if c.propose != nil {
			if assignmentPtr == nil {
				return nil
			}
			return c.propose(ctx, Command{
				Kind:       CommandKindAssignmentTaskUpdate,
				Assignment: assignmentPtr,
			})
		}
		if assignmentPtr != nil {
			return c.store.UpsertAssignment(ctx, *assignmentPtr)
		}
		return nil
	}
	task := *decision.Task
	if task.Status == controllermeta.TaskStatusUnknown {
		task.Status = controllermeta.TaskStatusPending
	}
	if task.NextRunAt.IsZero() {
		task.NextRunAt = state.Now
	}
	taskPtr = &task

	if c.propose != nil {
		return c.propose(ctx, Command{
			Kind:       CommandKindAssignmentTaskUpdate,
			Assignment: assignmentPtr,
			Task:       taskPtr,
		})
	}
	if assignmentPtr != nil {
		return c.store.UpsertAssignmentTask(ctx, *assignmentPtr, task)
	}
	return c.store.UpsertTask(ctx, task)
}

func (c *Controller) snapshot(ctx context.Context) (PlannerState, error) {
	nodes, err := c.store.ListNodes(ctx)
	if err != nil {
		return PlannerState{}, err
	}
	assignments, err := c.store.ListAssignments(ctx)
	if err != nil {
		return PlannerState{}, err
	}
	views, err := c.store.ListRuntimeViews(ctx)
	if err != nil {
		return PlannerState{}, err
	}
	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return PlannerState{}, err
	}
	table, err := c.store.LoadHashSlotTable(ctx)
	if err != nil && !errors.Is(err, controllermeta.ErrNotFound) {
		return PlannerState{}, err
	}

	state := PlannerState{
		Now:            c.now(),
		Nodes:          make(map[uint64]controllermeta.ClusterNode, len(nodes)),
		Assignments:    make(map[uint32]controllermeta.SlotAssignment, len(assignments)),
		Runtime:        make(map[uint32]controllermeta.SlotRuntimeView, len(views)),
		Tasks:          make(map[uint32]controllermeta.ReconcileTask, len(tasks)),
		PhysicalSlots:  make(map[uint32]struct{}),
		MigratingSlots: make(map[uint32]struct{}),
	}
	for _, node := range nodes {
		state.Nodes[node.NodeID] = node
	}
	for _, assignment := range assignments {
		state.Assignments[assignment.SlotID] = assignment
	}
	for _, view := range views {
		state.Runtime[view.SlotID] = view
	}
	for _, task := range tasks {
		state.Tasks[task.SlotID] = task
	}
	if table != nil {
		for _, slotID := range table.AssignedSlotIDs() {
			state.PhysicalSlots[uint32(slotID)] = struct{}{}
		}
		for _, migration := range table.ActiveMigrations() {
			state.MigratingSlots[uint32(migration.Source)] = struct{}{}
			state.MigratingSlots[uint32(migration.Target)] = struct{}{}
		}
	}
	return state, nil
}
