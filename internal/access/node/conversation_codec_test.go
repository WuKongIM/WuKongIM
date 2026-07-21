package node

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityCodecHideConversationsWireLayout(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpHideConversations,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, LeaderTerm: 4, ConfigEpoch: 5, RouteRevision: 6, AuthorityEpoch: 7},
		Kind:   metadb.ConversationKindNormal,
		Deletes: []metadb.ConversationDelete{{
			UID: "u", Kind: metadb.ConversationKindCMD, ChannelID: "g", ChannelType: 2, DeletedToSeq: 3, UpdatedAt: 4,
		}},
	}
	want, err := hex.DecodeString("574b5643011d686964655f636f6e766572736174696f6e735f666f725f7461726765740102030405060700010000000000010175020167040308")
	if err != nil {
		t.Fatalf("decode golden bytes: %v", err)
	}
	got, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded hide request = %x, want %x", got, want)
	}
}

func TestConversationAuthorityCodecRoundTripHideConversations(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpHideConversations,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, LeaderTerm: 6, ConfigEpoch: 7, RouteRevision: 4, AuthorityEpoch: 5},
		Kind:   metadb.ConversationKindNormal,
		Deletes: []metadb.ConversationDelete{
			{UID: "u2", Kind: metadb.ConversationKindCMD, ChannelID: "g2____cmd", ChannelType: 3, DeletedToSeq: 19, UpdatedAt: 102},
			{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 9, UpdatedAt: 101},
		},
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	secondBody, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("second encodeConversationAuthorityRequest() error = %v", err)
	}
	if !bytes.Equal(body, secondBody) {
		t.Fatal("encoded hide request is not deterministic")
	}
	got, err := decodeConversationAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
}

func TestConversationAuthorityCodecRoundTripAdmit(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpAdmitPatches,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, LeaderTerm: 6, ConfigEpoch: 7, RouteRevision: 4, AuthorityEpoch: 5},
		Kind:   metadb.ConversationKindNormal,
		Patches: []conversationusecase.ActivePatch{{
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 7, DeletedToSeq: 8, ActiveAt: 100, UpdatedAt: 101, SparseActive: true, MessageSeq: 9,
		}},
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	got, err := decodeConversationAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
}

func TestConversationAuthorityCodecRoundTripActiveBatch(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpAdmitActiveBatch,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, LeaderTerm: 6, ConfigEpoch: 7, RouteRevision: 4, AuthorityEpoch: 5},
		Kind:   metadb.ConversationKindNormal,
		ActiveBatch: conversationactive.ActiveBatch{
			Kind:        metadb.ConversationKindCMD,
			SenderUID:   "sender",
			ChannelID:   "g1",
			ChannelType: 2,
			MessageSeq:  9,
			ActiveAtMS:  100,
			Recipients: []conversationactive.ActiveEntry{
				{UID: "sender", IsSender: true},
				{UID: "receiver"},
			},
		},
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	got, err := decodeConversationAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
}

func TestConversationAuthorityBulkCodecRoundTripPreservesAlignedGroupsAndResults(t *testing.T) {
	target := testConversationAuthorityTarget()
	secondTarget := target
	secondTarget.HashSlot = 2
	secondTarget.SlotID = 3
	groups := []ConversationActiveBatchGroup{
		{
			Target: target,
			Batch: conversationactive.ActiveBatch{
				Kind:        metadb.ConversationKindNormal,
				SenderUID:   "sender",
				ChannelID:   "g1",
				ChannelType: 2,
				MessageSeq:  9,
				ActiveAtMS:  100,
			},
		},
		{
			Target: secondTarget,
			Batch: conversationactive.ActiveBatch{
				Kind:        metadb.ConversationKindCMD,
				ChannelID:   "g1____cmd",
				ChannelType: 2,
				MessageSeq:  9,
				ActiveAtMS:  100,
				Recipients: []conversationactive.ActiveEntry{
					{UID: "u1"},
					{UID: "u2"},
				},
			},
		},
	}
	body, err := encodeConversationActiveBatchGroups(groups)
	if err != nil {
		t.Fatalf("encodeConversationActiveBatchGroups() error = %v", err)
	}
	if !hasMagic(body, conversationAuthorityBatchRequestMagic[:]) {
		t.Fatalf("bulk request magic = %x", body[:len(conversationAuthorityBatchRequestMagic)])
	}
	gotGroups, err := decodeConversationActiveBatchGroups(body)
	if err != nil {
		t.Fatalf("decodeConversationActiveBatchGroups() error = %v", err)
	}
	if !reflect.DeepEqual(gotGroups, groups) {
		t.Fatalf("decoded groups = %#v, want %#v", gotGroups, groups)
	}

	results := []conversationActiveBatchWireResult{
		{Status: conversationRPCStatusOK},
		{Status: conversationRPCStatusStaleRoute},
	}
	responseBody, err := encodeConversationActiveBatchResults(results)
	if err != nil {
		t.Fatalf("encodeConversationActiveBatchResults() error = %v", err)
	}
	if !hasMagic(responseBody, conversationAuthorityBatchResponseMagic[:]) {
		t.Fatalf("bulk response magic = %x", responseBody[:len(conversationAuthorityBatchResponseMagic)])
	}
	gotResults, err := decodeConversationActiveBatchResults(responseBody)
	if err != nil {
		t.Fatalf("decodeConversationActiveBatchResults() error = %v", err)
	}
	if !reflect.DeepEqual(gotResults, results) {
		t.Fatalf("decoded results = %#v, want %#v", gotResults, results)
	}
}

