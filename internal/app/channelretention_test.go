package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/fnv"
	"testing"
	"time"

	appretention "github.com/WuKongIM/WuKongIM/internal/runtime/channelretention"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelRetentionChannelsListsParsedChannelIDsWithBatchLimit(t *testing.T) {
	firstID := channel.ChannelID{ID: "first", Type: 1}
	secondID := channel.ChannelID{ID: "second", Type: 2}
	lister := &appChannelRetentionChannels{
		keys: appChannelRetentionKeyListerFunc(func() ([]channel.ChannelKey, error) {
			return []channel.ChannelKey{
				channelhandler.KeyFromChannelID(firstID),
				channelhandler.KeyFromChannelID(secondID),
			}, nil
		}),
		batchSize: 1,
	}

	got, err := lister.ListRetentionChannels(context.Background())

	require.NoError(t, err)
	require.Equal(t, []appretention.Channel{{
		Key: channelhandler.KeyFromChannelID(firstID),
		ID:  firstID,
	}}, got)
}

func TestChannelRetentionChannelsRotatesBatchAcrossPasses(t *testing.T) {
	firstID := channel.ChannelID{ID: "first", Type: 1}
	secondID := channel.ChannelID{ID: "second", Type: 1}
	lister := &appChannelRetentionChannels{
		keys: appChannelRetentionKeyListerFunc(func() ([]channel.ChannelKey, error) {
			return []channel.ChannelKey{
				channelhandler.KeyFromChannelID(firstID),
				channelhandler.KeyFromChannelID(secondID),
			}, nil
		}),
		batchSize: 1,
	}

	_, err := lister.ListRetentionChannels(context.Background())
	require.NoError(t, err)
	got, err := lister.ListRetentionChannels(context.Background())

	require.NoError(t, err)
	require.Equal(t, []appretention.Channel{{
		Key: channelhandler.KeyFromChannelID(secondID),
		ID:  secondID,
	}}, got)
}

func TestChannelRetentionChannelsRejectsInvalidChannelKey(t *testing.T) {
	lister := &appChannelRetentionChannels{
		keys: appChannelRetentionKeyListerFunc(func() ([]channel.ChannelKey, error) {
			return []channel.ChannelKey{"invalid"}, nil
		}),
	}

	_, err := lister.ListRetentionChannels(context.Background())

	require.ErrorIs(t, err, channel.ErrInvalidMeta)
}

func TestChannelRetentionStoreProviderAdaptsChannelStore(t *testing.T) {
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	id := channel.ChannelID{ID: "retention-store", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	st := engine.ForChannel(key, id)
	_, err = st.Append([]channel.Record{
		{ID: 1, Index: 1, Payload: retentionRecordPayload(1, time.Unix(100, 0))},
	})
	require.NoError(t, err)
	require.NoError(t, st.StoreCommittedDispatchCursor("committed", 1))

	provider := appChannelRetentionStores{engine: engine}
	wrapped, err := provider.StoreForChannel(context.Background(), appretention.Channel{Key: key, ID: id})
	require.NoError(t, err)

	scan, err := wrapped.ScanExpiredMessagePrefix(1, time.Unix(200, 0), 10)
	require.NoError(t, err)
	require.Equal(t, appretention.ScanResult{FromSeq: 1, ThroughSeq: 1, Count: 1}, scan)
	cursor, err := wrapped.ConfirmCommittedDispatchCursorDurable("committed", 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor)
}

func TestChannelRetentionRuntimeAndMetadataAdaptersDelegate(t *testing.T) {
	key := channel.ChannelKey("channel/1/test")
	view := channel.RetentionView{ChannelKey: key, RetentionThroughSeq: 7}
	runtime := &fakeAppChannelRetentionRuntime{view: view}
	metadata := &fakeAppChannelRetentionMetadata{}

	got, err := appChannelRetentionRuntime{runtime: runtime}.RetentionView(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, view, got)

	require.NoError(t, appChannelRetentionRuntime{runtime: runtime}.ApplyRetentionBoundary(context.Background(), key, 9))
	require.Equal(t, uint64(9), runtime.applied)

	req := metadb.ChannelRetentionAdvance{ChannelID: "test", ChannelType: 1, RetentionThroughSeq: 9}
	require.NoError(t, appChannelRetentionMetadata{store: metadata}.AdvanceChannelRetentionThroughSeq(context.Background(), req))
	require.Equal(t, req, metadata.advance)
}

func TestChannelRetentionWorkerSingleNodeClusterFlow(t *testing.T) {
	now := time.Unix(10_000, 0)
	engine, err := channelstore.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	id := channel.ChannelID{ID: "single-node-retention", Type: 1}
	key := channelhandler.KeyFromChannelID(id)
	st := engine.ForChannel(key, id)
	records := []channel.Record{
		retentionRecord(1, now.Add(-2*time.Hour)),
		retentionRecord(2, now.Add(-90*time.Minute)),
		retentionRecord(3, now.Add(-10*time.Minute)),
	}
	_, err = st.Append(records)
	require.NoError(t, err)
	require.NoError(t, st.StoreCheckpoint(channel.Checkpoint{Epoch: 7, HW: 3}))
	require.NoError(t, st.StoreCommittedDispatchCursor(appChannelRetentionCursorName, 3))

	metaDB, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, metaDB.Close()) })
	metaStore := metaDB.ForSlot(1)
	require.NoError(t, metaStore.UpsertChannelRuntimeMeta(context.Background(), metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 7,
		LeaderEpoch:  8,
		Replicas:     []uint64{1},
		ISR:          []uint64{1},
		Leader:       1,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}))

	runtime := &fakeAppChannelRetentionRuntime{
		view: channel.RetentionView{
			ChannelKey:          key,
			Epoch:               7,
			LeaderEpoch:         8,
			Leader:              1,
			LeaseUntil:          now.Add(time.Minute),
			HW:                  3,
			CheckpointHW:        3,
			CommitReady:         true,
			RetentionThroughSeq: 0,
			MinAvailableSeq:     1,
			MinISRMatchOffset:   3,
		},
	}
	worker := appretention.NewWorker(appretention.Config{
		Channels: &appChannelRetentionChannels{
			keys:      engine,
			batchSize: 128,
		},
		Stores:          appChannelRetentionStores{engine: engine},
		Runtime:         appChannelRetentionRuntime{runtime: runtime},
		Metadata:        appChannelRetentionMetadata{store: metaStore},
		LocalNodeID:     1,
		TTL:             time.Hour,
		ScanInterval:    time.Hour,
		MaxTrimMessages: 10,
		CursorName:      appChannelRetentionCursorName,
		Now:             func() time.Time { return now },
	})

	require.NoError(t, worker.RunOnce(context.Background()))
	gotMeta, err := metaStore.GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, uint64(2), gotMeta.RetentionThroughSeq)
	require.Equal(t, now.UnixMilli(), gotMeta.RetentionUpdatedAtMS)
	require.Equal(t, uint64(2), runtime.applied)

	minAvailableSeq := channel.EffectiveMinAvailableSeq(gotMeta.RetentionThroughSeq, 0)
	retainedOnlyPage, err := channelhandler.SyncMessages(engine, 3, channelhandler.SyncMessagesRequest{
		ChannelID:       id,
		StartSeq:        1,
		EndSeq:          2,
		Limit:           10,
		PullMode:        channelhandler.SyncPullModeUp,
		MinAvailableSeq: minAvailableSeq,
	})
	require.NoError(t, err)
	require.Empty(t, retainedOnlyPage.Messages)

	page, err := channelhandler.SyncMessages(engine, 3, channelhandler.SyncMessagesRequest{
		ChannelID:       id,
		StartSeq:        1,
		EndSeq:          4,
		Limit:           10,
		PullMode:        channelhandler.SyncPullModeUp,
		MinAvailableSeq: minAvailableSeq,
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{3}, channelRetentionMessageSeqs(page.Messages))
}

