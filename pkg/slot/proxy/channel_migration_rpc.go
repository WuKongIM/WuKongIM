package proxy

import (
	"context"
	"errors"
	"fmt"
	"sort"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const channelMigrationRPCServiceID uint8 = 47

// channelMigrationListActiveForNodeMaxLimit bounds active task scan pages from manager or RPC callers.
const channelMigrationListActiveForNodeMaxLimit = 1024

const (
	channelMigrationRPCGetActive         = "get_active"
	channelMigrationRPCPropose           = "propose"
	channelMigrationRPCListActiveForNode = "list_active_for_node"
)

var channelMigrationProposalRPCStatuses = map[string]struct{}{
	rpcStatusOK:        {},
	rpcStatusStaleMeta: {},
}

type channelMigrationRPCRequest struct {
	Op          string `json:"op"`
	SlotID      uint64 `json:"slot_id"`
	HashSlot    uint16 `json:"hash_slot,omitempty"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelType int64  `json:"channel_type,omitempty"`
	Command     []byte `json:"command,omitempty"`
	NodeID      uint64 `json:"node_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type channelMigrationRPCResponse struct {
	Status   string                        `json:"status"`
	LeaderID uint64                        `json:"leader_id,omitempty"`
	Task     *metadb.ChannelMigrationTask  `json:"task,omitempty"`
	Tasks    []metadb.ChannelMigrationTask `json:"tasks,omitempty"`
	HasMore  bool                          `json:"has_more,omitempty"`
}

func (r channelMigrationRPCResponse) rpcStatus() string {
	return r.Status
}

func (r channelMigrationRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

// CreateChannelMigrationTask creates one authoritative migration task through the channel's slot Raft group.
func (s *Store) CreateChannelMigrationTask(ctx context.Context, task metadb.ChannelMigrationTask) error {
	cmd := metafsm.EncodeCreateChannelMigrationTaskCommand(task)
	return s.proposeChannelMigrationCommand(ctx, task.ChannelID, cmd)
}

// CreateChannelMigrationTaskWithRuntimeGuard creates one authoritative migration task
// while fencing creation to the caller's observed channel runtime metadata.
func (s *Store) CreateChannelMigrationTaskWithRuntimeGuard(ctx context.Context, req metadb.ChannelMigrationTaskCreate) error {
	cmd := metafsm.EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(req)
	return s.proposeChannelMigrationCommand(ctx, req.Task.ChannelID, cmd)
}

// GetActiveChannelMigrationTask reads the active task from the authoritative slot leader.
func (s *Store) GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error) {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetActiveChannelMigrationTask(ctx, channelID, channelType)
	}

	resp, err := s.callChannelMigrationRPC(ctx, slotID, channelMigrationRPCRequest{
		Op:          channelMigrationRPCGetActive,
		SlotID:      uint64(slotID),
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if resp.Task == nil {
		return metadb.ChannelMigrationTask{}, false, nil
	}
	return *resp.Task, true, nil
}

// ListRunnableChannelMigrationTasksForLocalLeaderSlots returns active tasks this node is allowed to drive.
func (s *Store) ListRunnableChannelMigrationTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]metadb.ChannelMigrationTask, error) {
	if s.cluster == nil {
		return nil, fmt.Errorf("metastore: cluster not configured")
	}
	if limit <= 0 {
		return nil, nil
	}

	localNodeID := uint64(s.cluster.NodeID())
	out := make([]metadb.ChannelMigrationTask, 0, limit)
	for _, slotID := range s.cluster.SlotIDs() {
		leaderID, err := s.cluster.LeaderOf(slotID)
		if err != nil {
			if errors.Is(err, raftcluster.ErrSlotNotFound) || errors.Is(err, raftcluster.ErrNoLeader) {
				continue
			}
			return nil, err
		}
		if !s.cluster.IsLocal(leaderID) {
			continue
		}
		for _, hashSlot := range s.cluster.HashSlotsOf(slotID) {
			tasks, err := s.db.ForHashSlot(hashSlot).ListChannelMigrationTasks(ctx)
			if err != nil {
				return nil, err
			}
			for _, task := range tasks {
				if !isRunnableChannelMigrationTaskForLocalLeader(task, nowMS, localNodeID) {
					continue
				}
				out = append(out, task)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

// ListActiveChannelMigrationTasksForNode returns active migration tasks whose source or target is the node.
func (s *Store) ListActiveChannelMigrationTasksForNode(ctx context.Context, nodeID uint64, limit int) ([]metadb.ChannelMigrationTask, bool, error) {
	if s.cluster == nil {
		return nil, false, fmt.Errorf("metastore: cluster not configured")
	}
	limit = normalizeChannelMigrationListActiveForNodeLimit(limit)
	if limit <= 0 {
		return nil, false, nil
	}

	out := make([]metadb.ChannelMigrationTask, 0, limit)
	slotIDs := append([]multiraft.SlotID(nil), s.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	for _, slotID := range slotIDs {
		remaining := limit - len(out)
		probeOnly := false
		if remaining <= 0 {
			remaining = 1
			probeOnly = true
		}

		var tasks []metadb.ChannelMigrationTask
		var hasMore bool
		leaderID, err := s.cluster.LeaderOf(slotID)
		if err != nil {
			return nil, false, err
		}
		if s.cluster.IsLocal(leaderID) {
			var err error
			tasks, hasMore, err = s.listActiveChannelMigrationTasksForNodeLocalSlot(ctx, slotID, nodeID, remaining)
			if err != nil {
				return nil, false, err
			}
		} else {
			resp, err := s.callChannelMigrationRPC(ctx, slotID, channelMigrationRPCRequest{
				Op:     channelMigrationRPCListActiveForNode,
				SlotID: uint64(slotID),
				NodeID: nodeID,
				Limit:  remaining,
			})
			if err != nil {
				return nil, false, err
			}
			tasks = resp.Tasks
			hasMore = resp.HasMore
		}

		if probeOnly {
			if len(tasks) > 0 || hasMore {
				return out, true, nil
			}
			continue
		}
		if len(tasks) > remaining {
			out = append(out, tasks[:remaining]...)
			return out, true, nil
		}
		out = append(out, tasks...)
		if hasMore {
			return out, true, nil
		}
	}
	return out, false, nil
}

// ClaimChannelMigrationTask claims a task only when this node currently leads the owning slot.
func (s *Store) ClaimChannelMigrationTask(ctx context.Context, req metadb.ChannelMigrationTaskClaim) error {
	slotID := s.cluster.SlotForKey(req.Guard.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, req.Guard.ChannelID)
	leaderID, err := s.cluster.LeaderOf(slotID)
	if err != nil {
		if errors.Is(err, raftcluster.ErrNoLeader) {
			return raftcluster.ErrNotLeader
		}
		return err
	}
	if !s.cluster.IsLocal(leaderID) {
		return raftcluster.ErrNotLeader
	}
	req.OwnerNodeID = uint64(leaderID)
	cmd := metafsm.EncodeClaimChannelMigrationTaskCommand(req)
	return proposeLocalWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// AdvanceChannelMigrationTask advances task-only durable phase and progress state.
func (s *Store) AdvanceChannelMigrationTask(ctx context.Context, req metadb.ChannelMigrationTaskAdvance) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeAdvanceChannelMigrationTaskCommand(req))
}

// SetChannelWriteFence sets or renews a migration write fence atomically with task state.
func (s *Store) SetChannelWriteFence(ctx context.Context, req metadb.ChannelMigrationFenceRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeSetChannelWriteFenceCommand(req))
}

// ResetChannelWriteFenceToPreCutover clears an expired cutover fence through the authoritative slot.
func (s *Store) ResetChannelWriteFenceToPreCutover(ctx context.Context, req metadb.ChannelMigrationResetFenceRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeResetChannelWriteFenceToPreCutoverCommand(req))
}

// CommitChannelLeaderTransfer commits a fenced leader metadata change.
func (s *Store) CommitChannelLeaderTransfer(ctx context.Context, req metadb.ChannelMigrationLeaderTransferRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeCommitChannelLeaderTransferCommand(req))
}

// AddChannelLearner adds a learner replica atomically with task advancement.
func (s *Store) AddChannelLearner(ctx context.Context, req metadb.ChannelMigrationAddLearnerRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeAddChannelLearnerCommand(req))
}

