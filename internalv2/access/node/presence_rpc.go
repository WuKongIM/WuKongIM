package node

import (
	"context"
	"errors"
	"fmt"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	rpcStatusOK            = "ok"
	rpcStatusNotLeader     = "not_leader"
	rpcStatusStaleRoute    = "stale_route"
	rpcStatusRouteNotReady = "route_not_ready"
	rpcStatusRejected      = "rejected"

	presenceOpRegisterRoute    = "register_route"
	presenceOpCommitRoute      = "commit_route"
	presenceOpAbortRoute       = "abort_route"
	presenceOpUnregisterRoute  = "unregister_route"
	presenceOpEndpointsByUID   = "endpoints_by_uid"
	presenceOpTouchRoutes      = "touch_routes"
	presenceOpApplyRouteAction = "apply_route_action"
)

// PresenceAuthorityRPCServiceID is the clusterv2 RPC service for UID route authority calls.
const PresenceAuthorityRPCServiceID uint8 = clusternet.RPCPresenceAuthority

// PresenceOwnerRPCServiceID is the clusterv2 RPC service for owner-node actions.
const PresenceOwnerRPCServiceID uint8 = clusternet.RPCPresenceOwner

// PresenceAuthority is the authority-side route API exposed over node RPC.
type PresenceAuthority interface {
	RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error)
	CommitRoute(context.Context, presence.RouteTarget, string) error
	AbortRoute(context.Context, presence.RouteTarget, string) error
	UnregisterRoute(context.Context, presence.RouteTarget, presence.RouteIdentity, uint64) error
	EndpointsByUID(context.Context, presence.RouteTarget, string) ([]presence.Route, error)
	TouchRoutes(context.Context, presence.RouteTarget, []presence.Route) error
}

// PresenceOwner applies authority-requested actions to owner-local sessions.
type PresenceOwner interface {
	ApplyRouteAction(context.Context, presence.RouteAction) error
}

// DeliveryOwnerPush accepts owner-node delivery batches over node RPC.
type DeliveryOwnerPush interface {
	Push(context.Context, runtimedelivery.PushCommand) (runtimedelivery.PushResult, error)
}

// DeliveryFanoutRunner accepts authority-node fanout tasks over node RPC.
type DeliveryFanoutRunner interface {
	RunTask(context.Context, runtimedelivery.FanoutTask) error
}

// ConversationAuthority handles UID-owned conversation active cache requests.
type ConversationAuthority interface {
	AdmitPatches(context.Context, conversationusecase.RouteTarget, []conversationusecase.ActivePatch) error
	ListUserConversationActiveViewForTarget(context.Context, conversationusecase.RouteTarget, string, metadb.UserConversationActiveCursor, int) (conversationusecase.ActiveViewPage, error)
	DrainAuthority(context.Context, conversationusecase.RouteTarget) (string, error)
}

// Options configures the internalv2 node RPC adapter.
type Options struct {
	// Authority handles UID route authority requests after payload decoding.
	Authority PresenceAuthority
	// Owner handles owner-local session conflict actions after payload decoding.
	Owner PresenceOwner
	// Delivery handles owner-local delivery push batches after payload decoding.
	Delivery DeliveryOwnerPush
	// DeliveryFanout handles authority-node delivery fanout tasks after payload decoding.
	DeliveryFanout DeliveryFanoutRunner
	// ConversationAuthority handles UID conversation authority cache requests after payload decoding.
	ConversationAuthority ConversationAuthority
	// Logger records node RPC adapter failures that are converted into statuses.
	Logger wklog.Logger
}

// Adapter decodes node RPC payloads and forwards them to local authority ports.
type Adapter struct {
	// authority owns business decisions; Adapter only performs RPC adaptation.
	authority PresenceAuthority
	// owner mutates only owner-local real session state.
	owner PresenceOwner
	// delivery pushes messages into owner-local delivery sessions.
	delivery DeliveryOwnerPush
	// deliveryFanout runs subscriber fanout tasks for this authority node.
	deliveryFanout DeliveryFanoutRunner
	// conversation owns UID conversation active cache decisions.
	conversation ConversationAuthority
	// logger records adapter decode errors and rejected local operations.
	logger wklog.Logger
}

// New creates a node RPC adapter.
func New(opts Options) *Adapter {
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &Adapter{authority: opts.Authority, owner: opts.Owner, delivery: opts.Delivery, deliveryFanout: opts.DeliveryFanout, conversation: opts.ConversationAuthority, logger: opts.Logger}
}

