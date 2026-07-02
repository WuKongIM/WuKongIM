package clusterv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

const (
	channelMigrationMetaRPCVersion = uint8(1)

	channelMigrationMetaOpGetRuntime = "get_runtime"
	channelMigrationMetaOpGetActive  = "get_active"
	channelMigrationMetaOpGetTask    = "get_task"
	channelMigrationMetaOpListActive = "list_active"
)

func (n *Node) readChannelMigrationRuntimeMeta(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	route, err := n.channelMigrationRoute(ctx, hashSlot, channelID)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if route.Leader == n.cfg.NodeID {
		return n.readChannelMigrationLocalRuntimeMeta(ctx, hashSlot, channelID, channelType)
	}
	resp, err := n.callChannelMigrationMetaRPC(ctx, route.Leader, channelMigrationMetaRPCRequest{
		Op:          channelMigrationMetaOpGetRuntime,
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if resp.RuntimeMeta == nil {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return *resp.RuntimeMeta, nil
}

func (n *Node) getActiveChannelMigrationTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error) {
	route, err := n.channelMigrationRoute(ctx, hashSlot, channelID)
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if route.Leader == n.cfg.NodeID {
		return n.getActiveChannelMigrationLocalTask(ctx, hashSlot, channelID, channelType)
	}
	resp, err := n.callChannelMigrationMetaRPC(ctx, route.Leader, channelMigrationMetaRPCRequest{
		Op:          channelMigrationMetaOpGetActive,
		HashSlot:    hashSlot,
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

func (n *Node) getChannelMigrationTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	route, err := n.channelMigrationRoute(ctx, hashSlot, channelID)
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if route.Leader == n.cfg.NodeID {
		return n.getChannelMigrationLocalTask(ctx, hashSlot, channelID, channelType, taskID)
	}
	resp, err := n.callChannelMigrationMetaRPC(ctx, route.Leader, channelMigrationMetaRPCRequest{
		Op:          channelMigrationMetaOpGetTask,
		HashSlot:    hashSlot,
		ChannelID:   channelID,
		ChannelType: channelType,
		TaskID:      taskID,
	})
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if resp.Task == nil {
		return metadb.ChannelMigrationTask{}, false, nil
	}
	return *resp.Task, true, nil
}

func (n *Node) listActiveChannelMigrationTasks(ctx context.Context, hashSlot uint16, limit int) ([]metadb.ChannelMigrationTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	route, err := n.channelMigrationRoute(ctx, hashSlot, "")
	if err != nil {
		return nil, err
	}
	if route.Leader == n.cfg.NodeID {
		return n.listActiveChannelMigrationLocalTasks(ctx, hashSlot, limit)
	}
	resp, err := n.callChannelMigrationMetaRPC(ctx, route.Leader, channelMigrationMetaRPCRequest{
		Op:       channelMigrationMetaOpListActive,
		HashSlot: hashSlot,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	return append([]metadb.ChannelMigrationTask(nil), resp.Tasks...), nil
}

func (n *Node) channelMigrationRoute(ctx context.Context, hashSlot uint16, channelID string) (Route, error) {
	if err := ctxErr(ctx); err != nil {
		return Route{}, err
	}
	if n == nil {
		return Route{}, ErrNotStarted
	}
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return Route{}, err
	}
	if channelID != "" {
		keyRoute, err := n.RouteKey(channelID)
		if err != nil {
			return Route{}, err
		}
		if keyRoute.HashSlot != hashSlot {
			return Route{}, fmt.Errorf("%w: channel %s belongs to hash slot %d, not %d", metadb.ErrInvalidArgument, channelID, keyRoute.HashSlot, hashSlot)
		}
	}
	return route, nil
}

func (n *Node) readChannelMigrationLocalRuntimeMeta(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if n == nil || n.defaultSlotMetaDB == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	route, err := n.channelMigrationRoute(ctx, hashSlot, channelID)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if route.Leader != n.cfg.NodeID {
		return metadb.ChannelRuntimeMeta{}, ErrNotLeader
	}
	if err := n.ensureLocalChannelMigrationSlotLeader(route); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(hashSlot).GetChannelRuntimeMeta(ctx, channelID, channelType)
}

func (n *Node) getActiveChannelMigrationLocalTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error) {
	if n == nil || n.defaultSlotMetaDB == nil {
		return metadb.ChannelMigrationTask{}, false, ErrNotStarted
	}
	route, err := n.channelMigrationRoute(ctx, hashSlot, channelID)
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if route.Leader != n.cfg.NodeID {
		return metadb.ChannelMigrationTask{}, false, ErrNotLeader
	}
	if err := n.ensureLocalChannelMigrationSlotLeader(route); err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(hashSlot).GetActiveChannelMigrationTask(ctx, channelID, channelType)
}

func (n *Node) getChannelMigrationLocalTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	if n == nil || n.defaultSlotMetaDB == nil {
		return metadb.ChannelMigrationTask{}, false, ErrNotStarted
	}
	route, err := n.channelMigrationRoute(ctx, hashSlot, channelID)
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if route.Leader != n.cfg.NodeID {
		return metadb.ChannelMigrationTask{}, false, ErrNotLeader
	}
	if err := n.ensureLocalChannelMigrationSlotLeader(route); err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	task, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, channelID, channelType, taskID)
	if errors.Is(err, metadb.ErrNotFound) {
		return metadb.ChannelMigrationTask{}, false, nil
	}
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	return task, true, nil
}

func (n *Node) listActiveChannelMigrationLocalTasks(ctx context.Context, hashSlot uint16, limit int) ([]metadb.ChannelMigrationTask, error) {
	if n == nil || n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	if limit <= 0 {
		return nil, nil
	}
	route, err := n.channelMigrationRoute(ctx, hashSlot, "")
	if err != nil {
		return nil, err
	}
	if route.Leader != n.cfg.NodeID {
		return nil, ErrNotLeader
	}
	if err := n.ensureLocalChannelMigrationSlotLeader(route); err != nil {
		return nil, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(hashSlot).ListActiveChannelMigrationTasks(ctx, limit)
}

func (n *Node) ensureLocalChannelMigrationSlotLeader(route Route) error {
	if n == nil || n.defaultSlotProposer == nil {
		return ErrNotStarted
	}
	if !n.defaultSlotProposer.IsLocalLeader(route.SlotID) {
		return ErrNotLeader
	}
	return nil
}

func (n *Node) callChannelMigrationMetaRPC(ctx context.Context, nodeID uint64, req channelMigrationMetaRPCRequest) (channelMigrationMetaRPCResponse, error) {
	body, err := encodeChannelMigrationMetaRPCRequest(req)
	if err != nil {
		return channelMigrationMetaRPCResponse{}, err
	}
	respBody, err := n.CallRPC(ctx, nodeID, clusternet.RPCChannelMigrationMeta, body)
	if err != nil {
		return channelMigrationMetaRPCResponse{}, mapChannelMigrationRemoteError(err)
	}
	return decodeChannelMigrationMetaRPCResponse(respBody)
}

func mapChannelMigrationRemoteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, propose.ErrNotLeader):
		return ErrNotLeader
	case errors.Is(err, ErrNotLeader),
		errors.Is(err, ErrNotStarted),
		errors.Is(err, metadb.ErrStaleMeta),
		errors.Is(err, metadb.ErrInvalidArgument),
		errors.Is(err, metadb.ErrNotFound):
		return err
	}
	var remoteErr transportv2.RemoteError
	if !errors.As(err, &remoteErr) {
		return err
	}
	msg := remoteErr.Message
	switch {
	case strings.Contains(msg, propose.ErrNotLeader.Error()) || strings.Contains(msg, ErrNotLeader.Error()):
		return ErrNotLeader
	case strings.Contains(msg, ErrNotStarted.Error()):
		return ErrNotStarted
	case strings.Contains(msg, metadb.ErrStaleMeta.Error()):
		return metadb.ErrStaleMeta
	case strings.Contains(msg, metadb.ErrInvalidArgument.Error()):
		return metadb.ErrInvalidArgument
	case strings.Contains(msg, metadb.ErrNotFound.Error()):
		return metadb.ErrNotFound
	default:
		return err
	}
}