func TestConversationAuthorityBulkCodecAcceptsMaximumAggregateRows(t *testing.T) {
	target := testConversationAuthorityTarget()
	groups := []ConversationActiveBatchGroup{{
		Target: target,
		Batch: conversationactive.ActiveBatch{
			Kind:       metadb.ConversationKindNormal,
			SenderUID:  "sender",
			Recipients: make([]conversationactive.ActiveEntry, maxConversationAuthorityCollectionLen-1),
		},
	}}
	body, err := encodeConversationActiveBatchGroups(groups)
	if err != nil {
		t.Fatalf("encodeConversationActiveBatchGroups(maximum) error = %v", err)
	}
	if _, err := decodeConversationActiveBatchGroups(body); err != nil {
		t.Fatalf("decodeConversationActiveBatchGroups(maximum) error = %v", err)
	}
}

func TestConversationAuthorityBulkCodecRejectsInvalidAndOversizedRequests(t *testing.T) {
	target := testConversationAuthorityTarget()
	validGroups := []ConversationActiveBatchGroup{{
		Target: target,
		Batch: conversationactive.ActiveBatch{
			Kind:       metadb.ConversationKindNormal,
			Recipients: []conversationactive.ActiveEntry{{UID: "u1"}},
		},
	}}
	validBody, err := encodeConversationActiveBatchGroups(validGroups)
	if err != nil {
		t.Fatalf("encode valid groups: %v", err)
	}
	secondTarget := target
	secondTarget.HashSlot++
	overRows := []ConversationActiveBatchGroup{
		{Target: target, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: make([]conversationactive.ActiveEntry, maxConversationAuthorityCollectionLen/2)}},
		{Target: secondTarget, Batch: conversationactive.ActiveBatch{Kind: metadb.ConversationKindNormal, Recipients: make([]conversationactive.ActiveEntry, maxConversationAuthorityCollectionLen/2+1)}},
	}
	zeroLeader := append([]ConversationActiveBatchGroup(nil), validGroups...)
	zeroLeader[0].Target.LeaderNodeID = 0
	invalidKind := append([]ConversationActiveBatchGroup(nil), validGroups...)
	invalidKind[0].Batch.Kind = metadb.ConversationKind(99)
	tooManyGroups := make([]ConversationActiveBatchGroup, maxConversationAuthorityCollectionLen+1)

	encodeTests := []struct {
		name   string
		groups []ConversationActiveBatchGroup
	}{
		{name: "too many groups", groups: tooManyGroups},
		{name: "aggregate rows", groups: overRows},
		{name: "zero leader", groups: zeroLeader},
		{name: "invalid kind", groups: invalidKind},
	}
	for _, tt := range encodeTests {
		t.Run("encode "+tt.name, func(t *testing.T) {
			if _, err := encodeConversationActiveBatchGroups(tt.groups); err == nil {
				t.Fatal("encodeConversationActiveBatchGroups() error = nil, want error")
			}
		})
	}

	decodeTests := []struct {
		name string
		body []byte
	}{
		{name: "invalid magic", body: []byte("bad")},
		{name: "truncated", body: validBody[:len(validBody)-1]},
		{name: "trailing", body: append(append([]byte(nil), validBody...), 0)},
		{name: "too many groups", body: conversationBulkBodyWithGroupCount(maxConversationAuthorityCollectionLen + 1)},
		{name: "aggregate rows", body: conversationBulkBodyWithGroupRows([]conversationusecase.RouteTarget{target, secondTarget}, []int{maxConversationAuthorityCollectionLen / 2, maxConversationAuthorityCollectionLen/2 + 1})},
		{name: "zero leader", body: conversationBulkBodyWithRows(conversationusecase.RouteTarget{}, 1)},
		{name: "invalid kind", body: conversationBulkBodyWithKind(target, metadb.ConversationKind(99), 1)},
	}
	for _, tt := range decodeTests {
		t.Run("decode "+tt.name, func(t *testing.T) {
			if _, err := decodeConversationActiveBatchGroups(tt.body); err == nil {
				t.Fatal("decodeConversationActiveBatchGroups() error = nil, want error")
			}
		})
	}
}

