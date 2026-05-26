package proxy

import (
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	runtimeMetaRPCRequestMagicV1   = [...]byte{'W', 'K', 'R', 'M', 1}
	runtimeMetaRPCRequestMagicV2   = [...]byte{'W', 'K', 'R', 'M', 2}
	runtimeMetaRPCRequestMagic     = [...]byte{'W', 'K', 'R', 'M', 3}
	runtimeMetaRPCRequestMagicLen  = len(runtimeMetaRPCRequestMagic)
	runtimeMetaRPCResponseMagicV1  = [...]byte{'W', 'K', 'R', 'R', 1}
	runtimeMetaRPCResponseMagicV2  = [...]byte{'W', 'K', 'R', 'R', 2}
	runtimeMetaRPCResponseMagic    = [...]byte{'W', 'K', 'R', 'R', 3}
	runtimeMetaRPCResponseMagicLen = len(runtimeMetaRPCResponseMagic)
)

const (
	runtimeMetaRPCGetID byte = iota + 1
	runtimeMetaRPCBatchGetID
	runtimeMetaRPCListID
	runtimeMetaRPCScanPageID
)

// encodeRuntimeMetaRPCRequestBinary encodes runtime meta requests without JSON reflection.
func encodeRuntimeMetaRPCRequestBinary(req runtimeMetaRPCRequest) ([]byte, error) {
	opID, err := runtimeMetaOpID(req.Op)
	if err != nil {
		return nil, err
	}
	codecVersion := runtimeMetaRPCCodecVersionOrLatest(req.CodecVersion)
	requestMagic, err := runtimeMetaRPCRequestMagicForVersion(codecVersion)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, estimateRuntimeMetaRequestBinarySize(req))
	dst = append(dst, requestMagic...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = runtimeMetaAppendConversationKeys(dst, req.Keys)
	dst = runtimeMetaAppendCursorPtr(dst, req.After)
	dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	return dst, nil
}

func decodeRuntimeMetaRPCRequest(body []byte) (runtimeMetaRPCRequest, error) {
	requestVersion, ok := runtimeMetaRPCRequestVersion(body)
	if !ok {
		return runtimeMetaRPCRequest{}, fmt.Errorf("metastore: invalid runtime meta request codec")
	}
	offset := runtimeMetaRPCRequestMagicLen
	if offset >= len(body) {
		return runtimeMetaRPCRequest{}, fmt.Errorf("metastore: short runtime meta op")
	}
	op, err := runtimeMetaOpFromID(body[offset])
	if err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	offset++

	var req runtimeMetaRPCRequest
	req.Op = op
	req.CodecVersion = requestVersion
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	if req.Keys, offset, err = runtimeMetaReadConversationKeys(body, offset); err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	if req.After, offset, err = runtimeMetaReadCursorPtr(body, offset); err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	if req.Limit, offset, err = runtimeMetaReadInt(body, offset, "runtime meta limit"); err != nil {
		return runtimeMetaRPCRequest{}, err
	}
	if offset != len(body) {
		return runtimeMetaRPCRequest{}, fmt.Errorf("metastore: trailing runtime meta request bytes")
	}
	return req, nil
}

func encodeRuntimeMetaRPCResponseBinary(resp runtimeMetaRPCResponse) ([]byte, error) {
	return encodeRuntimeMetaRPCResponseForVersion(resp, 0)
}

