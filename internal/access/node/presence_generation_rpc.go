package node

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// PresenceGenerationAuthority preserves the complete usecase boundary; the
// adapter does no reconstruction, conflict or retry orchestration.
type PresenceGenerationAuthority interface {
	RegisterRoute(context.Context, presence.RouteTarget, presence.Route) (presence.RegisterResult, error)
	CommitRouteWithReceipt(context.Context, presence.GenerationClaim, presence.PendingRouteToken) (presence.ActivationReceipt, error)
	AbortRouteWithClaim(context.Context, presence.GenerationClaim, presence.PendingRouteToken) error
	TouchRoutes(context.Context, presence.GenerationClaim, []presence.Route) error
	UnregisterRoute(context.Context, presence.GenerationClaim, presence.RouteIdentity, uint64) error
}

// PresenceGenerationOwner owns the exact local barrier, pages and UID recheck.
type PresenceGenerationOwner interface {
	BeginSlot(context.Context, presence.GenerationClaim) (presence.OwnerCut, error)
	ReadSlotPage(context.Context, presence.SlotPageRequest, *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error)
	EndSlot(context.Context, presence.SlotEndRequest) (presence.SlotEnd, error)
	AbortSlot(context.Context, presence.GenerationClaim, presence.OwnerCut) error
	ReadGenerationUIDs(context.Context, presence.GenerationClaim, presence.OwnerCut, []string, *presence.RecoveryResultBudget) (*presence.OwnedSlotRoutes, error)
}

func isGenerationFrame(body []byte) bool {
	return len(body) >= 4 && body[0] == 'W' && body[1] == 'K' && body[2] == 'V' && body[3] == 'G'
}

