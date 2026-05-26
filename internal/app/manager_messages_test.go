package app

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestManagerMessageReaderMaxMessageSeqReadsLocalCommittedHW(t *testing.T) {
	id := channel.ChannelID{ID: "g1", Type: 2}
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	require.NoError(t, store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 55}))

	reader := managerMessageReader{
		localNodeID: 1,
		channelLog:  engine,
		metas: managerMessageMetasFake{
			meta: metadb.ChannelRuntimeMeta{
				ChannelID:   id.ID,
				ChannelType: int64(id.Type),
				Leader:      1,
			},
		},
	}

	got, err := reader.MaxMessageSeq(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, uint64(55), got)
}

func TestManagerMessageReaderMaxMessageSeqForMetaReadsLocalCommittedHW(t *testing.T) {
	id := channel.ChannelID{ID: "g-meta-local", Type: 2}
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	require.NoError(t, store.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 77}))
	metas := &managerMessageMetasCapture{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1}}
	reader := managerMessageReader{localNodeID: 1, channelLog: engine, metas: metas}

	got, err := reader.MaxMessageSeqForMeta(context.Background(), metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 1})

	require.NoError(t, err)
	require.Equal(t, uint64(77), got)
	require.Zero(t, metas.calls)
}

func TestManagerMessageReaderMaxMessageSeqForMetaQueriesRemoteLeader(t *testing.T) {
	id := channel.ChannelID{ID: "g-meta-remote", Type: 2}
	remote := &managerMessageRemoteCapture{page: accessnode.ChannelMessagesPage{MaxMessageSeq: 88}}
	metas := &managerMessageMetasCapture{meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 9}}
	reader := managerMessageReader{localNodeID: 1, channelLog: openManagerMessageTestEngine(t), metas: metas, remote: remote}

	got, err := reader.MaxMessageSeqForMeta(context.Background(), metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), Leader: 9})

	require.NoError(t, err)
	require.Equal(t, uint64(88), got)
	require.Zero(t, metas.calls)
	require.Len(t, remote.requests, 1)
	require.Equal(t, id, remote.requests[0].ChannelID)
	require.True(t, remote.requests[0].MaxSeqOnly)
}

func TestManagerMessageReaderMaxMessageSeqForMetaRequiresLeader(t *testing.T) {
	reader := managerMessageReader{localNodeID: 1, channelLog: openManagerMessageTestEngine(t)}

	_, err := reader.MaxMessageSeqForMeta(context.Background(), metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2})

	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
}

func TestManagerMessageReaderLocalReadsUseRetentionFloor(t *testing.T) {
	id := channel.ChannelID{ID: "g-retained-local", Type: 2}
	engine := openManagerMessageTestEngine(t)
	appendManagerMessageTestRows(t, engine, id, 5)

	reader := managerMessageReader{
		localNodeID: 1,
		channelLog:  engine,
		metas: managerMessageMetasFake{
			meta: metadb.ChannelRuntimeMeta{
				ChannelID:           id.ID,
				ChannelType:         int64(id.Type),
				Leader:              1,
				RetentionThroughSeq: 2,
			},
		},
	}

	queryPage, err := reader.QueryMessages(context.Background(), managementusecase.MessageQueryRequest{
		ChannelID: id,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 4, 3}, managerMessageSeqs(queryPage.Items))

	syncPage, err := reader.SyncMessages(context.Background(), messageusecase.ChannelMessageQuery{
		ChannelID: id,
		StartSeq:  1,
		EndSeq:    6,
		Limit:     10,
		PullMode:  messageusecase.PullModeUp,
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 4, 5}, managerMessageSeqs(syncPage.Messages))
}

func TestManagerMessageReaderRemoteReadsForwardRetentionFloor(t *testing.T) {
	id := channel.ChannelID{ID: "g-retained-remote", Type: 2}
	remote := &managerMessageRemoteCapture{}
	reader := managerMessageReader{
		localNodeID: 1,
		channelLog:  openManagerMessageTestEngine(t),
		metas: managerMessageMetasFake{
			meta: metadb.ChannelRuntimeMeta{
				ChannelID:           id.ID,
				ChannelType:         int64(id.Type),
				Leader:              9,
				RetentionThroughSeq: 4,
			},
		},
		remote: remote,
	}

	_, err := reader.QueryMessages(context.Background(), managementusecase.MessageQueryRequest{
		ChannelID: id,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, remote.requests, 1)
	require.Equal(t, uint64(5), remote.requests[0].MinAvailableSeq)

	_, err = reader.SyncMessages(context.Background(), messageusecase.ChannelMessageQuery{
		ChannelID: id,
		StartSeq:  1,
		EndSeq:    8,
		Limit:     10,
		PullMode:  messageusecase.PullModeUp,
	})
	require.NoError(t, err)
	require.Len(t, remote.requests, 2)
	require.Equal(t, uint64(5), remote.requests[1].MinAvailableSeq)
}

type managerMessageMetasFake struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f managerMessageMetasFake) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return f.meta, f.err
}

type managerMessageMetasCapture struct {
	meta  metadb.ChannelRuntimeMeta
	err   error
	calls int
}

func (f *managerMessageMetasCapture) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	f.calls++
	return f.meta, f.err
}

type managerMessageRemoteCapture struct {
	requests []accessnode.ChannelMessagesQuery
	page     accessnode.ChannelMessagesPage
}

func (f *managerMessageRemoteCapture) QueryChannelMessages(_ context.Context, _ uint64, req accessnode.ChannelMessagesQuery) (accessnode.ChannelMessagesPage, error) {
	f.requests = append(f.requests, req)
	return f.page, nil
}

func openManagerMessageTestEngine(t *testing.T) *channelstore.Engine {
	t.Helper()
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	return engine
}

func appendManagerMessageTestRows(t *testing.T, engine *channelstore.Engine, id channel.ChannelID, count int) {
	t.Helper()
	store := engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	records := make([]channel.Record, 0, count)
	for i := 1; i <= count; i++ {
		payload := encodeManagerMessageTestPayload(channel.Message{
			MessageID:   uint64(100 + i),
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

func encodeManagerMessageTestPayload(msg channel.Message) []byte {
	payload := []byte{channel.DurableMessageCodecVersion}
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, encodeManagerMessageTestFramerFlags(msg.Framer))
	payload = append(payload, byte(msg.Setting))
	payload = append(payload, byte(msg.StreamFlag))
	payload = append(payload, msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, msg.Expire)
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, hashManagerMessageTestPayload(msg.Payload))
	payload = appendManagerMessageTestField(payload, []byte(msg.MsgKey))
	payload = appendManagerMessageTestField(payload, []byte(msg.ClientMsgNo))
	payload = appendManagerMessageTestField(payload, []byte(msg.StreamNo))
	payload = appendManagerMessageTestField(payload, []byte(msg.ChannelID))
	payload = appendManagerMessageTestField(payload, []byte(msg.Topic))
	payload = appendManagerMessageTestField(payload, []byte(msg.FromUID))
	payload = appendManagerMessageTestField(payload, msg.Payload)
	return payload
}

func encodeManagerMessageTestFramerFlags(framer frame.Framer) uint8 {
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

func hashManagerMessageTestPayload(payload []byte) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write(payload)
	return hasher.Sum64()
}

func appendManagerMessageTestField(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func managerMessageSeqs(messages []channel.Message) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.MessageSeq)
	}
	return seqs
}