// PromoteLearnerAndRemoveReplica promotes a learner and removes the source replica.
func (s *Store) PromoteLearnerAndRemoveReplica(ctx context.Context, req metadb.ChannelMigrationPromoteLearnerRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodePromoteLearnerAndRemoveReplicaCommand(req))
}

// ClearChannelWriteFence clears the matching migration write fence.
func (s *Store) ClearChannelWriteFence(ctx context.Context, req metadb.ChannelMigrationClearFenceRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeClearChannelWriteFenceCommand(req))
}

// AbortChannelMigration marks a migration aborted and rolls back safe side effects.
func (s *Store) AbortChannelMigration(ctx context.Context, req metadb.ChannelMigrationAbortRequest) error {
	return s.proposeChannelMigrationCommand(ctx, req.Guard.ChannelID, metafsm.EncodeAbortChannelMigrationCommand(req))
}

// GarbageCollectTerminalChannelMigrationTasks prunes terminal task records on locally led slot shards.
func (s *Store) GarbageCollectTerminalChannelMigrationTasks(ctx context.Context, beforeMS int64, limit int) (int, error) {
	if s.cluster == nil {
		return 0, fmt.Errorf("metastore: cluster not configured")
	}
	if limit <= 0 {
		return 0, nil
	}

	deleted := 0
	for _, slotID := range s.cluster.SlotIDs() {
		leaderID, err := s.cluster.LeaderOf(slotID)
		if err != nil {
			if errors.Is(err, raftcluster.ErrSlotNotFound) || errors.Is(err, raftcluster.ErrNoLeader) {
				continue
			}
			return deleted, err
		}
		if !s.cluster.IsLocal(leaderID) {
			continue
		}
		for _, hashSlot := range s.cluster.HashSlotsOf(slotID) {
			remaining := limit - deleted
			if remaining <= 0 {
				return deleted, nil
			}
			plan, err := s.db.ForHashSlot(hashSlot).PlanTerminalChannelMigrationTaskGC(ctx, beforeMS, remaining)
			if err != nil {
				return deleted, err
			}
			if plan.EntryCount == 0 {
				continue
			}
			cmd := metafsm.EncodeGarbageCollectTerminalChannelMigrationTasksCommand(metadb.ChannelMigrationTaskGCRequest{
				BeforeMS: beforeMS,
				Limit:    plan.EntryCount,
			})
			result, err := proposeWithHashSlotResult(ctx, s.cluster, slotID, hashSlot, cmd)
			if err != nil {
				return deleted, err
			}
			n, ok, err := metafsm.DecodeGarbageCollectTerminalChannelMigrationTasksResult(result)
			if err != nil {
				return deleted, err
			}
			if !ok {
				n = plan.TaskCount
			}
			deleted += n
		}
	}
	return deleted, nil
}

