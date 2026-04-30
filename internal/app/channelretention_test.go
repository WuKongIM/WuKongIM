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
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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
