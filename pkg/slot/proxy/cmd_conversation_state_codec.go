package proxy

import (
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	cmdConversationStateRPCRequestMagic  = [...]byte{'W', 'K', 'M', 'Q', 1}
	cmdConversationStateRPCResponseMagic = [...]byte{'W', 'K', 'M', 'S', 1}
)

const (
	cmdConversationStateRPCGetID byte = iota + 1
	cmdConversationStateRPCListID
	cmdConversationStateRPCUpsertID
	cmdConversationStateRPCAdvanceReadID
)

// encodeCMDConversationStateRPCRequestBinary encodes CMD state RPC requests without JSON reflection.
func encodeCMDConversationStateRPCRequestBinary(req cmdConversationStateRPCRequest) ([]byte, error) {
	opID, err := cmdConversationStateOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(cmdConversationStateRPCRequestMagic)+256)
	dst = append(dst, cmdConversationStateRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.UID)
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	dst = appendCMDConversationStates(dst, req.States)
	dst = appendCMDConversationReadPatches(dst, req.Patches)
	return dst, nil
}

func decodeCMDConversationStateRPCRequest(body []byte) (cmdConversationStateRPCRequest, error) {
	if !isCMDConversationStateRPCRequestBinary(body) {
		return cmdConversationStateRPCRequest{}, fmt.Errorf("metastore: invalid cmd conversation state request codec")
	}
	offset := len(cmdConversationStateRPCRequestMagic)
	if offset >= len(body) {
		return cmdConversationStateRPCRequest{}, fmt.Errorf("metastore: short cmd conversation state op")
	}
	op, err := cmdConversationStateOpFromID(body[offset])
	if err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	offset++

	var req cmdConversationStateRPCRequest
	req.Op = op
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return cmdConversationStateRPCRequest{}, fmt.Errorf("metastore: cmd conversation hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "cmd conversation limit"); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if req.States, offset, err = readCMDConversationStates(body, offset); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if req.Patches, offset, err = readCMDConversationReadPatches(body, offset); err != nil {
		return cmdConversationStateRPCRequest{}, err
	}
	if offset != len(body) {
		return cmdConversationStateRPCRequest{}, fmt.Errorf("metastore: trailing cmd conversation state request bytes")
	}
	return req, nil
}

func encodeCMDConversationStateRPCResponseBinary(resp cmdConversationStateRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(cmdConversationStateRPCResponseMagic)+256)
	dst = append(dst, cmdConversationStateRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendCMDConversationStatePtr(dst, resp.State)
	dst = appendCMDConversationStates(dst, resp.States)
	return dst, nil
}

func decodeCMDConversationStateRPCResponseBinary(body []byte) (cmdConversationStateRPCResponse, error) {
	if !isCMDConversationStateRPCResponseBinary(body) {
		return cmdConversationStateRPCResponse{}, fmt.Errorf("metastore: invalid cmd conversation state response codec")
	}
	offset := len(cmdConversationStateRPCResponseMagic)
	var resp cmdConversationStateRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return cmdConversationStateRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return cmdConversationStateRPCResponse{}, err
	}
	if resp.State, offset, err = readCMDConversationStatePtr(body, offset); err != nil {
		return cmdConversationStateRPCResponse{}, err
	}
	if resp.States, offset, err = readCMDConversationStates(body, offset); err != nil {
		return cmdConversationStateRPCResponse{}, err
	}
	if offset != len(body) {
		return cmdConversationStateRPCResponse{}, fmt.Errorf("metastore: trailing cmd conversation state response bytes")
	}
	return resp, nil
}

func isCMDConversationStateRPCRequestBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, cmdConversationStateRPCRequestMagic[:])
}

func isCMDConversationStateRPCResponseBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, cmdConversationStateRPCResponseMagic[:])
}

