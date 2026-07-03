package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestCMDSyncBinaryCodecRoundTrip(t *testing.T) {
	req := cmdSyncRPCRequest{
		Op: cmdSyncOpSync,
		Query: cmdsync.SyncQuery{
			UID:        "u1",
			MessageSeq: 7,
			Limit:      20,
		},
		Ack: cmdsync.SyncAckCommand{
			UID:            "u1",
			LastMessageSeq: 9,
		},
	}
	reqBody, err := encodeCMDSyncRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isCMDSyncRequestBinary(reqBody))

	gotReq, err := decodeCMDSyncRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := cmdSyncRPCResponse{
		Status: rpcStatusOK,
		Error:  "ignored when ok",
		Messages: []channel.Message{{
			MessageID:   101,
			MessageSeq:  11,
			Framer:      frame.Framer{NoPersist: false, RedDot: true, SyncOnce: true, DUP: true, End: true, FrameSize: 99},
			Setting:     frame.Setting(3),
			MsgKey:      "mk",
			Expire:      12,
			ClientSeq:   13,
			ClientMsgNo: "cmn",
			StreamNo:    "sn",
			StreamID:    14,
			StreamFlag:  frame.StreamFlag(2),
			Timestamp:   15,
			ChannelID:   "g1____cmd",
			ChannelType: frame.ChannelTypeGroup,
			Topic:       "topic",
			FromUID:     "u2",
			Payload:     []byte("payload"),
		}},
	}
	respBody, err := encodeCMDSyncResponse(resp)
	require.NoError(t, err)
	require.True(t, isCMDSyncResponseBinary(respBody))

	gotResp, err := decodeCMDSyncResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestCMDSyncAdapterHandlerCallsLocalUsecase(t *testing.T) {
	uc := &recordingCMDSyncUsecase{
		syncResult: cmdsync.SyncResult{Messages: []channel.Message{{MessageSeq: 8, ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup}}},
	}
	adapter := New(Options{CMDSync: uc})

	syncBody, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op:    cmdSyncOpSync,
		Query: cmdsync.SyncQuery{UID: "u1", MessageSeq: 3, Limit: 50},
	}))
	require.NoError(t, err)
	syncResp := mustDecodeCMDSyncResponse(t, syncBody)
	require.Equal(t, rpcStatusOK, syncResp.Status)
	require.Equal(t, []channel.Message{{MessageSeq: 8, ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup}}, syncResp.Messages)
	require.Equal(t, []cmdsync.SyncQuery{{UID: "u1", MessageSeq: 3, Limit: 50}}, uc.syncQueries)

	ackBody, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op:  cmdSyncOpSyncAck,
		Ack: cmdsync.SyncAckCommand{UID: "u1", LastMessageSeq: 8},
	}))
	require.NoError(t, err)
	ackResp := mustDecodeCMDSyncResponse(t, ackBody)
	require.Equal(t, rpcStatusOK, ackResp.Status)
	require.Equal(t, []cmdsync.SyncAckCommand{{UID: "u1", LastMessageSeq: 8}}, uc.acks)
}

func TestCMDSyncAdapterMissingProviderReturnsRejectedStatus(t *testing.T) {
	adapter := New(Options{})

	body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{
		Op:    cmdSyncOpSync,
		Query: cmdsync.SyncQuery{UID: "u1"},
	}))
	require.NoError(t, err)

	resp := mustDecodeCMDSyncResponse(t, body)
	require.Equal(t, rpcStatusRejected, resp.Status)
	require.Contains(t, resp.Error, "cmd sync usecase not configured")
}

func TestCMDSyncClientSyncCMDCallsRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	uc := &recordingCMDSyncUsecase{
		syncResult: cmdsync.SyncResult{Messages: []channel.Message{{MessageSeq: 9, ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup, Payload: []byte("cmd")}}},
	}
	_ = New(Options{Cluster: network.cluster(2), CMDSync: uc})
	client := NewClient(network.cluster(1))

	got, err := client.SyncCMD(context.Background(), 2, cmdsync.SyncQuery{UID: "u1", MessageSeq: 7, Limit: 10})
	require.NoError(t, err)

	require.Equal(t, []channel.Message{{MessageSeq: 9, ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup, Payload: []byte("cmd")}}, got.Messages)
	require.Equal(t, []cmdsync.SyncQuery{{UID: "u1", MessageSeq: 7, Limit: 10}}, uc.syncQueries)
}

func TestCMDSyncClientSyncAckCMDCallsRemoteNode(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	uc := &recordingCMDSyncUsecase{}
	_ = New(Options{Cluster: network.cluster(2), CMDSync: uc})
	client := NewClient(network.cluster(1))

	err := client.SyncAckCMD(context.Background(), 2, cmdsync.SyncAckCommand{UID: "u1", LastMessageSeq: 11})
	require.NoError(t, err)

	require.Equal(t, []cmdsync.SyncAckCommand{{UID: "u1", LastMessageSeq: 11}}, uc.acks)
}

func TestCMDSyncClientReturnsRemoteRejectedError(t *testing.T) {
	network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
	_ = New(Options{Cluster: network.cluster(2)})
	client := NewClient(network.cluster(1))

	_, err := client.SyncCMD(context.Background(), 2, cmdsync.SyncQuery{UID: "u1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cmd sync usecase not configured")
}

type recordingCMDSyncUsecase struct {
	syncQueries []cmdsync.SyncQuery
	acks        []cmdsync.SyncAckCommand
	syncResult  cmdsync.SyncResult
	syncErr     error
	ackErr      error
}

func (u *recordingCMDSyncUsecase) Sync(_ context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	u.syncQueries = append(u.syncQueries, query)
	if u.syncErr != nil {
		return cmdsync.SyncResult{}, u.syncErr
	}
	return u.syncResult, nil
}

func (u *recordingCMDSyncUsecase) SyncAck(_ context.Context, cmd cmdsync.SyncAckCommand) error {
	u.acks = append(u.acks, cmd)
	if u.ackErr != nil {
		return u.ackErr
	}
	return nil
}

func mustEncodeCMDSyncRequest(t *testing.T, req cmdSyncRPCRequest) []byte {
	t.Helper()
	body, err := encodeCMDSyncRequestBinary(req)
	require.NoError(t, err)
	return body
}

func mustDecodeCMDSyncResponse(t *testing.T, body []byte) cmdSyncRPCResponse {
	t.Helper()
	resp, err := decodeCMDSyncResponse(body)
	require.NoError(t, err)
	return resp
}
