package app

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"testing"
)

func TestDeliveryMetaStoreRefreshesRemoteSubscriberMutation(t *testing.T) {
	node := &recordingDeliveryMetaNode{subscribers: map[string][]string{"group": {"alice", "bob"}}}
	store := newDeliveryMetaStore(node)
	req := channelappend.SubscriberPageRequest{ChannelID: channelappend.ChannelID{ID: "group", Type: 2}, Limit: 10, SubscriberMutationVersion: 1}
	if _, err := store.NextSubscriberPage(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// A different node changes the authoritative store; this node's benchmark
	// generation does not change. Only the forwarded membership version changes.
	node.mu.Lock()
	node.subscribers["group"] = []string{"alice"}
	node.mu.Unlock()
	req.SubscriberMutationVersion = 2
	for range 2 {
		page, err := store.NextSubscriberPage(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Recipients) != 1 || page.Recipients[0].UID != "alice" {
			t.Fatalf("recipients after remote removal: %+v", page.Recipients)
		}
	}
	if node.listCalls != 2 {
		t.Fatalf("snapshot reads = %d, want one per mutation version", node.listCalls)
	}
}

func TestDeliveryMetaStoreDoesNotReplaceNewerMembershipSnapshot(t *testing.T) {
	store := newDeliveryMetaStore(nil)
	key := deliveryMetaSubscriberKey{channelID: "group", channelType: 2}
	store.storeSubscriberSnapshot(key, 0, 2, []string{"alice"})
	store.storeSubscriberSnapshot(key, 0, 1, []string{"alice", "bob"})
	got, ok := store.cachedSubscribers(key, 0, 2)
	if !ok || len(got) != 1 || got[0] != "alice" {
		t.Fatalf("newer snapshot replaced: %v", got)
	}
}
