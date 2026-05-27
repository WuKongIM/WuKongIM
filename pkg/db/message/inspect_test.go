package message

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestInspectListChannels(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()

	log := store.db.Channel(ChannelKey("g1:2"), ChannelID{ID: "g1", Type: 2})
	if _, err := log.Append(ctx, testRecords(100, "hello"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	got, err := InspectChannels(ctx, store.db, InspectMessageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("InspectChannels(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	row := got.Rows[0]
	if row["channel_key"] != "g1:2" || row["channel_id"] != "g1" || row["channel_type"] != uint8(2) {
		t.Fatalf("row = %+v, want channel catalog fields", row)
	}
	if !got.Done {
		t.Fatalf("Done = false, want true")
	}
	if got.Next != nil {
		t.Fatalf("Next = %+v, want nil", got.Next)
	}
}

func TestInspectChannelsCursorAndLimit(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()

	for i, key := range []ChannelKey{"g1:2", "g2:2", "g3:2"} {
		log := store.db.Channel(key, ChannelID{ID: string(key[:2]), Type: 2})
		if _, err := log.Append(ctx, testRecords(uint64(100+i), "seed"), AppendOptions{}); err != nil {
			t.Fatalf("Append(%s): %v", key, err)
		}
	}

	first, err := InspectChannels(ctx, store.db, InspectMessageRequest{Limit: 1})
	if err != nil {
		t.Fatalf("InspectChannels(first): %v", err)
	}
	if len(first.Rows) != 1 {
		t.Fatalf("first rows len = %d, want 1: %+v", len(first.Rows), first.Rows)
	}
	if first.Done {
		t.Fatalf("first Done = true, want false")
	}
	if first.Next == nil || first.Next.AfterChannelKey == "" {
		t.Fatalf("first Next = %+v, want after channel cursor", first.Next)
	}
	if first.ScannedRows < 1 {
		t.Fatalf("first ScannedRows = %d, want sensible positive count", first.ScannedRows)
	}

	second, err := InspectChannels(ctx, store.db, InspectMessageRequest{
		AfterChannelKey: first.Next.AfterChannelKey,
		Limit:           1,
	})
	if err != nil {
		t.Fatalf("InspectChannels(second): %v", err)
	}
	if len(second.Rows) != 1 {
		t.Fatalf("second rows len = %d, want 1: %+v", len(second.Rows), second.Rows)
	}
	if second.Rows[0]["channel_key"] == first.Rows[0]["channel_key"] {
		t.Fatalf("cursor returned duplicate channel %q", second.Rows[0]["channel_key"])
	}
	if second.ScannedRows < 1 {
		t.Fatalf("second ScannedRows = %d, want sensible positive count", second.ScannedRows)
	}

	third, err := InspectChannels(ctx, store.db, InspectMessageRequest{
		AfterChannelKey: second.Next.AfterChannelKey,
		Limit:           1,
	})
	if err != nil {
		t.Fatalf("InspectChannels(third): %v", err)
	}
	if len(third.Rows) != 1 {
		t.Fatalf("third rows len = %d, want 1: %+v", len(third.Rows), third.Rows)
	}
	if !third.Done {
		t.Fatalf("third Done = false, want true")
	}
	if third.Next != nil {
		t.Fatalf("third Next = %+v, want nil at exact final limit", third.Next)
	}
}

func TestInspectChannelsLimitDoesNotDecodeBeyondLookahead(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()

	for i, key := range []ChannelKey{"a", "b"} {
		log := store.db.Channel(key, ChannelID{ID: string(key), Type: 1})
		if _, err := log.Append(ctx, testRecords(uint64(400+i), "seed"), AppendOptions{}); err != nil {
			t.Fatalf("Append(%s): %v", key, err)
		}
	}
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeCatalogKey("z"), []byte{0x01}); err != nil {
		t.Fatalf("Set corrupt catalog row: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit corrupt catalog row: %v", err)
	}

	got, err := InspectChannels(ctx, store.db, InspectMessageRequest{Limit: 1})
	if err != nil {
		t.Fatalf("InspectChannels(): %v", err)
	}
	if len(got.Rows) != 1 || got.Rows[0]["channel_key"] != "a" {
		t.Fatalf("rows = %+v, want only first channel", got.Rows)
	}
	if got.Done || got.Next == nil || got.Next.AfterChannelKey != "a" {
		t.Fatalf("result = %+v, want bounded page with next after a", got)
	}
}

func TestInspectMessagesByChannelKeyAndCursor(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()

	log := store.db.Channel(ChannelKey("g1:2"), ChannelID{ID: "g1", Type: 2})
	records := []Record{
		{ID: 101, ClientMsgNo: "c1", FromUID: "u1", Payload: []byte("payload-one")},
		{ID: 102, ClientMsgNo: "c2", FromUID: "u2", Payload: []byte("payload-two")},
	}
	if _, err := log.Append(ctx, records, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	first, err := InspectMessages(ctx, store.db, InspectMessageRequest{ChannelKey: "g1:2", Limit: 1})
	if err != nil {
		t.Fatalf("InspectMessages(first): %v", err)
	}
	if len(first.Rows) != 1 {
		t.Fatalf("first rows len = %d, want 1: %+v", len(first.Rows), first.Rows)
	}
	if first.Done {
		t.Fatalf("first Done = true, want false")
	}
	if first.Next == nil || first.Next.AfterSeq != 1 {
		t.Fatalf("first Next = %+v, want after seq 1", first.Next)
	}
	assertInspectMessageRow(t, first.Rows[0], records[0], uint64(1))
	payload := first.Rows[0]["payload"].([]byte)
	payload[0] = 'X'

	second, err := InspectMessages(ctx, store.db, InspectMessageRequest{
		ChannelKey: "g1:2",
		AfterSeq:   first.Next.AfterSeq,
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("InspectMessages(second): %v", err)
	}
	if len(second.Rows) != 1 {
		t.Fatalf("second rows len = %d, want 1: %+v", len(second.Rows), second.Rows)
	}
	if !second.Done {
		t.Fatalf("second Done = false, want true")
	}
	if second.Next != nil {
		t.Fatalf("second Next = %+v, want nil at exact final limit", second.Next)
	}
	assertInspectMessageRow(t, second.Rows[0], records[1], uint64(2))

	recheck, err := InspectMessages(ctx, store.db, InspectMessageRequest{ChannelKey: "g1:2", Limit: 1})
	if err != nil {
		t.Fatalf("InspectMessages(recheck): %v", err)
	}
	if got := recheck.Rows[0]["payload"].([]byte); !bytes.Equal(got, records[0].Payload) {
		t.Fatalf("payload was not copied: got %q want %q", got, records[0].Payload)
	}
}

func TestInspectMessagesDoesNotPopulateChannelCache(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()

	got, err := InspectMessages(ctx, store.db, InspectMessageRequest{ChannelKey: "g1:2"})
	if err != nil {
		t.Fatalf("InspectMessages(): %v", err)
	}
	if len(got.Rows) != 0 || !got.Done || got.Next != nil {
		t.Fatalf("InspectMessages() = %+v, want empty done result", got)
	}
	if _, ok := store.db.logs[ChannelKey("g1:2")]; ok {
		t.Fatalf("InspectMessages populated db.logs for g1:2")
	}

	log := store.db.Channel(ChannelKey("g1:2"), ChannelID{ID: "g1", Type: 2})
	if _, err := log.Append(ctx, testRecords(200, "after-inspect"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	entries, err := store.db.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels(): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1: %+v", len(entries), entries)
	}
	if entries[0].ID.ID != "g1" || entries[0].ID.Type != 2 {
		t.Fatalf("catalog ID = %+v, want real channel ID/type", entries[0].ID)
	}
}

func TestInspectMessagesAfterMaxSeqIsDone(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()

	log := store.db.Channel(ChannelKey("g1:2"), ChannelID{ID: "g1", Type: 2})
	if _, err := log.Append(ctx, testRecords(300, "payload"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	got, err := InspectMessages(ctx, store.db, InspectMessageRequest{
		ChannelKey: "g1:2",
		AfterSeq:   math.MaxUint64,
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("InspectMessages(): %v", err)
	}
	if len(got.Rows) != 0 {
		t.Fatalf("rows len = %d, want 0: %+v", len(got.Rows), got.Rows)
	}
	if !got.Done {
		t.Fatalf("Done = false, want true")
	}
	if got.Next != nil {
		t.Fatalf("Next = %+v, want nil", got.Next)
	}
	if got.ScannedRows != 0 {
		t.Fatalf("ScannedRows = %d, want 0", got.ScannedRows)
	}
}

func TestInspectMessagesRequiresChannelKey(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	_, err := InspectMessages(context.Background(), store.db, InspectMessageRequest{})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectMessages() err = %v, want ErrInvalidArgument", err)
	}
}

func TestInspectMessagesRejectsNilDB(t *testing.T) {
	_, err := InspectMessages(context.Background(), nil, InspectMessageRequest{ChannelKey: "g1:2"})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectMessages(nil db) err = %v, want ErrClosed", err)
	}
}

func TestInspectChannelsRejectsNilDB(t *testing.T) {
	_, err := InspectChannels(context.Background(), nil, InspectMessageRequest{})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectChannels(nil db) err = %v, want ErrClosed", err)
	}
}

func TestInspectChannelsRejectsClosedStore(t *testing.T) {
	store := openTestMessageStore(t)
	store.close(t)

	_, err := InspectChannels(context.Background(), store.db, InspectMessageRequest{})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectChannels(closed store) err = %v, want ErrClosed", err)
	}
}

func TestInspectMessagesRejectsClosedStore(t *testing.T) {
	store := openTestMessageStore(t)
	store.close(t)

	_, err := InspectMessages(context.Background(), store.db, InspectMessageRequest{ChannelKey: "g1:2"})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectMessages(closed store) err = %v, want ErrClosed", err)
	}
}

func assertInspectMessageRow(t *testing.T, row InspectMessageRow, record Record, seq uint64) {
	t.Helper()
	if row["message_seq"] != seq {
		t.Fatalf("message_seq = %v, want %d in %+v", row["message_seq"], seq, row)
	}
	if row["message_id"] != record.ID {
		t.Fatalf("message_id = %v, want %d in %+v", row["message_id"], record.ID, row)
	}
	if row["client_msg_no"] != record.ClientMsgNo {
		t.Fatalf("client_msg_no = %v, want %q in %+v", row["client_msg_no"], record.ClientMsgNo, row)
	}
	if row["from_uid"] != record.FromUID {
		t.Fatalf("from_uid = %v, want %q in %+v", row["from_uid"], record.FromUID, row)
	}
	if row["payload_hash"] != hashPayload(record.Payload) {
		t.Fatalf("payload_hash = %v, want %d in %+v", row["payload_hash"], hashPayload(record.Payload), row)
	}
	if row["payload_size"] != uint64(len(record.Payload)) {
		t.Fatalf("payload_size = %v, want %d in %+v", row["payload_size"], len(record.Payload), row)
	}
	payload, ok := row["payload"].([]byte)
	if !ok {
		t.Fatalf("payload type = %T, want []byte", row["payload"])
	}
	if !bytes.Equal(payload, record.Payload) {
		t.Fatalf("payload = %q, want %q", payload, record.Payload)
	}
}
