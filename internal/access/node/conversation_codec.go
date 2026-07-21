package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	conversationAuthorityRequestMagic       = [...]byte{'W', 'K', 'V', 'C', 1}
	conversationAuthorityResponseMagic      = [...]byte{'W', 'K', 'V', 'c', 1}
	conversationAuthorityBatchRequestMagic  = [...]byte{'W', 'K', 'V', 'C', 2}
	conversationAuthorityBatchResponseMagic = [...]byte{'W', 'K', 'V', 'c', 2}
)

const maxConversationAuthorityCollectionLen = 4096

const conversationAuthorityV1InvalidRequestCodecMessage = "internal/access/node: invalid conversation authority request codec"

var maxConversationAuthorityDecodeLimit = int64(int(^uint(0) >> 1))

const (
	conversationOpAdmitPatches      = "admit_conversation_active_patches"
	conversationOpAdmitActiveBatch  = "admit_conversation_active_batch"
	conversationOpHideConversations = "hide_conversations_for_target"
	conversationOpList              = "list_conversations"
	conversationOpDrain             = "drain_conversation_authority"

	conversationRPCStatusOK            = rpcStatusOK
	conversationRPCStatusNotLeader     = rpcStatusNotLeader
	conversationRPCStatusStaleRoute    = rpcStatusStaleRoute
	conversationRPCStatusRouteNotReady = rpcStatusRouteNotReady
	conversationRPCStatusCachePressure = "cache_pressure"
	conversationRPCStatusCanceled      = rpcStatusContextCanceled
	conversationRPCStatusDeadline      = rpcStatusContextDeadlineExceeded
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
	// Kind selects the logical conversation projection view for list requests.
	Kind metadb.ConversationKind
	// After resumes list requests after one active-index row.
	After metadb.ConversationActiveCursor
	// Limit bounds list response rows.
	Limit int
	// Patches carries unflushed active-row candidates for admit requests.
	Patches []conversationusecase.ActivePatch
	// ActiveBatch carries channelappend active admission input for active-batch requests.
	ActiveBatch conversationactive.ActiveBatch
	// Deletes carries exact conversation hide mutations for hide requests.
	Deletes []metadb.ConversationDelete
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

// conversationActiveBatchWireResult is one input-aligned bulk admission status.
type conversationActiveBatchWireResult struct {
	// Status is one of the stable conversation RPC status strings.
	Status string
}

func encodeConversationAuthorityRequest(req conversationAuthorityRequest) ([]byte, error) {
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityRequestMagic[:]...)
	dst = appendString(dst, req.Op)
	dst = appendConversationRouteTarget(dst, req.Target)
	dst = appendString(dst, req.UID)
	dst = appendConversationKind(dst, req.Kind)
	dst = appendConversationActiveCursor(dst, req.After)
	dst = appendVarint(dst, int64(req.Limit))
	dst = appendConversationActivePatches(dst, req.Patches)
	if req.Op == conversationOpHideConversations {
		if len(req.Deletes) > maxConversationAuthorityCollectionLen {
			return nil, fmt.Errorf("internal/access/node: conversation deletes length exceeds limit")
		}
		dst = appendConversationDeletes(dst, req.Deletes)
	}
	if req.Op == conversationOpAdmitActiveBatch {
		dst = appendConversationActiveBatch(dst, req.ActiveBatch)
	}
	return dst, nil
}

