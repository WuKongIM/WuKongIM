package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func newTestChannelStore(tb testing.TB) *ChannelStore {
	tb.Helper()
	key, id := testChannelStoreIdentity("c1")
	return openTestChannelStore(tb, key, id)
}

func openTestEngine(tb testing.TB) *Engine {
	tb.Helper()

	engine, err := Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("Open() error = %v", err)
	}
	tb.Cleanup(func() {
		if err := engine.Close(); err != nil {
			tb.Fatalf("Close() error = %v", err)
		}
	})
	return engine
}

func openTestChannelStore(tb testing.TB, key channel.ChannelKey, id channel.ChannelID) *ChannelStore {
	tb.Helper()
	return openTestEngine(tb).ForChannel(key, id)
}

func openTestChannelStoresOnEngine(tb testing.TB, engine *Engine, names ...string) []*ChannelStore {
	tb.Helper()

	stores := make([]*ChannelStore, 0, len(names))
	for _, name := range names {
		stores = append(stores, engine.ForChannel(testChannelStoreIdentity(name)))
	}
	return stores
}

func testChannelStoreIdentity(name string) (channel.ChannelKey, channel.ChannelID) {
	return channel.ChannelKey("channel/1/" + name), channel.ChannelID{ID: name, Type: 1}
}

func mustAppendRecords(t *testing.T, st *ChannelStore, payloads []string) {
	t.Helper()

	records := make([]channel.Record, 0, len(payloads))
	for i, payload := range payloads {
		encoded := mustEncodeStoreMessage(t, channel.Message{
			MessageID:   uint64(i + 1),
			ClientMsgNo: fmt.Sprintf("client-%d", i+1),
			FromUID:     "u1",
			ChannelID:   st.id.ID,
			ChannelType: st.id.Type,
			Payload:     []byte(payload),
		})
		records = append(records, channel.Record{Payload: encoded, SizeBytes: len(encoded)})
	}
	if _, err := st.Append(records); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

func mustEncodeStoreMessage(tb testing.TB, msg channel.Message) []byte {
	tb.Helper()

	record, err := messageRowFromChannelMessage(msg).toCompatibilityRecord()
	if err != nil {
		tb.Fatalf("toRecord() error = %v", err)
	}
	return record.Payload
}

func mustDecodeStoreMessage(tb testing.TB, payload []byte) channel.Message {
	tb.Helper()

	row, err := decodeCompatibilityRecordPayload(payload)
	if err != nil {
		tb.Fatalf("decodeCompatibilityRecordPayload() error = %v", err)
	}
	return row.toChannelMessage()
}

func getStoredMessageRow(tb testing.TB, st *ChannelStore, seq uint64) (messageRow, bool, error) {
	tb.Helper()

	primary, ok, err := getDBValue(tb, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, seq, messagePrimaryFamilyID))
	if err != nil || !ok {
		return messageRow{}, ok, err
	}
	payload, ok, err := getDBValue(tb, st.engine.db, encodeTableStateKey(st.key, TableIDMessage, seq, messagePayloadFamilyID))
	if err != nil || !ok {
		if err != nil {
			return messageRow{}, false, err
		}
		return messageRow{}, false, channel.ErrCorruptState
	}

	row, err := decodeMessageFamilies(seq, primary, payload)
	if err != nil {
		return messageRow{}, false, err
	}
	if row.PayloadHash != hashMessagePayload(row.Payload) {
		return messageRow{}, false, channel.ErrCorruptState
	}
	return row, true, nil
}

