package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerLogRequestMagic  = [...]byte{'W', 'K', 'V', 'L', 1}
	managerLogResponseMagic = [...]byte{'W', 'K', 'V', 'l', 1}
)

const (
	managerLogOpController = "controller_logs"
	managerLogOpSlot       = "slot_logs"

	managerLogOpControllerID byte = iota + 1
	managerLogOpSlotID

	maxManagerLogRPCCollectionLen = 2048
)

type managerLogRPCRequest struct {
	Op     string
	NodeID uint64
	SlotID uint32
	Limit  int
	Cursor uint64
}

type managerLogRPCResponse struct {
	Status     string
	Controller managementusecase.ControllerLogEntriesResponse
	Slot       managementusecase.SlotLogEntriesResponse
}

func encodeManagerLogRequest(req managerLogRPCRequest) ([]byte, error) {
	opID, err := managerLogOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, 64)
	dst = append(dst, managerLogRequestMagic[:]...)
	dst = append(dst, opID)
	dst = appendUvarint(dst, req.NodeID)
	dst = appendUvarint(dst, uint64(req.SlotID))
	dst = appendVarint(dst, int64(req.Limit))
	dst = appendUvarint(dst, req.Cursor)
	return dst, nil
}

func decodeManagerLogRequest(body []byte) (managerLogRPCRequest, error) {
	if !hasMagic(body, managerLogRequestMagic[:]) {
		return managerLogRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid manager log request codec")
	}
	offset := len(managerLogRequestMagic)
	opID, next, err := readByte(body, offset, "manager log op")
	if err != nil {
		return managerLogRPCRequest{}, err
	}
	offset = next
	op, err := managerLogOpFromID(opID)
	if err != nil {
		return managerLogRPCRequest{}, err
	}
	nodeID, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerLogRPCRequest{}, err
	}
	slotID, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerLogRPCRequest{}, err
	}
	limit, offset, err := readVarint(body, offset)
	if err != nil {
		return managerLogRPCRequest{}, err
	}
	cursor, offset, err := readUvarint(body, offset)
	if err != nil {
		return managerLogRPCRequest{}, err
	}
	if offset != len(body) {
		return managerLogRPCRequest{}, fmt.Errorf("internalv2/access/node: trailing manager log request bytes")
	}
	return managerLogRPCRequest{Op: op, NodeID: nodeID, SlotID: uint32(slotID), Limit: int(limit), Cursor: cursor}, nil
}

func encodeManagerLogResponse(resp managerLogRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = append(dst, managerLogResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendControllerLogPage(dst, resp.Controller)
	dst = appendSlotLogPage(dst, resp.Slot)
	return dst, nil
}

func decodeManagerLogResponse(body []byte) (managerLogRPCResponse, error) {
	if !hasMagic(body, managerLogResponseMagic[:]) {
		return managerLogRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid manager log response codec")
	}
	offset := len(managerLogResponseMagic)
	var resp managerLogRPCResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return managerLogRPCResponse{}, err
	}
	if resp.Controller, offset, err = readControllerLogPage(body, offset); err != nil {
		return managerLogRPCResponse{}, err
	}
	if resp.Slot, offset, err = readSlotLogPage(body, offset); err != nil {
		return managerLogRPCResponse{}, err
	}
	if offset != len(body) {
		return managerLogRPCResponse{}, fmt.Errorf("internalv2/access/node: trailing manager log response bytes")
	}
	return resp, nil
}

func appendControllerLogPage(dst []byte, page managementusecase.ControllerLogEntriesResponse) []byte {
	dst = appendUvarint(dst, page.NodeID)
	dst = appendUvarint(dst, page.FirstIndex)
	dst = appendUvarint(dst, page.LastIndex)
	dst = appendUvarint(dst, page.CommitIndex)
	dst = appendUvarint(dst, page.AppliedIndex)
	dst = appendUvarint(dst, page.NextCursor)
	return appendLogEntries(dst, page.Items)
}

func readControllerLogPage(body []byte, offset int) (managementusecase.ControllerLogEntriesResponse, int, error) {
	var page managementusecase.ControllerLogEntriesResponse
	var err error
	if page.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.FirstIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.LastIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.CommitIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.AppliedIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.NextCursor, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.Items, offset, err = readLogEntries[managementusecase.ControllerLogEntry](body, offset, "manager controller logs"); err != nil {
		return page, offset, err
	}
	return page, offset, nil
}

