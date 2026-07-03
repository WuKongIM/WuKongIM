package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestConversationFactsBinaryCodecRoundTrip(t *testing.T) {
	req := conversationFactsRequest{
		Op: conversationFactsOpLatest,
		Key: newConversationFactsChannelKey(channel.ChannelID{
			ID:   "g1",
			Type: 2,
		}),
		Keys: []conversationFactsChannelKey{
			newConversationFactsChannelKey(channel.ChannelID{ID: "g1", Type: 2}),
			newConversationFactsChannelKey(channel.ChannelID{ID: "g2", Type: 3}),
		},
		Limit:    5,
		MaxBytes: 1024,
	}
	reqBody, err := encodeConversationFactsRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isConversationFactsRequestBinary(reqBody))

	gotReq, err := decodeConversationFactsRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := conversationFactsResponse{
		Status: rpcStatusOK,
		Messages: []channel.Message{{
			ChannelID:   "g0",
			ChannelType: 1,
			MessageSeq:  8,
			Payload:     []byte("latest"),
		}},
		Entries: []conversationFactsEntry{{
			Key: newConversationFactsChannelKey(channel.ChannelID{ID: "g1", Type: 2}),
			Messages: []channel.Message{{
				ChannelID:   "g1",
				ChannelType: 2,
				MessageSeq:  9,
				Payload:     []byte("recent"),
			}},
		}},
	}
	respBody, err := encodeConversationFactsResponse(resp)
	require.NoError(t, err)
	require.True(t, isConversationFactsResponseBinary(respBody))

	gotResp, err := decodeConversationFactsResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestConversationFactsRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{})

	_, err := adapter.handleConversationFactsRPC(context.Background(), []byte(`{"op":"latest","key":{"ChannelID":"g1","ChannelType":2}}`))
	require.Error(t, err)
}

func TestLoadLatestConversationMessageTreatsNotReadyAsEmpty(t *testing.T) {
	msg, ok, err := loadLatestConversationMessage(context.Background(), notReadyConversationFactsLog{}, channel.ChannelID{ID: "g1", Type: 2}, 1024)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, channel.Message{}, msg)
}

func TestLoadRecentConversationMessagesTreatsNotReadyAsEmpty(t *testing.T) {
	msgs, err := loadRecentConversationMessages(context.Background(), notReadyConversationFactsLog{}, channel.ChannelID{ID: "g1", Type: 2}, 10, 1024)
	require.NoError(t, err)
	require.Nil(t, msgs)
}

func TestConversationFactsRPCRefreshesStaleMetaForBatchRecentLoads(t *testing.T) {
	log := &refreshableConversationFactsLog{
		status: channel.ChannelRuntimeStatus{CommittedSeq: 7},
		fetch: channel.FetchResult{Messages: []channel.Message{{
			ChannelID:   "g1",
			ChannelType: 2,
			MessageSeq:  7,
		}}},
	}
	refresher := &refreshingConversationFactsMetaRefresher{
		meta: channel.Meta{ID: channel.ChannelID{ID: "g1", Type: 2}},
		onRefresh: func() {
			log.markRefreshed()
		},
	}
	adapter := New(Options{
		ChannelLog:  log,
		ChannelMeta: refresher,
	})

	body := mustEncodeConversationFactsRequest(t, conversationFactsRequest{
		Op: conversationFactsOpRecent,
		Keys: []conversationFactsChannelKey{
			newConversationFactsChannelKey(channel.ChannelID{ID: "g1", Type: 2}),
		},
		Limit:    1,
		MaxBytes: 1024,
	})

	respBody, err := adapter.handleConversationFactsRPC(context.Background(), body)
	require.NoError(t, err)

	resp, err := decodeConversationFactsResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, []channel.ChannelID{{ID: "g1", Type: 2}}, refresher.calls)
	require.Len(t, resp.Entries, 1)
	require.Len(t, resp.Entries[0].Messages, 1)
	require.Equal(t, uint64(7), resp.Entries[0].Messages[0].MessageSeq)
}

type notReadyConversationFactsLog struct{}

func (notReadyConversationFactsLog) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return channel.ChannelRuntimeStatus{}, channel.ErrNotReady
}

func (notReadyConversationFactsLog) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return channel.FetchResult{}, channel.ErrNotReady
}

func (notReadyConversationFactsLog) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return channel.AppendResult{}, channel.ErrNotReady
}

func (notReadyConversationFactsLog) AppendBatch(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return channel.AppendBatchResult{}, channel.ErrNotReady
}

type refreshableConversationFactsLog struct {
	status    channel.ChannelRuntimeStatus
	fetch     channel.FetchResult
	refreshed bool
}

func (l *refreshableConversationFactsLog) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	if !l.refreshed {
		return channel.ChannelRuntimeStatus{}, channel.ErrStaleMeta
	}
	return l.status, nil
}

func (l *refreshableConversationFactsLog) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	if !l.refreshed {
		return channel.FetchResult{}, channel.ErrStaleMeta
	}
	return l.fetch, nil
}

func (l *refreshableConversationFactsLog) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return channel.AppendResult{}, nil
}

func (l *refreshableConversationFactsLog) AppendBatch(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return channel.AppendBatchResult{}, nil
}

func (l *refreshableConversationFactsLog) markRefreshed() {
	l.refreshed = true
}

type refreshingConversationFactsMetaRefresher struct {
	meta      channel.Meta
	err       error
	calls     []channel.ChannelID
	onRefresh func()
}

func (r *refreshingConversationFactsMetaRefresher) RefreshChannelMeta(_ context.Context, id channel.ChannelID) (channel.Meta, error) {
	r.calls = append(r.calls, id)
	if r.onRefresh != nil {
		r.onRefresh()
	}
	return r.meta, r.err
}

func mustEncodeConversationFactsRequest(t *testing.T, req conversationFactsRequest) []byte {
	t.Helper()
	body, err := encodeConversationFactsRequestBinary(req)
	require.NoError(t, err)
	return body
}
