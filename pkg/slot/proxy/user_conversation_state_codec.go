package proxy

import (
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	userConversationStateRPCRequestMagic  = [...]byte{'W', 'K', 'C', 'Q', 1}
	userConversationStateRPCResponseMagic = [...]byte{'W', 'K', 'C', 'S', 1}
)

const (
	userConversationStateRPCGetID byte = iota + 1
	userConversationStateRPCListID
	userConversationStateRPCScanPageID
	userConversationStateRPCUpsertID
	userConversationStateRPCTouchID
	userConversationStateRPCClearID
	userConversationStateRPCHideID
	userConversationStateRPCHotHintID
	userConversationStateRPCRemoveHotHintID
)

// encodeUserConversationStateRPCRequestBinary encodes user conversation RPC requests without JSON reflection.
func encodeUserConversationStateRPCRequestBinary(req userConversationStateRPCRequest) ([]byte, error) {
	opID, err := userConversationStateOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(userConversationStateRPCRequestMagic)+256)
	dst = append(dst, userConversationStateRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.UID)
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = appendConversationCursorPtr(dst, req.After)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	dst = appendUserConversationStates(dst, req.States)
	dst = appendUserConversationActivePatches(dst, req.Patches)
	dst = runtimeMetaAppendConversationKeys(dst, req.Keys)
	dst = appendUserConversationDeletes(dst, req.Deletes)
	dst = appendUserConversationActiveHints(dst, req.Hints)
	dst = appendUserConversationDeleteBarriers(dst, req.Barriers)
	return dst, nil
}

func decodeUserConversationStateRPCRequest(body []byte) (userConversationStateRPCRequest, error) {
	if !isUserConversationStateRPCRequestBinary(body) {
		return userConversationStateRPCRequest{}, fmt.Errorf("metastore: invalid user conversation state request codec")
	}
	offset := len(userConversationStateRPCRequestMagic)
	if offset >= len(body) {
		return userConversationStateRPCRequest{}, fmt.Errorf("metastore: short user conversation state op")
	}
	op, err := userConversationStateOpFromID(body[offset])
	if err != nil {
		return userConversationStateRPCRequest{}, err
	}
	offset++
	var req userConversationStateRPCRequest
	req.Op = op
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return userConversationStateRPCRequest{}, fmt.Errorf("metastore: user conversation hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.After, offset, err = readConversationCursorPtr(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "user conversation limit"); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.States, offset, err = readUserConversationStates(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.Patches, offset, err = readUserConversationActivePatches(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.Keys, offset, err = runtimeMetaReadConversationKeys(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.Deletes, offset, err = readUserConversationDeletes(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.Hints, offset, err = readUserConversationActiveHints(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if req.Barriers, offset, err = readUserConversationDeleteBarriers(body, offset); err != nil {
		return userConversationStateRPCRequest{}, err
	}
	if offset != len(body) {
		return userConversationStateRPCRequest{}, fmt.Errorf("metastore: trailing user conversation state request bytes")
	}
	return req, nil
}

func encodeUserConversationStateRPCResponseBinary(resp userConversationStateRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(userConversationStateRPCResponseMagic)+256)
	dst = append(dst, userConversationStateRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendUserConversationStatePtr(dst, resp.State)
	dst = appendUserConversationStates(dst, resp.States)
	dst = appendConversationCursor(dst, resp.Cursor)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	return dst, nil
}

func decodeUserConversationStateRPCResponseBinary(body []byte) (userConversationStateRPCResponse, error) {
	if !isUserConversationStateRPCResponseBinary(body) {
		return userConversationStateRPCResponse{}, fmt.Errorf("metastore: invalid user conversation state response codec")
	}
	offset := len(userConversationStateRPCResponseMagic)
	var resp userConversationStateRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	if resp.State, offset, err = readUserConversationStatePtr(body, offset); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	if resp.States, offset, err = readUserConversationStates(body, offset); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	if resp.Cursor, offset, err = readConversationCursor(body, offset); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return userConversationStateRPCResponse{}, err
	}
	if offset != len(body) {
		return userConversationStateRPCResponse{}, fmt.Errorf("metastore: trailing user conversation state response bytes")
	}
	return resp, nil
}

func isUserConversationStateRPCRequestBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, userConversationStateRPCRequestMagic[:])
}

