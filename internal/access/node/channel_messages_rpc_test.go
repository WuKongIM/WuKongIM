package node

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestChannelMessagesRPCReturnsMaxMessageSeq(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	require.NoError(t, store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 42}))

	adapter := New(Options{
		LocalNodeID:  2,
		ChannelLogDB: engine,
		ChannelMeta: &stubNodeMetaRefresher{
			meta: channel.Meta{
				ID:     id,
				Leader: 2,
			},
		},
	})

	body := mustEncodeChannelMessagesRequest(t, channelMessagesRequest{
		Query: ChannelMessagesQuery{
			ChannelID:  id,
			MaxSeqOnly: true,
		},
	})
	respBody, err := adapter.handleChannelMessagesRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeChannelMessagesResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, uint64(42), resp.Page.MaxMessageSeq)
	require.Empty(t, resp.Page.Messages)
	require.False(t, resp.Page.HasMore)
}

func TestChannelMessagesBinaryCodecRoundTrip(t *testing.T) {
	req := channelMessagesRequest{Query: ChannelMessagesQuery{
		ChannelID:       channel.ChannelID{ID: "messages-binary", Type: frame.ChannelTypeGroup},
		SyncMode:        true,
		MaxSeqOnly:      true,
		MinAvailableSeq: 3,
		BeforeSeq:       10,
		StartSeq:        2,
		EndSeq:          9,
		Limit:           50,
		PullMode:        uint8(channelhandler.SyncPullModeDown),
		MessageID:       101,
		ClientMsgNo:     "client-1",
	}}
	reqBody, err := encodeChannelMessagesRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelMessagesRequestBinary(reqBody))

	gotReq, err := decodeChannelMessagesRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelMessagesResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Page: ChannelMessagesPage{
			Messages: []channel.Message{{
				MessageID:   101,
				MessageSeq:  8,
				ChannelID:   "messages-binary",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				ClientMsgNo: "client-1",
				Payload:     []byte("payload"),
			}},
			HasMore:       true,
			NextBeforeSeq: 7,
			MaxMessageSeq: 9,
		},
	}
	respBody, err := encodeChannelMessagesResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelMessagesResponseBinary(respBody))

	gotResp, err := decodeChannelMessagesResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelMessagesRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{ChannelLogDB: openChannelMessagesRPCTestEngine(t)})

	_, err := adapter.handleChannelMessagesRPC(context.Background(), []byte(`{"query":{"channel_id":{"id":"messages-json","type":2}}}`))
	require.Error(t, err)
}

func TestChannelMessagesRPCQueryUsesRetentionFloor(t *testing.T) {
	id := channel.ChannelID{ID: "g-retained-query", Type: 2}
	engine := openChannelMessagesRPCTestEngine(t)
	appendChannelMessagesRPCTestRows(t, engine, id, 5)

	adapter := New(Options{
		LocalNodeID:  2,
		ChannelLogDB: engine,
		ChannelMeta: &stubNodeMetaRefresher{
			meta: channel.Meta{
				ID:     id,
				Leader: 2,
			},
		},
	})

	body := mustEncodeChannelMessagesRequest(t, channelMessagesRequest{
		Query: ChannelMessagesQuery{
			ChannelID:       id,
			Limit:           10,
			MinAvailableSeq: 4,
		},
	})
	respBody, err := adapter.handleChannelMessagesRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeChannelMessagesResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []uint64{5, 4}, channelMessagesRPCSeqs(resp.Page.Messages))
}

func TestChannelMessagesRPCQueryUsesAuthoritativeRetentionFloor(t *testing.T) {
	id := channel.ChannelID{ID: "g-authoritative-retained-query", Type: 2}
	engine := openChannelMessagesRPCTestEngine(t)
	appendChannelMessagesRPCTestRows(t, engine, id, 8)

	tests := []struct {
		name            string
		minAvailableSeq uint64
	}{
		{name: "omitted"},
		{name: "lowered", minAvailableSeq: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := New(Options{
				LocalNodeID:  2,
				ChannelLogDB: engine,
				ChannelMeta: &stubNodeMetaRefresher{
					meta: channel.Meta{
						ID:                  id,
						Leader:              2,
						RetentionThroughSeq: 5,
					},
				},
			})

			body := mustEncodeChannelMessagesRequest(t, channelMessagesRequest{
				Query: ChannelMessagesQuery{
					ChannelID:       id,
					Limit:           10,
					MinAvailableSeq: tt.minAvailableSeq,
				},
			})
			respBody, err := adapter.handleChannelMessagesRPC(context.Background(), body)
			require.NoError(t, err)
			resp, err := decodeChannelMessagesResponse(respBody)
			require.NoError(t, err)

			require.Equal(t, rpcStatusOK, resp.Status)
			require.Equal(t, []uint64{8, 7, 6}, channelMessagesRPCSeqs(resp.Page.Messages))
		})
	}
}

