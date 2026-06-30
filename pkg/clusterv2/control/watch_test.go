package control

import "testing"

func TestPublishLatestSnapshotEventCoalescesWhenFull(t *testing.T) {
	watch := make(chan SnapshotEvent, 1)
	publisher := newSnapshotWatchPublisher(watch)

	publisher.publish(Snapshot{Revision: 1})
	publisher.publish(Snapshot{Revision: 2})

	select {
	case event := <-watch:
		if event.Snapshot.Revision != 2 {
			t.Fatalf("published revision = %d, want latest revision 2", event.Snapshot.Revision)
		}
	default:
		t.Fatal("watch channel empty, want latest event")
	}
}

func TestPublishLatestSnapshotEventDoesNotLetOlderRevisionOverwriteLatest(t *testing.T) {
	watch := make(chan SnapshotEvent, 1)
	publisher := newSnapshotWatchPublisher(watch)

	publisher.publish(Snapshot{Revision: 2})
	publisher.publish(Snapshot{Revision: 1})

	select {
	case event := <-watch:
		if event.Snapshot.Revision != 2 {
			t.Fatalf("published revision = %d, want latest revision 2", event.Snapshot.Revision)
		}
	default:
		t.Fatal("watch channel empty, want latest event")
	}
}
