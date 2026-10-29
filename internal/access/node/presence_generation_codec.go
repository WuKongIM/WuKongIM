package node

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Generation frames have independent magic and operation numbers. Legacy
// WKVP/WKVR operations, including owner operations 9/10/11, retain their bytes.
var generationRequestMagic = []byte{'W', 'K', 'V', 'G', 1}
var generationResponseMagic = []byte{'W', 'K', 'V', 'g', 1}

const (
	generationRegister byte = iota + 1
	generationCommit
	generationAbortRoute
	generationBegin
	generationPage
	generationEnd
	generationAbortSlot
	generationReadUIDs
	generationTouch
	generationUnregister
	generationMaxWire = 8 << 20
	// The original 512 maximum stored UID selectors can occupy nearly 32 MiB.
	generationMaxRequestWire   = 40 << 20
	generationStatusStale      = "stale_generation"
	generationStatusIncomplete = "slot_incomplete"
)

var errGenerationWire = errors.New("internal/access/node: malformed presence generation frame")

type generationRequest struct {
	Op                                  byte
	Claim                               authority.GenerationClaim
	Owner                               authority.OwnerCut
	Sequence, AfterSessionID, RowsTotal uint64
	MaxRows, MaxBytes                   int
	Route                               presence.Route
	Token                               string
	Routes                              []presence.Route
	Identity                            presence.RouteIdentity
	OwnerSeq                            uint64
	UIDs                                []string
}

type generationResponse struct {
	Op                                                 byte
	Status                                             string
	Claim                                              authority.GenerationClaim
	Owner                                              authority.OwnerCut
	Sequence, AfterSessionID, NextSessionID, RowsTotal uint64
	Exhausted                                          bool
	Register                                           presence.RegisterResult
	Receipt                                            *authority.ActivationReceipt
	Routes                                             []presence.Route
}

func generationOperation(op byte) bool { return op >= generationRegister && op <= generationUnregister }
func appendGenerationClaim(dst []byte, c authority.GenerationClaim) []byte {
	dst = appendPresenceRouteTarget(dst, c.Target)
	for _, v := range []uint64{c.OwnerSetEpoch, c.ServingBoot, c.Generation, c.PreviousGeneration, c.AppliedRaftIndex} {
		dst = appendUvarint(dst, v)
	}
	return dst
}
func readGenerationClaim(c *ownerPageCursor) authority.GenerationClaim {
	target, offset, err := readPresenceRouteTarget(c.body, c.offset)
	if err != nil {
		c.err = errGenerationWire
		return authority.GenerationClaim{}
	}
	c.offset = offset
	return authority.GenerationClaim{Target: target, OwnerSetEpoch: c.unsigned(), ServingBoot: c.unsigned(), Generation: c.unsigned(), PreviousGeneration: c.unsigned(), AppliedRaftIndex: c.unsigned()}
}
func appendGenerationCut(dst []byte, o authority.OwnerCut) []byte {
	dst = appendUvarint(dst, o.NodeID)
	dst = appendUvarint(dst, o.BootID)
	return appendUvarint(dst, o.Cut)
}
func readGenerationCut(c *ownerPageCursor) authority.OwnerCut {
	return authority.OwnerCut{NodeID: c.unsigned(), BootID: c.unsigned(), Cut: c.unsigned()}
}
func appendGenerationReceipt(dst []byte, r *authority.ActivationReceipt) []byte {
	if r == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendGenerationClaim(dst, r.Claim)
	dst = appendGenerationCut(dst, r.Owner)
	dst = appendPresenceRouteIdentity(dst, r.Identity)
	return appendUvarint(dst, r.OwnerSeq)
}
func readGenerationReceipt(c *ownerPageCursor, allowed bool) *authority.ActivationReceipt {
	flag := c.take(1)
	if len(flag) != 1 {
		return nil
	}
	if flag[0] == 0 {
		return nil
	}
	// Page/UID replies may not carry activation state. Reject their flag before
	// decoding the receipt identity, whose string is not result-budget prepaid.
	if flag[0] != 1 || !allowed {
		c.err = errGenerationWire
		return nil
	}
	claim, cut := readGenerationClaim(c), readGenerationCut(c)
	identity, offset, err := readPresenceRouteIdentity(c.body, c.offset)
	if err != nil {
		c.err = errGenerationWire
		return nil
	}
	c.offset = offset
	return &authority.ActivationReceipt{Claim: claim, Owner: cut, Identity: identity, OwnerSeq: c.unsigned()}
}