func (s *Store) proposeChannelMigrationCommand(ctx context.Context, channelID string, cmd []byte) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
	}
	resp, err := s.callChannelMigrationProposalRPC(ctx, slotID, channelMigrationRPCRequest{
		Op:        channelMigrationRPCPropose,
		SlotID:    uint64(slotID),
		HashSlot:  hashSlot,
		ChannelID: channelID,
		Command:   cmd,
	})
	if err != nil {
		return err
	}
	switch resp.Status {
	case rpcStatusOK:
		return nil
	case rpcStatusStaleMeta:
		return metadb.ErrStaleMeta
	default:
		return fmt.Errorf("metastore: unexpected channel migration proposal status %q", resp.Status)
	}
}

func (s *Store) callChannelMigrationRPC(ctx context.Context, slotID multiraft.SlotID, req channelMigrationRPCRequest) (channelMigrationRPCResponse, error) {
	payload, err := encodeChannelMigrationRPCRequestBinary(req)
	if err != nil {
		return channelMigrationRPCResponse{}, err
	}
	return callAuthoritativeRPC(ctx, s, slotID, channelMigrationRPCServiceID, payload, decodeChannelMigrationRPCResponse)
}

func (s *Store) callChannelMigrationProposalRPC(ctx context.Context, slotID multiraft.SlotID, req channelMigrationRPCRequest) (channelMigrationRPCResponse, error) {
	payload, err := encodeChannelMigrationRPCRequestBinary(req)
	if err != nil {
		return channelMigrationRPCResponse{}, err
	}
	return callAuthoritativeRPCWithStatuses(ctx, s, slotID, channelMigrationRPCServiceID, payload, decodeChannelMigrationRPCResponse, channelMigrationProposalRPCStatuses)
}