func generationOwnerError(err error) error {
	if errors.Is(err, online.ErrStaleActivation) {
		return authority.ErrStaleGeneration
	}
	if errors.Is(err, online.ErrSlotPageCapacity) {
		return authority.ErrSlotIncomplete
	}
	return err
}
func (a *Adapter) handlePresenceGeneration(ctx context.Context, body []byte, owner bool) ([]byte, error) {
	req, err := decodeGenerationRequest(body)
	if err != nil {
		return nil, err
	}
	resp := generationResponse{Op: req.Op, Status: rpcStatusOK}
	fail := func(err error) ([]byte, error) {
		status := generationStatus(generationOwnerError(err))
		// Full activation loses source/provider details in compatible not-ready
		// and deadline statuses. Reuse the bounded local log before encoding;
		// ordinary owner reads, mutations and unknown causes add no event.
		if !owner && (status == rpcStatusRouteNotReady || status == rpcStatusContextDeadlineExceeded) {
			var op string
			switch req.Op {
			case generationRegister:
				op = presenceOpRegisterRoute
			case generationCommit:
				op = presenceOpCommitRoute
			}
			if op != "" {
				a.logPresenceAuthorityError(presenceRPCRequest{Op: op, Target: req.Claim.Target, Route: req.Route}, status, err)
			}
		}
		// A rejected owner Begin blocks full recovery for the whole hash slot,
		// yet its status alone cannot say which owner check failed (#1097).
		if owner && req.Op == generationBegin {
			a.logPresenceOwnerBeginRejected(req.Claim, status, err)
		}
		return encodeGenerationResponse(generationResponse{Op: req.Op, Status: status})
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	if !owner {
		if a == nil || a.generationAuthority == nil {
			return fail(authority.ErrSlotIncomplete)
		}
		if req.Op != generationRegister && !req.Claim.Valid() {
			return fail(authority.ErrStaleGeneration)
		}
		switch req.Op {
		case generationRegister:
			resp.Register, err = a.generationAuthority.RegisterRoute(ctx, req.Claim.Target, req.Route)
		case generationCommit:
			var receipt presence.ActivationReceipt
			receipt, err = a.generationAuthority.CommitRouteWithReceipt(ctx, req.Claim, presence.PendingRouteToken(req.Token))
			if err == nil {
				resp.Receipt = &receipt
			}
		case generationAbortRoute:
			err = a.generationAuthority.AbortRouteWithClaim(ctx, req.Claim, presence.PendingRouteToken(req.Token))
		case generationTouch:
			err = a.generationAuthority.TouchRoutes(ctx, req.Claim, req.Routes)
		case generationUnregister:
			err = a.generationAuthority.UnregisterRoute(ctx, req.Claim, req.Identity, req.OwnerSeq)
		default:
			return nil, errGenerationWire
		}
	} else {
		if a == nil || a.generationOwner == nil {
			return fail(authority.ErrSlotIncomplete)
		}
		if !req.Claim.Valid() {
			return fail(authority.ErrStaleGeneration)
		}
		switch req.Op {
		case generationBegin:
			resp.Claim = req.Claim
			resp.Owner, err = a.generationOwner.BeginSlot(ctx, req.Claim)
		case generationPage:
			var page *presence.OwnedSlotPage
			page, err = a.generationOwner.ReadSlotPage(ctx, presence.SlotPageRequest{Claim: req.Claim, Owner: req.Owner, Sequence: req.Sequence, AfterSessionID: req.AfterSessionID, MaxRows: req.MaxRows, MaxBytes: req.MaxBytes}, a.generationBudget)
			if err == nil {
				defer page.Release()
				p := page.Page
				resp.Claim = p.Claim
				resp.Owner = p.Owner
				resp.Sequence = p.Sequence
				resp.AfterSessionID = p.AfterSessionID
				resp.NextSessionID = p.NextSessionID
				resp.RowsTotal = p.RowsTotal
				resp.Exhausted = p.Exhausted
				resp.Routes = p.Routes
			}
		case generationEnd:
			var end presence.SlotEnd
			end, err = a.generationOwner.EndSlot(ctx, presence.SlotEndRequest{Claim: req.Claim, Owner: req.Owner, Sequence: req.Sequence, LastSessionID: req.AfterSessionID, RowsTotal: req.RowsTotal})
			if err == nil {
				resp.Claim = end.Claim
				resp.Owner = end.Owner
				resp.Sequence = end.Sequence
				resp.NextSessionID = end.LastSessionID
				resp.RowsTotal = end.RowsTotal
			}
		case generationAbortSlot:
			err = a.generationOwner.AbortSlot(ctx, req.Claim, req.Owner)
		case generationReadUIDs:
			var routes *presence.OwnedSlotRoutes
			routes, err = a.generationOwner.ReadGenerationUIDs(ctx, req.Claim, req.Owner, req.UIDs, a.generationBudget)
			if err == nil {
				defer routes.Release()
				resp.Claim = req.Claim
				resp.Owner = req.Owner
				resp.Routes = routes.Routes
			}
		default:
			return nil, errGenerationWire
		}
	}
	if err != nil {
		return fail(err)
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	return encodeGenerationResponse(resp)
}

func (c *Client) callGenerationWire(ctx context.Context, nodeID uint64, service uint8, req generationRequest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.node == nil || nodeID == 0 {
		return nil, authority.ErrSlotIncomplete
	}
	body, err := encodeGenerationRequest(req)
	if err != nil {
		return nil, err
	}
	return c.node.CallRPC(ctx, nodeID, service, body)
}
func (c *Client) callGeneration(ctx context.Context, nodeID uint64, service uint8, req generationRequest) (generationResponse, error) {
	body, err := c.callGenerationWire(ctx, nodeID, service, req)
	if err != nil {
		return generationResponse{}, err
	}
	resp, err := decodeGenerationResponse(body, req.Op)
	if err == nil {
		err = requireGenerationOperation(resp, req.Op)
	}
	return resp, err
}
func sameGenerationTarget(a, b presence.RouteTarget) bool {
	return a.HashSlot == b.HashSlot && a.SlotID == b.SlotID && a.LeaderNodeID == b.LeaderNodeID && a.LeaderTerm == b.LeaderTerm && a.ConfigEpoch == b.ConfigEpoch
}

// RegisterPresenceGeneration requires a receipt or an exactly bound pending
// claim. It never calls the legacy registration operation after a failure.
func (c *Client) RegisterPresenceGeneration(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	resp, err := c.callGeneration(ctx, target.LeaderNodeID, PresenceAuthorityRPCServiceID, generationRequest{Op: generationRegister, Claim: presence.GenerationClaim{Target: target}, Route: route})
	if err != nil {
		return presence.RegisterResult{}, err
	}
	result := resp.Register
	if result.PendingToken != "" {
		if result.Receipt != nil || result.PendingClaim == nil || !result.PendingClaim.Valid() || !sameGenerationTarget(target, result.PendingClaim.Target) {
			return presence.RegisterResult{}, errGenerationWire
		}
	} else if result.Receipt == nil || !result.Receipt.Valid() || !sameGenerationTarget(target, result.Receipt.Claim.Target) || result.Receipt.Identity != route.Identity() || result.Receipt.OwnerSeq != route.OwnerSeq || result.PendingClaim != nil {
		return presence.RegisterResult{}, errGenerationWire
	}
	return result, nil
}
func (c *Client) CommitPresenceGeneration(ctx context.Context, claim presence.GenerationClaim, token string) (presence.ActivationReceipt, error) {
	resp, err := c.callGeneration(ctx, claim.Target.LeaderNodeID, PresenceAuthorityRPCServiceID, generationRequest{Op: generationCommit, Claim: claim, Token: token})
	if err != nil {
		return presence.ActivationReceipt{}, err
	}
	if resp.Receipt == nil || !resp.Receipt.Valid() || !resp.Receipt.Claim.Equal(claim) {
		return presence.ActivationReceipt{}, errGenerationWire
	}
	return *resp.Receipt, nil
}
func (c *Client) AbortPresenceGeneration(ctx context.Context, claim presence.GenerationClaim, token string) error {
	_, err := c.callGeneration(ctx, claim.Target.LeaderNodeID, PresenceAuthorityRPCServiceID, generationRequest{Op: generationAbortRoute, Claim: claim, Token: token})
	return err
}
func (c *Client) TouchPresenceGeneration(ctx context.Context, claim presence.GenerationClaim, routes []presence.Route) error {
	_, err := c.callGeneration(ctx, claim.Target.LeaderNodeID, PresenceAuthorityRPCServiceID, generationRequest{Op: generationTouch, Claim: claim, Routes: routes})
	return err
}
func (c *Client) UnregisterPresenceGeneration(ctx context.Context, claim presence.GenerationClaim, id presence.RouteIdentity, seq uint64) error {
	_, err := c.callGeneration(ctx, claim.Target.LeaderNodeID, PresenceAuthorityRPCServiceID, generationRequest{Op: generationUnregister, Claim: claim, Identity: id, OwnerSeq: seq})
	return err
}
func (c *Client) BeginPresenceSlot(ctx context.Context, nodeID uint64, claim presence.GenerationClaim) (presence.OwnerCut, error) {
	resp, err := c.callGeneration(ctx, nodeID, PresenceOwnerRPCServiceID, generationRequest{Op: generationBegin, Claim: claim})
	if err != nil {
		return presence.OwnerCut{}, err
	}
	if !resp.Claim.Equal(claim) || !resp.Owner.Valid() || resp.Owner.NodeID != nodeID {
		return presence.OwnerCut{}, errGenerationWire
	}
	return resp.Owner, nil
}
func (c *Client) ReadPresenceSlotPage(ctx context.Context, req presence.SlotPageRequest, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	body, err := c.callGenerationWire(ctx, req.Owner.NodeID, PresenceOwnerRPCServiceID, generationRequest{Op: generationPage, Claim: req.Claim, Owner: req.Owner, Sequence: req.Sequence, AfterSessionID: req.AfterSessionID, MaxRows: req.MaxRows, MaxBytes: req.MaxBytes})
	if err != nil {
		return nil, err
	}
	return decodeGenerationPage(ctx, body, req, budget)
}
func (c *Client) EndPresenceSlot(ctx context.Context, req presence.SlotEndRequest) (presence.SlotEnd, error) {
	resp, err := c.callGeneration(ctx, req.Owner.NodeID, PresenceOwnerRPCServiceID, generationRequest{Op: generationEnd, Claim: req.Claim, Owner: req.Owner, Sequence: req.Sequence, AfterSessionID: req.LastSessionID, RowsTotal: req.RowsTotal})
	if err != nil {
		return presence.SlotEnd{}, err
	}
	if !resp.Claim.Equal(req.Claim) || resp.Owner != req.Owner || resp.Sequence != req.Sequence || resp.NextSessionID != req.LastSessionID || resp.RowsTotal != req.RowsTotal {
		return presence.SlotEnd{}, errGenerationWire
	}
	return presence.SlotEnd{Claim: resp.Claim, Owner: resp.Owner, Sequence: resp.Sequence, LastSessionID: resp.NextSessionID, RowsTotal: resp.RowsTotal}, nil
}
func (c *Client) AbortPresenceSlot(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut) error {
	_, err := c.callGeneration(ctx, cut.NodeID, PresenceOwnerRPCServiceID, generationRequest{Op: generationAbortSlot, Claim: claim, Owner: cut})
	return err
}
func (c *Client) ReadPresenceGenerationUIDs(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut, uids []string, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotRoutes, error) {
	body, err := c.callGenerationWire(ctx, cut.NodeID, PresenceOwnerRPCServiceID, generationRequest{Op: generationReadUIDs, Claim: claim, Owner: cut, UIDs: uids})
	if err != nil {
		return nil, err
	}
	resp, offset, rows, bytes, err := scanGenerationResponse(body, generationReadUIDs)
	if err != nil {
		return nil, err
	}
	if err = requireGenerationOperation(resp, generationReadUIDs); err != nil {
		return nil, err
	}
	if !resp.Claim.Equal(claim) || resp.Owner != cut {
		return nil, errGenerationWire
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	lease, err := budget.TryReserve(rows, bytes)
	if err != nil {
		return nil, err
	}
	routes, end, err := readPresenceRoutes(body, offset)
	if err != nil || end != len(body) {
		lease.Release()
		return nil, errGenerationWire
	}
	wanted := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		wanted[uid] = struct{}{}
	}
	for _, route := range routes {
		_, ok := wanted[route.UID]
		if !ok || route.OwnerNodeID != cut.NodeID || route.OwnerBootID != cut.BootID || route.SessionID == 0 || route.OwnerSeq == 0 {
			clear(routes)
			lease.Release()
			return nil, errGenerationWire
		}
	}
	if err = ctx.Err(); err != nil {
		clear(routes)
		lease.Release()
		return nil, err
	}
	return &presence.OwnedSlotRoutes{Routes: routes, Lease: lease}, nil
}

// presenceOwnerRejectReason maps an owner Begin failure to a closed log value;
// raw cause text is never logged.
func presenceOwnerRejectReason(err error) string {
	if reason := controller.PresenceRejectReasonOf(err); reason != controller.PresenceRejectUnknown {
		return reason.String()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, controller.ErrPresenceGenerationConflict):
		return "generation_conflict"
	case errors.Is(err, controller.ErrNotLeader), errors.Is(err, controller.ErrNotStarted):
		return "controller_unavailable"
	case errors.Is(err, online.ErrStaleActivation):
		return "owner_registry_stale"
	}
	return "unknown"
}

// logPresenceOwnerBeginRejected records one bounded event per rejected Full
// Begin: a closed reason plus the numeric claim, with no identities or causes.
// Begin runs at most once per owner and hash slot per recovery round.
func (a *Adapter) logPresenceOwnerBeginRejected(claim presence.GenerationClaim, status string, err error) {
	if a == nil || a.logger == nil || err == nil {
		return
	}
	t := claim.Target
	a.rpcLogger().Warn("presence owner begin rejected",
		wklog.Event("internal.access.node.presence_owner_begin_rejected"),
		wklog.String("status", status),
		wklog.String("owner_reject_reason", presenceOwnerRejectReason(err)),
		wklog.Int("hashSlot", int(t.HashSlot)),
		wklog.Uint64("slotID", uint64(t.SlotID)),
		wklog.LeaderNodeID(t.LeaderNodeID),
		wklog.Uint64("leaderTerm", t.LeaderTerm),
		wklog.Uint64("generation", claim.Generation),
		wklog.Uint64("ownerSetEpoch", claim.OwnerSetEpoch),
		wklog.Uint64("servingBoot", claim.ServingBoot),
	)
}
