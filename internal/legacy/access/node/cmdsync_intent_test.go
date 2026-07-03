package node

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/cmdsync"
	"github.com/stretchr/testify/require"
)

func TestCMDConversationIntentBinaryCodecRoundTrip(t *testing.T) {
	req := cmdSyncRPCRequest{
		Op: cmdSyncOpPushIntent,
		Intent: cmdsync.ConversationIntent{
			CommandChannelID: "g1____cmd",
			ChannelType:      2,
			MessageSeq:       9,
			ActiveAt:         100,
			SenderUID:        "u1",
			UserReadSeqs:     map[string]uint64{"u1": 9, "u2": 0},
		},
	}

	body, err := encodeCMDSyncRequestBinary(req)
	require.NoError(t, err)

	got, err := decodeCMDSyncRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestCMDConversationIntentBinaryCodecIsDeterministic(t *testing.T) {
	left := cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, UserReadSeqs: map[string]uint64{"u2": 0, "u1": 9}}}
	right := cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, UserReadSeqs: map[string]uint64{"u1": 9, "u2": 0}}}

	leftBody, err := encodeCMDSyncRequestBinary(left)
	require.NoError(t, err)
	rightBody, err := encodeCMDSyncRequestBinary(right)
	require.NoError(t, err)

	require.Equal(t, leftBody, rightBody)
}

func TestCMDSyncLegacyBinaryCodecDecodesSyncWithoutIntent(t *testing.T) {
	req := cmdSyncRPCRequest{
		Op:    cmdSyncOpSync,
		Query: cmdsync.SyncQuery{UID: "u1", MessageSeq: 7, Limit: 20},
		Ack:   cmdsync.SyncAckCommand{UID: "u1", LastMessageSeq: 9},
	}
	body := make([]byte, 0)
	body = append(body, cmdSyncRPCRequestMagic[:]...)
	body = appendString(body, req.Op)
	body = appendCMDSyncQuery(body, req.Query)
	body = appendCMDSyncAckCommand(body, req.Ack)

	got, err := decodeCMDSyncRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestCMDSyncBinaryCodecKeepsSyncWithoutIntentLegacyShape(t *testing.T) {
	req := cmdSyncRPCRequest{
		Op:    cmdSyncOpSync,
		Query: cmdsync.SyncQuery{UID: "u1", MessageSeq: 7, Limit: 20},
		Ack:   cmdsync.SyncAckCommand{UID: "u1", LastMessageSeq: 9},
	}
	legacy := make([]byte, 0)
	legacy = append(legacy, cmdSyncRPCRequestMagic[:]...)
	legacy = appendString(legacy, req.Op)
	legacy = appendCMDSyncQuery(legacy, req.Query)
	legacy = appendCMDSyncAckCommand(legacy, req.Ack)

	body, err := encodeCMDSyncRequestBinary(req)
	require.NoError(t, err)
	require.Equal(t, legacy, body)
}

func TestCMDIntentAdapterMissingProviderReturnsRejectedStatus(t *testing.T) {
	adapter := New(Options{})

	body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op:     cmdSyncOpPushIntent,
		Intent: validCMDConversationIntent(),
	}))
	require.NoError(t, err)

	resp := mustDecodeCMDSyncResponse(t, body)
	require.Equal(t, rpcStatusRejected, resp.Status)
	require.Contains(t, resp.Error, "cmd conversation intent")
}

func TestCMDIntentAdapterRejectsMalformedIntent(t *testing.T) {
	sink := &recordingCMDIntentSink{}
	adapter := New(Options{CMDConversationIntents: sink})

	body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op: cmdSyncOpPushIntent,
		Intent: cmdsync.ConversationIntent{
			CommandChannelID: "g1",
			ChannelType:      2,
			MessageSeq:       0,
			UserReadSeqs:     map[string]uint64{},
		},
	}))
	require.NoError(t, err)

	resp := mustDecodeCMDSyncResponse(t, body)
	require.Equal(t, rpcStatusRejected, resp.Status)
	require.Empty(t, sink.intents)
}

