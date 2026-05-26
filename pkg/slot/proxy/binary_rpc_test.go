package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestIdentityRPCBinaryCodecRoundTrip(t *testing.T) {
	req := identityRPCRequest{
		Op:         identityRPCGetDevice,
		SlotID:     2,
		UID:        "u1",
		DeviceFlag: 3,
	}

	reqBody, err := encodeIdentityRPCRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isIdentityRPCRequestBinary(reqBody))

	gotReq, err := decodeIdentityRPCRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := identityRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		User:     &metadb.User{UID: "u1", Token: "token", DeviceFlag: 3, DeviceLevel: 1},
		Device:   &metadb.Device{UID: "u1", DeviceFlag: 3, Token: "device-token", DeviceLevel: 1},
	}
	respBody, err := encodeIdentityRPCResponse(resp)
	require.NoError(t, err)
	require.True(t, isIdentityRPCResponseBinary(respBody))

	gotResp, err := decodeIdentityRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestIdentityRPCBinaryCodecRoundTripsUserScanPage(t *testing.T) {
	req := identityRPCRequest{
		Op:     identityRPCScanUsersPage,
		SlotID: 2,
		After:  metadb.UserCursor{UID: "u1"},
		Limit:  25,
	}

	reqBody, err := encodeIdentityRPCRequestBinary(req)
	require.NoError(t, err)

	gotReq, err := decodeIdentityRPCRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := identityRPCResponse{
		Status: rpcStatusOK,
		Users: []metadb.User{
			{UID: "u2", Token: "token-2", DeviceFlag: 1, DeviceLevel: 2},
			{UID: "u3", Token: "token-3", DeviceFlag: 2, DeviceLevel: 1},
		},
		Cursor: metadb.UserCursor{UID: "u3"},
		Done:   true,
	}
	respBody, err := encodeIdentityRPCResponse(resp)
	require.NoError(t, err)

	gotResp, err := decodeIdentityRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestSubscriberRPCBinaryCodecRoundTrip(t *testing.T) {
	req := subscriberRPCRequest{
		SlotID:      2,
		HashSlot:    7,
		ChannelID:   "g1",
		ChannelType: 2,
		Snapshot:    true,
		AfterUID:    "u1",
		Limit:       128,
	}

	reqBody, err := encodeSubscriberRPCRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isSubscriberRPCRequestBinary(reqBody))

	gotReq, err := decodeSubscriberRPCRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := subscriberRPCResponse{
		Status:     rpcStatusOK,
		LeaderID:   2,
		UIDs:       []string{"u1", "u2"},
		NextCursor: "u2",
		Done:       true,
	}
	respBody, err := encodeSubscriberRPCResponse(resp)
	require.NoError(t, err)
	require.True(t, isSubscriberRPCResponseBinary(respBody))

	gotResp, err := decodeSubscriberRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestSubscriberRPCBinaryCodecRoundTripsPointLookupFields(t *testing.T) {
	req := subscriberRPCRequest{
		SlotID:      3,
		HashSlot:    7,
		ChannelID:   "g1",
		ChannelType: 2,
		ContainsUID: "u1",
		HasAny:      true,
	}
	body, err := encodeSubscriberRPCRequestBinary(req)
	require.NoError(t, err)
	got, err := decodeSubscriberRPCRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)

	resp := subscriberRPCResponse{Status: rpcStatusOK, UIDs: []string{}, Contains: true, HasAny: true}
	body, err = encodeSubscriberRPCResponseBinary(resp)
	require.NoError(t, err)
	gotResp, err := decodeSubscriberRPCResponseBinary(body)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestUserConversationStateRPCBinaryCodecRoundTrip(t *testing.T) {
	req := userConversationStateRPCRequest{
		Op:          userConversationStateRPCHide,
		SlotID:      2,
		HashSlot:    7,
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		After:       &metadb.ConversationCursor{ChannelID: "g0", ChannelType: 2},
		Limit:       64,
		States: []metadb.UserConversationState{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 4, ActiveAt: 5, UpdatedAt: 6,
		}},
		Patches: []metadb.UserConversationActivePatch{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 7, MessageSeq: 8,
		}},
		Keys: []metadb.ConversationKey{{ChannelID: "g1", ChannelType: 2}},
		Deletes: []metadb.UserConversationDelete{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 9, UpdatedAt: 10,
		}},
		Hints: []metadb.UserConversationActiveHint{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 11, MessageSeq: 12,
		}},
		Barriers: []metadb.UserConversationDeleteBarrier{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 13,
		}},
	}

	reqBody, err := encodeUserConversationStateRPCRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isUserConversationStateRPCRequestBinary(reqBody))

	gotReq, err := decodeUserConversationStateRPCRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := userConversationStateRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		State:    &metadb.UserConversationState{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 4, ActiveAt: 5, UpdatedAt: 6},
		States:   []metadb.UserConversationState{{UID: "u2", ChannelID: "g2", ChannelType: 3, ReadSeq: 7, DeletedToSeq: 8, ActiveAt: 9, UpdatedAt: 10}},
		Cursor:   metadb.ConversationCursor{ChannelID: "g2", ChannelType: 3},
		Done:     true,
	}
	respBody, err := encodeUserConversationStateRPCResponse(resp)
	require.NoError(t, err)
	require.True(t, isUserConversationStateRPCResponseBinary(respBody))

	gotResp, err := decodeUserConversationStateRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelRPCBinaryCodecRoundTripsStatusFlags(t *testing.T) {
	resp := channelRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Channel:  &metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 7},
	}
	body := encodeChannelRPCResponseBinary(resp)
	got, err := decodeChannelRPCResponseBinary(body)
	require.NoError(t, err)
	require.Equal(t, resp, got)
}