func getStoredMessageIDIndexSeq(tb testing.TB, st *ChannelStore, messageID uint64) (uint64, bool, error) {
	tb.Helper()

	value, ok, err := getDBValue(tb, st.engine.db, testEncodeMessageIDIndexKey(st.key, messageID))
	if err != nil || !ok {
		return 0, ok, err
	}
	seq, err := decodeMessageIDIndexValue(value)
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func getStoredClientMsgNoIndexSeq(tb testing.TB, st *ChannelStore, clientMsgNo string, seq uint64) (uint64, bool, error) {
	tb.Helper()

	value, ok, err := getDBValue(tb, st.engine.db, encodeMessageClientMsgNoIndexKey(st.key, clientMsgNo, seq))
	if err != nil || !ok {
		return 0, ok, err
	}
	indexSeq, err := decodeMessageIDIndexValue(value)
	if err != nil {
		return 0, false, err
	}
	return indexSeq, true, nil
}

func getIndexedIdempotencyHit(tb testing.TB, st *ChannelStore, fromUID, clientMsgNo string) (messageIndexHit, bool, error) {
	tb.Helper()

	value, ok, err := getDBValue(tb, st.engine.db, encodeMessageIdempotencyIndexKey(st.key, fromUID, clientMsgNo))
	if err != nil || !ok {
		return messageIndexHit{}, ok, err
	}
	hit, err := decodeIdempotencyIndexValue(value)
	if err != nil {
		return messageIndexHit{}, false, err
	}
	return hit, true, nil
}

func getDBValue(tb testing.TB, db *pebble.DB, key []byte) ([]byte, bool, error) {
	tb.Helper()

	value, closer, err := db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), true, nil
}

func mustSetDBValue(tb testing.TB, db *pebble.DB, key []byte, value []byte) {
	tb.Helper()
	if err := db.Set(key, append([]byte(nil), value...), pebble.Sync); err != nil {
		tb.Fatalf("db.Set() error = %v", err)
	}
}

func callGetMessageBySeq(st *ChannelStore, seq uint64) (channel.Message, bool, error) {
	out, err := callChannelStoreMethod(st, "GetMessageBySeq", seq)
	if err != nil {
		return channel.Message{}, false, err
	}
	return out[0].(channel.Message), out[1].(bool), nilAsError(out[2])
}

func callGetMessageByMessageID(st *ChannelStore, messageID uint64) (channel.Message, bool, error) {
	out, err := callChannelStoreMethod(st, "GetMessageByMessageID", messageID)
	if err != nil {
		return channel.Message{}, false, err
	}
	return out[0].(channel.Message), out[1].(bool), nilAsError(out[2])
}

func callListMessagesBySeq(st *ChannelStore, fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error) {
	out, err := callChannelStoreMethod(st, "ListMessagesBySeq", fromSeq, limit, maxBytes, reverse)
	if err != nil {
		return nil, err
	}
	return out[0].([]channel.Message), nilAsError(out[1])
}

func callListMessagesByClientMsgNo(st *ChannelStore, clientMsgNo string, beforeSeq uint64, limit int) ([]channel.Message, uint64, bool, error) {
	out, err := callChannelStoreMethod(st, "ListMessagesByClientMsgNo", clientMsgNo, beforeSeq, limit)
	if err != nil {
		return nil, 0, false, err
	}
	return out[0].([]channel.Message), out[1].(uint64), out[2].(bool), nilAsError(out[3])
}

func callLookupIdempotency(st *ChannelStore, key channel.IdempotencyKey) (channel.IdempotencyEntry, uint64, bool, error) {
	out, err := callChannelStoreMethod(st, "LookupIdempotency", key)
	if err != nil {
		return channel.IdempotencyEntry{}, 0, false, err
	}
	return out[0].(channel.IdempotencyEntry), out[1].(uint64), out[2].(bool), nilAsError(out[3])
}

func callChannelStoreMethod(st *ChannelStore, name string, args ...any) ([]any, error) {
	method := reflect.ValueOf(st).MethodByName(name)
	if !method.IsValid() {
		return nil, fmt.Errorf("ChannelStore.%s not implemented", name)
	}

	values := make([]reflect.Value, 0, len(args))
	for _, arg := range args {
		values = append(values, reflect.ValueOf(arg))
	}
	results := method.Call(values)
	out := make([]any, 0, len(results))
	for _, result := range results {
		out = append(out, result.Interface())
	}
	return out, nil
}

func nilAsError(value any) error {
	if value == nil {
		return nil
	}
	err, _ := value.(error)
	return err
}

func testEncodeMessageIDIndexKey(channelKey channel.ChannelKey, messageID uint64) []byte {
	key := encodeTableIndexPrefix(channelKey, TableIDMessage, messageIndexIDMessageID)
	return binary.BigEndian.AppendUint64(key, messageID)
}