func decodeConversationAuthorityRequest(body []byte) (conversationAuthorityRequest, error) {
	if !hasMagic(body, conversationAuthorityRequestMagic[:]) {
		return conversationAuthorityRequest{}, fmt.Errorf("%s", conversationAuthorityV1InvalidRequestCodecMessage)
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
	if req.Kind, offset, err = readConversationKind(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if req.After, offset, err = readConversationActiveCursor(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if limit, offset, err = readVarint(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if limit < 0 {
		return conversationAuthorityRequest{}, fmt.Errorf("internal/access/node: conversation limit is negative")
	}
	if limit > maxConversationAuthorityDecodeLimit {
		return conversationAuthorityRequest{}, fmt.Errorf("internal/access/node: conversation limit overflows int")
	}
	req.Limit = int(limit)
	if req.Patches, offset, err = readConversationActivePatches(body, offset); err != nil {
		return conversationAuthorityRequest{}, err
	}
	if req.Op == conversationOpHideConversations {
		if req.Deletes, offset, err = readConversationDeletes(body, offset); err != nil {
			return conversationAuthorityRequest{}, err
		}
	}
	if req.Op == conversationOpAdmitActiveBatch {
		if req.ActiveBatch, offset, err = readConversationActiveBatch(body, offset); err != nil {
			return conversationAuthorityRequest{}, err
		}
	}
	if offset != len(body) {
		return conversationAuthorityRequest{}, fmt.Errorf("internal/access/node: trailing conversation authority request bytes")
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
		return conversationAuthorityResponse{}, fmt.Errorf("internal/access/node: invalid conversation authority response codec")
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
		return conversationAuthorityResponse{}, fmt.Errorf("internal/access/node: trailing conversation authority response bytes")
	}
	return resp, nil
}

func encodeConversationActiveBatchGroups(groups []ConversationActiveBatchGroup) ([]byte, error) {
	if len(groups) > maxConversationAuthorityCollectionLen {
		return nil, fmt.Errorf("internal/access/node: conversation active batch groups length exceeds limit")
	}
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityBatchRequestMagic[:]...)
	dst = appendUvarint(dst, uint64(len(groups)))
	totalRows := 0
	for i, group := range groups {
		if group.Target.LeaderNodeID == 0 {
			return nil, fmt.Errorf("internal/access/node: conversation active batch group %d leader is zero", i)
		}
		if err := validateConversationKind(group.Batch.Kind); err != nil {
			return nil, fmt.Errorf("internal/access/node: conversation active batch group %d: %w", i, err)
		}
		rows := conversationActiveBatchRowCount(group.Batch)
		if rows > maxConversationAuthorityCollectionLen || totalRows > maxConversationAuthorityCollectionLen-rows {
			return nil, fmt.Errorf("internal/access/node: aggregate conversation active batch rows length exceeds limit")
		}
		totalRows += rows
		dst = appendConversationRouteTarget(dst, group.Target)
		dst = appendConversationActiveBatch(dst, group.Batch)
	}
	return dst, nil
}

func decodeConversationActiveBatchGroups(body []byte) ([]ConversationActiveBatchGroup, error) {
	if !hasMagic(body, conversationAuthorityBatchRequestMagic[:]) {
		return nil, fmt.Errorf("internal/access/node: invalid conversation active batch request codec")
	}
	offset := len(conversationAuthorityBatchRequestMagic)
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, err
	}
	offset = next
	if err := validateConversationCollectionLen(count, len(body)-offset, "conversation active batch groups"); err != nil {
		return nil, err
	}
	groups := make([]ConversationActiveBatchGroup, 0, int(count))
	totalRows := 0
	for i := uint64(0); i < count; i++ {
		var group ConversationActiveBatchGroup
		if group.Target, offset, err = readConversationRouteTarget(body, offset); err != nil {
			return nil, err
		}
		if group.Target.LeaderNodeID == 0 {
			return nil, fmt.Errorf("internal/access/node: conversation active batch group %d leader is zero", i)
		}
		if group.Batch, offset, err = readConversationActiveBatch(body, offset); err != nil {
			return nil, err
		}
		rows := conversationActiveBatchRowCount(group.Batch)
		if rows > maxConversationAuthorityCollectionLen || totalRows > maxConversationAuthorityCollectionLen-rows {
			return nil, fmt.Errorf("internal/access/node: aggregate conversation active batch rows length exceeds limit")
		}
		totalRows += rows
		groups = append(groups, group)
	}
	if offset != len(body) {
		return nil, fmt.Errorf("internal/access/node: trailing conversation active batch request bytes")
	}
	return groups, nil
}

func encodeConversationActiveBatchResults(results []conversationActiveBatchWireResult) ([]byte, error) {
	if len(results) > maxConversationAuthorityCollectionLen {
		return nil, fmt.Errorf("internal/access/node: conversation active batch results length exceeds limit")
	}
	dst := make([]byte, 0, 64)
	dst = append(dst, conversationAuthorityBatchResponseMagic[:]...)
	dst = appendUvarint(dst, uint64(len(results)))
	for i, result := range results {
		if err := validateConversationRPCStatus(result.Status); err != nil {
			return nil, fmt.Errorf("internal/access/node: conversation active batch result %d: %w", i, err)
		}
		dst = appendString(dst, result.Status)
	}
	return dst, nil
}

func decodeConversationActiveBatchResults(body []byte) ([]conversationActiveBatchWireResult, error) {
	if !hasMagic(body, conversationAuthorityBatchResponseMagic[:]) {
		return nil, fmt.Errorf("internal/access/node: invalid conversation active batch response codec")
	}
	offset := len(conversationAuthorityBatchResponseMagic)
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, err
	}
	offset = next
	if err := validateConversationCollectionLen(count, len(body)-offset, "conversation active batch results"); err != nil {
		return nil, err
	}
	results := make([]conversationActiveBatchWireResult, 0, int(count))
	for i := uint64(0); i < count; i++ {
		status, next, err := readString(body, offset)
		if err != nil {
			return nil, err
		}
		offset = next
		if err := validateConversationRPCStatus(status); err != nil {
			return nil, fmt.Errorf("internal/access/node: conversation active batch result %d: %w", i, err)
		}
		results = append(results, conversationActiveBatchWireResult{Status: status})
	}
	if offset != len(body) {
		return nil, fmt.Errorf("internal/access/node: trailing conversation active batch response bytes")
	}
	return results, nil
}

func conversationActiveBatchRowCount(batch conversationactive.ActiveBatch) int {
	rows := len(batch.Recipients)
	if batch.SenderUID != "" {
		rows++
	}
	return rows
}

func validateConversationRPCStatus(status string) error {
	switch status {
	case conversationRPCStatusOK,
		conversationRPCStatusNotLeader,
		conversationRPCStatusStaleRoute,
		conversationRPCStatusRouteNotReady,
		conversationRPCStatusCachePressure,
		conversationRPCStatusCanceled,
		conversationRPCStatusDeadline,
		conversationRPCStatusRejected:
		return nil
	default:
		return fmt.Errorf("unknown conversation authority rpc status %q", status)
	}
}

func validateConversationAuthorityOp(op string) error {
	switch op {
	case conversationOpAdmitPatches, conversationOpAdmitActiveBatch, conversationOpHideConversations, conversationOpList, conversationOpDrain:
		return nil
	default:
		return fmt.Errorf("internal/access/node: unknown conversation authority op %q", op)
	}
}

func appendConversationDelete(dst []byte, req metadb.ConversationDelete) []byte {
	dst = appendString(dst, req.UID)
	dst = appendConversationKind(dst, req.Kind)
	dst = appendString(dst, req.ChannelID)
	dst = appendVarint(dst, req.ChannelType)
	dst = appendUvarint(dst, req.DeletedToSeq)
	return appendVarint(dst, req.UpdatedAt)
}

func readConversationDelete(body []byte, offset int) (metadb.ConversationDelete, int, error) {
	var req metadb.ConversationDelete
	var err error
	if req.UID, offset, err = readString(body, offset); err != nil {
		return metadb.ConversationDelete{}, offset, err
	}
	if req.Kind, offset, err = readConversationKind(body, offset); err != nil {
		return metadb.ConversationDelete{}, offset, err
	}
	if req.ChannelID, offset, err = readString(body, offset); err != nil {
		return metadb.ConversationDelete{}, offset, err
	}
	if req.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationDelete{}, offset, err
	}
	if req.DeletedToSeq, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ConversationDelete{}, offset, err
	}
	if req.UpdatedAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationDelete{}, offset, err
	}
	return req, offset, nil
}