// HandlePresenceAuthorityRPC handles one encoded presence authority RPC payload.
func (a *Adapter) HandlePresenceAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodePresenceRPCRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("presence authority rpc decode failed",
			wklog.Event("internalv2.access.node.presence_authority_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.authority == nil {
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusRejected})
	}

	switch req.Op {
	case presenceOpRegisterRoute:
		result, err := a.authority.RegisterRoute(ctx, req.Target, req.Route)
		if err != nil {
			status := presenceRPCStatusForError(err)
			a.logPresenceAuthorityError(req, status, err)
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Register: result})
	case presenceOpCommitRoute:
		err := a.authority.CommitRoute(ctx, req.Target, req.PendingToken)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	case presenceOpAbortRoute:
		err := a.authority.AbortRoute(ctx, req.Target, req.PendingToken)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	case presenceOpUnregisterRoute:
		err := a.authority.UnregisterRoute(ctx, req.Target, req.Identity, req.OwnerSeq)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	case presenceOpEndpointsByUID:
		routes, err := a.authority.EndpointsByUID(ctx, req.Target, req.UID)
		if err != nil {
			status := presenceRPCStatusForError(err)
			a.logPresenceAuthorityError(req, status, err)
			return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
		}
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusOK, Endpoints: routes})
	case presenceOpTouchRoutes:
		err := a.authority.TouchRoutes(ctx, req.Target, req.Routes)
		status := presenceRPCStatusForError(err)
		a.logPresenceAuthorityError(req, status, err)
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown presence rpc op %q", req.Op)
		a.rpcLogger().Warn("presence authority rpc unknown operation",
			wklog.Event("internalv2.access.node.presence_authority_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// HandlePresenceOwnerRPC handles one encoded presence owner-action RPC payload.
func (a *Adapter) HandlePresenceOwnerRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodePresenceRPCRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("presence owner rpc decode failed",
			wklog.Event("internalv2.access.node.presence_owner_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.owner == nil {
		return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: rpcStatusRejected})
	}
	if req.Op != presenceOpApplyRouteAction {
		err := fmt.Errorf("internalv2/access/node: unknown presence owner rpc op %q", req.Op)
		a.rpcLogger().Warn("presence owner rpc unknown operation",
			wklog.Event("internalv2.access.node.presence_owner_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
	err = a.owner.ApplyRouteAction(ctx, req.Action)
	status := presenceRPCStatusForError(err)
	if status == rpcStatusRejected {
		fields := []wklog.Field{
			wklog.Event("internalv2.access.node.presence_owner_rejected"),
			wklog.String("op", req.Op),
			wklog.String("status", status),
			wklog.UID(req.Action.UID),
			wklog.Uint64("ownerNodeID", req.Action.OwnerNodeID),
			wklog.Uint64("ownerBootID", req.Action.OwnerBootID),
			wklog.SessionID(req.Action.SessionID),
			wklog.String("action", req.Action.Kind),
			wklog.Error(err),
		}
		a.rpcLogger().Warn("presence owner rpc rejected", fields...)
	}
	return encodePresenceRPCResponseBinary(presenceRPCResponse{Status: status})
}

func (a *Adapter) rpcLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("rpc")
}

func (a *Adapter) logPresenceAuthorityError(req presenceRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	fields := []wklog.Field{
		wklog.Event("internalv2.access.node.presence_authority_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Int("hashSlot", int(req.Target.HashSlot)),
		wklog.Uint64("slotID", uint64(req.Target.SlotID)),
		wklog.LeaderNodeID(req.Target.LeaderNodeID),
		wklog.Uint64("routeRevision", req.Target.RouteRevision),
		wklog.Uint64("authorityEpoch", req.Target.AuthorityEpoch),
		wklog.UID(req.UID),
		wklog.Int("routes", len(req.Routes)),
		wklog.Error(err),
	}
	if req.Route.UID != "" {
		fields = append(fields, wklog.UID(req.Route.UID), wklog.SessionID(req.Route.SessionID))
	}
	a.rpcLogger().Warn("presence authority rpc rejected", fields...)
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

// TouchRoutes refreshes owner routes on the target authority node.
func (c *Client) TouchRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	resp, err := c.call(ctx, target, presenceRPCRequest{Op: presenceOpTouchRoutes, Target: target, Routes: routes})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

// ApplyRouteAction applies one conflict action on the owner node.
func (c *Client) ApplyRouteAction(ctx context.Context, ownerNodeID uint64, action presence.RouteAction) error {
	resp, err := c.callService(ctx, ownerNodeID, PresenceOwnerRPCServiceID, presenceRPCRequest{Op: presenceOpApplyRouteAction, Action: action})
	if err != nil {
		return err
	}
	return presenceRPCErrorForStatus(resp.Status)
}

func (c *Client) call(ctx context.Context, target presence.RouteTarget, req presenceRPCRequest) (presenceRPCResponse, error) {
	return c.callService(ctx, target.LeaderNodeID, PresenceAuthorityRPCServiceID, req)
}

func (c *Client) callService(ctx context.Context, nodeID uint64, serviceID uint8, req presenceRPCRequest) (presenceRPCResponse, error) {
	if c == nil || c.node == nil {
		return presenceRPCResponse{}, fmt.Errorf("internalv2/access/node: presence rpc client not configured")
	}
	body, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		return presenceRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, serviceID, body)
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