type channelMigrationMetaHandler struct {
	node *Node
}

func (h channelMigrationMetaHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeChannelMigrationMetaRPCRequest(payload)
	if err != nil {
		return nil, err
	}
	if h.node == nil || h.node.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	resp := channelMigrationMetaRPCResponse{}
	switch req.Op {
	case channelMigrationMetaOpGetRuntime:
		meta, err := h.node.readChannelMigrationLocalRuntimeMeta(ctx, req.HashSlot, req.ChannelID, req.ChannelType)
		if errors.Is(err, metadb.ErrNotFound) {
			return encodeChannelMigrationMetaRPCResponse(resp)
		}
		if err != nil {
			return nil, err
		}
		resp.RuntimeMeta = &meta
	case channelMigrationMetaOpGetActive:
		task, ok, err := h.node.getActiveChannelMigrationLocalTask(ctx, req.HashSlot, req.ChannelID, req.ChannelType)
		if err != nil {
			return nil, err
		}
		if ok {
			resp.Task = &task
		}
	case channelMigrationMetaOpGetTask:
		task, ok, err := h.node.getChannelMigrationLocalTask(ctx, req.HashSlot, req.ChannelID, req.ChannelType, req.TaskID)
		if err != nil {
			return nil, err
		}
		if ok {
			resp.Task = &task
		}
	case channelMigrationMetaOpListActive:
		tasks, err := h.node.listActiveChannelMigrationLocalTasks(ctx, req.HashSlot, req.Limit)
		if err != nil {
			return nil, err
		}
		resp.Tasks = tasks
	default:
		return nil, fmt.Errorf("%w: channel migration meta op %q", metadb.ErrInvalidArgument, req.Op)
	}
	return encodeChannelMigrationMetaRPCResponse(resp)
}