func appendConversationDeletes(dst []byte, reqs []metadb.ConversationDelete) []byte {
	dst = appendUvarint(dst, uint64(len(reqs)))
	for _, req := range reqs {
		dst = appendConversationDelete(dst, req)
	}
	return dst
}

func readConversationDeletes(body []byte, offset int) ([]metadb.ConversationDelete, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateConversationCollectionLen(count, len(body)-offset, "conversation deletes"); err != nil {
		return nil, offset, err
	}
	reqs := make([]metadb.ConversationDelete, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var req metadb.ConversationDelete
		if req, offset, err = readConversationDelete(body, offset); err != nil {
			return nil, offset, err
		}
		reqs = append(reqs, req)
	}
	return reqs, offset, nil
}

func appendConversationRouteTarget(dst []byte, target conversationusecase.RouteTarget) []byte {
	dst = appendUvarint(dst, uint64(target.HashSlot))
	dst = appendUvarint(dst, uint64(target.SlotID))
	dst = appendUvarint(dst, target.LeaderNodeID)
	dst = appendUvarint(dst, target.LeaderTerm)
	dst = appendUvarint(dst, target.ConfigEpoch)
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
		return conversationusecase.RouteTarget{}, offset, fmt.Errorf("internal/access/node: conversation hash slot overflows uint16")
	}
	target.HashSlot = uint16(v)
	if v, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if v > uint64(^uint32(0)) {
		return conversationusecase.RouteTarget{}, offset, fmt.Errorf("internal/access/node: conversation slot id overflows uint32")
	}
	target.SlotID = uint32(v)
	if target.LeaderNodeID, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if target.LeaderTerm, offset, err = readUvarint(body, offset); err != nil {
		return conversationusecase.RouteTarget{}, offset, err
	}
	if target.ConfigEpoch, offset, err = readUvarint(body, offset); err != nil {
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

func appendConversationKind(dst []byte, kind metadb.ConversationKind) []byte {
	return appendUvarint(dst, uint64(kind))
}

func validateConversationKind(kind metadb.ConversationKind) error {
	switch kind {
	case metadb.ConversationKindNormal, metadb.ConversationKindCMD:
		return nil
	default:
		return fmt.Errorf("invalid conversation kind %d", kind)
	}
}

func readConversationKind(body []byte, offset int) (metadb.ConversationKind, int, error) {
	v, next, err := readUvarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	kind := metadb.ConversationKind(v)
	if err := validateConversationKind(kind); err != nil {
		return 0, offset, fmt.Errorf("internal/access/node: %w", err)
	}
	return kind, next, nil
}

func appendConversationActiveCursor(dst []byte, cursor metadb.ConversationActiveCursor) []byte {
	dst = appendVarint(dst, cursor.ActiveAt)
	dst = appendString(dst, cursor.ChannelID)
	return appendVarint(dst, cursor.ChannelType)
}

func readConversationActiveCursor(body []byte, offset int) (metadb.ConversationActiveCursor, int, error) {
	var cursor metadb.ConversationActiveCursor
	var err error
	if cursor.ActiveAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationActiveCursor{}, offset, err
	}
	if cursor.ChannelID, offset, err = readString(body, offset); err != nil {
		return metadb.ConversationActiveCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationActiveCursor{}, offset, err
	}
	return cursor, offset, nil
}

