package cluster

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

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
		leaderID := c.controller.LeaderID()
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			NotLeader:  true,
			LeaderID:   leaderID,
			LeaderAddr: c.controllerLeaderAddr(leaderID),
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
	case controllerRPCJoinCluster:
		return h.handleJoinCluster(ctx, req, marshalRedirect, loadHashSlotTable)
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
		if !c.isNodeAuthorizedForObservation(ctx, req.Report.NodeID) {
			return nil, ErrInvalidConfig
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
		if !c.isNodeAuthorizedForObservation(ctx, req.RuntimeReport.NodeID) {
			return nil, ErrInvalidConfig
		}
		if err := c.activateJoinedNodeOnFullSync(ctx, *req.RuntimeReport); err != nil {
			if errors.Is(err, controllerraft.ErrNotLeader) {
				return marshalRedirect()
			}
			return nil, err
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

func (h *controllerHandler) handleJoinCluster(
	ctx context.Context,
	req controllerRPCRequest,
	marshalRedirect func() ([]byte, error),
	loadHashSlotTable func() (*HashSlotTable, error),
) ([]byte, error) {
	if req.Join == nil {
		return nil, ErrInvalidConfig
	}
	c := h.cluster
	if leaderID := c.controller.LeaderID(); leaderID != uint64(c.cfg.NodeID) {
		if c.controllerClient != nil {
			resp, err := c.controllerClient.JoinCluster(ctx, *req.Join)
			if err == nil {
				return encodeControllerResponse(req.Kind, controllerRPCResponse{Join: &resp})
			}
			var joinErr *joinClusterError
			if errors.As(err, &joinErr) {
				return encodeControllerResponse(req.Kind, controllerRPCResponse{
					Join: &joinClusterResponse{
						JoinErrorCode:    joinErr.Code,
						JoinErrorMessage: sanitizeJoinErrorMessage(joinErr.Message),
					},
				})
			}
		}
		return marshalRedirect()
	}
	if c.controllerHost == nil {
		return nil, ErrNotStarted
	}

	if resp, ok := c.validateJoinClusterRequest(ctx, *req.Join); !ok {
		return encodeControllerResponse(req.Kind, controllerRPCResponse{Join: resp})
	}

	joinedAt := time.Now()
	proposeCtx, cancel := c.withControllerTimeout(ctx)
	err := c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoin,
		NodeJoin: &slotcontroller.NodeJoinRequest{
			NodeID:         req.Join.NodeID,
			Name:           req.Join.Name,
			Addr:           req.Join.Addr,
			CapacityWeight: req.Join.CapacityWeight,
			JoinedAt:       joinedAt,
		},
	})
	cancel()
	if err != nil {
		switch {
		case errors.Is(err, controllerraft.ErrNotLeader):
			return marshalRedirect()
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			return encodeControllerResponse(req.Kind, controllerRPCResponse{Join: joinRejection(joinErrorCommitTimeout, "join commit timed out")})
		case errors.Is(err, controllermeta.ErrInvalidArgument):
			if resp, ok := c.validateJoinClusterRequest(ctx, *req.Join); !ok {
				return encodeControllerResponse(req.Kind, controllerRPCResponse{Join: resp})
			}
			return encodeControllerResponse(req.Kind, controllerRPCResponse{Join: joinRejection(joinErrorInvalidRequest, "invalid join request")})
		default:
			return encodeControllerResponse(req.Kind, controllerRPCResponse{Join: joinRejection(joinErrorTemporary, "join temporarily unavailable")})
		}
	}

	nodes, err := c.controllerMeta.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	table, err := loadHashSlotTable()
	if err != nil {
		return nil, err
	}
	return encodeControllerResponse(req.Kind, controllerRPCResponse{
		LeaderID:   uint64(c.cfg.NodeID),
		LeaderAddr: c.controllerLeaderAddr(uint64(c.cfg.NodeID)),
		Join: &joinClusterResponse{
			Nodes:                nodes,
			HashSlotTableVersion: table.Version(),
			HashSlotTable:        table.Encode(),
		},
	})
}

