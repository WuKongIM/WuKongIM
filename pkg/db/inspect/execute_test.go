package inspect

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNormalizeLimitDefaultAndMax(t *testing.T) {
	if got := normalizeLimit(Options{}, 0); got != defaultLimit {
		t.Fatalf("normalizeLimit(default) = %d, want %d", got, defaultLimit)
	}
	if got := normalizeLimit(Options{DefaultLimit: 7}, 0); got != 7 {
		t.Fatalf("normalizeLimit(custom default) = %d, want 7", got)
	}
	if got := normalizeLimit(Options{MaxLimit: 5}, 99); got != 5 {
		t.Fatalf("normalizeLimit(max) = %d, want 5", got)
	}
}

func TestStoreShowTablesAndDescribe(t *testing.T) {
	path := seedInspectMetaUser(t, 16, meta.User{UID: "u1", Token: "t1"})
	store, err := OpenStore(Options{MetaPath: path, HashSlotCount: 16})
	if err != nil {
		t.Fatalf("OpenStore() err = %v", err)
	}
	defer store.Close()

	tables, err := store.Query(context.Background(), "show tables")
	if err != nil {
		t.Fatalf("show tables err = %v", err)
	}
	if !resultHasRowValue(tables, "table", "meta.user") || !resultHasRowValue(tables, "table", "message.message") {
		t.Fatalf("show tables rows = %+v, want meta.user and message.message", tables.Rows)
	}

	desc, err := store.Query(context.Background(), "describe message.message")
	if err != nil {
		t.Fatalf("describe err = %v", err)
	}
	if !resultHasRowValue(desc, "column", "message_seq") || !resultHasRowValue(desc, "column", "channel_key") {
		t.Fatalf("describe rows = %+v, want message.message columns", desc.Rows)
	}
}

func TestStoreQueryMetaUserByUID(t *testing.T) {
	path := seedInspectMetaUser(t, 16, meta.User{UID: "u1", Token: "t1"})
	store, err := OpenStore(Options{MetaPath: path, HashSlotCount: 16})
	if err != nil {
		t.Fatalf("OpenStore() err = %v", err)
	}
	defer store.Close()

	result, err := store.Query(context.Background(), "select uid, token from meta.user where uid='u1' limit 10")
	if err != nil {
		t.Fatalf("Query() err = %v", err)
	}
	if result.Stats.ScanMode != scanModePointPartition {
		t.Fatalf("ScanMode = %q, want %q", result.Stats.ScanMode, scanModePointPartition)
	}
	if len(result.Rows) != 1 || result.Rows[0]["uid"] != "u1" || result.Rows[0]["token"] != "t1" {
		t.Fatalf("rows = %+v, want u1/t1", result.Rows)
	}
	if _, ok := result.Rows[0]["device_flag"]; ok {
		t.Fatalf("row = %+v, projected row should not include device_flag", result.Rows[0])
	}
}

func TestStoreQueryMessageMessagesByCursor(t *testing.T) {
	path := seedInspectMessages(t)
	store, err := OpenStore(Options{MessagePath: path})
	if err != nil {
		t.Fatalf("OpenStore() err = %v", err)
	}
	defer store.Close()

	first, err := store.Query(context.Background(), "select * from message.message where channel_key='g1:2' limit 1")
	if err != nil {
		t.Fatalf("first Query() err = %v", err)
	}
	if first.Stats.ScanMode != scanModeMessageChannel {
		t.Fatalf("ScanMode = %q, want %q", first.Stats.ScanMode, scanModeMessageChannel)
	}
	if len(first.Rows) != 1 || first.Rows[0]["message_seq"] != uint64(1) {
		t.Fatalf("first rows = %+v, want seq 1", first.Rows)
	}
	if !first.Stats.HasMore || first.Stats.NextCursor == "" {
		t.Fatalf("first stats = %+v, want next cursor", first.Stats)
	}

	second, err := store.Query(context.Background(), "select * from message.message where channel_key='g1:2' limit 1 cursor '"+first.Stats.NextCursor+"'")
	if err != nil {
		t.Fatalf("second Query() err = %v", err)
	}
	if len(second.Rows) != 1 || second.Rows[0]["message_seq"] != uint64(2) {
		t.Fatalf("second rows = %+v, want seq 2", second.Rows)
	}
	if second.Rows[0]["message_seq"] == first.Rows[0]["message_seq"] {
		t.Fatalf("cursor returned duplicate row: first=%+v second=%+v", first.Rows, second.Rows)
	}
}

func seedInspectMetaUser(t *testing.T, hashSlotCount uint16, user meta.User) string {
	t.Helper()

	path := t.TempDir()
	eng, err := engine.Open(path, engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open() err = %v", err)
	}
	db := meta.NewDB(eng)
	hashSlot := meta.HashSlot(cluster.HashSlotForKey(user.UID, hashSlotCount))
	if err := db.HashSlot(hashSlot).UpsertUser(context.Background(), user); err != nil {
		t.Fatalf("UpsertUser() err = %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("engine.Close() err = %v", err)
	}
	return path
}

func seedInspectMessages(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	eng, err := engine.Open(path, engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open() err = %v", err)
	}
	db := message.NewDB(eng)
	log := db.Channel(message.ChannelKey("g1:2"), message.ChannelID{ID: "g1", Type: 2})
	_, err = log.Append(context.Background(), []message.Record{
		{ID: 101, ClientMsgNo: "c1", FromUID: "u1", Payload: []byte("one")},
		{ID: 102, ClientMsgNo: "c2", FromUID: "u2", Payload: []byte("two")},
	}, message.AppendOptions{})
	if err != nil {
		t.Fatalf("Append() err = %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("engine.Close() err = %v", err)
	}
	return path
}

func resultHasRowValue(result Result, key string, value any) bool {
	for _, row := range result.Rows {
		if row[key] == value {
			return true
		}
	}
	return false
}