func appendConversationActivePatch(dst []byte, patch conversationusecase.ActivePatch) []byte {
	dst = appendString(dst, patch.UID)
	dst = appendConversationKind(dst, patch.Kind)
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
	if patch.Kind, offset, err = readConversationKind(body, offset); err != nil {
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

func appendConversationActiveBatch(dst []byte, batch conversationactive.ActiveBatch) []byte {
	dst = appendConversationKind(dst, batch.Kind)
	dst = appendString(dst, batch.SenderUID)
	dst = appendString(dst, batch.ChannelID)
	dst = appendUvarint(dst, uint64(batch.ChannelType))
	dst = appendUvarint(dst, batch.MessageSeq)
	dst = appendVarint(dst, batch.ActiveAtMS)
	return appendConversationActiveEntries(dst, batch.Recipients)
}

func readConversationActiveBatch(body []byte, offset int) (conversationactive.ActiveBatch, int, error) {
	var batch conversationactive.ActiveBatch
	var channelType uint64
	var err error
	if batch.Kind, offset, err = readConversationKind(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	if batch.SenderUID, offset, err = readString(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	if batch.ChannelID, offset, err = readString(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	if channelType, offset, err = readUvarint(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	if channelType > uint64(^uint8(0)) {
		return conversationactive.ActiveBatch{}, offset, fmt.Errorf("internal/access/node: conversation active batch channel type overflows uint8")
	}
	batch.ChannelType = uint8(channelType)
	if batch.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	if batch.ActiveAtMS, offset, err = readVarint(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	if batch.Recipients, offset, err = readConversationActiveEntries(body, offset); err != nil {
		return conversationactive.ActiveBatch{}, offset, err
	}
	return batch, offset, nil
}

func appendConversationActiveEntry(dst []byte, entry conversationactive.ActiveEntry) []byte {
	dst = appendString(dst, entry.UID)
	return appendConversationBool(dst, entry.IsSender)
}

func readConversationActiveEntry(body []byte, offset int) (conversationactive.ActiveEntry, int, error) {
	var entry conversationactive.ActiveEntry
	var err error
	if entry.UID, offset, err = readString(body, offset); err != nil {
		return conversationactive.ActiveEntry{}, offset, err
	}
	if entry.IsSender, offset, err = readConversationBool(body, offset, "conversation active entry sender"); err != nil {
		return conversationactive.ActiveEntry{}, offset, err
	}
	return entry, offset, nil
}

func appendConversationActiveEntries(dst []byte, entries []conversationactive.ActiveEntry) []byte {
	dst = appendUvarint(dst, uint64(len(entries)))
	for _, entry := range entries {
		dst = appendConversationActiveEntry(dst, entry)
	}
	return dst
}

func readConversationActiveEntries(body []byte, offset int) ([]conversationactive.ActiveEntry, int, error) {
	count, next, err := readUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	if count == 0 {
		return nil, offset, nil
	}
	if err := validateConversationCollectionLen(count, len(body)-offset, "conversation active entries"); err != nil {
		return nil, offset, err
	}
	entries := make([]conversationactive.ActiveEntry, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var entry conversationactive.ActiveEntry
		if entry, offset, err = readConversationActiveEntry(body, offset); err != nil {
			return nil, offset, err
		}
		entries = append(entries, entry)
	}
	return entries, offset, nil
}

func appendConversationState(dst []byte, state metadb.ConversationState) []byte {
	dst = appendString(dst, state.UID)
	dst = appendConversationKind(dst, state.Kind)
	dst = appendString(dst, state.ChannelID)
	dst = appendVarint(dst, state.ChannelType)
	dst = appendUvarint(dst, state.ReadSeq)
	dst = appendUvarint(dst, state.DeletedToSeq)
	dst = appendVarint(dst, state.ActiveAt)
	dst = appendVarint(dst, state.UpdatedAt)
	return appendConversationBool(dst, state.SparseActive)
}

func readConversationState(body []byte, offset int) (metadb.ConversationState, int, error) {
	var state metadb.ConversationState
	var err error
	if state.UID, offset, err = readString(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.Kind, offset, err = readConversationKind(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.ChannelID, offset, err = readString(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.ChannelType, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.ReadSeq, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.DeletedToSeq, offset, err = readUvarint(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.ActiveAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.UpdatedAt, offset, err = readVarint(body, offset); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	if state.SparseActive, offset, err = readConversationBool(body, offset, "conversation sparse active"); err != nil {
		return metadb.ConversationState{}, offset, err
	}
	return state, offset, nil
}

func appendConversationStates(dst []byte, states []metadb.ConversationState) []byte {
	dst = appendUvarint(dst, uint64(len(states)))
	for _, state := range states {
		dst = appendConversationState(dst, state)
	}
	return dst
}

func readConversationStates(body []byte, offset int) ([]metadb.ConversationState, int, error) {
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
	states := make([]metadb.ConversationState, 0, int(count))
	for i := uint64(0); i < count; i++ {
		var state metadb.ConversationState
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
		return false, offset, fmt.Errorf("internal/access/node: invalid %s flag", label)
	}
}

func validateConversationCollectionLen(count uint64, remaining int, label string) error {
	if count > uint64(remaining) {
		return fmt.Errorf("internal/access/node: %s length exceeds payload", label)
	}
	if count > maxConversationAuthorityCollectionLen {
		return fmt.Errorf("internal/access/node: %s length exceeds limit", label)
	}
	return nil
}
