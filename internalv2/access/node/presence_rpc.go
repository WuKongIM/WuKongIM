package node

import (
	"context"
	"errors"
	"fmt"

	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

const (
	rpcStatusOK            = "ok"
	rpcStatusNotLeader     = "not_leader"
	rpcStatusStaleRoute    = "stale_route"
	rpcStatusRouteNotReady = "route_not_ready"
	rpcStatusRejected      = "rejected"

	presenceOpRegisterRoute   = "register_route"
	presenceOpCommitRoute     = "commit_route"
	presenceOpAbortRoute      = "abort_route"
	presenceOpUnregisterRoute = "unregister_route"
	presenceOpEndpointsByUID  = "endpoints_by_uid"
	presenceOpRehydrateRoutes = "rehydrate_routes"
)

// PresenceAuthorityRPCServiceID is the clusterv2 RPC service for UID route authority calls.
const PresenceAuthorityRPCServiceID uint8 = clusternet.RPCPresenceAuthority

// PresenceAuthority is the authority-side route API exposed over node RPC.
type PresenceAuthority interface {
	RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error)
	CommitRoute(context.Context, presence.RouteTarget, string) error
	AbortRoute(context.Context, presence.RouteTarget, string) error
	UnregisterRoute(context.Context, presence.RouteTarget, presence.RouteIdentity, uint64) error
	EndpointsByUID(context.Context, presence.RouteTarget, string) ([]presence.Route, error)
	RehydrateRoutes(context.Context, presence.RouteTarget, []presence.Route) ([]presence.RehydrateResult, error)
}

// Options configures the internalv2 node RPC adapter.
type Options struct {
	// Authority handles UID route authority requests after payload decoding.
	Authority PresenceAuthority
}

// Adapter decodes node RPC payloads and forwards them to local authority ports.
type Adapter struct {
	// authority owns business decisions; Adapter only performs RPC adaptation.
	authority PresenceAuthority
}

// New creates a node RPC adapter.
func New(opts Options) *Adapter {
	return &Adapter{authority: opts.Authority}
}

// HandlePresenceAuthorityRPC handles one encoded presence authority RPC payload.
func (a *Adapter) HandlePresenceAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodePresenceRPCRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.authority == nil {
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusRejected})
	}

	switch req.Op {
	case presenceOpRegisterRoute:
		result, err := a.authority.RegisterRoute(ctx, req.Target, req.Route)
		if err != nil {
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Register: result})
	case presenceOpCommitRoute:
		err := a.authority.CommitRoute(ctx, req.Target, req.PendingToken)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
	case presenceOpAbortRoute:
		err := a.authority.AbortRoute(ctx, req.Target, req.PendingToken)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
	case presenceOpUnregisterRoute:
		err := a.authority.UnregisterRoute(ctx, req.Target, req.Identity, req.OwnerSeq)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
	case presenceOpEndpointsByUID:
		routes, err := a.authority.EndpointsByUID(ctx, req.Target, req.UID)
		if err != nil {
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Endpoints: routes})
	case presenceOpRehydrateRoutes:
		results, err := a.authority.RehydrateRoutes(ctx, req.Target, req.Routes)
		if err != nil {
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: presenceRPCStatusForError(err)})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Rehydrate: results})
	default:
		return nil, fmt.Errorf("internalv2/access/node: unknown presence rpc op %q", req.Op)
	}
}

// PresenceRPCNode sends raw RPC payloads to another internalv2 node.
type PresenceRPCNode interface {
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// Client forwards presence authority calls to the target leader node.
type Client struct {
	// node is the raw cluster RPC transport used by this adapter.
	node PresenceRPCNode
}

// NewClient creates a presence authority RPC client.
func NewClient(node PresenceRPCNode) *Client {
	return &Client{node: node}
}

// RegisterRoute registers one owner route on the target authority node.
func (c *Client) RegisterRoute(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpRegisterRoute, Target: target, Route: route})
	if err != nil {
		return presence.RegisterResult{}, err
	}
	if err := presenceRPCErrorForStatus(resp.Status); err != nil {
		return presence.RegisterResult{}, err
	}
	return resp.Register, nil
}

// CommitRoute promotes one pending authority route on the target node.
func (c *Client) CommitRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpCommitRoute, Target: target, PendingToken: token})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// AbortRoute drops one pending authority route on the target node.
func (c *Client) AbortRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpAbortRoute, Target: target, PendingToken: token})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// UnregisterRoute tombstones one exact owner route on the target node.
func (c *Client) UnregisterRoute(ctx context.Context, target presence.RouteTarget, identity presence.RouteIdentity, ownerSeq uint64) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpUnregisterRoute, Target: target, Identity: identity, OwnerSeq: ownerSeq})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// EndpointsByUID reads active authority routes for one UID from the target node.
func (c *Client) EndpointsByUID(ctx context.Context, target presence.RouteTarget, uid string) ([]presence.Route, error) {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpEndpointsByUID, Target: target, UID: uid})
	if err != nil {
		return nil, err
	}
	if err := presenceRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	return resp.Endpoints, nil
}

// RehydrateRoutes replays owner routes on the target authority node.
func (c *Client) RehydrateRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) ([]presence.RehydrateResult, error) {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpRehydrateRoutes, Target: target, Routes: routes})
	if err != nil {
		return nil, err
	}
	if err := presenceRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	return resp.Rehydrate, nil
}

func (c *Client) call(ctx context.Context, target presence.RouteTarget, req presenceRPCRequest) (presenceRPCResponse, error) {
	if c == nil || c.node == nil {
		return presenceRPCResponse{}, fmt.Errorf("internalv2/access/node: presence rpc client not configured")
	}
	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, target.LeaderNodeID, PresenceAuthorityRPCServiceID, body)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	return decodePresenceRPCResponse(respBody)
}

func presenceRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, authoritypresence.ErrNotLeader):
		return rpcStatusNotLeader
	case errors.Is(err, authoritypresence.ErrStaleRoute):
		return rpcStatusStaleRoute
	case errors.Is(err, authoritypresence.ErrRouteNotReady):
		return rpcStatusRouteNotReady
	default:
		return rpcStatusRejected
	}
}

func presenceRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusNotLeader:
		return authoritypresence.ErrNotLeader
	case rpcStatusStaleRoute:
		return authoritypresence.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return authoritypresence.ErrRouteNotReady
	case rpcStatusRejected:
		return fmt.Errorf("internalv2/access/node: presence rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown presence rpc status %q", status)
	}
}
