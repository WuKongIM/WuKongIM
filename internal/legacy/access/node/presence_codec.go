package node

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
)

var (
	presenceRPCRequestMagic  = [...]byte{'W', 'K', 'P', 'Q', 1}
	presenceRPCResponseMagic = [...]byte{'W', 'K', 'P', 'S', 1}
)

const (
	presenceOpRegisterID byte = iota + 1
	presenceOpUnregisterID
	presenceOpHeartbeatID
	presenceOpReplayID
	presenceOpEndpointsID
	presenceOpEndpointsByUIDsID
	presenceOpApplyActionID
)

// encodePresenceRPCRequestBinary encodes presence RPC requests without JSON reflection.
func encodePresenceRPCRequestBinary(req presenceRPCRequest) ([]byte, error) {
	opID, err := presenceOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, estimatePresenceRequestBinarySize(req))
	dst = append(dst, presenceRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendUvarint(dst, req.SlotID)
	dst = appendString(dst, req.UID)
	dst = appendStrings(dst, req.UIDs)
	dst = appendPresenceRoutePtr(dst, req.Route)
	dst = appendPresenceRoutes(dst, req.Routes)
	dst = appendPresenceActionPtr(dst, req.Action)
	dst = appendPresenceLeasePtr(dst, req.Lease)
	return dst, nil
}