func TestChannelRPCBinaryCodecRoundTripsChannelScanPage(t *testing.T) {
	req := channelRPCRequest{
		Op:     channelRPCScanChannelsPage,
		SlotID: 1,
		After:  metadb.ChannelCursor{ChannelID: "a", ChannelType: 2},
		Limit:  50,
	}

	body, err := encodeChannelRPCRequestBinary(req)
	require.NoError(t, err)
	gotReq, err := decodeChannelRPCRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelRPCResponse{
		Status: rpcStatusOK,
		Channels: []metadb.Channel{
			{ChannelID: "a", ChannelType: 1, AllowStranger: 1},
			{ChannelID: "b", ChannelType: 2, Ban: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 9},
		},
		Cursor: metadb.ChannelCursor{ChannelID: "b", ChannelType: 2},
		Done:   true,
	}

	body = encodeChannelRPCResponseBinary(resp)
	gotResp, err := decodeChannelRPCResponseBinary(body)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelRPCBinaryCodecDecodesV2ChannelWithoutAllowStranger(t *testing.T) {
	body := make([]byte, 0, 64)
	body = append(body, channelRPCResponseMagicV2[:]...)
	body = runtimeMetaAppendString(body, rpcStatusOK)
	body = runtimeMetaAppendUvarint(body, 0)
	body = append(body, 1)
	body = appendChannelLegacyV2ForTest(body, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, SubscriberMutationVersion: 7})
	body = runtimeMetaAppendUvarint(body, 0)
	body = runtimeMetaAppendChannelCursor(body, metadb.ChannelCursor{})
	body = runtimeMetaAppendBool(body, true)

	got, err := decodeChannelRPCResponseBinary(body)
	require.NoError(t, err)
	require.NotNil(t, got.Channel)
	require.Equal(t, int64(0), got.Channel.AllowStranger)
}

func TestChannelRPCBinaryCodecDecodesV2ChannelScanPageWithoutAllowStranger(t *testing.T) {
	resp := channelRPCResponse{
		Status: rpcStatusOK,
		Channels: []metadb.Channel{
			{ChannelID: "a", ChannelType: 1, Ban: 1, SubscriberMutationVersion: 3},
			{ChannelID: "b", ChannelType: 2, Disband: 1, SendBan: 1, SubscriberMutationVersion: 7},
		},
		Cursor: metadb.ChannelCursor{ChannelID: "b", ChannelType: 2},
		Done:   true,
	}

	body := make([]byte, 0, 128)
	body = append(body, channelRPCResponseMagicV2[:]...)
	body = runtimeMetaAppendString(body, resp.Status)
	body = runtimeMetaAppendUvarint(body, resp.LeaderID)
	body = append(body, 0)
	body = runtimeMetaAppendUvarint(body, uint64(len(resp.Channels)))
	for _, ch := range resp.Channels {
		body = appendChannelLegacyV2ForTest(body, ch)
	}
	body = runtimeMetaAppendChannelCursor(body, resp.Cursor)
	body = runtimeMetaAppendBool(body, resp.Done)

	got, err := decodeChannelRPCResponseBinary(body)
	require.NoError(t, err)
	require.Equal(t, resp, got)
}

func TestChannelRPCBinaryCodecDecodesV1ChannelWithoutAllowStranger(t *testing.T) {
	body := make([]byte, 0, 64)
	body = append(body, channelRPCResponseMagicV1[:]...)
	body = runtimeMetaAppendString(body, rpcStatusOK)
	body = runtimeMetaAppendUvarint(body, 0)
	body = append(body, 1)
	body = appendChannelLegacyV2ForTest(body, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, SubscriberMutationVersion: 7})

	got, err := decodeChannelRPCResponseBinary(body)
	require.NoError(t, err)
	require.NotNil(t, got.Channel)
	require.Equal(t, int64(0), got.Channel.AllowStranger)
}

func appendChannelLegacyV2ForTest(dst []byte, ch metadb.Channel) []byte {
	dst = runtimeMetaAppendString(dst, ch.ChannelID)
	dst = runtimeMetaAppendVarint(dst, ch.ChannelType)
	dst = runtimeMetaAppendVarint(dst, ch.Ban)
	dst = runtimeMetaAppendVarint(dst, ch.Disband)
	dst = runtimeMetaAppendVarint(dst, ch.SendBan)
	dst = runtimeMetaAppendUvarint(dst, ch.SubscriberMutationVersion)
	return dst
}

func TestRemainingProxyRPCsRejectJSONPayload(t *testing.T) {
	store := New(nil, openTestDB(t))

	identityBody := []byte(`{"op":"get_user","slot_id":1,"uid":"u1"}`)
	var err error
	_, err = store.handleIdentityRPC(context.Background(), identityBody)
	require.Error(t, err)

	subscriberBody := []byte(`{"slot_id":1,"channel_id":"g1","channel_type":2}`)
	_, err = store.handleSubscriberRPC(context.Background(), subscriberBody)
	require.Error(t, err)

	conversationBody := []byte(`{"op":"get","slot_id":1,"uid":"u1"}`)
	_, err = store.handleUserConversationStateRPC(context.Background(), conversationBody)
	require.Error(t, err)

	cmdConversationBody := []byte(`{"op":"get","slot_id":1,"uid":"u1"}`)
	_, err = store.handleCMDConversationStateRPC(context.Background(), cmdConversationBody)
	require.Error(t, err)
}
