package app

import "testing"

func TestDeliveryMetaStoreRejectsLatePreRestoreSnapshot(t *testing.T) {
	store := newDeliveryMetaStore(nil)
	key := deliveryMetaSubscriberKey{channelID: "room", channelType: 2}
	version := store.version.Load()

	store.resetAfterRestore()
	store.storeSubscriberSnapshot(key, version, 0, []string{"stale-user"})

	if _, ok := store.cachedSubscribers(key, store.version.Load(), 0); ok {
		t.Fatal("late pre-restore subscriber snapshot repopulated the cache")
	}
}