func isUserConversationStateRPCResponseBinary(body []byte) bool {
	return runtimeMetaHasMagic(body, userConversationStateRPCResponseMagic[:])
}

func userConversationStateOpID(op string) (byte, error) {
	switch op {
	case userConversationStateRPCGet:
		return userConversationStateRPCGetID, nil
	case userConversationStateRPCList:
		return userConversationStateRPCListID, nil
	case userConversationStateRPCScanPage:
		return userConversationStateRPCScanPageID, nil
	case userConversationStateRPCUpsert:
		return userConversationStateRPCUpsertID, nil
	case userConversationStateRPCTouch:
		return userConversationStateRPCTouchID, nil
	case userConversationStateRPCClear:
		return userConversationStateRPCClearID, nil
	case userConversationStateRPCHide:
		return userConversationStateRPCHideID, nil
	case userConversationStateRPCHotHint:
		return userConversationStateRPCHotHintID, nil
	case userConversationStateRPCRemoveHotHint:
		return userConversationStateRPCRemoveHotHintID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown user conversation state rpc op %q", op)
	}
}

func userConversationStateOpFromID(op byte) (string, error) {
	switch op {
	case userConversationStateRPCGetID:
		return userConversationStateRPCGet, nil
	case userConversationStateRPCListID:
		return userConversationStateRPCList, nil
	case userConversationStateRPCScanPageID:
		return userConversationStateRPCScanPage, nil
	case userConversationStateRPCUpsertID:
		return userConversationStateRPCUpsert, nil
	case userConversationStateRPCTouchID:
		return userConversationStateRPCTouch, nil
	case userConversationStateRPCClearID:
		return userConversationStateRPCClear, nil
	case userConversationStateRPCHideID:
		return userConversationStateRPCHide, nil
	case userConversationStateRPCHotHintID:
		return userConversationStateRPCHotHint, nil
	case userConversationStateRPCRemoveHotHintID:
		return userConversationStateRPCRemoveHotHint, nil
	default:
		return "", fmt.Errorf("metastore: unknown user conversation state rpc op id %d", op)
	}
}

func appendConversationCursorPtr(dst []byte, cursor *metadb.ConversationCursor) []byte {
	if cursor == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendConversationCursor(dst, *cursor)
}

func readConversationCursorPtr(body []byte, offset int) (*metadb.ConversationCursor, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "conversation cursor")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	cursor, next, err := readConversationCursor(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &cursor, next, nil
}

func appendConversationCursor(dst []byte, cursor metadb.ConversationCursor) []byte {
	dst = runtimeMetaAppendString(dst, cursor.ChannelID)
	dst = runtimeMetaAppendVarint(dst, cursor.ChannelType)
	return dst
}

func readConversationCursor(body []byte, offset int) (metadb.ConversationCursor, int, error) {
	var cursor metadb.ConversationCursor
	var err error
	if cursor.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ConversationCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ConversationCursor{}, offset, err
	}
	return cursor, offset, nil
}

func appendUserConversationStatePtr(dst []byte, state *metadb.UserConversationState) []byte {
	if state == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendUserConversationState(dst, *state)
}

func readUserConversationStatePtr(body []byte, offset int) (*metadb.UserConversationState, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "user conversation state")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	state, next, err := readUserConversationState(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &state, next, nil
}

func appendUserConversationStates(dst []byte, states []metadb.UserConversationState) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(states)))
	for _, state := range states {
		dst = appendUserConversationState(dst, state)
	}
	return dst
}