func encodeRuntimeMetaRPCResponseForVersion(resp runtimeMetaRPCResponse, version byte) ([]byte, error) {
	version = runtimeMetaRPCCodecVersionOrLatest(version)
	responseMagic, err := runtimeMetaRPCResponseMagicForVersion(version)
	if err != nil {
		return nil, err
	}
	includeWriteFence := version >= 2
	includeRouteGeneration := version >= 3
	dst := make([]byte, 0, estimateRuntimeMetaResponseBinarySize(resp))
	dst = append(dst, responseMagic...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = runtimeMetaAppendMetaPtrWithOptions(dst, resp.Meta, runtimeMetaEncodeOptions{
		includeWriteFence:      includeWriteFence,
		includeRouteGeneration: includeRouteGeneration,
	})
	dst = runtimeMetaAppendMetasWithOptions(dst, resp.Metas, runtimeMetaEncodeOptions{
		includeWriteFence:      includeWriteFence,
		includeRouteGeneration: includeRouteGeneration,
	})
	dst = runtimeMetaAppendCursor(dst, resp.Cursor)
	dst = runtimeMetaAppendBool(dst, resp.Done)
	return dst, nil
}

func decodeRuntimeMetaRPCResponseBinary(body []byte) (runtimeMetaRPCResponse, error) {
	responseVersion, ok := runtimeMetaRPCResponseVersion(body)
	if !ok {
		return runtimeMetaRPCResponse{}, fmt.Errorf("metastore: invalid runtime meta response codec")
	}
	offset := runtimeMetaRPCResponseMagicLen
	includeWriteFence := responseVersion >= 2
	includeRouteGeneration := responseVersion >= 3
	var resp runtimeMetaRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return runtimeMetaRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return runtimeMetaRPCResponse{}, err
	}
	if resp.Meta, offset, err = runtimeMetaReadMetaPtr(body, offset, runtimeMetaEncodeOptions{
		includeWriteFence:      includeWriteFence,
		includeRouteGeneration: includeRouteGeneration,
	}); err != nil {
		return runtimeMetaRPCResponse{}, err
	}
	if resp.Metas, offset, err = runtimeMetaReadMetas(body, offset, runtimeMetaEncodeOptions{
		includeWriteFence:      includeWriteFence,
		includeRouteGeneration: includeRouteGeneration,
	}); err != nil {
		return runtimeMetaRPCResponse{}, err
	}
	if resp.Cursor, offset, err = runtimeMetaReadCursor(body, offset); err != nil {
		return runtimeMetaRPCResponse{}, err
	}
	if resp.Done, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return runtimeMetaRPCResponse{}, err
	}
	if offset != len(body) {
		return runtimeMetaRPCResponse{}, fmt.Errorf("metastore: trailing runtime meta response bytes")
	}
	return resp, nil
}

func isRuntimeMetaRPCRequestBinary(body []byte) bool {
	_, ok := runtimeMetaRPCRequestVersion(body)
	return ok
}

func isRuntimeMetaRPCResponseBinary(body []byte) bool {
	_, ok := runtimeMetaRPCResponseVersion(body)
	return ok
}

func runtimeMetaRPCRequestVersion(body []byte) (byte, bool) {
	if runtimeMetaHasMagic(body, runtimeMetaRPCRequestMagic[:]) {
		return runtimeMetaRPCRequestMagic[len(runtimeMetaRPCRequestMagic)-1], true
	}
	if runtimeMetaHasMagic(body, runtimeMetaRPCRequestMagicV2[:]) {
		return runtimeMetaRPCRequestMagicV2[len(runtimeMetaRPCRequestMagicV2)-1], true
	}
	if runtimeMetaHasMagic(body, runtimeMetaRPCRequestMagicV1[:]) {
		return runtimeMetaRPCRequestMagicV1[len(runtimeMetaRPCRequestMagicV1)-1], true
	}
	return 0, false
}

func runtimeMetaRPCResponseVersion(body []byte) (byte, bool) {
	if runtimeMetaHasMagic(body, runtimeMetaRPCResponseMagic[:]) {
		return runtimeMetaRPCResponseMagic[len(runtimeMetaRPCResponseMagic)-1], true
	}
	if runtimeMetaHasMagic(body, runtimeMetaRPCResponseMagicV2[:]) {
		return runtimeMetaRPCResponseMagicV2[len(runtimeMetaRPCResponseMagicV2)-1], true
	}
	if runtimeMetaHasMagic(body, runtimeMetaRPCResponseMagicV1[:]) {
		return runtimeMetaRPCResponseMagicV1[len(runtimeMetaRPCResponseMagicV1)-1], true
	}
	return 0, false
}

func runtimeMetaRPCCodecVersionOrLatest(version byte) byte {
	if version == 0 {
		return 3
	}
	return version
}

func runtimeMetaRPCRequestMagicForVersion(version byte) ([]byte, error) {
	switch version {
	case 1:
		return runtimeMetaRPCRequestMagicV1[:], nil
	case 2:
		return runtimeMetaRPCRequestMagicV2[:], nil
	case 3:
		return runtimeMetaRPCRequestMagic[:], nil
	default:
		return nil, fmt.Errorf("metastore: unsupported runtime meta request codec version %d", version)
	}
}

