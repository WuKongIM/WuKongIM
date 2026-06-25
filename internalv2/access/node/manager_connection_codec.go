package node

import (
	"fmt"
	"sort"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

var (
	managerConnectionRequestMagic  = [...]byte{'W', 'K', 'V', 'M', 2}
	managerConnectionResponseMagic = [...]byte{'W', 'K', 'V', 'm', 2}
)

const (
	managerConnectionOpList           = "list_connections"
	managerConnectionOpGet            = "get_connection"
	managerConnectionOpRuntimeSummary = "runtime_summary"
	managerConnectionOpSetDrainMode   = "set_drain_mode"

	managerConnectionOpListID byte = iota + 1
	managerConnectionOpGetID
	managerConnectionOpRuntimeSummaryID
	managerConnectionOpSetDrainModeID

	maxManagerConnectionRPCCollectionLen = 4096
)

type managerConnectionRPCRequest struct {
	Op        string
	NodeID    uint64
	SessionID uint64
	Limit     int
	Draining  bool
}

type managerConnectionRPCResponse struct {
	Status      string
	Connections []managementusecase.Connection
	Connection  managementusecase.ConnectionDetail
	Summary     managementusecase.NodeRuntimeSummary
}

func encodeManagerConnectionRequest(req managerConnectionRPCRequest) ([]byte, error) {
	opID, err := managerConnectionOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, 64)
	dst = append(dst, managerConnectionRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendUvarint(dst, req.SessionID)
	dst = appendUvarint(dst, uint64(req.Limit))
	dst = appendBoolByte(dst, req.Draining)
	return dst, nil
}

func decodeManagerConnectionRequest(body []byte) (managerConnectionRPCRequest, error) {
	if !hasMagic(body, managerConnectionRequestMagic[:]) {
		return managerConnectionRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager connection request codec")
	}
	offset := len(managerConnectionRequestMagic)
	opID, next, err := readByte(body, offset, "manager connection op")
	if err != nil {
		return managerConnectionRPCRequest{}, err
	}
	offset = next
	op, err := managerConnectionOpFromID(opID)
	if err != nil {
		return managerConnectionRPCRequest{}, err
	}
	nodeID, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerConnectionRPCRequest{}, err
	}
	sessionID, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerConnectionRPCRequest{}, err
	}
	limit, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerConnectionRPCRequest{}, err
	}
	draining, offset, err := readBoolByte(body, offset, "manager connection draining")
	if err != nil {
		return managerConnectionRPCRequest{}, err
	}
	if offset != len(body) {
		return managerConnectionRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager connection request bytes")
	}
	return managerConnectionRPCRequest{Op: op, NodeID: nodeID, SessionID: sessionID, Limit: int(limit), Draining: draining}, nil
}

func encodeManagerConnectionResponse(resp managerConnectionRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, managerConnectionResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendManagerConnections(dst, resp.Connections)
	dst = appendManagerConnection(dst, resp.Connection)
	dst = appendNodeRuntimeSummary(dst, resp.Summary)
	return dst, nil
}

func decodeManagerConnectionResponse(body []byte) (managerConnectionRPCResponse, error) {
	if !hasMagic(body, managerConnectionResponseMagic[:]) {
		return managerConnectionRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager connection response codec")
	}
	offset := len(managerConnectionResponseMagic)
	var resp managerConnectionRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return managerConnectionRPCResponse{}, err
	}
	if resp.Connections, offset, err = readManagerConnections(body, offset); err != nil {
		return managerConnectionRPCResponse{}, err
	}
	if resp.Connection, offset, err = readManagerConnection(body, offset); err != nil {
		return managerConnectionRPCResponse{}, err
	}
	if resp.Summary, offset, err = readNodeRuntimeSummary(body, offset); err != nil {
		return managerConnectionRPCResponse{}, err
	}
	if offset != len(body) {
		return managerConnectionRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager connection response bytes")
	}
	return resp, nil
}

func appendManagerConnections(dst []byte, items []managementusecase.Connection) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendManagerConnection(dst, item)
	}
	return dst
}

func readManagerConnections(body []byte, offset int) ([]managementusecase.Connection, int, error) {
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if n > maxManagerConnectionRPCCollectionLen {
		return nil, offset, fmt.Errorf("internalv2/access/node: too many manager connections: %d", n)
	}
	items := make([]managementusecase.Connection, 0, n)
	for i := uint64(0); i < n; i++ {
		item, next, err := readManagerConnection(body, offset)
		if err != nil {
			return nil, offset, err
		}
		offset = next
		items = append(items, item)
	}
	return items, offset, nil
}

func appendManagerConnection(dst []byte, item managementusecase.Connection) []byte {
	dst = appendUvarint(dst, item.NodeID)
	dst = appendUvarint(dst, item.SessionID)
	dst = appendString(dst, item.UID)
	dst = appendString(dst, item.DeviceID)
	dst = appendString(dst, item.DeviceFlag)
	dst = appendString(dst, item.DeviceLevel)
	dst = appendUvarint(dst, item.SlotID)
	dst = appendString(dst, item.State)
	dst = appendString(dst, item.Listener)
	dst = appendVarint(dst, item.ConnectedAt.UnixNano())
	dst = appendString(dst, item.RemoteAddr)
	dst = appendString(dst, item.LocalAddr)
	return dst
}

