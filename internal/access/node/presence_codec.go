package node

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

var (
	presenceRPCRequestMagic  = [...]byte{'W', 'K', 'V', 'P', 2}
	presenceRPCResponseMagic = [...]byte{'W', 'K', 'V', 'R', 2}
)

const (
	presenceOpRegisterRouteID byte = iota + 1
	presenceOpCommitRouteID
	presenceOpAbortRouteID
	presenceOpUnregisterRouteID
	presenceOpEndpointsByUIDID
	presenceOpTouchRoutesID
	presenceOpApplyRouteActionID
)

const maxPresenceRPCCollectionLen = 4096

// presenceRPCRequest is the deterministic binary DTO for authority calls.
type presenceRPCRequest struct {
	// Op selects the authority method to call after decoding.
	Op string
	// Target fences the request to one observed hash-slot authority epoch.
	Target presence.RouteTarget
	// Route carries a route for register requests.
	Route presence.Route
	// Routes carries bulk owner routes for touch requests.
	Routes []presence.Route
	// Action carries an owner-node conflict action.
	Action presence.RouteAction
	// Identity identifies a route for unregister requests.
	Identity presence.RouteIdentity
	// PendingToken names a pending route for commit or abort requests.
	PendingToken string
	// UID selects the endpoint lookup key.
	UID string
	// OwnerSeq carries owner-side fencing for unregister requests.
	OwnerSeq uint64
}

// presenceRPCResponse is the deterministic binary DTO returned by authority calls.
type presenceRPCResponse struct {
	// Status is one of the stable presence RPC status strings.
	Status string
	// Register carries RegisterRoute output when Status is ok.
	Register presence.RegisterResult
	// Endpoints carries EndpointsByUID output when Status is ok.
	Endpoints []presence.Route
}

func encodePresenceRPCRequestBinary(req presenceRPCRequest) ([]byte, error) {
	opID, err := presenceOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, 128)
	dst = append(dst, presenceRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendPresenceRouteTarget(dst, req.Target)
	dst = appendPresenceRoute(dst, req.Route)
	dst = appendPresenceRoutes(dst, req.Routes)
	dst = appendPresenceAction(dst, req.Action)
	dst = appendPresenceRouteIdentity(dst, req.Identity)
	dst = appendString(dst, req.PendingToken)
	dst = appendString(dst, req.UID)
	dst = appendUvarint(dst, req.OwnerSeq)
	return dst, nil
}

