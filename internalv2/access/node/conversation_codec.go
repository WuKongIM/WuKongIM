package node

import (
	"fmt"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	conversationAuthorityRequestMagic  = [...]byte{'W', 'K', 'V', 'C', 1}
	conversationAuthorityResponseMagic = [...]byte{'W', 'K', 'V', 'c', 1}
)

const maxConversationAuthorityCollectionLen = 4096

var maxConversationAuthorityDecodeLimit = int64(int(^uint(0) >> 1))

const (
	conversationOpAdmitPatches = "admit_conversation_active_patches"
	conversationOpList         = "list_conversations"
	conversationOpDrain        = "drain_conversation_authority"

	conversationRPCStatusOK            = rpcStatusOK
	conversationRPCStatusNotLeader     = rpcStatusNotLeader
	conversationRPCStatusStaleRoute    = rpcStatusStaleRoute
	conversationRPCStatusRouteNotReady = rpcStatusRouteNotReady
	conversationRPCStatusCachePressure = "cache_pressure"
	conversationRPCStatusRejected      = rpcStatusRejected

	conversationDrainResultDrained     = "drained"
	conversationDrainResultTransferred = "transferred"
	conversationDrainResultNoDirty     = "no_dirty"
	conversationDrainResultBusy        = "busy"
)

// conversationAuthorityRequest is the deterministic binary DTO for conversation authority calls.
type conversationAuthorityRequest struct {
	// Op selects the conversation authority method to call after decoding.
	Op string
	// Target fences the request to one observed UID authority epoch.
	Target conversationusecase.RouteTarget
	// UID selects the conversation active view owner for list requests.
	UID string
	// After resumes list requests after one active-index row.
	After metadb.UserConversationActiveCursor
	// Limit bounds list response rows.
	Limit int
	// Patches carries unflushed active-row candidates for admit requests.
	Patches []conversationusecase.ActivePatch
}

// conversationAuthorityResponse is the deterministic binary DTO returned by authority calls.
type conversationAuthorityResponse struct {
	// Status is one of the stable conversation RPC status strings.
	Status string
	// Page carries list output when Status is ok.
	Page conversationusecase.ActiveViewPage
	// DrainResult reports the drain outcome when Status is ok.
	DrainResult string
}

func encodeConversationAuthorityRequest(req conversationAuthorityRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendConversationRouteTarget(dst, req.Target)
	dst = appendString(dst, req.UID)
	dst = appendConversationActiveCursor(dst, req.After)
	dst = appendVarint(dst, int64(req.Limit))
	dst = appendConversationActivePatches(dst, req.Patches)
	return dst, nil
}