// generationRouteWireBound counts borrowed input before allocating a wire
// backing. Scalar varints and string prefixes use their maximum encoded size.
func generationRouteWireBound(route presence.Route) int {
	return 128 + len(route.UID) + len(route.DeviceID) + len(route.Listener)
}
func generationRequestWireBound(req generationRequest) (int, bool) {
	n := 1024 + len(req.Token) + len(req.Identity.UID) + generationRouteWireBound(req.Route)
	if n > generationMaxRequestWire {
		return 0, false
	}
	for _, route := range req.Routes {
		size := generationRouteWireBound(route)
		if size > generationMaxRequestWire-n {
			return 0, false
		}
		n += size
	}
	for _, uid := range req.UIDs {
		if len(uid) > generationMaxRequestWire-n-10 {
			return 0, false
		}
		n += len(uid) + 10
	}
	return n, n <= generationMaxRequestWire
}
func generationResponseWireBound(resp generationResponse) (int, bool) {
	n := 2048 + len(resp.Status) + len(resp.Register.PendingToken)
	for _, receipt := range []*authority.ActivationReceipt{resp.Receipt, resp.Register.Receipt} {
		if receipt != nil {
			if len(receipt.Identity.UID) > generationMaxWire-n {
				return 0, false
			}
			n += len(receipt.Identity.UID)
		}
	}
	if n > generationMaxWire {
		return 0, false
	}
	for _, route := range resp.Routes {
		size := generationRouteWireBound(route)
		if size > generationMaxWire-n {
			return 0, false
		}
		n += size
	}
	for _, action := range resp.Register.Actions {
		size := 128 + len(action.UID) + len(action.Kind) + len(action.Reason)
		if size > generationMaxWire-n {
			return 0, false
		}
		n += size
	}
	return n, n <= generationMaxWire
}

