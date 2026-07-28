package app

import "testing"

func TestDeliveryMetaStoreRejectsLatePreRestoreSnapshot(t *testing.T) {
	store := newDeliveryMetaStore(nil)
	key := deliveryMetaSubscriberKey{channelID: "room", channelType: 2}
	version := store.version.Load()

	store.resetAfterRestore()
	store.storeSubscriberSnapshot(key, version, []string{"stale-user"})

	if _, ok := store.cachedSubscribers(key, store.version.Load()); ok {
		t.Fatal("late pre-restore subscriber snapshot repopulated the cache")
	}
}