func runtimeMetaRPCResponseMagicForVersion(version byte) ([]byte, error) {
	switch version {
	case 1:
		return runtimeMetaRPCResponseMagicV1[:], nil
	case 2:
		return runtimeMetaRPCResponseMagicV2[:], nil
	case 3:
		return runtimeMetaRPCResponseMagic[:], nil
	default:
		return nil, fmt.Errorf("metastore: unsupported runtime meta response codec version %d", version)
	}
}

func runtimeMetaOpID(op string) (byte, error) {
	switch op {
	case runtimeMetaRPCGet:
		return runtimeMetaRPCGetID, nil
	case runtimeMetaRPCBatchGet:
		return runtimeMetaRPCBatchGetID, nil
	case runtimeMetaRPCList:
		return runtimeMetaRPCListID, nil
	case runtimeMetaRPCScanPage:
		return runtimeMetaRPCScanPageID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown runtime meta rpc op %q", op)
	}
}

func runtimeMetaOpFromID(op byte) (string, error) {
	switch op {
	case runtimeMetaRPCGetID:
		return runtimeMetaRPCGet, nil
	case runtimeMetaRPCBatchGetID:
		return runtimeMetaRPCBatchGet, nil
	case runtimeMetaRPCListID:
		return runtimeMetaRPCList, nil
	case runtimeMetaRPCScanPageID:
		return runtimeMetaRPCScanPage, nil
	default:
		return "", fmt.Errorf("metastore: unknown runtime meta rpc op id %d", op)
	}
}

func runtimeMetaAppendConversationKeys(dst []byte, keys []metadb.ConversationKey) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(keys)))
	for _, key := range keys {
		dst = runtimeMetaAppendString(dst, key.ChannelID)
		dst = runtimeMetaAppendVarint(dst, key.ChannelType)
	}
	return dst
}

func runtimeMetaReadConversationKeys(body []byte, offset int) ([]metadb.ConversationKey, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	keysLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "runtime meta keys")
	if err != nil {
		return nil, offset, err
	}
	keys := make([]metadb.ConversationKey, keysLen)
	for i := range keys {
		if keys[i].ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
			return nil, offset, err
		}
		if keys[i].ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return keys, offset, nil
}

func runtimeMetaAppendCursorPtr(dst []byte, cursor *metadb.ChannelRuntimeMetaCursor) []byte {
	if cursor == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return runtimeMetaAppendCursor(dst, *cursor)
}

func runtimeMetaReadCursorPtr(body []byte, offset int) (*metadb.ChannelRuntimeMetaCursor, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "runtime meta cursor")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	cursor, next, err := runtimeMetaReadCursor(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &cursor, next, nil
}

func runtimeMetaAppendCursor(dst []byte, cursor metadb.ChannelRuntimeMetaCursor) []byte {
	dst = runtimeMetaAppendString(dst, cursor.ChannelID)
	dst = runtimeMetaAppendVarint(dst, cursor.ChannelType)
	return dst
}

func runtimeMetaReadCursor(body []byte, offset int) (metadb.ChannelRuntimeMetaCursor, int, error) {
	var cursor metadb.ChannelRuntimeMetaCursor
	var err error
	if cursor.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelRuntimeMetaCursor{}, offset, err
	}
	if cursor.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMetaCursor{}, offset, err
	}
	return cursor, offset, nil
}

type runtimeMetaEncodeOptions struct {
	includeWriteFence      bool
	includeRouteGeneration bool
}

func runtimeMetaAppendMetaPtr(dst []byte, meta *metadb.ChannelRuntimeMeta) []byte {
	return runtimeMetaAppendMetaPtrWithOptions(dst, meta, runtimeMetaEncodeOptions{
		includeWriteFence:      true,
		includeRouteGeneration: true,
	})
}

func runtimeMetaAppendMetaPtrWithOptions(dst []byte, meta *metadb.ChannelRuntimeMeta, opts runtimeMetaEncodeOptions) []byte {
	if meta == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return runtimeMetaAppendMeta(dst, *meta, opts)
}

func runtimeMetaReadMetaPtr(body []byte, offset int, opts runtimeMetaEncodeOptions) (*metadb.ChannelRuntimeMeta, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "runtime meta")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	meta, next, err := runtimeMetaReadMeta(body, next, opts)
	if err != nil {
		return nil, offset, err
	}
	return &meta, next, nil
}

func runtimeMetaAppendMetas(dst []byte, metas []metadb.ChannelRuntimeMeta) []byte {
	return runtimeMetaAppendMetasWithOptions(dst, metas, runtimeMetaEncodeOptions{
		includeWriteFence:      true,
		includeRouteGeneration: true,
	})
}