func TestConversationAuthorityBulkCodecRejectsMalformedResponses(t *testing.T) {
	validBody, err := encodeConversationActiveBatchResults([]conversationActiveBatchWireResult{{Status: conversationRPCStatusOK}})
	if err != nil {
		t.Fatalf("encode valid results: %v", err)
	}
	tooMany := make([]conversationActiveBatchWireResult, maxConversationAuthorityCollectionLen+1)
	for i := range tooMany {
		tooMany[i].Status = conversationRPCStatusOK
	}
	if _, err := encodeConversationActiveBatchResults(tooMany); err == nil {
		t.Fatal("encodeConversationActiveBatchResults(too many) error = nil, want error")
	}
	if _, err := encodeConversationActiveBatchResults([]conversationActiveBatchWireResult{{Status: "mystery"}}); err == nil {
		t.Fatal("encodeConversationActiveBatchResults(unknown status) error = nil, want error")
	}
	unknownStatus := append(append([]byte(nil), conversationAuthorityBatchResponseMagic[:]...), 1)
	unknownStatus = appendString(unknownStatus, "mystery")
	tooManyResults := append(append([]byte(nil), conversationAuthorityBatchResponseMagic[:]...), appendUvarint(nil, maxConversationAuthorityCollectionLen+1)...)
	tooManyResults = append(tooManyResults, make([]byte, maxConversationAuthorityCollectionLen+1)...)
	decodeTests := []struct {
		name string
		body []byte
	}{
		{name: "invalid magic", body: []byte("bad")},
		{name: "truncated", body: validBody[:len(validBody)-1]},
		{name: "trailing", body: append(append([]byte(nil), validBody...), 0)},
		{name: "unknown status", body: unknownStatus},
		{name: "too many results", body: tooManyResults},
	}
	for _, tt := range decodeTests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeConversationActiveBatchResults(tt.body); err == nil {
				t.Fatal("decodeConversationActiveBatchResults() error = nil, want error")
			}
		})
	}
}

func TestConversationAuthorityCodecRoundTripListResponse(t *testing.T) {
	resp := conversationAuthorityResponse{
		Status: conversationRPCStatusOK,
		Page: conversationusecase.ActiveViewPage{
			Rows:   []metadb.ConversationState{{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 7, DeletedToSeq: 8, ActiveAt: 100, UpdatedAt: 101, SparseActive: true}},
			Cursor: metadb.ConversationActiveCursor{ActiveAt: 100, ChannelID: "g1____cmd", ChannelType: 2},
			Done:   true,
		},
	}
	body, err := encodeConversationAuthorityResponse(resp)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityResponse() error = %v", err)
	}
	got, err := decodeConversationAuthorityResponse(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityResponse() error = %v", err)
	}
	if !reflect.DeepEqual(got, resp) {
		t.Fatalf("decoded = %#v, want %#v", got, resp)
	}
}

func TestConversationAuthorityCodecRoundTripListRequestPreservesKind(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpList,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, LeaderTerm: 6, ConfigEpoch: 7, RouteRevision: 4, AuthorityEpoch: 5},
		Kind:   metadb.ConversationKindCMD,
		UID:    "u1",
		After:  metadb.ConversationActiveCursor{ActiveAt: 100, ChannelID: "g1____cmd", ChannelType: 2},
		Limit:  20,
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	got, err := decodeConversationAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
}

func TestConversationAuthorityCodecRoundTripDrainResponse(t *testing.T) {
	resp := conversationAuthorityResponse{
		Status:      conversationRPCStatusOK,
		DrainResult: conversationDrainResultDrained,
	}
	body, err := encodeConversationAuthorityResponse(resp)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityResponse() error = %v", err)
	}
	got, err := decodeConversationAuthorityResponse(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityResponse() error = %v", err)
	}
	if !reflect.DeepEqual(got, resp) {
		t.Fatalf("decoded = %#v, want %#v", got, resp)
	}
}