func (s *Store) handleChannelMigrationRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeChannelMigrationRPCRequest(body)
	if err != nil {
		return nil, err
	}

	slotID := multiraft.SlotID(req.SlotID)
	if statusBody, handled, err := s.handleAuthoritativeRPC(slotID, func(status string, leaderID uint64) ([]byte, error) {
		return encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{
			Status:   status,
			LeaderID: leaderID,
		})
	}); handled || err != nil {
		return statusBody, err
	}

	switch req.Op {
	case channelMigrationRPCGetActive:
		hashSlot, redirected, err := s.resolveChannelMigrationRPCRoute(req.ChannelID, slotID)
		if err != nil || redirected != nil {
			return redirected, err
		}
		task, ok, err := s.db.ForHashSlot(hashSlot).GetActiveChannelMigrationTask(ctx, req.ChannelID, req.ChannelType)
		if err != nil {
			return nil, err
		}
		if !ok {
			return encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{Status: rpcStatusNotFound})
		}
		return encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{
			Status: rpcStatusOK,
			Task:   &task,
		})
	case channelMigrationRPCListActiveForNode:
		tasks, hasMore, err := s.listActiveChannelMigrationTasksForNodeLocalSlot(ctx, slotID, req.NodeID, req.Limit)
		if err != nil {
			return nil, err
		}
		return encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{
			Status:  rpcStatusOK,
			Tasks:   tasks,
			HasMore: hasMore,
		})
	case channelMigrationRPCPropose:
		hashSlot := req.HashSlot
		if req.ChannelID != "" {
			routedHashSlot, redirected, err := s.resolveChannelMigrationRPCRoute(req.ChannelID, slotID)
			if err != nil || redirected != nil {
				return redirected, err
			}
			hashSlot = routedHashSlot
		}
		if len(req.Command) == 0 {
			return nil, fmt.Errorf("metastore: empty channel migration proposal")
		}
		if err := proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, req.Command); err != nil {
			if errors.Is(err, metadb.ErrStaleMeta) {
				return encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{Status: rpcStatusStaleMeta})
			}
			return nil, err
		}
		return encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("metastore: unknown channel migration rpc op %q", req.Op)
	}
}

func (s *Store) listActiveChannelMigrationTasksForNodeLocalSlot(ctx context.Context, slotID multiraft.SlotID, nodeID uint64, limit int) ([]metadb.ChannelMigrationTask, bool, error) {
	limit = normalizeChannelMigrationListActiveForNodeLimit(limit)
	if limit <= 0 {
		return nil, false, nil
	}

	out := make([]metadb.ChannelMigrationTask, 0, limit)
	hashSlots := append([]uint16(nil), s.cluster.HashSlotsOf(slotID)...)
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	for _, hashSlot := range hashSlots {
		tasks, err := s.db.ForHashSlot(hashSlot).ListChannelMigrationTasks(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, task := range tasks {
			if !channelMigrationTaskActive(task) || (task.SourceNode != nodeID && task.TargetNode != nodeID) {
				continue
			}
			if len(out) >= limit {
				return out, true, nil
			}
			out = append(out, task)
		}
	}
	return out, false, nil
}

func normalizeChannelMigrationListActiveForNodeLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit > channelMigrationListActiveForNodeMaxLimit {
		return channelMigrationListActiveForNodeMaxLimit
	}
	return limit
}

func (s *Store) resolveChannelMigrationRPCRoute(channelID string, slotID multiraft.SlotID) (uint16, []byte, error) {
	actualSlotID := s.cluster.SlotForKey(channelID)
	if actualSlotID == slotID {
		return hashSlotForKey(s.cluster, channelID), nil, nil
	}
	leaderID, err := s.cluster.LeaderOf(actualSlotID)
	if err != nil {
		body, encodeErr := encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{Status: rpcStatusNoLeader})
		return 0, body, encodeErr
	}
	body, err := encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{
		Status:   rpcStatusNotLeader,
		LeaderID: uint64(leaderID),
	})
	return 0, body, err
}

func isRunnableChannelMigrationTaskForLocalLeader(task metadb.ChannelMigrationTask, nowMS int64, localNodeID uint64) bool {
	if !task.IsActive() {
		return false
	}
	if task.NextRunAtMS > 0 && task.NextRunAtMS > nowMS {
		return false
	}
	if task.OwnerNodeID == 0 || task.OwnerNodeID == localNodeID {
		return true
	}
	return task.OwnerLeaseUntilMS > 0 && task.OwnerLeaseUntilMS <= nowMS
}

func channelMigrationTaskActive(task metadb.ChannelMigrationTask) bool {
	return task.IsActive()
}