func TestCMDIntentAdapterRejectsBlankUIDOrReadSeqAboveMessageSeq(t *testing.T) {
	for _, intent := range []cmdsync.ConversationIntent{
		{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, UserReadSeqs: map[string]uint64{" ": 0}},
		{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, UserReadSeqs: map[string]uint64{"u1": 10}},
		{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, UserReadSeqs: map[string]uint64{"": 0, "u1": 0}},
	} {
		sink := &recordingCMDIntentSink{}
		adapter := New(Options{CMDConversationIntents: sink})
		body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: intent}))
		require.NoError(t, err)
		resp := mustDecodeCMDSyncResponse(t, body)
		require.Equal(t, rpcStatusRejected, resp.Status)
		require.Empty(t, sink.intents)
	}
}

func TestCMDIntentAdapterPushDoesNotRequireCMDSyncUsecase(t *testing.T) {
	sink := &recordingCMDIntentSink{}
	adapter := New(Options{CMDConversationIntents: sink})
	intent := validCMDConversationIntent()

	body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op:     cmdSyncOpPushIntent,
		Intent: intent,
	}))
	require.NoError(t, err)

	resp := mustDecodeCMDSyncResponse(t, body)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []cmdsync.ConversationIntent{intent}, sink.intents)
}

func TestCMDIntentAdapterStaleOwnerIsRetryable(t *testing.T) {
	sink := &recordingCMDIntentSink{err: cmdsync.ErrConversationIntentStaleOwner}
	adapter := New(Options{CMDConversationIntents: sink})

	body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op:     cmdSyncOpPushIntent,
		Intent: validCMDConversationIntent(),
	}))
	require.NoError(t, err)

	resp := mustDecodeCMDSyncResponse(t, body)
	require.Equal(t, rpcStatusStaleOwner, resp.Status)
}

func TestCMDIntentClientPushCallsRemoteOwner(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	sink := &recordingCMDIntentSink{}
	_ = New(Options{Cluster: network.cluster(2), CMDConversationIntents: sink})
	client := NewClient(network.cluster(1))
	intent := validCMDConversationIntent()

	err := client.PushCMDConversationIntent(context.Background(), 2, intent)
	require.NoError(t, err)
	require.Equal(t, []cmdsync.ConversationIntent{intent}, sink.intents)
}

func TestCMDIntentClientStaleOwnerErrorIsRetryable(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	_ = New(Options{
		Cluster:                network.cluster(2),
		CMDConversationIntents: &recordingCMDIntentSink{err: cmdsync.ErrConversationIntentStaleOwner},
	})
	client := NewClient(network.cluster(1))

	err := client.PushCMDConversationIntent(context.Background(), 2, validCMDConversationIntent())
	require.Error(t, err)
	require.True(t, errors.Is(err, cmdsync.ErrConversationIntentStaleOwner), "got %v", err)
}

type recordingCMDIntentSink struct {
	intents []cmdsync.ConversationIntent
	err     error
}

func (s *recordingCMDIntentSink) PushIntent(_ context.Context, intent cmdsync.ConversationIntent) error {
	intent.UserReadSeqs = cloneUserReadSeqs(intent.UserReadSeqs)
	s.intents = append(s.intents, intent)
	return s.err
}

func validCMDConversationIntent() cmdsync.ConversationIntent {
	return cmdsync.ConversationIntent{
		CommandChannelID: "g1____cmd",
		ChannelType:      2,
		MessageSeq:       9,
		ActiveAt:         100,
		SenderUID:        "u1",
		UserReadSeqs:     map[string]uint64{"u1": 9, "u2": 0},
	}
}

func cloneUserReadSeqs(in map[string]uint64) map[string]uint64 {
	if in == nil {
		return nil
	}
	out := make(map[string]uint64, len(in))
	for uid, seq := range in {
		out[uid] = seq
	}
	return out
}