func decodePresenceRPCRequest(body []byte) (presenceRPCRequest, error) {
	if !isPresenceRPCRequestBinary(body) {
		return presenceRPCRequest{}, fmt.Errorf("internal/access/node: invalid presence request codec")
	}
	offset := len(presenceRPCRequestMagic)
	opID, next, err := readByte(body, offset, "presence op")
	if err != nil {
		return presenceRPCRequest{}, err
	}
	offset = next
	op, err := presenceOpFromID(opID)
	if err != nil {
		return presenceRPCRequest{}, err
	}

	var req presenceRPCRequest
	req.Op = op
	if req.Target, offset, err = readPresenceRouteTarget(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Route, offset, err = readPresenceRoute(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Routes, offset, err = readPresenceRoutes(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Action, offset, err = readPresenceAction(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Identity, offset, err = readPresenceRouteIdentity(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.PendingToken, offset, err = readString(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.UID, offset, err = readString(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.OwnerSeq, offset, err = readUvarint(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if offset != len(body) {
		return presenceRPCRequest{}, fmt.Errorf("internal/access/node: trailing presence request bytes")
	}
	return req, nil
}

func encodePresenceRPCResponseBinary(resp presenceRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, presenceRPCResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendPresenceRegisterResult(dst, resp.Register)
	dst = appendPresenceRoutes(dst, resp.Endpoints)
	return dst, nil
}

func decodePresenceRPCResponse(body []byte) (presenceRPCResponse, error) {
	return decodePresenceRPCResponseBinary(body)
}

func decodePresenceRPCResponseBinary(body []byte) (presenceRPCResponse, error) {
	if !isPresenceRPCResponseBinary(body) {
		return presenceRPCResponse{}, fmt.Errorf("internal/access/node: invalid presence response codec")
	}
	offset := len(presenceRPCResponseMagic)
	var resp presenceRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.Register, offset, err = readPresenceRegisterResult(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.Endpoints, offset, err = readPresenceRoutes(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if offset != len(body) {
		return presenceRPCResponse{}, fmt.Errorf("internal/access/node: trailing presence response bytes")
	}
	return resp, nil
}

func isPresenceRPCRequestBinary(body []byte) bool {
	return hasMagic(body, presenceRPCRequestMagic[:])
}

func isPresenceRPCResponseBinary(body []byte) bool {
	return hasMagic(body, presenceRPCResponseMagic[:])
}

func presenceOpID(op string) (byte, error) {
	switch op {
	case presenceOpRegisterRoute:
		return presenceOpRegisterRouteID, nil
	case presenceOpCommitRoute:
		return presenceOpCommitRouteID, nil
	case presenceOpAbortRoute:
		return presenceOpAbortRouteID, nil
	case presenceOpUnregisterRoute:
		return presenceOpUnregisterRouteID, nil
	case presenceOpEndpointsByUID:
		return presenceOpEndpointsByUIDID, nil
	case presenceOpTouchRoutes:
		return presenceOpTouchRoutesID, nil
	case presenceOpApplyRouteAction:
		return presenceOpApplyRouteActionID, nil
	default:
		return 0, fmt.Errorf("internal/access/node: unknown presence op %q", op)
	}
}

func presenceOpFromID(op byte) (string, error) {
	switch op {
	case presenceOpRegisterRouteID:
		return presenceOpRegisterRoute, nil
	case presenceOpCommitRouteID:
		return presenceOpCommitRoute, nil
	case presenceOpAbortRouteID:
		return presenceOpAbortRoute, nil
	case presenceOpUnregisterRouteID:
		return presenceOpUnregisterRoute, nil
	case presenceOpEndpointsByUIDID:
		return presenceOpEndpointsByUID, nil
	case presenceOpTouchRoutesID:
		return presenceOpTouchRoutes, nil
	case presenceOpApplyRouteActionID:
		return presenceOpApplyRouteAction, nil
	default:
		return "", fmt.Errorf("internal/access/node: unknown presence op id %d", op)
	}
}

func appendPresenceRouteTarget(dst []byte, target presence.RouteTarget) []byte {
	dst = appendUvarint(dst, uint64(target.HashSlot))
	dst = appendUvarint(dst, uint64(target.SlotID))
	dst = appendUvarint(dst, target.LeaderNodeID)
	dst = appendUvarint(dst, target.LeaderTerm)
	dst = appendUvarint(dst, target.ConfigEpoch)
	dst = appendUvarint(dst, target.RouteRevision)
	dst = appendUvarint(dst, target.AuthorityEpoch)
	return dst
}

func readPresenceRouteTarget(body []byte, offset int) (presence.RouteTarget, int, error) {
	var target presence.RouteTarget
	var v uint64
	var err error
	if v, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	if v > uint64(^uint16(0)) {
		return presence.RouteTarget{}, offset, fmt.Errorf("internal/access/node: presence hash slot overflows uint16")
	}
	target.HashSlot = uint16(v)
	if v, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	if v > uint64(^uint32(0)) {
		return presence.RouteTarget{}, offset, fmt.Errorf("internal/access/node: presence slot id overflows uint32")
	}
	target.SlotID = uint32(v)
	if target.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	if target.LeaderTerm, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	if target.ConfigEpoch, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	if target.RouteRevision, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	if target.AuthorityEpoch, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteTarget{}, offset, err
	}
	return target, offset, nil
}

func appendPresenceRoute(dst []byte, route presence.Route) []byte {
	dst = appendString(dst, route.UID)
	dst = appendUvarint(dst, route.OwnerNodeID)
	dst = appendUvarint(dst, route.OwnerBootID)
	dst = appendUvarint(dst, route.OwnerSeq)
	dst = appendUvarint(dst, route.SessionID)
	dst = appendString(dst, route.DeviceID)
	dst = append(dst, route.DeviceFlag, route.DeviceLevel)
	dst = appendString(dst, route.Listener)
	dst = appendVarint(dst, route.ConnectedUnix)
	dst = appendVarint(dst, route.LastSeenUnix)
	return dst
}

func readPresenceRoute(body []byte, offset int) (presence.Route, int, error) {
	var route presence.Route
	var err error
	if route.UID, offset, err = readString(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.OwnerBootID, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.OwnerSeq, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.DeviceID, offset, err = readString(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.DeviceFlag, offset, err = readByte(body, offset, "presence device flag"); err != nil {
		return presence.Route{}, offset, err
	}
	if route.DeviceLevel, offset, err = readByte(body, offset, "presence device level"); err != nil {
		return presence.Route{}, offset, err
	}
	if route.Listener, offset, err = readString(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.ConnectedUnix, offset, err = readVarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.LastSeenUnix, offset, err = readVarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	return route, offset, nil
}

func appendPresenceRoutes(dst []byte, routes []presence.Route) []byte {
	dst = appendUvarint(dst, uint64(len(routes)))
	for _, route := range routes {
		dst = appendPresenceRoute(dst, route)
	}
	return dst
}

func readPresenceRoutes(body []byte, offset int) ([]presence.Route, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateCollectionLen(count, len(body)-offset, "presence routes"); err != nil {
		return nil, offset, err
	}
	routes := make([]presence.Route, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var route presence.Route
		if route, offset, err = readPresenceRoute(body, offset); err != nil {
			return nil, offset, err
		}
		routes = append(routes, route)
	}
	return routes, offset, nil
}

func appendPresenceRouteIdentity(dst []byte, identity presence.RouteIdentity) []byte {
	dst = appendString(dst, identity.UID)
	dst = appendUvarint(dst, identity.OwnerNodeID)
	dst = appendUvarint(dst, identity.OwnerBootID)
	dst = appendUvarint(dst, identity.SessionID)
	return dst
}

func readPresenceRouteIdentity(body []byte, offset int) (presence.RouteIdentity, int, error) {
	var identity presence.RouteIdentity
	var err error
	if identity.UID, offset, err = readString(body, offset); err != nil {
		return presence.RouteIdentity{}, offset, err
	}
	if identity.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteIdentity{}, offset, err
	}
	if identity.OwnerBootID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteIdentity{}, offset, err
	}
	if identity.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteIdentity{}, offset, err
	}
	return identity, offset, nil
}

func appendPresenceRegisterResult(dst []byte, result presence.RegisterResult) []byte {
	dst = appendString(dst, string(result.PendingToken))
	return appendPresenceActions(dst, result.Actions)
}

func readPresenceRegisterResult(body []byte, offset int) (presence.RegisterResult, int, error) {
	var result presence.RegisterResult
	var token string
	var err error
	if token, offset, err = readString(body, offset); err != nil {
		return presence.RegisterResult{}, offset, err
	}
	result.PendingToken = presence.PendingRouteToken(token)
	if result.Actions, offset, err = readPresenceActions(body, offset); err != nil {
		return presence.RegisterResult{}, offset, err
	}
	return result, offset, nil
}

func appendPresenceActions(dst []byte, actions []presence.RouteAction) []byte {
	dst = appendUvarint(dst, uint64(len(actions)))
	for _, action := range actions {
		dst = appendPresenceAction(dst, action)
	}
	return dst
}

func appendPresenceAction(dst []byte, action presence.RouteAction) []byte {
	dst = appendString(dst, action.UID)
	dst = appendUvarint(dst, action.OwnerNodeID)
	dst = appendUvarint(dst, action.OwnerBootID)
	dst = appendUvarint(dst, action.SessionID)
	dst = appendString(dst, action.Kind)
	dst = appendString(dst, action.Reason)
	dst = appendVarint(dst, action.DelayMS)
	return dst
}

func readPresenceActions(body []byte, offset int) ([]presence.RouteAction, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateCollectionLen(count, len(body)-offset, "presence actions"); err != nil {
		return nil, offset, err
	}
	actions := make([]presence.RouteAction, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var action presence.RouteAction
		if action, offset, err = readPresenceAction(body, offset); err != nil {
			return nil, offset, err
		}
		actions = append(actions, action)
	}
	return actions, offset, nil
}

func readPresenceAction(body []byte, offset int) (presence.RouteAction, int, error) {
	var action presence.RouteAction
	var err error
	if action.UID, offset, err = readString(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.OwnerNodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.OwnerBootID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.Kind, offset, err = readString(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.Reason, offset, err = readString(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.DelayMS, offset, err = readVarint(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	return action, offset, nil
}

func appendString(dst []byte, value string) []byte {
	dst = appendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readString(body []byte, offset int) (string, int, error) {
	n, next, err := readUvarint(body, offset)
	if err != nil {
		return "", offset, err
	}
	offset = next
	if n > uint64(len(body)-offset) {
		return "", offset, fmt.Errorf("internal/access/node: short string")
	}
	end := offset + int(n)
	return string(body[offset:end]), end, nil
}

func appendUvarint(dst []byte, value uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], value)
	return append(dst, buf[:n]...)
}

func readUvarint(body []byte, offset int) (uint64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("internal/access/node: short uvarint")
	}
	value, n := binary.Uvarint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("internal/access/node: invalid uvarint")
	}
	return value, offset + n, nil
}

func appendVarint(dst []byte, value int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], value)
	return append(dst, buf[:n]...)
}

func readVarint(body []byte, offset int) (int64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("internal/access/node: short varint")
	}
	value, n := binary.Varint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("internal/access/node: invalid varint")
	}
	return value, offset + n, nil
}

func readByte(body []byte, offset int, label string) (byte, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("internal/access/node: short %s", label)
	}
	return body[offset], offset + 1, nil
}

func validateCollectionLen(count uint64, remaining int, label string) error {
	if count > uint64(remaining) {
		return fmt.Errorf("internal/access/node: %s length exceeds payload", label)
	}
	if count > maxPresenceRPCCollectionLen {
		return fmt.Errorf("internal/access/node: %s length exceeds limit", label)
	}
	return nil
}

func hasMagic(body []byte, magic []byte) bool {
	if len(body) < len(magic) {
		return false
	}
	for i := range magic {
		if body[i] != magic[i] {
			return false
		}
	}
	return true
}
