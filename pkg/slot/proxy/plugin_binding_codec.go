package proxy

import (
	"encoding/base64"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

var (
	pluginBindingRPCRequestMagic   = [...]byte{'W', 'K', 'P', 'Q', 1}
	pluginBindingRPCResponseMagic  = [...]byte{'W', 'K', 'P', 'S', 1}
	pluginBindingPageCursorMagic   = [...]byte{'W', 'K', 'P', 'C', 1}
	pluginBindingPageCursorBase64  = base64.RawURLEncoding
	pluginBindingEmptyPageCursor   = pluginBindingPageCursor{}
	pluginBindingEmptyRPCCursorPtr *pluginBindingRPCCursor
)

const (
	pluginBindingRPCBindID byte = iota + 1
	pluginBindingRPCUnbindID
	pluginBindingRPCListByUIDID
	pluginBindingRPCScanByPluginNoID
	pluginBindingRPCExistsByUIDID
	pluginBindingRPCGetInHashSlotID
)

func encodePluginBindingRPCRequestBinary(req pluginBindingRPCRequest) ([]byte, error) {
	opID, err := pluginBindingOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(pluginBindingRPCRequestMagic)+len(req.UID)+len(req.PluginNo)+64)
	dst = append(dst, pluginBindingRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.UID)
	dst = runtimeMetaAppendString(dst, req.PluginNo)
	dst = appendPluginBindingRPCCursorPtr(dst, req.After)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	return dst, nil
}

func decodePluginBindingRPCRequest(body []byte) (pluginBindingRPCRequest, error) {
	if !isPluginBindingRPCRequestBinary(body) {
		return pluginBindingRPCRequest{}, fmt.Errorf("metastore: invalid plugin binding request codec")
	}
	offset := len(pluginBindingRPCRequestMagic)
	if offset >= len(body) {
		return pluginBindingRPCRequest{}, fmt.Errorf("metastore: short plugin binding op")
	}
	op, err := pluginBindingOpFromID(body[offset])
	if err != nil {
		return pluginBindingRPCRequest{}, err
	}
	offset++

	var req pluginBindingRPCRequest
	req.Op = op
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return pluginBindingRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return pluginBindingRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return pluginBindingRPCRequest{}, fmt.Errorf("metastore: plugin binding hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return pluginBindingRPCRequest{}, err
	}
	if req.PluginNo, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return pluginBindingRPCRequest{}, err
	}
	if req.After, offset, err = readPluginBindingRPCCursorPtr(body, offset); err != nil {
		return pluginBindingRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "plugin binding limit"); err != nil {
		return pluginBindingRPCRequest{}, err
	}
	if offset != len(body) {
		return pluginBindingRPCRequest{}, fmt.Errorf("metastore: trailing plugin binding request bytes")
	}
	return req, nil
}

func encodePluginBindingRPCResponse(resp pluginBindingRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(pluginBindingRPCResponseMagic)+len(resp.Bindings)*32+64)
	dst = append(dst, pluginBindingRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendPluginBindings(dst, resp.Bindings)
	dst = appendPluginBindingRPCCursor(dst, resp.Cursor)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	dst = runtimeMetaAppendBool(dst, resp.Exists)
	return dst, nil
}

func decodePluginBindingRPCResponse(body []byte) (pluginBindingRPCResponse, error) {
	if !isPluginBindingRPCResponseBinary(body) {
		return pluginBindingRPCResponse{}, fmt.Errorf("metastore: invalid plugin binding response codec")
	}
	offset := len(pluginBindingRPCResponseMagic)
	var resp pluginBindingRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return pluginBindingRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return pluginBindingRPCResponse{}, err
	}
	if resp.Bindings, offset, err = readPluginBindings(body, offset); err != nil {
		return pluginBindingRPCResponse{}, err
	}
	if resp.Cursor, offset, err = readPluginBindingRPCCursor(body, offset); err != nil {
		return pluginBindingRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return pluginBindingRPCResponse{}, err
	}
	if resp.Exists, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return pluginBindingRPCResponse{}, err
	}
	if offset != len(body) {
		return pluginBindingRPCResponse{}, fmt.Errorf("metastore: trailing plugin binding response bytes")
	}
	return resp, nil
}

func isPluginBindingRPCRequestBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, pluginBindingRPCRequestMagic[:])
}

func isPluginBindingRPCResponseBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, pluginBindingRPCResponseMagic[:])
}

func pluginBindingOpID(op string) (byte, error) {
	switch op {
	case pluginBindingRPCBind:
		return pluginBindingRPCBindID, nil
	case pluginBindingRPCUnbind:
		return pluginBindingRPCUnbindID, nil
	case pluginBindingRPCListByUID:
		return pluginBindingRPCListByUIDID, nil
	case pluginBindingRPCScanByPluginNo:
		return pluginBindingRPCScanByPluginNoID, nil
	case pluginBindingRPCExistsByUID:
		return pluginBindingRPCExistsByUIDID, nil
	case pluginBindingRPCGetInHashSlot:
		return pluginBindingRPCGetInHashSlotID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown plugin binding rpc op %q", op)
	}
}

func pluginBindingOpFromID(op byte) (string, error) {
	switch op {
	case pluginBindingRPCBindID:
		return pluginBindingRPCBind, nil
	case pluginBindingRPCUnbindID:
		return pluginBindingRPCUnbind, nil
	case pluginBindingRPCListByUIDID:
		return pluginBindingRPCListByUID, nil
	case pluginBindingRPCScanByPluginNoID:
		return pluginBindingRPCScanByPluginNo, nil
	case pluginBindingRPCExistsByUIDID:
		return pluginBindingRPCExistsByUID, nil
	case pluginBindingRPCGetInHashSlotID:
		return pluginBindingRPCGetInHashSlot, nil
	default:
		return "", fmt.Errorf("metastore: unknown plugin binding rpc op id %d", op)
	}
}