func runtimeMetaAppendMetasWithOptions(dst []byte, metas []metadb.ChannelRuntimeMeta, opts runtimeMetaEncodeOptions) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(metas)))
	for _, meta := range metas {
		dst = runtimeMetaAppendMeta(dst, meta, opts)
	}
	return dst
}

func runtimeMetaReadMetas(body []byte, offset int, opts runtimeMetaEncodeOptions) ([]metadb.ChannelRuntimeMeta, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	metasLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "runtime meta list")
	if err != nil {
		return nil, offset, err
	}
	metas := make([]metadb.ChannelRuntimeMeta, metasLen)
	for i := range metas {
		if metas[i], offset, err = runtimeMetaReadMeta(body, offset, opts); err != nil {
			return nil, offset, err
		}
	}
	return metas, offset, nil
}

func runtimeMetaAppendMeta(dst []byte, meta metadb.ChannelRuntimeMeta, opts runtimeMetaEncodeOptions) []byte {
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	dst = runtimeMetaAppendString(dst, meta.ChannelID)
	dst = runtimeMetaAppendVarint(dst, meta.ChannelType)
	dst = runtimeMetaAppendUvarint(dst, meta.ChannelEpoch)
	dst = runtimeMetaAppendUvarint(dst, meta.LeaderEpoch)
	dst = runtimeMetaAppendUint64s(dst, meta.Replicas)
	dst = runtimeMetaAppendUint64s(dst, meta.ISR)
	dst = runtimeMetaAppendUvarint(dst, meta.Leader)
	dst = runtimeMetaAppendVarint(dst, meta.MinISR)
	dst = append(dst, meta.Status)
	dst = runtimeMetaAppendUvarint(dst, meta.Features)
	dst = runtimeMetaAppendVarint(dst, meta.LeaseUntilMS)
	dst = runtimeMetaAppendUvarint(dst, meta.RetentionThroughSeq)
	dst = runtimeMetaAppendVarint(dst, meta.RetentionUpdatedAtMS)
	if !opts.includeWriteFence {
		if opts.includeRouteGeneration {
			dst = runtimeMetaAppendUvarint(dst, meta.RouteGeneration)
		}
		return dst
	}
	dst = runtimeMetaAppendString(dst, meta.WriteFenceToken)
	dst = runtimeMetaAppendUvarint(dst, meta.WriteFenceVersion)
	dst = append(dst, meta.WriteFenceReason)
	dst = runtimeMetaAppendVarint(dst, meta.WriteFenceUntilMS)
	if opts.includeRouteGeneration {
		dst = runtimeMetaAppendUvarint(dst, meta.RouteGeneration)
	}
	return dst
}

func runtimeMetaReadMeta(body []byte, offset int, opts runtimeMetaEncodeOptions) (metadb.ChannelRuntimeMeta, int, error) {
	var meta metadb.ChannelRuntimeMeta
	var err error
	if meta.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.ChannelEpoch, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.LeaderEpoch, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.Replicas, offset, err = runtimeMetaReadUint64s(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.ISR, offset, err = runtimeMetaReadUint64s(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.Leader, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.MinISR, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if offset >= len(body) {
		return metadb.ChannelRuntimeMeta{}, offset, fmt.Errorf("metastore: short runtime meta status")
	}
	meta.Status = body[offset]
	offset++
	if meta.Features, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.LeaseUntilMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.RetentionThroughSeq, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.RetentionUpdatedAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if !opts.includeWriteFence {
		if opts.includeRouteGeneration {
			if meta.RouteGeneration, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
				return metadb.ChannelRuntimeMeta{}, offset, err
			}
		}
		return metadb.NormalizeChannelRuntimeMeta(meta), offset, nil
	}
	if meta.WriteFenceToken, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if meta.WriteFenceVersion, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if offset >= len(body) {
		return metadb.ChannelRuntimeMeta{}, offset, fmt.Errorf("metastore: short runtime meta write fence reason")
	}
	meta.WriteFenceReason = body[offset]
	offset++
	if meta.WriteFenceUntilMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelRuntimeMeta{}, offset, err
	}
	if opts.includeRouteGeneration {
		if meta.RouteGeneration, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return metadb.ChannelRuntimeMeta{}, offset, err
		}
	}
	return metadb.NormalizeChannelRuntimeMeta(meta), offset, nil
}