func decodePresenceRPCRequest(body []byte) (presenceRPCRequest, error) {
	if !isPresenceRPCRequestBinary(body) {
		return presenceRPCRequest{}, fmt.Errorf("access/node: invalid presence request codec")
	}
	offset := len(presenceRPCRequestMagic)
	if offset >= len(body) {
		return presenceRPCRequest{}, fmt.Errorf("access/node: short presence op")
	}
	op, err := presenceOpFromID(body[offset])
	if err != nil {
		return presenceRPCRequest{}, err
	}
	offset++

	var req presenceRPCRequest
	req.Op = op
	if req.SlotID, offset, err = readUvarint(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.UID, offset, err = readString(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.UIDs, offset, err = readStrings(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Route, offset, err = readPresenceRoutePtr(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Routes, offset, err = readPresenceRoutes(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Action, offset, err = readPresenceActionPtr(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if req.Lease, offset, err = readPresenceLeasePtr(body, offset); err != nil {
		return presenceRPCRequest{}, err
	}
	if offset != len(body) {
		return presenceRPCRequest{}, fmt.Errorf("access/node: trailing presence request bytes")
	}
	return req, nil
}

func encodePresenceRPCResponseBinary(resp presenceRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, estimatePresenceResponseBinarySize(resp))
	dst = append(dst, presenceRPCResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendUvarint(dst, resp.LeaderID)
	dst = appendPresenceRegisterResultPtr(dst, resp.Register)
	dst = appendPresenceHeartbeatResultPtr(dst, resp.Heartbeat)
	dst = appendPresenceRoutes(dst, resp.Endpoints)
	dst = appendPresenceEndpointMap(dst, resp.EndpointMap)
	return dst, nil
}

func decodePresenceRPCResponseBinary(body []byte) (presenceRPCResponse, error) {
	if !isPresenceRPCResponseBinary(body) {
		return presenceRPCResponse{}, fmt.Errorf("access/node: invalid presence response codec")
	}
	offset := len(presenceRPCResponseMagic)
	var resp presenceRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = readUvarint(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.Register, offset, err = readPresenceRegisterResultPtr(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.Heartbeat, offset, err = readPresenceHeartbeatResultPtr(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.Endpoints, offset, err = readPresenceRoutes(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if resp.EndpointMap, offset, err = readPresenceEndpointMap(body, offset); err != nil {
		return presenceRPCResponse{}, err
	}
	if offset != len(body) {
		return presenceRPCResponse{}, fmt.Errorf("access/node: trailing presence response bytes")
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
	case presenceOpRegister:
		return presenceOpRegisterID, nil
	case presenceOpUnregister:
		return presenceOpUnregisterID, nil
	case presenceOpHeartbeat:
		return presenceOpHeartbeatID, nil
	case presenceOpReplay:
		return presenceOpReplayID, nil
	case presenceOpEndpoints:
		return presenceOpEndpointsID, nil
	case presenceOpEndpointsByUIDs:
		return presenceOpEndpointsByUIDsID, nil
	case presenceOpApplyAction:
		return presenceOpApplyActionID, nil
	default:
		return 0, fmt.Errorf("access/node: unknown presence op %q", op)
	}
}

func presenceOpFromID(op byte) (string, error) {
	switch op {
	case presenceOpRegisterID:
		return presenceOpRegister, nil
	case presenceOpUnregisterID:
		return presenceOpUnregister, nil
	case presenceOpHeartbeatID:
		return presenceOpHeartbeat, nil
	case presenceOpReplayID:
		return presenceOpReplay, nil
	case presenceOpEndpointsID:
		return presenceOpEndpoints, nil
	case presenceOpEndpointsByUIDsID:
		return presenceOpEndpointsByUIDs, nil
	case presenceOpApplyActionID:
		return presenceOpApplyAction, nil
	default:
		return "", fmt.Errorf("access/node: unknown presence op id %d", op)
	}
}

func appendPresenceRoutePtr(dst []byte, route *presence.Route) []byte {
	if route == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendPresenceRoute(dst, *route)
}

func readPresenceRoutePtr(body []byte, offset int) (*presence.Route, int, error) {
	marker, next, err := readPresenceMarker(body, offset, "presence route")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	route, next, err := readPresenceRoute(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &route, next, nil
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
	routesLen, err := readCollectionLen(count, len(body)-offset, "presence routes")
	if err != nil {
		return nil, offset, err
	}
	routes := make([]presence.Route, routesLen)
	for i := range routes {
		if routes[i], offset, err = readPresenceRoute(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return routes, offset, nil
}

func appendPresenceRoute(dst []byte, route presence.Route) []byte {
	dst = appendString(dst, route.UID)
	dst = appendUvarint(dst, route.NodeID)
	dst = appendUvarint(dst, route.BootID)
	dst = appendUvarint(dst, route.SessionID)
	dst = appendString(dst, route.DeviceID)
	dst = append(dst, route.DeviceFlag)
	dst = append(dst, route.DeviceLevel)
	dst = appendString(dst, route.Listener)
	return dst
}

func readPresenceRoute(body []byte, offset int) (presence.Route, int, error) {
	var route presence.Route
	var err error
	if route.UID, offset, err = readString(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.BootID, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.SessionID, offset, err = readUvarint(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if route.DeviceID, offset, err = readString(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	if offset+2 > len(body) {
		return presence.Route{}, offset, fmt.Errorf("access/node: short presence device flags")
	}
	route.DeviceFlag = body[offset]
	route.DeviceLevel = body[offset+1]
	offset += 2
	if route.Listener, offset, err = readString(body, offset); err != nil {
		return presence.Route{}, offset, err
	}
	return route, offset, nil
}

func appendPresenceActionPtr(dst []byte, action *presence.RouteAction) []byte {
	if action == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendPresenceAction(dst, *action)
}

func readPresenceActionPtr(body []byte, offset int) (*presence.RouteAction, int, error) {
	marker, next, err := readPresenceMarker(body, offset, "presence action")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	action, next, err := readPresenceAction(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &action, next, nil
}

func appendPresenceActions(dst []byte, actions []presence.RouteAction) []byte {
	dst = appendUvarint(dst, uint64(len(actions)))
	for _, action := range actions {
		dst = appendPresenceAction(dst, action)
	}
	return dst
}

func readPresenceActions(body []byte, offset int) ([]presence.RouteAction, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	actionsLen, err := readCollectionLen(count, len(body)-offset, "presence actions")
	if err != nil {
		return nil, offset, err
	}
	actions := make([]presence.RouteAction, actionsLen)
	for i := range actions {
		if actions[i], offset, err = readPresenceAction(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return actions, offset, nil
}

func appendPresenceAction(dst []byte, action presence.RouteAction) []byte {
	dst = appendString(dst, action.UID)
	dst = appendUvarint(dst, action.NodeID)
	dst = appendUvarint(dst, action.BootID)
	dst = appendUvarint(dst, action.SessionID)
	dst = appendString(dst, action.Kind)
	dst = appendString(dst, action.Reason)
	dst = appendPresenceVarint(dst, action.DelayMS)
	return dst
}

func readPresenceAction(body []byte, offset int) (presence.RouteAction, int, error) {
	var action presence.RouteAction
	var err error
	if action.UID, offset, err = readString(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	if action.BootID, offset, err = readUvarint(body, offset); err != nil {
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
	if action.DelayMS, offset, err = readPresenceVarint(body, offset); err != nil {
		return presence.RouteAction{}, offset, err
	}
	return action, offset, nil
}

func appendPresenceLeasePtr(dst []byte, lease *presence.GatewayLease) []byte {
	if lease == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendPresenceLease(dst, *lease)
}

func readPresenceLeasePtr(body []byte, offset int) (*presence.GatewayLease, int, error) {
	marker, next, err := readPresenceMarker(body, offset, "presence lease")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	lease, next, err := readPresenceLease(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &lease, next, nil
}

func appendPresenceLease(dst []byte, lease presence.GatewayLease) []byte {
	dst = appendUvarint(dst, lease.SlotID)
	dst = appendUvarint(dst, lease.GatewayNodeID)
	dst = appendUvarint(dst, lease.GatewayBootID)
	dst = appendPresenceVarint(dst, int64(lease.RouteCount))
	dst = appendUvarint(dst, lease.RouteDigest)
	dst = appendPresenceVarint(dst, lease.LeaseUntilUnix)
	return dst
}

func readPresenceLease(body []byte, offset int) (presence.GatewayLease, int, error) {
	var lease presence.GatewayLease
	var err error
	if lease.SlotID, offset, err = readUvarint(body, offset); err != nil {
		return presence.GatewayLease{}, offset, err
	}
	if lease.GatewayNodeID, offset, err = readUvarint(body, offset); err != nil {
		return presence.GatewayLease{}, offset, err
	}
	if lease.GatewayBootID, offset, err = readUvarint(body, offset); err != nil {
		return presence.GatewayLease{}, offset, err
	}
	routeCount, next, err := readPresenceInt(body, offset, "presence route count")
	if err != nil {
		return presence.GatewayLease{}, offset, err
	}
	lease.RouteCount = routeCount
	offset = next
	if lease.RouteDigest, offset, err = readUvarint(body, offset); err != nil {
		return presence.GatewayLease{}, offset, err
	}
	if lease.LeaseUntilUnix, offset, err = readPresenceVarint(body, offset); err != nil {
		return presence.GatewayLease{}, offset, err
	}
	return lease, offset, nil
}

func appendPresenceRegisterResultPtr(dst []byte, result *presence.RegisterAuthoritativeResult) []byte {
	if result == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendPresenceActions(dst, result.Actions)
}

func readPresenceRegisterResultPtr(body []byte, offset int) (*presence.RegisterAuthoritativeResult, int, error) {
	marker, next, err := readPresenceMarker(body, offset, "presence register result")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	actions, next, err := readPresenceActions(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &presence.RegisterAuthoritativeResult{Actions: actions}, next, nil
}

func appendPresenceHeartbeatResultPtr(dst []byte, result *presence.HeartbeatAuthoritativeResult) []byte {
	if result == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendPresenceVarint(dst, int64(result.RouteCount))
	dst = appendUvarint(dst, result.RouteDigest)
	return appendPresenceBool(dst, result.Mismatch)
}

func readPresenceHeartbeatResultPtr(body []byte, offset int) (*presence.HeartbeatAuthoritativeResult, int, error) {
	marker, next, err := readPresenceMarker(body, offset, "presence heartbeat result")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	routeCount, next, err := readPresenceInt(body, next, "presence heartbeat route count")
	if err != nil {
		return nil, offset, err
	}
	result := &presence.HeartbeatAuthoritativeResult{RouteCount: routeCount}
	if result.RouteDigest, next, err = readUvarint(body, next); err != nil {
		return nil, offset, err
	}
	if result.Mismatch, next, err = readPresenceBool(body, next); err != nil {
		return nil, offset, err
	}
	return result, next, nil
}

func appendPresenceEndpointMap(dst []byte, endpoints map[string][]presence.Route) []byte {
	dst = appendUvarint(dst, uint64(len(endpoints)))
	keys := make([]string, 0, len(endpoints))
	for uid := range endpoints {
		keys = append(keys, uid)
	}
	sort.Strings(keys)
	for _, uid := range keys {
		dst = appendString(dst, uid)
		dst = appendPresenceRoutes(dst, endpoints[uid])
	}
	return dst
}

func readPresenceEndpointMap(body []byte, offset int) (map[string][]presence.Route, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	mapLen, err := readCollectionLen(count, len(body)-offset, "presence endpoint map")
	if err != nil {
		return nil, offset, err
	}
	endpoints := make(map[string][]presence.Route, mapLen)
	for i := 0; i < mapLen; i++ {
		uid, next, err := readString(body, offset)
		if err != nil {
			return nil, offset, err
		}
		routes, next, err := readPresenceRoutes(body, next)
		if err != nil {
			return nil, offset, err
		}
		endpoints[uid] = routes
		offset = next
	}
	return endpoints, offset, nil
}

func appendStrings(dst []byte, values []string) []byte {
	dst = appendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendString(dst, value)
	}
	return dst
}

func readStrings(body []byte, offset int) ([]string, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	valuesLen, err := readCollectionLen(count, len(body)-offset, "presence strings")
	if err != nil {
		return nil, offset, err
	}
	values := make([]string, valuesLen)
	for i := range values {
		if values[i], offset, err = readString(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return values, offset, nil
}

func appendPresenceBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readPresenceBool(body []byte, offset int) (bool, int, error) {
	if offset >= len(body) {
		return false, offset, fmt.Errorf("access/node: short presence bool")
	}
	return body[offset] != 0, offset + 1, nil
}

func readPresenceMarker(body []byte, offset int, label string) (byte, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short %s marker", label)
	}
	marker := body[offset]
	if marker > 1 {
		return 0, offset, fmt.Errorf("access/node: invalid %s marker", label)
	}
	return marker, offset + 1, nil
}

func appendPresenceVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func readPresenceVarint(body []byte, offset int) (int64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short varint")
	}
	v, n := binary.Varint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("access/node: invalid varint")
	}
	return v, offset + n, nil
}

func readPresenceInt(body []byte, offset int, label string) (int, int, error) {
	v, next, err := readPresenceVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	if int64(int(v)) != v {
		return 0, offset, fmt.Errorf("access/node: %s overflows int", label)
	}
	return int(v), next, nil
}

func estimatePresenceRequestBinarySize(req presenceRPCRequest) int {
	size := len(presenceRPCRequestMagic) + 1 + binary.MaxVarintLen64 + len(req.UID) + binary.MaxVarintLen64
	size += estimateStringsBinarySize(req.UIDs)
	size += 1 + estimatePresenceRoutePtrBinarySize(req.Route)
	size += estimatePresenceRoutesBinarySize(req.Routes)
	size += 1 + estimatePresenceActionPtrBinarySize(req.Action)
	size += 1 + binary.MaxVarintLen64*6
	return size
}

func estimatePresenceResponseBinarySize(resp presenceRPCResponse) int {
	size := len(presenceRPCResponseMagic) + len(resp.Status) + binary.MaxVarintLen64*2
	size += 1 + estimatePresenceActionsBinarySize(nil)
	if resp.Register != nil {
		size += estimatePresenceActionsBinarySize(resp.Register.Actions)
	}
	size += 1 + binary.MaxVarintLen64*3
	size += estimatePresenceRoutesBinarySize(resp.Endpoints)
	for uid, routes := range resp.EndpointMap {
		size += len(uid) + binary.MaxVarintLen64 + estimatePresenceRoutesBinarySize(routes)
	}
	return size
}

func estimatePresenceRoutePtrBinarySize(route *presence.Route) int {
	if route == nil {
		return 0
	}
	return estimatePresenceRouteBinarySize(*route)
}

func estimatePresenceRoutesBinarySize(routes []presence.Route) int {
	size := binary.MaxVarintLen64
	for _, route := range routes {
		size += estimatePresenceRouteBinarySize(route)
	}
	return size
}

func estimatePresenceRouteBinarySize(route presence.Route) int {
	return len(route.UID) + len(route.DeviceID) + len(route.Listener) + binary.MaxVarintLen64*7 + 2
}

func estimatePresenceActionPtrBinarySize(action *presence.RouteAction) int {
	if action == nil {
		return 0
	}
	return estimatePresenceActionBinarySize(*action)
}

func estimatePresenceActionsBinarySize(actions []presence.RouteAction) int {
	size := binary.MaxVarintLen64
	for _, action := range actions {
		size += estimatePresenceActionBinarySize(action)
	}
	return size
}

func estimatePresenceActionBinarySize(action presence.RouteAction) int {
	return len(action.UID) + len(action.Kind) + len(action.Reason) + binary.MaxVarintLen64*7
}

func estimateStringsBinarySize(values []string) int {
	size := binary.MaxVarintLen64
	for _, value := range values {
		size += len(value) + binary.MaxVarintLen64
	}
	return size
}