func appendPluginBindingRPCCursorPtr(dst []byte, cursor *pluginBindingRPCCursor) []byte {
	if cursor == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendPluginBindingRPCCursor(dst, *cursor)
}

func readPluginBindingRPCCursorPtr(body []byte, offset int) (*pluginBindingRPCCursor, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "plugin binding cursor")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	cursor, next, err := readPluginBindingRPCCursor(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &cursor, next, nil
}

func appendPluginBindingRPCCursor(dst []byte, cursor pluginBindingRPCCursor) []byte {
	dst = runtimeMetaAppendString(dst, cursor.PluginNo)
	dst = runtimeMetaAppendString(dst, cursor.UID)
	return dst
}

func readPluginBindingRPCCursor(body []byte, offset int) (pluginBindingRPCCursor, int, error) {
	var cursor pluginBindingRPCCursor
	var err error
	if cursor.PluginNo, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return pluginBindingRPCCursor{}, offset, err
	}
	if cursor.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return pluginBindingRPCCursor{}, offset, err
	}
	return cursor, offset, nil
}

func appendPluginBindings(dst []byte, bindings []metadb.PluginUserBinding) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(bindings)))
	for _, binding := range bindings {
		dst = appendPluginBinding(dst, binding)
	}
	return dst
}

func readPluginBindings(body []byte, offset int) ([]metadb.PluginUserBinding, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	bindingsLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "plugin bindings")
	if err != nil {
		return nil, offset, err
	}
	bindings := make([]metadb.PluginUserBinding, bindingsLen)
	for i := range bindings {
		if bindings[i], offset, err = readPluginBinding(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return bindings, offset, nil
}

func appendPluginBinding(dst []byte, binding metadb.PluginUserBinding) []byte {
	dst = runtimeMetaAppendString(dst, binding.UID)
	dst = runtimeMetaAppendString(dst, binding.PluginNo)
	dst = runtimeMetaAppendVarint(dst, binding.CreatedAtMS)
	dst = runtimeMetaAppendVarint(dst, binding.UpdatedAtMS)
	return dst
}

func readPluginBinding(body []byte, offset int) (metadb.PluginUserBinding, int, error) {
	var binding metadb.PluginUserBinding
	var err error
	if binding.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.PluginUserBinding{}, offset, err
	}
	if binding.PluginNo, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.PluginUserBinding{}, offset, err
	}
	if binding.CreatedAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.PluginUserBinding{}, offset, err
	}
	if binding.UpdatedAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.PluginUserBinding{}, offset, err
	}
	return binding, offset, nil
}

func encodePluginBindingPageCursor(cursor pluginBindingPageCursor) (string, error) {
	if cursor == pluginBindingEmptyPageCursor {
		return "", nil
	}
	dst := make([]byte, 0, len(pluginBindingPageCursorMagic)+len(cursor.Binding.PluginNo)+len(cursor.Binding.UID)+16)
	dst = append(dst, pluginBindingPageCursorMagic[:]...)
	dst = runtimeMetaAppendUvarint(dst, uint64(cursor.SlotID))
	dst = runtimeMetaAppendUvarint(dst, uint64(cursor.HashSlot))
	dst = runtimeMetaAppendString(dst, cursor.Binding.PluginNo)
	dst = runtimeMetaAppendString(dst, cursor.Binding.UID)
	return pluginBindingPageCursorBase64.EncodeToString(dst), nil
}

func decodePluginBindingPageCursor(raw string) (pluginBindingPageCursor, error) {
	if raw == "" {
		return pluginBindingPageCursor{}, nil
	}
	if len(raw) > pluginBindingPageCursorMaxEncodedLen {
		return pluginBindingPageCursor{}, metadb.ErrInvalidArgument
	}
	body, err := pluginBindingPageCursorBase64.DecodeString(raw)
	if err != nil {
		return pluginBindingPageCursor{}, err
	}
	if !runtimeMetaHasMagic(body, pluginBindingPageCursorMagic[:]) {
		return pluginBindingPageCursor{}, fmt.Errorf("metastore: invalid plugin binding page cursor")
	}
	offset := len(pluginBindingPageCursorMagic)
	slotID, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return pluginBindingPageCursor{}, err
	}
	offset = next
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return pluginBindingPageCursor{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return pluginBindingPageCursor{}, fmt.Errorf("metastore: plugin binding page cursor hash slot overflows uint16")
	}
	offset = next
	pluginNo, next, err := runtimeMetaReadString(body, offset)
	if err != nil {
		return pluginBindingPageCursor{}, err
	}
	offset = next
	uid, next, err := runtimeMetaReadString(body, offset)
	if err != nil {
		return pluginBindingPageCursor{}, err
	}
	offset = next
	if offset != len(body) {
		return pluginBindingPageCursor{}, fmt.Errorf("metastore: trailing plugin binding page cursor bytes")
	}
	if slotID == 0 || pluginNo == "" || uid == "" {
		return pluginBindingPageCursor{}, metadb.ErrInvalidArgument
	}
	return pluginBindingPageCursor{
		SlotID:   multiraft.SlotID(slotID),
		HashSlot: uint16(hashSlot),
		Binding:  metadb.PluginUserBindingCursor{PluginNo: pluginNo, UID: uid},
	}, nil
}