type channelMigrationMetaRPCRequest struct {
	Version     uint8  `json:"version"`
	Op          string `json:"op"`
	HashSlot    uint16 `json:"hash_slot"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelType int64  `json:"channel_type,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type channelMigrationMetaRPCResponse struct {
	Version     uint8                         `json:"version"`
	RuntimeMeta *metadb.ChannelRuntimeMeta    `json:"runtime_meta,omitempty"`
	Task        *metadb.ChannelMigrationTask  `json:"task,omitempty"`
	Tasks       []metadb.ChannelMigrationTask `json:"tasks,omitempty"`
}

func encodeChannelMigrationMetaRPCRequest(req channelMigrationMetaRPCRequest) ([]byte, error) {
	req.Version = channelMigrationMetaRPCVersion
	return json.Marshal(req)
}

func decodeChannelMigrationMetaRPCRequest(payload []byte) (channelMigrationMetaRPCRequest, error) {
	var req channelMigrationMetaRPCRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return channelMigrationMetaRPCRequest{}, err
	}
	if req.Version != channelMigrationMetaRPCVersion {
		return channelMigrationMetaRPCRequest{}, fmt.Errorf("%w: channel migration meta rpc version", metadb.ErrInvalidArgument)
	}
	return req, nil
}

func encodeChannelMigrationMetaRPCResponse(resp channelMigrationMetaRPCResponse) ([]byte, error) {
	resp.Version = channelMigrationMetaRPCVersion
	return json.Marshal(resp)
}

func decodeChannelMigrationMetaRPCResponse(payload []byte) (channelMigrationMetaRPCResponse, error) {
	var resp channelMigrationMetaRPCResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return channelMigrationMetaRPCResponse{}, err
	}
	if resp.Version != channelMigrationMetaRPCVersion {
		return channelMigrationMetaRPCResponse{}, fmt.Errorf("%w: channel migration meta rpc version", metadb.ErrInvalidArgument)
	}
	return resp, nil
}