func encodeGenerationRequest(req generationRequest) ([]byte, error) {
	if !generationOperation(req.Op) || len(req.UIDs) > 512 || len(req.Routes) > 4096 || req.MaxRows < 0 || req.MaxRows > 4096 || req.MaxBytes < 0 || req.MaxBytes > 128<<20 {
		return nil, errGenerationWire
	}
	wireBound, ok := generationRequestWireBound(req)
	if !ok {
		return nil, errGenerationWire
	}
	b := make([]byte, 0, wireBound)
	b = append(b, generationRequestMagic...)
	b = append(b, req.Op)
	b = appendGenerationClaim(b, req.Claim)
	b = appendGenerationCut(b, req.Owner)
	for _, v := range []uint64{req.Sequence, req.AfterSessionID, req.RowsTotal, uint64(req.MaxRows), uint64(req.MaxBytes)} {
		b = appendUvarint(b, v)
	}
	b = appendPresenceRoute(b, req.Route)
	b = appendPresenceRoutes(b, req.Routes)
	b = appendPresenceRouteIdentity(b, req.Identity)
	b = appendUvarint(b, req.OwnerSeq)
	b = appendString(b, req.Token)
	b = appendUvarint(b, uint64(len(req.UIDs)))
	for _, uid := range req.UIDs {
		b = appendString(b, uid)
	}
	if len(b) > generationMaxRequestWire {
		return nil, errGenerationWire
	}
	return b, nil
}
func decodeGenerationRequest(body []byte) (generationRequest, error) {
	var req generationRequest
	if len(body) < 6 || len(body) > generationMaxRequestWire || !hasMagic(body, generationRequestMagic) || !generationOperation(body[5]) {
		return req, errGenerationWire
	}
	req.Op = body[5]
	c := ownerPageCursor{body: body, offset: 6}
	req.Claim = readGenerationClaim(&c)
	req.Owner = readGenerationCut(&c)
	req.Sequence = c.unsigned()
	req.AfterSessionID = c.unsigned()
	req.RowsTotal = c.unsigned()
	rows, bytes := c.unsigned(), c.unsigned()
	if rows > 4096 || bytes > 128<<20 {
		return req, errGenerationWire
	}
	req.MaxRows = int(rows)
	req.MaxBytes = int(bytes)
	if c.err != nil {
		return req, errGenerationWire
	}
	route, offset, err := readPresenceRoute(body, c.offset)
	if err != nil {
		return req, errGenerationWire
	}
	req.Route = route
	c.offset = offset
	req.Routes, offset, err = readPresenceRoutes(body, c.offset)
	if err != nil {
		return req, errGenerationWire
	}
	c.offset = offset
	req.Identity, offset, err = readPresenceRouteIdentity(body, c.offset)
	if err != nil {
		return req, errGenerationWire
	}
	c.offset = offset
	req.OwnerSeq = c.unsigned()
	req.Token = string(c.text())
	count := c.unsigned()
	if c.err != nil || count > 512 {
		return req, errGenerationWire
	}
	if count > 0 {
		req.UIDs = make([]string, int(count))
	}
	for i := range req.UIDs {
		req.UIDs[i] = string(c.text())
	}
	if c.err != nil || c.offset != len(body) {
		return req, errGenerationWire
	}
	return req, nil
}
func knownGenerationStatus(status string) bool {
	switch status {
	case rpcStatusOK, rpcStatusNotLeader, rpcStatusStaleRoute, rpcStatusRouteNotReady, rpcStatusContextCanceled, rpcStatusContextDeadlineExceeded, rpcStatusRejected, generationStatusStale, generationStatusIncomplete:
		return true
	}
	return false
}
func encodeGenerationResponse(resp generationResponse) ([]byte, error) {
	if !generationOperation(resp.Op) || !knownGenerationStatus(resp.Status) || len(resp.Routes) > 4096 || len(resp.Register.Actions) > 4096 {
		return nil, errGenerationWire
	}
	wireBound, ok := generationResponseWireBound(resp)
	if !ok {
		return nil, errGenerationWire
	}
	b := make([]byte, 0, wireBound)
	b = append(b, generationResponseMagic...)
	b = append(b, resp.Op)
	b = appendString(b, resp.Status)
	b = appendGenerationClaim(b, resp.Claim)
	b = appendGenerationCut(b, resp.Owner)
	for _, v := range []uint64{resp.Sequence, resp.AfterSessionID, resp.NextSessionID, resp.RowsTotal} {
		b = appendUvarint(b, v)
	}
	if resp.Exhausted {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	b = appendGenerationReceipt(b, resp.Receipt)
	b = appendGenerationReceipt(b, resp.Register.Receipt)
	if resp.Register.PendingClaim != nil {
		b = append(b, 1)
		b = appendGenerationClaim(b, *resp.Register.PendingClaim)
	} else {
		b = append(b, 0)
	}
	b = appendString(b, string(resp.Register.PendingToken))
	b = appendPresenceActions(b, resp.Register.Actions)
	b = appendPresenceRoutes(b, resp.Routes)
	if len(b) > generationMaxWire {
		return nil, errGenerationWire
	}
	return b, nil
}

// scanGenerationResponse verifies the caller's operation before decoding its
// fields. Recovery route strings are scanned without allocating decoded backing;
// page and metadata replies cannot hide activation or ignored route payloads.
func scanGenerationResponse(body []byte, expectedOp byte) (generationResponse, int, uint64, uint64, error) {
	var resp generationResponse
	if len(body) < 6 || len(body) > generationMaxWire || !hasMagic(body, generationResponseMagic) || !generationOperation(body[5]) || body[5] != expectedOp {
		return resp, 0, 0, 0, errGenerationWire
	}
	resp.Op = body[5]
	c := ownerPageCursor{body: body, offset: 6}
	statusBytes := c.text()
	if len(statusBytes) > 40 {
		return resp, 0, 0, 0, errGenerationWire
	}
	resp.Status = string(statusBytes)
	if !knownGenerationStatus(resp.Status) {
		return resp, 0, 0, 0, errGenerationWire
	}
	resp.Claim = readGenerationClaim(&c)
	resp.Owner = readGenerationCut(&c)
	resp.Sequence = c.unsigned()
	resp.AfterSessionID = c.unsigned()
	resp.NextSessionID = c.unsigned()
	resp.RowsTotal = c.unsigned()
	exhausted := c.take(1)
	if len(exhausted) != 1 || exhausted[0] > 1 {
		return resp, 0, 0, 0, errGenerationWire
	}
	resp.Exhausted = exhausted[0] == 1
	// The expected operation is caller-owned. An untrusted response cannot
	// select a more permissive decoder before its operation echo is checked.
	resp.Receipt = readGenerationReceipt(&c, resp.Op == generationCommit && resp.Status == rpcStatusOK)
	resp.Register.Receipt = readGenerationReceipt(&c, resp.Op == generationRegister && resp.Status == rpcStatusOK)
	flag := c.take(1)
	if len(flag) != 1 || flag[0] > 1 || (flag[0] != 0 && (resp.Op != generationRegister || resp.Status != rpcStatusOK)) {
		return resp, 0, 0, 0, errGenerationWire
	}
	if flag[0] == 1 {
		claim := readGenerationClaim(&c)
		resp.Register.PendingClaim = &claim
	}
	tokenBytes := c.text()
	if len(tokenBytes) != 0 && (resp.Op != generationRegister || resp.Status != rpcStatusOK) {
		return resp, 0, 0, 0, errGenerationWire
	}
	resp.Register.PendingToken = presence.PendingRouteToken(tokenBytes)
	// Only register replies may carry actions; bound their decoded ownership by
	// the small activation response frame, not the owner recovery page ledger.
	if c.err != nil {
		return resp, 0, 0, 0, errGenerationWire
	}
	actionsOffset := c.offset
	actionsCount := c.unsigned()
	if actionsCount > 4096 || (resp.Op != generationRegister && actionsCount != 0) {
		return resp, 0, 0, 0, errGenerationWire
	}
	for i := uint64(0); i < actionsCount; i++ {
		c.text()
		c.unsigned()
		c.unsigned()
		c.unsigned()
		c.text()
		c.text()
		c.signed()
	}
	if c.err != nil {
		return resp, 0, 0, 0, errGenerationWire
	}
	afterActions := c.offset
	if actionsCount > 0 {
		actions, offset, err := readPresenceActions(body, actionsOffset)
		if err != nil || offset != afterActions {
			return resp, 0, 0, 0, errGenerationWire
		}
		resp.Register.Actions = actions
	}
	routesOffset := c.offset
	rows := c.unsigned()
	if rows > 4096 || (rows != 0 && resp.Op != generationPage && resp.Op != generationReadUIDs) {
		return resp, 0, 0, 0, errGenerationWire
	}
	bytes := uint64(unsafe.Sizeof(presence.OwnedSlotPage{})) + rows*uint64(unsafe.Sizeof(presence.Route{}))
	for i := uint64(0); i < rows; i++ {
		bytes += uint64(len(c.text()))
		for range 4 {
			c.unsigned()
		}
		bytes += uint64(len(c.text()))
		c.take(2)
		bytes += uint64(len(c.text()))
		c.signed()
		c.signed()
	}
	if c.err != nil || c.offset != len(body) {
		return resp, 0, 0, 0, errGenerationWire
	}
	if resp.Op == generationPage || resp.Op == generationReadUIDs {
		if resp.Receipt != nil || resp.Register.Receipt != nil || resp.Register.PendingClaim != nil || resp.Register.PendingToken != "" || len(resp.Register.Actions) != 0 {
			return resp, 0, 0, 0, errGenerationWire
		}
	}
	if resp.Status != rpcStatusOK && (rows != 0 || resp.Receipt != nil || resp.Register.Receipt != nil || resp.Register.PendingClaim != nil || resp.Register.PendingToken != "" || len(resp.Register.Actions) != 0) {
		return resp, 0, 0, 0, errGenerationWire
	}
	return resp, routesOffset, rows, bytes, nil
}
func decodeGenerationResponse(body []byte, expectedOp byte) (generationResponse, error) {
	resp, offset, _, _, err := scanGenerationResponse(body, expectedOp)
	if err != nil {
		return resp, err
	}
	routes, end, err := readPresenceRoutes(body, offset)
	if err != nil || end != len(body) {
		return resp, errGenerationWire
	}
	resp.Routes = routes
	return resp, nil
}
func generationError(status string) error {
	switch status {
	case generationStatusStale:
		return authority.ErrStaleGeneration
	case generationStatusIncomplete:
		return authority.ErrSlotIncomplete
	}
	if !knownGenerationStatus(status) {
		return errGenerationWire
	}
	return presenceRPCErrorForStatus(status)
}
func generationStatus(err error) string {
	if errors.Is(err, authority.ErrStaleGeneration) {
		return generationStatusStale
	}
	if errors.Is(err, authority.ErrSlotIncomplete) || errors.Is(err, presence.ErrRecoveryResultBudget) {
		return generationStatusIncomplete
	}
	return presenceRPCStatusForError(err)
}
func decodeGenerationPage(ctx context.Context, body []byte, req authority.SlotPageRequest, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, offset, rows, bytes, err := scanGenerationResponse(body, generationPage)
	if err != nil {
		return nil, err
	}
	if resp.Op != generationPage {
		return nil, errGenerationWire
	}
	if err = generationError(resp.Status); err != nil {
		return nil, err
	}
	if !resp.Claim.Equal(req.Claim) || resp.Owner != req.Owner || resp.Sequence != req.Sequence || resp.AfterSessionID != req.AfterSessionID ||
		resp.NextSessionID < req.AfterSessionID || (rows != 0 && resp.NextSessionID <= req.AfterSessionID) || rows > uint64(req.MaxRows) || resp.RowsTotal < rows || bytes > uint64(req.MaxBytes) {
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
	last := req.AfterSessionID
	for _, route := range routes {
		if route.SessionID <= last || route.OwnerNodeID != req.Owner.NodeID || route.OwnerBootID != req.Owner.BootID || route.UID == "" || route.OwnerSeq == 0 {
			clear(routes)
			lease.Release()
			return nil, errGenerationWire
		}
		last = route.SessionID
	}
	if (rows > 0 && last != resp.NextSessionID) || (rows == 0 && (!resp.Exhausted || resp.NextSessionID != req.AfterSessionID)) {
		clear(routes)
		lease.Release()
		return nil, errGenerationWire
	}
	if err = ctx.Err(); err != nil {
		clear(routes)
		lease.Release()
		return nil, err
	}
	return &presence.OwnedSlotPage{Page: authority.SlotPage{Claim: resp.Claim, Owner: resp.Owner, Sequence: resp.Sequence, AfterSessionID: resp.AfterSessionID, NextSessionID: resp.NextSessionID, RowsTotal: resp.RowsTotal, Exhausted: resp.Exhausted, Routes: routes}, Lease: lease}, nil
}

func requireGenerationOperation(resp generationResponse, op byte) error {
	if resp.Op != op {
		return fmt.Errorf("%w: operation echo", errGenerationWire)
	}
	return generationError(resp.Status)
}
