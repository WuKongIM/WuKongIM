package message

import (
	"context"
	"testing"
)

func TestCatalogListsChannelsAfterAppendAndSystemMutation(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	alpha := store.db.Channel(ChannelKey("alpha"), ChannelID{ID: "alpha", Type: 1})
	if _, err := alpha.Append(context.Background(), testRecords(301, "one"), AppendOptions{}); err != nil {
		t.Fatalf("alpha Append(): %v", err)
	}
	beta := store.db.Channel(ChannelKey("beta"), ChannelID{ID: "beta", Type: 2})
	if err := beta.StoreCheckpoint(context.Background(), Checkpoint{Epoch: 1, HW: 0}); err != nil {
		t.Fatalf("beta StoreCheckpoint(): %v", err)
	}

	channels, err := store.db.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels(): %v", err)
	}
	got := make(map[ChannelKey]ChannelID, len(channels))
	for _, channel := range channels {
		got[channel.Key] = channel.ID
	}
	if got[ChannelKey("alpha")] != (ChannelID{ID: "alpha", Type: 1}) {
		t.Fatalf("alpha catalog entry = %+v", got[ChannelKey("alpha")])
	}
	if got[ChannelKey("beta")] != (ChannelID{ID: "beta", Type: 2}) {
		t.Fatalf("beta catalog entry = %+v", got[ChannelKey("beta")])
	}
}

func TestCatalogListEmpty(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	channels, err := store.db.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels(): %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("channels = %+v, want empty", channels)
	}
}

func TestMessageDBListChannelsPage(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	for _, key := range []ChannelKey{"ch-01", "ch-02", "ch-03", "ch-04", "ch-05"} {
		log := store.db.Channel(key, ChannelID{ID: string(key), Type: 1})
		if _, err := log.Append(context.Background(), testRecords(700, string(key)), AppendOptions{}); err != nil {
			t.Fatalf("%s Append(): %v", key, err)
		}
	}

	page, last, more, err := store.db.ListChannelsPage(context.Background(), "", 2)
	if err != nil {
		t.Fatalf("ListChannelsPage first: %v", err)
	}
	assertCatalogKeys(t, page, "ch-01", "ch-02")
	if last != "ch-02" || !more {
		t.Fatalf("first cursor = (%q, %v), want ch-02 true", last, more)
	}

	page, last, more, err = store.db.ListChannelsPage(context.Background(), last, 2)
	if err != nil {
		t.Fatalf("ListChannelsPage second: %v", err)
	}
	assertCatalogKeys(t, page, "ch-03", "ch-04")
	if last != "ch-04" || !more {
		t.Fatalf("second cursor = (%q, %v), want ch-04 true", last, more)
	}

	page, last, more, err = store.db.ListChannelsPage(context.Background(), last, 2)
	if err != nil {
		t.Fatalf("ListChannelsPage third: %v", err)
	}
	assertCatalogKeys(t, page, "ch-05")
	if last != "ch-05" || more {
		t.Fatalf("third cursor = (%q, %v), want ch-05 false", last, more)
	}

	page, last, more, err = store.db.ListChannelsPage(context.Background(), last, 2)
	if err != nil {
		t.Fatalf("ListChannelsPage empty tail: %v", err)
	}
	assertCatalogKeys(t, page)
	if last != "" || more {
		t.Fatalf("empty tail cursor = (%q, %v), want empty false", last, more)
	}
}

func assertCatalogKeys(t *testing.T, entries []ChannelCatalogEntry, keys ...ChannelKey) {
	t.Helper()
	if len(entries) != len(keys) {
		t.Fatalf("len(entries) = %d, want %d: %+v", len(entries), len(keys), entries)
	}
	for i, key := range keys {
		if entries[i].Key != key {
			t.Fatalf("entries[%d].Key = %q, want %q", i, entries[i].Key, key)
		}
	}
}