func retentionRecord(id uint64, ts time.Time) channel.Record {
	payload := retentionRecordPayload(id, ts)
	return channel.Record{ID: id, Payload: payload, SizeBytes: len(payload)}
}

func retentionRecordPayload(id uint64, ts time.Time) []byte {
	body := []byte("payload")
	var buf bytes.Buffer
	_ = buf.WriteByte(channel.DurableMessageCodecVersion)
	_ = binary.Write(&buf, binary.BigEndian, id)
	_ = buf.WriteByte(0) // framer flags
	_ = buf.WriteByte(0) // setting
	_ = buf.WriteByte(0) // stream flag
	_ = buf.WriteByte(1) // channel type
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))
	_ = binary.Write(&buf, binary.BigEndian, uint64(0))
	_ = binary.Write(&buf, binary.BigEndian, uint64(0))
	_ = binary.Write(&buf, binary.BigEndian, int32(ts.Unix()))
	_ = binary.Write(&buf, binary.BigEndian, hashRetentionPayload(body))
	writeRetentionRecordBytes(&buf, nil)
	writeRetentionRecordBytes(&buf, []byte("client"))
	writeRetentionRecordBytes(&buf, nil)
	writeRetentionRecordBytes(&buf, []byte("retention-store"))
	writeRetentionRecordBytes(&buf, nil)
	writeRetentionRecordBytes(&buf, nil)
	writeRetentionRecordBytes(&buf, body)
	return buf.Bytes()
}

func writeRetentionRecordBytes(buf *bytes.Buffer, value []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint32(len(value)))
	_, _ = buf.Write(value)
}

func hashRetentionPayload(payload []byte) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write(payload)
	return hasher.Sum64()
}

func channelRetentionMessageSeqs(messages []channel.Message) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.MessageSeq)
	}
	return seqs
}

type fakeAppChannelRetentionRuntime struct {
	view    channel.RetentionView
	applied uint64
}

func (f *fakeAppChannelRetentionRuntime) RetentionView(key channel.ChannelKey) (channel.RetentionView, error) {
	f.view.ChannelKey = key
	return f.view, nil
}

func (f *fakeAppChannelRetentionRuntime) ApplyRetentionBoundary(_ context.Context, _ channel.ChannelKey, throughSeq uint64) error {
	f.applied = throughSeq
	return nil
}

type fakeAppChannelRetentionMetadata struct {
	advance metadb.ChannelRetentionAdvance
}

func (f *fakeAppChannelRetentionMetadata) AdvanceChannelRetentionThroughSeq(_ context.Context, req metadb.ChannelRetentionAdvance) error {
	f.advance = req
	return nil
}