func TestConversationAuthorityCodecRejectsMalformedPayloads(t *testing.T) {
	validReq, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpList, Kind: metadb.ConversationKindNormal, Limit: 1})
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	validResp, err := encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusOK})
	if err != nil {
		t.Fatalf("encodeConversationAuthorityResponse() error = %v", err)
	}
	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid magic", body: []byte("bad")},
		{name: "trailing bytes", body: append(append([]byte(nil), validReq...), 0)},
		{name: "truncated payload", body: validReq[:len(validReq)-1]},
		{name: "oversized patches", body: conversationAuthorityRequestWithPatchCount(maxConversationAuthorityCollectionLen + 1)},
		{name: "oversized deletes", body: conversationAuthorityRequestWithDeleteCount(maxConversationAuthorityCollectionLen + 1)},
		{name: "truncated delete", body: truncatedConversationAuthorityDeleteRequest(t)},
		{name: "invalid op", body: conversationAuthorityRequestWithOp("unknown")},
		{name: "invalid patch bool", body: conversationAuthorityRequestWithPatchBool(2)},
		{name: "invalid response bool", body: conversationAuthorityResponseWithStateBool(2)},
		{name: "trailing response bytes", body: append(append([]byte(nil), validResp...), 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if strings.Contains(tt.name, "response") {
				_, err = decodeConversationAuthorityResponse(tt.body)
			} else {
				_, err = decodeConversationAuthorityRequest(tt.body)
			}
			if err == nil {
				t.Fatal("decode error = nil, want error")
			}
		})
	}
}

func TestConversationAuthorityCodecRejectsOversizedDeleteCollectionOnEncode(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:      conversationOpHideConversations,
		Kind:    metadb.ConversationKindNormal,
		Deletes: make([]metadb.ConversationDelete, maxConversationAuthorityCollectionLen+1),
	}
	if _, err := encodeConversationAuthorityRequest(req); err == nil {
		t.Fatal("encodeConversationAuthorityRequest() error = nil, want collection limit error")
	}
}

func TestConversationAuthorityCodecRejectsNegativeLimit(t *testing.T) {
	body := conversationAuthorityRequestWithLimit(-1)
	if _, err := decodeConversationAuthorityRequest(body); err == nil {
		t.Fatal("decodeConversationAuthorityRequest() error = nil, want negative limit error")
	}
}

func TestConversationAuthorityCodecRejectsOverflowLimit(t *testing.T) {
	original := maxConversationAuthorityDecodeLimit
	maxConversationAuthorityDecodeLimit = 10
	defer func() { maxConversationAuthorityDecodeLimit = original }()

	body := conversationAuthorityRequestWithLimit(11)
	if _, err := decodeConversationAuthorityRequest(body); err == nil {
		t.Fatal("decodeConversationAuthorityRequest() error = nil, want overflow limit error")
	}
}

func conversationAuthorityRequestWithOp(op string) []byte {
	return conversationAuthorityRequestWithOpAndLimit(op, 1)
}

func conversationAuthorityRequestWithLimit(limit int64) []byte {
	return conversationAuthorityRequestWithOpAndLimit(conversationOpList, limit)
}

func conversationAuthorityRequestWithOpAndLimit(op string, limit int64) []byte {
	dst := make([]byte, 0, 64)
	dst = append(dst, conversationAuthorityRequestMagic[:]...)
	dst = appendString(dst, op)
	dst = appendConversationRouteTarget(dst, conversationusecase.RouteTarget{})
	dst = appendString(dst, "u1")
	dst = appendConversationKind(dst, metadb.ConversationKindNormal)
	dst = appendConversationActiveCursor(dst, metadb.ConversationActiveCursor{})
	dst = appendVarint(dst, limit)
	return appendUvarint(dst, 0)
}

func conversationAuthorityRequestWithPatchCount(count int) []byte {
	dst := conversationAuthorityRequestWithOpAndLimit(conversationOpAdmitPatches, 1)
	dst = dst[:len(dst)-1]
	dst = appendUvarint(dst, uint64(count))
	return append(dst, make([]byte, count)...)
}

func conversationAuthorityRequestWithDeleteCount(count int) []byte {
	dst := conversationAuthorityRequestWithOpAndLimit(conversationOpHideConversations, 1)
	dst = appendUvarint(dst, uint64(count))
	for i := 0; i < count; i++ {
		// Six zero varints encode empty UID, normal kind, empty channel,
		// channel type 0, deleted sequence 0, and update time 0.
		dst = append(dst, 0, 0, 0, 0, 0, 0)
	}
	return dst
}