func (c *Cluster) validateJoinClusterRequest(ctx context.Context, req joinClusterRequest) (*joinClusterResponse, bool) {
	if req.NodeID == 0 || strings.TrimSpace(req.Addr) == "" || req.CapacityWeight < 0 {
		return joinRejection(joinErrorInvalidRequest, "invalid join request"), false
	}
	if req.Version != supportedJoinProtocolVersion {
		return joinRejection(joinErrorUnsupportedVersion, "unsupported join protocol version"), false
	}
	if c == nil || c.cfg.JoinToken == "" ||
		subtle.ConstantTimeCompare([]byte(req.Token), []byte(c.cfg.JoinToken)) != 1 {
		return joinRejection(joinErrorInvalidToken, "invalid join token"), false
	}
	existing, err := c.controllerMeta.GetNode(ctx, req.NodeID)
	switch {
	case err == nil:
		if existing.Addr != req.Addr {
			return joinRejection(joinErrorNodeIDConflict, "node id already uses a different address"), false
		}
	case errors.Is(err, controllermeta.ErrNotFound):
	default:
		return joinRejection(joinErrorTemporary, "join temporarily unavailable"), false
	}
	nodes, err := c.controllerMeta.ListNodes(ctx)
	if err != nil {
		return joinRejection(joinErrorTemporary, "join temporarily unavailable"), false
	}
	for _, node := range nodes {
		if node.NodeID != req.NodeID && node.Addr == req.Addr {
			return joinRejection(joinErrorAddrConflict, "address already belongs to another node"), false
		}
	}
	return nil, true
}

func joinRejection(code joinErrorCode, message string) *joinClusterResponse {
	return &joinClusterResponse{
		JoinErrorCode:    code,
		JoinErrorMessage: sanitizeJoinErrorMessage(message),
	}
}

func sanitizeJoinErrorMessage(message string) string {
	return strings.TrimSpace(message)
}

func (c *Cluster) controllerLeaderAddr(leaderID uint64) string {
	if c == nil || c.discovery == nil || leaderID == 0 {
		return ""
	}
	addr, err := c.discovery.Resolve(leaderID)
	if err != nil {
		return ""
	}
	return addr
}

func (c *Cluster) isNodeAuthorizedForObservation(ctx context.Context, nodeID uint64) bool {
	if c == nil || nodeID == 0 {
		return false
	}
	if !c.cfg.JoinModeEnabled() {
		return true
	}
	for _, node := range c.cfg.Nodes {
		if uint64(node.NodeID) == nodeID {
			return true
		}
	}
	if c.controllerHost != nil {
		if snapshot, ok := c.controllerHost.metadataSnapshot(); ok {
			if node, exists := snapshot.NodesByID[nodeID]; exists {
				return node.JoinState != controllermeta.NodeJoinStateRejected
			}
		}
	}
	if c.controllerMeta == nil {
		return false
	}
	node, err := c.controllerMeta.GetNode(ctx, nodeID)
	return err == nil && node.JoinState != controllermeta.NodeJoinStateRejected
}

func (c *Cluster) activateJoinedNodeOnFullSync(ctx context.Context, report runtimeObservationReport) error {
	if c == nil || c.controller == nil || c.controllerMeta == nil || !report.FullSync || report.NodeID == 0 {
		return nil
	}
	node, err := c.controllerMeta.GetNode(ctx, report.NodeID)
	if errors.Is(err, controllermeta.ErrNotFound) {
		return nil
	}
	if err != nil || node.JoinState != controllermeta.NodeJoinStateJoining {
		return err
	}
	proposeCtx, cancel := c.withControllerTimeout(ctx)
	defer cancel()
	return c.controller.Propose(proposeCtx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoinActivate,
		NodeJoinActivate: &slotcontroller.NodeJoinActivateRequest{
			NodeID:      report.NodeID,
			ActivatedAt: report.ObservedAt,
		},
	})
}
