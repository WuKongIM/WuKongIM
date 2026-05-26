package message

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

type testMessageStore struct {
	path   string
	engine *engine.DB
	db     *MessageDB
}

func openTestMessageStore(t *testing.T) *testMessageStore {
	t.Helper()
	return openTestMessageStoreAt(t, t.TempDir())
}

func openTestMessageStoreAt(t *testing.T, path string) *testMessageStore {
	t.Helper()
	eng, err := engine.Open(path, engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open(): %v", err)
	}
	return &testMessageStore{
		path:   path,
		engine: eng,
		db:     NewDB(eng),
	}
}

func (s *testMessageStore) close(t *testing.T) {
	t.Helper()
	if s == nil || s.engine == nil {
		return
	}
	if err := s.engine.Close(); err != nil {
		t.Fatalf("engine.Close(): %v", err)
	}
}

func testChannelLog(store *testMessageStore) *ChannelLog {
	return store.db.Channel(ChannelKey("channel-a"), ChannelID{ID: "channel-a", Type: 1})
}

func testRecords(baseID uint64, payloads ...string) []Record {
	records := make([]Record, 0, len(payloads))
	for i, payload := range payloads {
		records = append(records, Record{
			ID:      baseID + uint64(i),
			Payload: []byte(payload),
		})
	}
	return records
}