func truncatedConversationAuthorityDeleteRequest(t *testing.T) []byte {
	t.Helper()
	body, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{
		Op:   conversationOpHideConversations,
		Kind: metadb.ConversationKindNormal,
		Deletes: []metadb.ConversationDelete{{
			UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 9, UpdatedAt: 101,
		}},
	})
	if err != nil {
		t.Fatalf("encode hide request: %v", err)
	}
	return body[:len(body)-1]
}

func conversationAuthorityRequestWithPatchBool(flag byte) []byte {
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityRequestMagic[:]...)
	dst = appendString(dst, conversationOpAdmitPatches)
	dst = appendConversationRouteTarget(dst, conversationusecase.RouteTarget{})
	dst = appendString(dst, "")
	dst = appendConversationKind(dst, metadb.ConversationKindNormal)
	dst = appendConversationActiveCursor(dst, metadb.ConversationActiveCursor{})
	dst = appendVarint(dst, 1)
	dst = appendUvarint(dst, 1)
	dst = appendString(dst, "u1")
	dst = appendConversationKind(dst, metadb.ConversationKindNormal)
	dst = appendString(dst, "g1")
	dst = appendVarint(dst, 2)
	dst = appendUvarint(dst, 0)
	dst = appendUvarint(dst, 0)
	dst = appendVarint(dst, 100)
	dst = appendVarint(dst, 101)
	dst = append(dst, flag)
	return appendUvarint(dst, 9)
}

func conversationAuthorityResponseWithStateBool(flag byte) []byte {
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityResponseMagic[:]...)
	dst = appendString(dst, conversationRPCStatusOK)
	dst = appendUvarint(dst, 1)
	dst = appendString(dst, "u1")
	dst = appendConversationKind(dst, metadb.ConversationKindNormal)
	dst = appendString(dst, "g1")
	dst = appendVarint(dst, 2)
	dst = appendUvarint(dst, 7)
	dst = appendUvarint(dst, 8)
	dst = appendVarint(dst, 100)
	dst = appendVarint(dst, 101)
	dst = append(dst, flag)
	dst = appendConversationActiveCursor(dst, metadb.ConversationActiveCursor{})
	dst = appendConversationBool(dst, true)
	return appendString(dst, "")
}

func conversationBulkBodyWithGroupCount(count int) []byte {
	dst := append([]byte(nil), conversationAuthorityBatchRequestMagic[:]...)
	dst = appendUvarint(dst, uint64(count))
	return append(dst, make([]byte, count)...)
}

func conversationBulkBodyWithRows(target conversationusecase.RouteTarget, rows int) []byte {
	return conversationBulkBodyWithKind(target, metadb.ConversationKindNormal, rows)
}

func conversationBulkBodyWithKind(target conversationusecase.RouteTarget, kind metadb.ConversationKind, rows int) []byte {
	dst := append([]byte(nil), conversationAuthorityBatchRequestMagic[:]...)
	dst = appendUvarint(dst, 1)
	dst = appendConversationRouteTarget(dst, target)
	dst = appendConversationKind(dst, kind)
	dst = appendString(dst, "")
	dst = appendString(dst, "g1")
	dst = appendUvarint(dst, 2)
	dst = appendUvarint(dst, 9)
	dst = appendVarint(dst, 100)
	dst = appendUvarint(dst, uint64(rows))
	for i := 0; i < rows; i++ {
		dst = appendString(dst, "u")
		dst = appendConversationBool(dst, false)
	}
	return dst
}

func conversationBulkBodyWithGroupRows(targets []conversationusecase.RouteTarget, rows []int) []byte {
	dst := append([]byte(nil), conversationAuthorityBatchRequestMagic[:]...)
	dst = appendUvarint(dst, uint64(len(targets)))
	for i, target := range targets {
		dst = appendConversationRouteTarget(dst, target)
		dst = appendConversationKind(dst, metadb.ConversationKindNormal)
		dst = appendString(dst, "")
		dst = appendString(dst, "g1")
		dst = appendUvarint(dst, 2)
		dst = appendUvarint(dst, 9)
		dst = appendVarint(dst, 100)
		dst = appendUvarint(dst, uint64(rows[i]))
		for j := 0; j < rows[i]; j++ {
			dst = appendString(dst, "u")
			dst = appendConversationBool(dst, false)
		}
	}
	return dst
}