func TestChannelMessagesRPCSyncUsesRetentionFloor(t *testing.T) {
	id := channel.ChannelID{ID: "g-retained-sync", Type: 2}
	engine := openChannelMessagesRPCTestEngine(t)
	appendChannelMessagesRPCTestRows(t, engine, id, 5)

	adapter := New(Options{
		LocalNodeID:  2,
		ChannelLogDB: engine,
		ChannelMeta: &stubNodeMetaRefresher{
			meta: channel.Meta{
				ID:     id,
				Leader: 2,
			},
		},
	})

	body := mustEncodeChannelMessagesRequest(t, channelMessagesRequest{
		Query: ChannelMessagesQuery{
			ChannelID:       id,
			SyncMode:        true,
			StartSeq:        1,
			EndSeq:          6,
			Limit:           10,
			PullMode:        uint8(channelhandler.SyncPullModeUp),
			MinAvailableSeq: 4,
		},
	})
	respBody, err := adapter.handleChannelMessagesRPC(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeChannelMessagesResponse(respBody)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []uint64{4, 5}, channelMessagesRPCSeqs(resp.Page.Messages))
}

func TestChannelMessagesRPCSyncUsesAuthoritativeRetentionFloor(t *testing.T) {
	id := channel.ChannelID{ID: "g-authoritative-retained-sync", Type: 2}
	engine := openChannelMessagesRPCTestEngine(t)
	appendChannelMessagesRPCTestRows(t, engine, id, 8)

	tests := []struct {
		name            string
		minAvailableSeq uint64
	}{
		{name: "omitted"},
		{name: "lowered", minAvailableSeq: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := New(Options{
				LocalNodeID:  2,
				ChannelLogDB: engine,
				ChannelMeta: &stubNodeMetaRefresher{
					meta: channel.Meta{
						ID:                  id,
						Leader:              2,
						RetentionThroughSeq: 5,
					},
				},
			})

			body := mustEncodeChannelMessagesRequest(t, channelMessagesRequest{
				Query: ChannelMessagesQuery{
					ChannelID:       id,
					SyncMode:        true,
					StartSeq:        1,
					EndSeq:          9,
					Limit:           10,
					PullMode:        uint8(channelhandler.SyncPullModeUp),
					MinAvailableSeq: tt.minAvailableSeq,
				},
			})
			respBody, err := adapter.handleChannelMessagesRPC(context.Background(), body)
			require.NoError(t, err)
			resp, err := decodeChannelMessagesResponse(respBody)
			require.NoError(t, err)

			require.Equal(t, rpcStatusOK, resp.Status)
			require.Equal(t, []uint64{6, 7, 8}, channelMessagesRPCSeqs(resp.Page.Messages))
		})
	}
}

func openChannelMessagesRPCTestEngine(t *testing.T) *channelstore.Engine {
	t.Helper()
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	return engine
}

func appendChannelMessagesRPCTestRows(t *testing.T, engine *channelstore.Engine, id channel.ChannelID, count int) {
	t.Helper()
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	records := make([]channel.Record, 0, count)
	for i := 1; i <= count; i++ {
		payload := encodeChannelMessagesRPCTestPayload(channel.Message{
			MessageID:   uint64(200 + i),
			ChannelID:   id.ID,
			ChannelType: id.Type,
			FromUID:     "u1",
			Payload:     []byte{byte(i)},
		})
		records = append(records, channel.Record{Payload: payload, SizeBytes: len(payload)})
	}
	_, err := store.Append(records)
	require.NoError(t, err)
	require.NoError(t, store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: uint64(count)}))
}

func encodeChannelMessagesRPCTestPayload(msg channel.Message) []byte {
	payload := []byte{channel.DurableMessageCodecVersion}
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, encodeChannelMessagesRPCTestFramerFlags(msg.Framer))
	payload = append(payload, byte(msg.Setting))
	payload = append(payload, byte(msg.StreamFlag))
	payload = append(payload, msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, msg.Expire)
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, hashChannelMessagesRPCTestPayload(msg.Payload))
	payload = appendChannelMessagesRPCTestField(payload, []byte(msg.MsgKey))
	payload = appendChannelMessagesRPCTestField(payload, []byte(msg.ClientMsgNo))
	payload = appendChannelMessagesRPCTestField(payload, []byte(msg.StreamNo))
	payload = appendChannelMessagesRPCTestField(payload, []byte(msg.ChannelID))
	payload = appendChannelMessagesRPCTestField(payload, []byte(msg.Topic))
	payload = appendChannelMessagesRPCTestField(payload, []byte(msg.FromUID))
	payload = appendChannelMessagesRPCTestField(payload, msg.Payload)
	return payload
}

func encodeChannelMessagesRPCTestFramerFlags(framer frame.Framer) uint8 {
	var flags uint8
	if framer.NoPersist {
		flags |= 1 << 0
	}
	if framer.RedDot {
		flags |= 1 << 1
	}
	if framer.SyncOnce {
		flags |= 1 << 2
	}
	if framer.DUP {
		flags |= 1 << 3
	}
	if framer.HasServerVersion {
		flags |= 1 << 4
	}
	if framer.End {
		flags |= 1 << 5
	}
	return flags
}

func hashChannelMessagesRPCTestPayload(payload []byte) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write(payload)
	return hasher.Sum64()
}

func appendChannelMessagesRPCTestField(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func mustEncodeChannelMessagesRequest(t *testing.T, req channelMessagesRequest) []byte {
	t.Helper()
	body, err := encodeChannelMessagesRequestBinary(req)
	require.NoError(t, err)
	return body
}

func channelMessagesRPCSeqs(messages []channel.Message) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.MessageSeq)
	}
	return seqs
}