func readUserConversationStates(body []byte, offset int) ([]metadb.UserConversationState, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	statesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "user conversation states")
	if err != nil {
		return nil, offset, err
	}
	states := make([]metadb.UserConversationState, statesLen)
	for i := range states {
		if states[i], offset, err = readUserConversationState(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return states, offset, nil
}

func appendUserConversationState(dst []byte, state metadb.UserConversationState) []byte {
	dst = runtimeMetaAppendString(dst, state.UID)
	dst = runtimeMetaAppendString(dst, state.ChannelID)
	dst = runtimeMetaAppendVarint(dst, state.ChannelType)
	dst = runtimeMetaAppendUvarint(dst, state.ReadSeq)
	dst = runtimeMetaAppendUvarint(dst, state.DeletedToSeq)
	dst = runtimeMetaAppendVarint(dst, state.ActiveAt)
	dst = runtimeMetaAppendVarint(dst, state.UpdatedAt)
	return dst
}

func readUserConversationState(body []byte, offset int) (metadb.UserConversationState, int, error) {
	var state metadb.UserConversationState
	var err error
	if state.UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ReadSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.DeletedToSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ActiveAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.UpdatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	return state, offset, nil
}

func appendUserConversationActivePatches(dst []byte, patches []metadb.UserConversationActivePatch) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(patches)))
	for _, patch := range patches {
		dst = runtimeMetaAppendString(dst, patch.UID)
		dst = runtimeMetaAppendString(dst, patch.ChannelID)
		dst = runtimeMetaAppendVarint(dst, patch.ChannelType)
		dst = runtimeMetaAppendVarint(dst, patch.ActiveAt)
		dst = runtimeMetaAppendUvarint(dst, patch.MessageSeq)
	}
	return dst
}

func readUserConversationActivePatches(body []byte, offset int) ([]metadb.UserConversationActivePatch, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	patchesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "user conversation active patches")
	if err != nil {
		return nil, offset, err
	}
	patches := make([]metadb.UserConversationActivePatch, patchesLen)
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
		if patches[i].ActiveAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if patches[i].MessageSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return patches, offset, nil
}

func appendUserConversationDeletes(dst []byte, deletes []metadb.UserConversationDelete) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(deletes)))
	for _, del := range deletes {
		dst = runtimeMetaAppendString(dst, del.UID)
		dst = runtimeMetaAppendString(dst, del.ChannelID)
		dst = runtimeMetaAppendVarint(dst, del.ChannelType)
		dst = runtimeMetaAppendUvarint(dst, del.DeletedToSeq)
		dst = runtimeMetaAppendVarint(dst, del.UpdatedAt)
	}
	return dst
}

func readUserConversationDeletes(body []byte, offset int) ([]metadb.UserConversationDelete, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	deletesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "user conversation deletes")
	if err != nil {
		return nil, offset, err
	}
	deletes := make([]metadb.UserConversationDelete, deletesLen)
	for i := range deletes {
		if deletes[i].UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if deletes[i].ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if deletes[i].ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if deletes[i].DeletedToSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
		if deletes[i].UpdatedAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return deletes, offset, nil
}

func appendUserConversationActiveHints(dst []byte, hints []metadb.UserConversationActiveHint) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(hints)))
	for _, hint := range hints {
		dst = runtimeMetaAppendString(dst, hint.UID)
		dst = runtimeMetaAppendString(dst, hint.ChannelID)
		dst = runtimeMetaAppendVarint(dst, hint.ChannelType)
		dst = runtimeMetaAppendVarint(dst, hint.ActiveAt)
		dst = runtimeMetaAppendUvarint(dst, hint.MessageSeq)
	}
	return dst
}

func readUserConversationActiveHints(body []byte, offset int) ([]metadb.UserConversationActiveHint, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	hintsLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "user conversation active hints")
	if err != nil {
		return nil, offset, err
	}
	hints := make([]metadb.UserConversationActiveHint, hintsLen)
	for i := range hints {
		if hints[i].UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if hints[i].ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if hints[i].ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if hints[i].ActiveAt, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if hints[i].MessageSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return hints, offset, nil
}

func appendUserConversationDeleteBarriers(dst []byte, barriers []metadb.UserConversationDeleteBarrier) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(barriers)))
	for _, barrier := range barriers {
		dst = runtimeMetaAppendString(dst, barrier.UID)
		dst = runtimeMetaAppendString(dst, barrier.ChannelID)
		dst = runtimeMetaAppendVarint(dst, barrier.ChannelType)
		dst = runtimeMetaAppendUvarint(dst, barrier.DeletedToSeq)
	}
	return dst
}

func readUserConversationDeleteBarriers(body []byte, offset int) ([]metadb.UserConversationDeleteBarrier, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	barriersLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "user conversation delete barriers")
	if err != nil {
		return nil, offset, err
	}
	barriers := make([]metadb.UserConversationDeleteBarrier, barriersLen)
	for i := range barriers {
		if barriers[i].UID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if barriers[i].ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if barriers[i].ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
		if barriers[i].DeletedToSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return barriers, offset, nil
}
