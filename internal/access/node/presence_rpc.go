package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	rpcStatusOK              = "ok"
	rpcStatusNotLeader       = "not_leader"
	rpcStatusNoLeader        = "no_leader"
	rpcStatusNoSlot          = "no_group"
	rpcStatusRejected        = "rejected"
	rpcStatusNoSafeCandidate = "no_safe_candidate"

	presenceOpRegister        = "register"
	presenceOpUnregister      = "unregister"
	presenceOpHeartbeat       = "heartbeat"
	presenceOpReplay          = "replay"
	presenceOpEndpoints       = "endpoints"
	presenceOpEndpointsByUIDs = "endpoints_batch"
	presenceOpApplyAction     = "apply_action"
)

type presenceRPCRequest struct {
	Op     string                 `json:"op"`
	SlotID uint64                 `json:"slot_id,omitempty"`
	UID    string                 `json:"uid,omitempty"`
	UIDs   []string               `json:"uids,omitempty"`
	Route  *presence.Route        `json:"route,omitempty"`
	Routes []presence.Route       `json:"routes,omitempty"`
	Action *presence.RouteAction  `json:"action,omitempty"`
	Lease  *presence.GatewayLease `json:"lease,omitempty"`
}

type presenceRPCResponse struct {
	Status      string                                 `json:"status"`
	LeaderID    uint64                                 `json:"leader_id,omitempty"`
	Register    *presence.RegisterAuthoritativeResult  `json:"register,omitempty"`
	Heartbeat   *presence.HeartbeatAuthoritativeResult `json:"heartbeat,omitempty"`
	Endpoints   []presence.Route                       `json:"endpoints,omitempty"`
	EndpointMap map[string][]presence.Route            `json:"endpoint_map,omitempty"`
}

func (r presenceRPCResponse) rpcStatus() string {
	return r.Status
}

func (r presenceRPCResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handlePresenceRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req presenceRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	switch req.Op {
	case presenceOpRegister:
		return a.handleRegister(ctx, req)
	case presenceOpUnregister:
		return a.handleUnregister(ctx, req)
	case presenceOpHeartbeat:
		return a.handleHeartbeat(ctx, req)
	case presenceOpReplay:
		return a.handleReplay(ctx, req)
	case presenceOpEndpoints:
		return a.handleEndpoints(ctx, req)
	case presenceOpEndpointsByUIDs:
		return a.handleEndpointsByUIDs(ctx, req)
	case presenceOpApplyAction:
		return a.handleApplyAction(ctx, req)
	default:
		return nil, fmt.Errorf("access/node: unknown presence op %q", req.Op)
	}
}

func (a *Adapter) handleRegister(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if body, handled, err := a.handleAuthoritativeRPC(multiraft.SlotID(req.SlotID)); handled || err != nil {
		return body, err
	}
	result, err := a.presence.RegisterAuthoritative(ctx, presence.RegisterAuthoritativeCommand{
		SlotID: req.SlotID,
		Route:  derefRoute(req.Route),
	})
	if err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{
		Status:   rpcStatusOK,
		Register: &result,
	})
}

func (a *Adapter) handleUnregister(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if body, handled, err := a.handleAuthoritativeRPC(multiraft.SlotID(req.SlotID)); handled || err != nil {
		return body, err
	}
	err := a.presence.UnregisterAuthoritative(ctx, presence.UnregisterAuthoritativeCommand{
		SlotID: req.SlotID,
		Route:  derefRoute(req.Route),
	})
	if err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{Status: rpcStatusOK})
}

func (a *Adapter) handleHeartbeat(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	lease := derefLease(req.Lease)
	if body, handled, err := a.handleAuthoritativeRPC(multiraft.SlotID(lease.SlotID)); handled || err != nil {
		return body, err
	}
	result, err := a.presence.HeartbeatAuthoritative(ctx, presence.HeartbeatAuthoritativeCommand{
		Lease: lease,
	})
	if err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{
		Status:    rpcStatusOK,
		Heartbeat: &result,
	})
}

func (a *Adapter) handleReplay(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	lease := derefLease(req.Lease)
	if body, handled, err := a.handleAuthoritativeRPC(multiraft.SlotID(lease.SlotID)); handled || err != nil {
		return body, err
	}
	err := a.presence.ReplayAuthoritative(ctx, presence.ReplayAuthoritativeCommand{
		Lease:  lease,
		Routes: append([]presence.Route(nil), req.Routes...),
	})
	if err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{Status: rpcStatusOK})
}

func (a *Adapter) handleEndpoints(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if body, handled, err := a.handleAuthoritativeRPC(multiraft.SlotID(req.SlotID)); handled || err != nil {
		return body, err
	}
	endpoints, err := a.presence.EndpointsByUID(ctx, req.UID)
	if err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{
		Status:    rpcStatusOK,
		Endpoints: endpoints,
	})
}

func (a *Adapter) handleEndpointsByUIDs(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if body, handled, err := a.handleAuthoritativeRPC(multiraft.SlotID(req.SlotID)); handled || err != nil {
		return body, err
	}
	endpoints, err := a.presence.EndpointsByUIDs(ctx, req.UIDs)
	if err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{
		Status:      rpcStatusOK,
		EndpointMap: endpoints,
	})
}

func (a *Adapter) handleApplyAction(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if err := a.presence.ApplyRouteAction(ctx, derefAction(req.Action)); err != nil {
		return nil, err
	}
	return encodePresenceResponse(presenceRPCResponse{Status: rpcStatusOK})
}

func (a *Adapter) handleAuthoritativeRPC(slotID multiraft.SlotID) ([]byte, bool, error) {
	status, leaderID, handled := a.authoritativeRPCStatus(slotID)
	if !handled {
		return nil, false, nil
	}
	body, encodeErr := encodePresenceResponse(presenceRPCResponse{
		Status:   status,
		LeaderID: leaderID,
	})
	return body, true, encodeErr
}

func (a *Adapter) authoritativeRPCStatus(slotID multiraft.SlotID) (string, uint64, bool) {
	if a.cluster == nil || slotID == 0 {
		return "", 0, false
	}
	leaderID, err := a.cluster.LeaderOf(slotID)
	switch {
	case errors.Is(err, raftcluster.ErrSlotNotFound):
		return rpcStatusNoSlot, 0, true
	case err != nil:
		return rpcStatusNoLeader, 0, true
	case !a.cluster.IsLocal(leaderID):
		return rpcStatusNotLeader, uint64(leaderID), true
	default:
		return "", 0, false
	}
}

func encodePresenceResponse(resp presenceRPCResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func derefRoute(route *presence.Route) presence.Route {
	if route == nil {
		return presence.Route{}
	}
	return *route
}

func derefAction(action *presence.RouteAction) presence.RouteAction {
	if action == nil {
		return presence.RouteAction{}
	}
	return *action
}

func derefLease(lease *presence.GatewayLease) presence.GatewayLease {
	if lease == nil {
		return presence.GatewayLease{}
	}
	return *lease
}
