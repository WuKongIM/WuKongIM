package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestConversationFactsRequestJSONPreservesLegacyChannelKeyFields(t *testing.T) {
	body := mustMarshal(t, conversationFactsRequest{
		Op: conversationFactsOpLatest,
		Key: newConversationFactsChannelKey(channel.ChannelID{
			ID:   "g1",
			Type: 2,
		}),
		Keys: []conversationFactsChannelKey{
			newConversationFactsChannelKey(channel.ChannelID{ID: "g1", Type: 2}),
			newConversationFactsChannelKey(channel.ChannelID{ID: "g2", Type: 3}),
		},
	})

	require.JSONEq(t, `{
		"op":"latest",
		"key":{"ChannelID":"g1","ChannelType":2},
		"keys":[
			{"ChannelID":"g1","ChannelType":2},
			{"ChannelID":"g2","ChannelType":3}
		]
	}`, string(body))
	require.NotContains(t, string(body), `"ID":"g1"`)
	require.NotContains(t, string(body), `"Type":2`)
}

func TestConversationFactsRequestJSONDecodesLegacyChannelKeyFields(t *testing.T) {
	body := []byte(`{
		"op":"recent",
		"key":{"ChannelID":"g1","ChannelType":2},
		"keys":[{"ChannelID":"g2","ChannelType":3}],
		"limit":5,
		"max_bytes":1024
	}`)

	var req conversationFactsRequest
	require.NoError(t, json.Unmarshal(body, &req))
	require.Equal(t, channel.ChannelID{ID: "g1", Type: 2}, req.Key.channelID())
	require.Equal(t, []conversationFactsChannelKey{
		newConversationFactsChannelKey(channel.ChannelID{ID: "g2", Type: 3}),
	}, req.Keys)
	require.Equal(t, 5, req.Limit)
	require.Equal(t, 1024, req.MaxBytes)
}

func TestConversationFactsResponseJSONPreservesLegacyBatchEntryKeyFields(t *testing.T) {
	body, err := encodeConversationFactsResponse(conversationFactsResponse{
		Status: rpcStatusOK,
		Entries: []conversationFactsEntry{{
			Key: newConversationFactsChannelKey(channel.ChannelID{ID: "g1", Type: 2}),
			Messages: []channel.Message{{
				ChannelID:   "g1",
				ChannelType: 2,
				MessageSeq:  9,
			}},
		}},
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, rpcStatusOK, payload["status"])
	entries, ok := payload["entries"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
	entry, ok := entries[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"ChannelID":   "g1",
		"ChannelType": float64(2),
	}, entry["key"])
	require.NotContains(t, string(body), `"ID":"g1"`)
	require.NotContains(t, string(body), `"Type":2`)
}

func TestConversationFactsResponseJSONDecodesLegacyBatchEntryKeyFields(t *testing.T) {
	body := []byte(`{
		"status":"ok",
		"entries":[{
			"key":{"ChannelID":"g1","ChannelType":2},
			"messages":[{"ChannelID":"g1","ChannelType":2,"MessageSeq":9}]
		}]
	}`)

	resp, err := decodeConversationFactsResponse(body)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	require.Equal(t, channel.ChannelID{ID: "g1", Type: 2}, resp.Entries[0].Key.channelID())
	require.Len(t, resp.Entries[0].Messages, 1)
	require.Equal(t, uint64(9), resp.Entries[0].Messages[0].MessageSeq)
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

	body := mustMarshal(t, conversationFactsRequest{
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