func decodeConversationAuthorityRequest(body []byte) (conversationAuthorityRequest, error) {
	if !hasMagic(body, conversationAuthorityRequestMagic[:]) {
		return conversationAuthorityRequest{}, fmt.Errorf("internalv2/access/node: invalid conversation authority request codec")
	}
	offset := len(conversationAuthorityRequestMagic)
	var req conversationAuthorityRequest
	var limit int64
	var err error
	if req.Op, offset, err = readString(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if err := validateConversationAuthorityOp(req.Op); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if req.Target, offset, err = readConversationRouteTarget(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if req.UID, offset, err = readString(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if req.After, offset, err = readConversationActiveCursor(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if limit, offset, err = readVarint(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if limit < 0 {
		return conversationAuthorityRequest{}, fmt.Errorf("internalv2/access/node: conversation limit is negative")
	}
	if limit > maxConversationAuthorityDecodeLimit {
		return conversationAuthorityRequest{}, fmt.Errorf("internalv2/access/node: conversation limit overflows int")
	}
	req.Limit = int(limit)
	if req.Patches, offset, err = readConversationActivePatches(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if offset != len(body) {
		return conversationAuthorityRequest{}, fmt.Errorf("internalv2/access/node: trailing conversation authority request bytes")
	}
	return req, nil
}

func encodeConversationAuthorityResponse(resp conversationAuthorityResponse) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityResponseMagic[:]...)
	dst = appendString(dst, resp.Status)
	dst = appendConversationActiveViewPage(dst, resp.Page)
	dst = appendString(dst, resp.DrainResult)
	return dst, nil
}

func decodeConversationAuthorityResponse(body []byte) (conversationAuthorityResponse, error) {
	if !hasMagic(body, conversationAuthorityResponseMagic[:]) {
		return conversationAuthorityResponse{}, fmt.Errorf("internalv2/access/node: invalid conversation authority response codec")
	}
	offset := len(conversationAuthorityResponseMagic)
	var resp conversationAuthorityResponse
	var err error
	if resp.Status, offset, err = readString(body, offset); err != nil {
		return conversationAuthorityResponse{}, err
	}
	if resp.Page, offset, err = readConversationActiveViewPage(body, offset); err != nil {
		return conversationAuthorityResponse{}, err
	}
	if resp.DrainResult, offset, err = readString(body, offset); err != nil {
		return conversationAuthorityResponse{}, err
	}
	if offset != len(body) {
		return conversationAuthorityResponse{}, fmt.Errorf("internalv2/access/node: trailing conversation authority response bytes")
	}
	return resp, nil
}

func validateConversationAuthorityOp(op string) error {
	switch op {
	case conversationOpAdmitPatches, conversationOpList, conversationOpDrain:
		return nil
	default:
		return fmt.Errorf("internalv2/access/node: unknown conversation authority op %q", op)
	}
}

func appendConversationRouteTarget(dst []byte, target conversationusecase.RouteTarget) []byte {
	dst = appendUvarint(dst, uint64(target.HashSlot))
	dst = appendUvarint(dst, uint64(target.SlotID))
	dst = appendUvarint(dst, target.LeaderNodeID)
	dst = appendUvarint(dst, target.RouteRevision)
	return appendUvarint(dst, target.AuthorityEpoch)
}

func readConversationRouteTarget(body []byte, offset int) (conversationusecase.RouteTarget, int, error) {
	var target conversationusecase.RouteTarget
	var v uint64
	var err error
	if v, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if v > uint64(^uint16(0)) {
		return conversationusecase.RouteTarget{}, offset, fmt.Errorf("internalv2/access/node: conversation hash slot overflows uint16")
	}
	target.HashSlot = uint16(v)
	if v, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if v > uint64(^uint32(0)) {
		return conversationusecase.RouteTarget{}, offset, fmt.Errorf("internalv2/access/node: conversation slot id overflows uint32")
	}
	target.SlotID = uint32(v)
	if target.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if target.RouteRevision, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if target.AuthorityEpoch, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	return target, offset, nil
}

func appendConversationActiveCursor(dst []byte, cursor metadb.UserConversationActiveCursor) []byte {
	dst = appendVarint(dst, cursor.ActiveAt)
	dst = appendString(dst, cursor.ChannelID)
	return appendVarint(dst, cursor.ChannelType)
}

func readConversationActiveCursor(body []byte, offset int) (metadb.UserConversationActiveCursor, int, error) {
	var cursor metadb.UserConversationActiveCursor
	var err error
	if cursor.ActiveAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.UserConversationActiveCursor{}, offset, err
	}
	if cursor.ChannelID, offset, err = readString(body, offset); err != nil {
		return metadb.UserConversationActiveCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return metadb.UserConversationActiveCursor{}, offset, err
	}
	return cursor, offset, nil
}

func appendConversationActivePatch(dst []byte, patch conversationusecase.ActivePatch) []byte {
	dst = appendString(dst, patch.UID)
	dst = appendString(dst, patch.ChannelID)
	dst = appendVarint(dst, patch.ChannelType)
	dst = appendUvarint(dst, patch.ReadSeq)
	dst = appendUvarint(dst, patch.DeletedToSeq)
	dst = appendVarint(dst, patch.ActiveAt)
	dst = appendVarint(dst, patch.UpdatedAt)
	dst = appendConversationBool(dst, patch.SparseActive)
	return appendUvarint(dst, patch.MessageSeq)
}

func readConversationActivePatch(body []byte, offset int) (conversationusecase.ActivePatch, int, error) {
	var patch conversationusecase.ActivePatch
	var err error
	if patch.UID, offset, err = readString(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.ChannelID, offset, err = readString(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.ReadSeq, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.DeletedToSeq, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.ActiveAt, offset, err = readVarint(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.UpdatedAt, offset, err = readVarint(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.SparseActive, offset, err = readConversationBool(body, offset, "conversation sparse active"); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	if patch.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.ActivePatch{}, offset, err
	}
	return patch, offset, nil
}

func appendConversationActivePatches(dst []byte, patches []conversationusecase.ActivePatch) []byte {
	dst = appendUvarint(dst, uint64(len(patches)))
	for _, patch := range patches {
		dst = appendConversationActivePatch(dst, patch)
	}
	return dst
}

func readConversationActivePatches(body []byte, offset int) ([]conversationusecase.ActivePatch, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateConversationCollectionLen(count, len(body)-offset, "conversation active patches"); err != nil {
		return nil, offset, err
	}
	patches := make([]conversationusecase.ActivePatch, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var patch conversationusecase.ActivePatch
		if patch, offset, err = readConversationActivePatch(body, offset); err != nil {
			return nil, offset, err
		}
		patches = append(patches, patch)
	}
	return patches, offset, nil
}

func appendConversationState(dst []byte, state metadb.UserConversationState) []byte {
	dst = appendString(dst, state.UID)
	dst = appendString(dst, state.ChannelID)
	dst = appendVarint(dst, state.ChannelType)
	dst = appendUvarint(dst, state.ReadSeq)
	dst = appendUvarint(dst, state.DeletedToSeq)
	dst = appendVarint(dst, state.ActiveAt)
	dst = appendVarint(dst, state.UpdatedAt)
	return appendConversationBool(dst, state.SparseActive)
}

func readConversationState(body []byte, offset int) (metadb.UserConversationState, int, error) {
	var state metadb.UserConversationState
	var err error
	if state.UID, offset, err = readString(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ChannelID, offset, err = readString(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ReadSeq, offset, err = readUvarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.DeletedToSeq, offset, err = readUvarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.ActiveAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.UpdatedAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	if state.SparseActive, offset, err = readConversationBool(body, offset, "conversation sparse active"); err != nil {
		return metadb.UserConversationState{}, offset, err
	}
	return state, offset, nil
}

func appendConversationStates(dst []byte, states []metadb.UserConversationState) []byte {
	dst = appendUvarint(dst, uint64(len(states)))
	for _, state := range states {
		dst = appendConversationState(dst, state)
	}
	return dst
}

func readConversationStates(body []byte, offset int) ([]metadb.UserConversationState, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateConversationCollectionLen(count, len(body)-offset, "conversation states"); err != nil {
		return nil, offset, err
	}
	states := make([]metadb.UserConversationState, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var state metadb.UserConversationState
		if state, offset, err = readConversationState(body, offset); err != nil {
			return nil, offset, err
		}
		states = append(states, state)
	}
	return states, offset, nil
}

func appendConversationActiveViewPage(dst []byte, page conversationusecase.ActiveViewPage) []byte {
	dst = appendConversationStates(dst, page.Rows)
	dst = appendConversationActiveCursor(dst, page.Cursor)
	return appendConversationBool(dst, page.Done)
}

func readConversationActiveViewPage(body []byte, offset int) (conversationusecase.ActiveViewPage, int, error) {
	var page conversationusecase.ActiveViewPage
	var err error
	if page.Rows, offset, err = readConversationStates(body, offset); err != nil {
		return conversationusecase.ActiveViewPage{}, offset, err
	}
	if page.Cursor, offset, err = readConversationActiveCursor(body, offset); err != nil {
		return conversationusecase.ActiveViewPage{}, offset, err
	}
	if page.Done, offset, err = readConversationBool(body, offset, "conversation page done"); err != nil {
		return conversationusecase.ActiveViewPage{}, offset, err
	}
	return page, offset, nil
}

func appendConversationBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readConversationBool(body []byte, offset int, label string) (bool, int, error) {
	v, next, err := readByte(body, offset, label)
	if err != nil {
		return false, offset, err
	}
	switch v {
	case 0:
		return false, next, nil
	case 1:
		return true, next, nil
	default:
		return false, offset, fmt.Errorf("internalv2/access/node: invalid %s flag", label)
	}
}

func validateConversationCollectionLen(count uint64, remaining int, label string) error {
	if count > uint64(remaining) {
		return fmt.Errorf("internalv2/access/node: %s length exceeds payload", label)
	}
	if count > maxConversationAuthorityCollectionLen {
		return fmt.Errorf("internalv2/access/node: %s length exceeds limit", label)
	}
	return nil
}