func appendSlotLogPage(dst []byte, page managementusecase.SlotLogEntriesResponse) []byte {
	dst = appendUvarint(dst, page.NodeID)
	dst = appendUvarint(dst, uint64(page.SlotID))
	dst = appendUvarint(dst, page.FirstIndex)
	dst = appendUvarint(dst, page.LastIndex)
	dst = appendUvarint(dst, page.CommitIndex)
	dst = appendUvarint(dst, page.AppliedIndex)
	dst = appendUvarint(dst, page.NextCursor)
	return appendLogEntries(dst, page.Items)
}

func readSlotLogPage(body []byte, offset int) (managementusecase.SlotLogEntriesResponse, int, error) {
	var page managementusecase.SlotLogEntriesResponse
	var value uint64
	var err error
	if page.NodeID, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if value, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	page.SlotID = uint32(value)
	if page.FirstIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.LastIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.CommitIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.AppliedIndex, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.NextCursor, offset, err = readUvarint(body, offset); err != nil {
		return page, offset, err
	}
	if page.Items, offset, err = readLogEntries[managementusecase.SlotLogEntry](body, offset, "manager slot logs"); err != nil {
		return page, offset, err
	}
	return page, offset, nil
}

func appendLogEntries[T ~struct {
	Index        uint64
	Term         uint64
	Type         string
	CreatedAtMS  int64
	DataSize     int
	DecodeStatus string
	DecodedType  string
	Decoded      map[string]any
}](dst []byte, items []T) []byte {
	dst = appendUvarint(dst, uint64(len(items)))
	for _, item := range items {
		dst = appendLogEntry(dst, managementusecase.LogEntry(item))
	}
	return dst
}

func readLogEntries[T ~struct {
	Index        uint64
	Term         uint64
	Type         string
	CreatedAtMS  int64
	DataSize     int
	DecodeStatus string
	DecodedType  string
	Decoded      map[string]any
}](body []byte, offset int, label string) ([]T, int, error) {
	n, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if n > maxManagerLogRPCCollectionLen {
		return nil, offset, fmt.Errorf("internalv2/access/node: too many %s: %d", label, n)
	}
	items := make([]T, 0, n)
	for i := uint64(0); i < n; i++ {
		item, next, err := readLogEntry(body, offset)
		if err != nil {
			return nil, offset, err
		}
		offset = next
		items = append(items, T(item))
	}
	return items, offset, nil
}

func appendLogEntry(dst []byte, item managementusecase.LogEntry) []byte {
	dst = appendUvarint(dst, item.Index)
	dst = appendUvarint(dst, item.Term)
	dst = appendString(dst, item.Type)
	dst = appendVarint(dst, item.CreatedAtMS)
	dst = appendVarint(dst, int64(item.DataSize))
	dst = appendString(dst, item.DecodeStatus)
	dst = appendString(dst, item.DecodedType)
	decoded, _ := json.Marshal(item.Decoded)
	return appendBytes(dst, decoded)
}

func readLogEntry(body []byte, offset int) (managementusecase.LogEntry, int, error) {
	var item managementusecase.LogEntry
	var size int64
	var decoded []byte
	var err error
	if item.Index, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	if item.Term, offset, err = readUvarint(body, offset); err != nil {
		return item, offset, err
	}
	if item.Type, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.CreatedAtMS, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	if size, offset, err = readVarint(body, offset); err != nil {
		return item, offset, err
	}
	item.DataSize = int(size)
	if item.DecodeStatus, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if item.DecodedType, offset, err = readString(body, offset); err != nil {
		return item, offset, err
	}
	if decoded, offset, err = readBytes(body, offset); err != nil {
		return item, offset, err
	}
	if len(decoded) != 0 && string(decoded) != "null" {
		if err := json.Unmarshal(decoded, &item.Decoded); err != nil {
			return item, offset, fmt.Errorf("internalv2/access/node: decode manager log payload: %w", err)
		}
	}
	return item, offset, nil
}

func managerLogOpID(op string) (byte, error) {
	switch op {
	case managerLogOpController:
		return managerLogOpControllerID, nil
	case managerLogOpSlot:
		return managerLogOpSlotID, nil
	default:
		return 0, fmt.Errorf("internalv2/access/node: unknown manager log op %q", op)
	}
}

func managerLogOpFromID(op byte) (string, error) {
	switch op {
	case managerLogOpControllerID:
		return managerLogOpController, nil
	case managerLogOpSlotID:
		return managerLogOpSlot, nil
	default:
		return "", fmt.Errorf("internalv2/access/node: unknown manager log op id %d", op)
	}
}
