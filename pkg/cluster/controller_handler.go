package cluster

import (
	"context"
	"errors"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
)

type controllerHandler struct {
	cluster *Cluster
}

func (h *controllerHandler) Handle(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeControllerRequest(body)
	if err != nil {
		return nil, err
	}
	if h == nil || h.cluster == nil || h.cluster.controller == nil || h.cluster.controllerMeta == nil {
		return nil, ErrNotStarted
	}

	c := h.cluster
	marshalRedirect := func() ([]byte, error) {
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			NotLeader: true,
			LeaderID:  c.controller.LeaderID(),
		})
	}
	loadHashSlotTable := func() (*HashSlotTable, error) {
		if c.controllerHost != nil {
			if table, ok := c.controllerHost.hashSlotTableSnapshot(); ok {
				return table, nil
			}
		}
		table, err := c.ensureControllerHashSlotTable(ctx, c.controllerMeta)
		if err != nil {
			return nil, err
		}
		if c.controllerHost != nil {
			c.controllerHost.storeHashSlotTableSnapshot(table)
		}
		return table, nil
	}

	switch req.Kind {
	case controllerRPCHeartbeat:
		if req.Report == nil {
			return nil, ErrInvalidConfig
		}
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if c.controllerHost == nil {
			return nil, ErrNotStarted
		}
		c.controllerHost.applyObservation(*req.Report)
		table, err := loadHashSlotTable()
		if err != nil {
			return nil, err
		}
		resp := controllerRPCResponse{
			HashSlotTableVersion: table.Version(),
		}
		if req.Report.HashSlotTableVersion != table.Version() {
			resp.HashSlotTable = table.Encode()
		}
		return encodeControllerResponse(req.Kind, resp)
	case controllerRPCRuntimeReport:
		if req.RuntimeReport == nil {
			return nil, ErrInvalidConfig
		}
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if c.controllerHost == nil {
			return nil, ErrNotStarted
		}
		c.controllerHost.applyRuntimeReport(*req.RuntimeReport)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	case controllerRPCTaskResult:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if req.Advance == nil {
			return nil, ErrInvalidConfig
		}
		advance := &slotcontroller.TaskAdvance{
			SlotID:  req.Advance.SlotID,
			Attempt: req.Advance.Attempt,
			Now:     req.Advance.Now,
		}
		if req.Advance.Err != "" {
			advance.Err = errors.New(req.Advance.Err)
		}
		proposeCtx, cancel := c.withControllerTimeout(ctx)
		defer cancel()
		if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
			Kind:    slotcontroller.CommandKindTaskResult,
			Advance: advance,
		}); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return marshalRedirect()
			}
			return nil, err
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	case controllerRPCListAssignments:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		var assignments []controllermeta.SlotAssignment
		if c.controllerHost != nil {
			if snapshot, ok := c.controllerHost.metadataSnapshot(); ok {
				assignments = snapshot.Assignments
			}
		}
		if assignments == nil {
			assignments, err = c.controllerMeta.ListAssignments(ctx)
			if err != nil {
				return nil, err
			}
		}
		table, err := loadHashSlotTable()
		if err != nil {
			return nil, err
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Assignments:          assignments,
			HashSlotTableVersion: table.Version(),
			HashSlotTable:        table.Encode(),
		})
	case controllerRPCListNodes:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		var nodes []controllermeta.ClusterNode
		if c.controllerHost != nil {
			if snapshot, ok := c.controllerHost.metadataSnapshot(); ok {
				nodes = snapshot.Nodes
			}
		}
		if nodes == nil {
			nodes, err = c.controllerMeta.ListNodes(ctx)
			if err != nil {
				return nil, err
			}
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{Nodes: nodes})
	case controllerRPCListRuntimeViews:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if c.controllerHost == nil {
			return nil, ErrNotStarted
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			RuntimeViews: c.controllerHost.snapshotObservations().RuntimeViews,
		})
	case controllerRPCListTasks:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		var tasks []controllermeta.ReconcileTask
		if c.controllerHost != nil {
			if snapshot, ok := c.controllerHost.metadataSnapshot(); ok {
				tasks = snapshot.Tasks
			}
		}
		if tasks == nil {
			tasks, err = c.controllerMeta.ListTasks(ctx)
			if err != nil {
				return nil, err
			}
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{Tasks: tasks})
	case controllerRPCFetchObservationDelta:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if req.ObservationDelta == nil {
			return nil, ErrInvalidConfig
		}
		if c.controllerHost == nil {
			return nil, ErrNotStarted
		}
		delta := c.controllerHost.buildObservationDelta(*req.ObservationDelta)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{ObservationDelta: &delta})
	case controllerRPCOperator:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if req.Op == nil {
			return nil, ErrInvalidConfig
		}
		proposeCtx, cancel := c.withControllerTimeout(ctx)
		defer cancel()
		if err := c.controller.Propose(proposeCtx, slotcontroller.Command{
			Kind: slotcontroller.CommandKindOperatorRequest,
			Op:   req.Op,
		}); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return marshalRedirect()
			}
			return nil, err
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	case controllerRPCGetTask:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if c.controllerHost != nil {
			if snapshot, ok := c.controllerHost.metadataSnapshot(); ok {
				task, exists := snapshot.TasksBySlot[req.SlotID]
				if !exists {
					return encodeControllerResponse(req.Kind, controllerRPCResponse{NotFound: true})
				}
				return encodeControllerResponse(req.Kind, controllerRPCResponse{Task: &task})
			}
		}
		task, err := c.controllerMeta.GetTask(ctx, req.SlotID)
		if errors.Is(err, controllermeta.ErrNotFound) {
			return encodeControllerResponse(req.Kind, controllerRPCResponse{NotFound: true})
		}
		if err != nil {
			return nil, err
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{Task: &task})
	case controllerRPCForceReconcile:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		if err := c.forceReconcileOnLeader(ctx, req.SlotID); err != nil {
			if errors.Is(err, ErrNotLeader) {
				return marshalRedirect()
			}
			return nil, err
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	case controllerRPCStartMigration, controllerRPCAdvanceMigration, controllerRPCFinalizeMigration, controllerRPCAbortMigration,
		controllerRPCAddSlot, controllerRPCRemoveSlot:
		if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
			return marshalRedirect()
		}
		command := slotcontroller.Command{}
		switch req.Kind {
		case controllerRPCStartMigration:
			if req.Migration == nil {
				return nil, ErrInvalidConfig
			}
			command.Kind = slotcontroller.CommandKindStartMigration
			command.Migration = req.Migration
		case controllerRPCAdvanceMigration:
			if req.Migration == nil {
				return nil, ErrInvalidConfig
			}
			command.Kind = slotcontroller.CommandKindAdvanceMigration
			command.Migration = req.Migration
		case controllerRPCFinalizeMigration:
			if req.Migration == nil {
				return nil, ErrInvalidConfig
			}
			command.Kind = slotcontroller.CommandKindFinalizeMigration
			command.Migration = req.Migration
		case controllerRPCAbortMigration:
			if req.Migration == nil {
				return nil, ErrInvalidConfig
			}
			command.Kind = slotcontroller.CommandKindAbortMigration
			command.Migration = req.Migration
		case controllerRPCAddSlot:
			if req.AddSlot == nil {
				return nil, ErrInvalidConfig
			}
			command.Kind = slotcontroller.CommandKindAddSlot
			command.AddSlot = req.AddSlot
		case controllerRPCRemoveSlot:
			if req.RemoveSlot == nil {
				return nil, ErrInvalidConfig
			}
			command.Kind = slotcontroller.CommandKindRemoveSlot
			command.RemoveSlot = req.RemoveSlot
		}
		proposeCtx, cancel := c.withControllerTimeout(ctx)
		defer cancel()
		if err := c.controller.Propose(proposeCtx, command); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return marshalRedirect()
			}
			return nil, err
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	default:
		return nil, ErrInvalidConfig
	}
}
