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
