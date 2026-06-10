package node

import (
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityCodecRoundTripAdmit(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpAdmitPatches,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Patches: []conversationusecase.ActivePatch{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 7, DeletedToSeq: 8, ActiveAt: 100, UpdatedAt: 101, SparseActive: true, MessageSeq: 9,
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
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		ActiveBatch: conversationactive.ActiveBatch{
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

func TestConversationAuthorityCodecRoundTripListResponse(t *testing.T) {
	resp := conversationAuthorityResponse{
		Status: conversationRPCStatusOK,
		Page: conversationusecase.ActiveViewPage{
			Rows:   []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 7, DeletedToSeq: 8, ActiveAt: 100, UpdatedAt: 101, SparseActive: true}},
			Cursor: metadb.UserConversationActiveCursor{ActiveAt: 100, ChannelID: "g1", ChannelType: 2},
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
	validReq, err := encodeConversationAuthorityRequest(conversationAuthorityRequest{Op: conversationOpList, Limit: 1})
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
	dst = appendConversationActiveCursor(dst, metadb.UserConversationActiveCursor{})
	dst = appendVarint(dst, limit)
	return appendUvarint(dst, 0)
}

func conversationAuthorityRequestWithPatchCount(count int) []byte {
	dst := conversationAuthorityRequestWithOpAndLimit(conversationOpAdmitPatches, 1)
	dst = dst[:len(dst)-1]
	dst = appendUvarint(dst, uint64(count))
	return append(dst, make([]byte, count)...)
}

func conversationAuthorityRequestWithPatchBool(flag byte) []byte {
	dst := make([]byte, 0, 128)
	dst = append(dst, conversationAuthorityRequestMagic[:]...)
	dst = appendString(dst, conversationOpAdmitPatches)
	dst = appendConversationRouteTarget(dst, conversationusecase.RouteTarget{})
	dst = appendString(dst, "")
	dst = appendConversationActiveCursor(dst, metadb.UserConversationActiveCursor{})
	dst = appendVarint(dst, 1)
	dst = appendUvarint(dst, 1)
	dst = appendString(dst, "u1")
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
	dst = appendString(dst, "g1")
	dst = appendVarint(dst, 2)
	dst = appendUvarint(dst, 7)
	dst = appendUvarint(dst, 8)
	dst = appendVarint(dst, 100)
	dst = appendVarint(dst, 101)
	dst = append(dst, flag)
	dst = appendConversationActiveCursor(dst, metadb.UserConversationActiveCursor{})
	dst = appendConversationBool(dst, true)
	return appendString(dst, "")
}
