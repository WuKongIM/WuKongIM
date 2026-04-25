package store

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreAppendPersistsStructuredRowsAndCompatibilityRead(t *testing.T) {
	st := newTestChannelStore(t)
	payload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   11,
		ClientMsgNo: "client-1",
		FromUID:     "u1",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("one"),
	})

	base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
	require.NoError(t, err)
	require.Equal(t, uint64(0), base)

	row, ok, err := getStoredMessageRow(t, st, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(11), row.MessageID)

	records, err := st.Read(0, 1024)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, uint64(11), mustDecodeStoreMessage(t, records[0].Payload).MessageID)
}

func TestEngineReadScopesByChannelKeyAndBudget(t *testing.T) {
	engine := openTestEngine(t)
	first := engine.ForChannel(channel.ChannelKey("channel/1/c1"), channel.ChannelID{ID: "c1", Type: 1})
	second := engine.ForChannel(channel.ChannelKey("channel/1/c2"), channel.ChannelID{ID: "c2", Type: 1})
	mustAppendRecords(t, first, []string{"one", "two"})
	mustAppendRecords(t, second, []string{"zzz"})

	budget := len(mustEncodeStoreMessage(t, channel.Message{
		MessageID:   1,
		ClientMsgNo: "client-1",
		FromUID:     "u1",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("one"),
	}))
	records, err := engine.Read(first.key, 0, 10, budget)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
}

func TestChannelStoreLEOTracksAppendAndTruncate(t *testing.T) {
	st := newTestChannelStore(t)
	firstPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   1,
		ClientMsgNo: "m1",
		FromUID:     "u1",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("one"),
	})
	secondPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   2,
		ClientMsgNo: "m2",
		FromUID:     "u1",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("two"),
	})

	if _, err := st.Append([]channel.Record{
		{Payload: firstPayload, SizeBytes: len(firstPayload)},
		{Payload: secondPayload, SizeBytes: len(secondPayload)},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if got := st.LEO(); got != 2 {
		t.Fatalf("LEO() = %d, want 2", got)
	}

	if err := st.Truncate(1); err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}
	if got := st.LEO(); got != 1 {
		t.Fatalf("LEO() after truncate = %d, want 1", got)
	}

	thirdPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   3,
		ClientMsgNo: "m3",
		FromUID:     "u1",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("three"),
	})
	base, err := st.Append([]channel.Record{{Payload: thirdPayload, SizeBytes: len(thirdPayload)}})
	if err != nil {
		t.Fatalf("Append(after truncate) error = %v", err)
	}
	if base != 1 {
		t.Fatalf("base after truncate = %d, want 1", base)
	}
	if got := st.LEO(); got != 2 {
		t.Fatalf("LEO() after re-append = %d, want 2", got)
	}
}

func TestChannelStoreSyncPreservesTrimmedLogAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	engine, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	key := channel.ChannelKey("channel/1/c1")
	id := channel.ChannelID{ID: "c1", Type: 1}
	st := engine.ForChannel(key, id)

	mustAppendRecords(t, st, []string{"one", "two"})
	if err := st.Truncate(1); err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(reopen) error = %v", err)
	}
	defer reopened.Close()

	reloaded := reopened.ForChannel(key, id)
	if got := reloaded.LEO(); got != 1 {
		t.Fatalf("LEO() = %d, want 1", got)
	}
	records, err := reloaded.Read(0, 1024)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if got := string(mustDecodeStoreMessage(t, records[0].Payload).Payload); got != "one" {
		t.Fatalf("decoded payload = %q, want %q", got, "one")
	}
}

func TestChannelStoreRestoresLEOFromMaxMessageSeqOnRestart(t *testing.T) {
	dir := t.TempDir()
	engine, err := Open(dir)
	require.NoError(t, err)
	key := channel.ChannelKey("channel/1/c1")
	id := channel.ChannelID{ID: "c1", Type: 1}
	st := engine.ForChannel(key, id)

	mustAppendRecords(t, st, []string{"one", "two", "three"})
	require.Equal(t, uint64(3), st.LEO())
	require.NoError(t, engine.Close())

	reopened, err := Open(dir)
	require.NoError(t, err)
	defer reopened.Close()

	reloaded := reopened.ForChannel(key, id)
	require.Equal(t, uint64(3), reloaded.LEO())

	msg, ok, err := reloaded.GetMessageBySeq(3)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(3), msg.MessageSeq)
	require.Equal(t, uint64(3), msg.MessageID)
	require.Equal(t, "three", string(msg.Payload))
}

func TestChannelStoreTruncateRemovesRowsAndIndexes(t *testing.T) {
	st := newTestChannelStore(t)

	firstPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   21,
		ClientMsgNo: "same",
		FromUID:     "u1",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("one"),
	})
	secondPayload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   22,
		ClientMsgNo: "same",
		FromUID:     "u2",
		ChannelID:   "c1",
		ChannelType: 1,
		Payload:     []byte("two"),
	})

	_, err := st.Append([]channel.Record{
		{Payload: firstPayload, SizeBytes: len(firstPayload)},
		{Payload: secondPayload, SizeBytes: len(secondPayload)},
	})
	require.NoError(t, err)

	row, ok, err := getStoredMessageRow(t, st, 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(22), row.MessageID)

	seq, ok, err := getStoredMessageIDIndexSeq(t, st, 22)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(2), seq)

	hit, ok, err := getIndexedIdempotencyHit(t, st, "u2", "same")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(2), hit.MessageSeq)

	require.NoError(t, st.Truncate(1))

	_, ok, err = getStoredMessageRow(t, st, 2)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = getStoredMessageIDIndexSeq(t, st, 22)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = getIndexedIdempotencyHit(t, st, "u2", "same")
	require.NoError(t, err)
	require.False(t, ok)
}