func cmdConversationStateOpID(op string) (byte, error) {
	switch op {
	case cmdConversationStateRPCGet:
		return cmdConversationStateRPCGetID, nil
	case cmdConversationStateRPCList:
		return cmdConversationStateRPCListID, nil
	case cmdConversationStateRPCUpsert:
		return cmdConversationStateRPCUpsertID, nil
	case cmdConversationStateRPCAdvanceRead:
		return cmdConversationStateRPCAdvanceReadID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown cmd conversation state rpc op %q", op)
	}
}

func cmdConversationStateOpFromID(op byte) (string, error) {
	switch op {
	case cmdConversationStateRPCGetID:
		return cmdConversationStateRPCGet, nil
	case cmdConversationStateRPCListID:
		return cmdConversationStateRPCList, nil
	case cmdConversationStateRPCUpsertID:
		return cmdConversationStateRPCUpsert, nil
	case cmdConversationStateRPCAdvanceReadID:
		return cmdConversationStateRPCAdvanceRead, nil
	default:
		return "", fmt.Errorf("metastore: unknown cmd conversation state rpc op id %d", op)
	}
}

func appendCMDConversationStatePtr(dst []byte, state *metadb.CMDConversationState) []byte {
	if state == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendCMDConversationState(dst, *state)
}

func readCMDConversationStatePtr(body []byte, offset int) (*metadb.CMDConversationState, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "cmd conversation state")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	state, next, err := readCMDConversationState(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &state, next, nil
}

func appendCMDConversationStates(dst []byte, states []metadb.CMDConversationState) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(states)))
	for _, state := range states {
		dst = appendCMDConversationState(dst, state)
	}
	return dst
}

func readCMDConversationStates(body []byte, offset int) ([]metadb.CMDConversationState, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	statesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "cmd conversation states")
	if err != nil {
		return nil, offset, err
	}
	states := make([]metadb.CMDConversationState, statesLen)
	for i := range states {
		if states[i], offset, err = readCMDConversationState(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return states, offset, nil
}

func appendCMDConversationState(dst []byte, state metadb.CMDConversationState) []byte {
	dst = runtimeMetaAppendString(dst, state.UID)
	dst = runtimeMetaAppendString(dst, state.ChannelID)
	dst = runtimeMetaAppendVarint(dst, state.ChannelType)
	dst = runtimeMetaAppendUvarint(dst, state.ReadSeq)
	dst = runtimeMetaAppendUvarint(dst, state.DeletedToSeq)
	dst = runtimeMetaAppendVarint(dst, state.ActiveAt)
	dst = runtimeMetaAppendVarint(dst, state.UpdatedAt)
	return dst
}

func readCMDConversationState(body []byte, offset int) (metadb.CMDConversationState, int, error) {
	var state metadb.CMDConversationState
	var err error
	if state.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	if state.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	if state.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	if state.ReadSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	if state.DeletedToSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	if state.ActiveAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	if state.UpdatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.CMDConversationState{}, offset, err
	}
	return state, offset, nil
}

func appendCMDConversationReadPatches(dst []byte, patches []metadb.CMDConversationReadPatch) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(patches)))
	for _, patch := range patches {
		dst = runtimeMetaAppendString(dst, patch.UID)
		dst = runtimeMetaAppendString(dst, patch.ChannelID)
		dst = runtimeMetaAppendVarint(dst, patch.ChannelType)
		dst = runtimeMetaAppendUvarint(dst, patch.ReadSeq)
		dst = runtimeMetaAppendVarint(dst, patch.UpdatedAt)
	}
	return dst
}

func readCMDConversationReadPatches(body []byte, offset int) ([]metadb.CMDConversationReadPatch, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	patchesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "cmd conversation read patches")
	if err != nil {
		return nil, offset, err
	}
	patches := make([]metadb.CMDConversationReadPatch, patchesLen)
	for i := range patches {
		if patches[i].UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if patches[i].ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if patches[i].ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if patches[i].ReadSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		if patches[i].UpdatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return patches, offset, nil
}