func runtimeMetaAppendUint64s(dst []byte, values []uint64) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = runtimeMetaAppendUvarint(dst, value)
	}
	return dst
}

func runtimeMetaReadUint64s(body []byte, offset int) ([]uint64, int, error) {
	count, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	valuesLen, err := runtimeMetaCollectionLen(count, len(body)-offset, "runtime meta uint64 list")
	if err != nil {
		return nil, offset, err
	}
	values := make([]uint64, valuesLen)
	for i := range values {
		if values[i], offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
			return nil, offset, err
		}
	}
	return values, offset, nil
}

func runtimeMetaAppendBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func runtimeMetaReadBool(body []byte, offset int) (bool, int, error) {
	if offset >= len(body) {
		return false, offset, fmt.Errorf("metastore: short runtime meta bool")
	}
	return body[offset] != 0, offset + 1, nil
}

func runtimeMetaReadMarker(body []byte, offset int, label string) (byte, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("metastore: short %s marker", label)
	}
	marker := body[offset]
	if marker > 1 {
		return 0, offset, fmt.Errorf("metastore: invalid %s marker", label)
	}
	return marker, offset + 1, nil
}

func runtimeMetaAppendUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func runtimeMetaAppendVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func runtimeMetaAppendString(dst []byte, v string) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(v)))
	return append(dst, v...)
}

func runtimeMetaReadUvarint(body []byte, offset int) (uint64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("metastore: short uvarint")
	}
	v, n := binary.Uvarint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("metastore: invalid uvarint")
	}
	return v, offset + n, nil
}

func runtimeMetaReadVarint(body []byte, offset int) (int64, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("metastore: short varint")
	}
	v, n := binary.Varint(body[offset:])
	if n <= 0 {
		return 0, offset, fmt.Errorf("metastore: invalid varint")
	}
	return v, offset + n, nil
}

func runtimeMetaReadInt(body []byte, offset int, label string) (int, int, error) {
	v, next, err := runtimeMetaReadVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	if int64(int(v)) != v {
		return 0, offset, fmt.Errorf("metastore: %s overflows int", label)
	}
	return int(v), next, nil
}

func runtimeMetaReadString(body []byte, offset int) (string, int, error) {
	length, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return "", offset, err
	}
	offset = next
	end := offset + int(length)
	if end < offset || end > len(body) {
		return "", offset, fmt.Errorf("metastore: short string")
	}
	return string(body[offset:end]), end, nil
}

func runtimeMetaCollectionLen(count uint64, remaining int, label string) (int, error) {
	maxInt := uint64(^uint(0) >> 1)
	if count > maxInt {
		return 0, fmt.Errorf("metastore: %s count overflows int", label)
	}
	if count > uint64(remaining) {
		return 0, fmt.Errorf("metastore: %s count exceeds remaining bytes", label)
	}
	return int(count), nil
}

func runtimeMetaHasMagic(body []byte, magic []byte) bool {
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

func estimateRuntimeMetaRequestBinarySize(req runtimeMetaRPCRequest) int {
	size := len(runtimeMetaRPCRequestMagic) + 1 + binary.MaxVarintLen64*3 + len(req.ChannelID)
	for _, key := range req.Keys {
		size += len(key.ChannelID) + binary.MaxVarintLen64*2
	}
	size += 1
	if req.After != nil {
		size += len(req.After.ChannelID) + binary.MaxVarintLen64*2
	}
	return size
}

func estimateRuntimeMetaResponseBinarySize(resp runtimeMetaRPCResponse) int {
	size := len(runtimeMetaRPCResponseMagic) + len(resp.Status) + binary.MaxVarintLen64*2 + 1
	if resp.Meta != nil {
		size += estimateRuntimeMetaBinarySize(*resp.Meta)
	}
	size += binary.MaxVarintLen64
	for _, meta := range resp.Metas {
		size += estimateRuntimeMetaBinarySize(meta)
	}
	size += len(resp.Cursor.ChannelID) + binary.MaxVarintLen64*2 + 1
	return size
}

func estimateRuntimeMetaBinarySize(meta metadb.ChannelRuntimeMeta) int {
	return len(meta.ChannelID) + len(meta.WriteFenceToken) + binary.MaxVarintLen64*15 + len(meta.Replicas)*binary.MaxVarintLen64 + len(meta.ISR)*binary.MaxVarintLen64 + 2
}