func readManagerConnection(body []byte, offset int) (managementusecase.Connection, int, error) {
	var item managementusecase.Connection
	var value uint64
	var signed int64
	var err error
	if value, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	item.NodeID = value
	if value, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	item.SessionID = value
	if item.UID, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.DeviceID, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.DeviceFlag, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.DeviceLevel, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if value, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	item.SlotID = value
	if item.State, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.Listener, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if signed, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	if signed > 0 {
		item.ConnectedAt = time.Unix(0, signed).UTC()
	}
	if item.RemoteAddr, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.LocalAddr, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	return item, offset, nil
}

func appendNodeRuntimeSummary(dst []byte, summary managementusecase.NodeRuntimeSummary) []byte {
	dst = appendUvarint(dst, summary.NodeID)
	dst = appendUvarint(dst, summary.ControlRevision)
	dst = appendManagerConnectionInt(dst, summary.ActiveOnline)
	dst = appendManagerConnectionInt(dst, summary.ClosingOnline)
	dst = appendManagerConnectionInt(dst, summary.TotalOnline)
	dst = appendManagerConnectionInt(dst, summary.GatewaySessions)
	dst = appendManagerConnectionInt(dst, summary.PendingActivations)
	dst = appendManagerConnectionListenerMap(dst, summary.SessionsByListener)
	dst = appendBoolByte(dst, summary.AcceptingNewSessions)
	dst = appendBoolByte(dst, summary.Draining)
	dst = appendBoolByte(dst, summary.Unknown)
	return dst
}

func readNodeRuntimeSummary(body []byte, offset int) (managementusecase.NodeRuntimeSummary, int, error) {
	var summary managementusecase.NodeRuntimeSummary
	var value uint64
	var err error
	if value, offset, err = readUvarint(body, offset); err != nil {
		return summary, offset, err
	}
	summary.NodeID = value
	if value, offset, err = readUvarint(body, offset); err != nil {
		return summary, offset, err
	}
	summary.ControlRevision = value
	if summary.ActiveOnline, offset, err = readManagerConnectionInt(body, offset); err != nil {
		return summary, offset, err
	}
	if summary.ClosingOnline, offset, err = readManagerConnectionInt(body, offset); err != nil {
		return summary, offset, err
	}
	if summary.TotalOnline, offset, err = readManagerConnectionInt(body, offset); err != nil {
		return summary, offset, err
	}
	if summary.GatewaySessions, offset, err = readManagerConnectionInt(body, offset); err != nil {
		return summary, offset, err
	}
	if summary.PendingActivations, offset, err = readManagerConnectionInt(body, offset); err != nil {
		return summary, offset, err
	}
	if summary.SessionsByListener, offset, err = readManagerConnectionListenerMap(body, offset); err != nil {
		return summary, offset, err
	}
	if summary.AcceptingNewSessions, offset, err = readBoolByte(body, offset, "runtime accepting new sessions"); err != nil {
		return summary, offset, err
	}
	if summary.Draining, offset, err = readBoolByte(body, offset, "runtime draining"); err != nil {
		return summary, offset, err
	}
	if summary.Unknown, offset, err = readBoolByte(body, offset, "runtime unknown"); err != nil {
		return summary, offset, err
	}
	return summary, offset, nil
}

func appendManagerConnectionInt(dst []byte, value int) []byte {
	return appendVarint(dst, int64(value))
}

func readManagerConnectionInt(body []byte, offset int) (int, int, error) {
	value, next, err := readVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	return int(value), next, nil
}

func appendManagerConnectionListenerMap(dst []byte, values map[string]int) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	dst = appendUvarint(dst, uint64(len(keys)))
	for _, key := range keys {
		dst = appendString(dst, key)
		dst = appendManagerConnectionInt(dst, values[key])
	}
	return dst
}

func readManagerConnectionListenerMap(body []byte, offset int) (map[string]int, int, error) {
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if n > maxManagerConnectionRPCCollectionLen {
		return nil, offset, fmt.Errorf("internalv2/access/node: too many runtime listener counts: %d", n)
	}
	values := make(map[string]int, n)
	for i := uint64(0); i < n; i++ {
		var key string
		var value int
		key, offset, err = readString(body, offset)
		if err != nil {
			return nil, offset, err
		}
		value, offset, err = readManagerConnectionInt(body, offset)
		if err != nil {
			return nil, offset, err
		}
		values[key] = value
	}
	return values, offset, nil
}

func managerConnectionOpID(op string) (byte, error) {
	switch op {
	case managerConnectionOpList:
		return managerConnectionOpListID, nil
	case managerConnectionOpGet:
		return managerConnectionOpGetID, nil
	case managerConnectionOpRuntimeSummary:
		return managerConnectionOpRuntimeSummaryID, nil
	case managerConnectionOpSetDrainMode:
		return managerConnectionOpSetDrainModeID, nil
	default:
		return 0, fmt.Errorf("internalv2/access/node: unknown manager connection op %q", op)
	}
}

func managerConnectionOpFromID(op byte) (string, error) {
	switch op {
	case managerConnectionOpListID:
		return managerConnectionOpList, nil
	case managerConnectionOpGetID:
		return managerConnectionOpGet, nil
	case managerConnectionOpRuntimeSummaryID:
		return managerConnectionOpRuntimeSummary, nil
	case managerConnectionOpSetDrainModeID:
		return managerConnectionOpSetDrainMode, nil
	default:
		return "", fmt.Errorf("internalv2/access/node: unknown manager connection op id %d", op)
	}
}
