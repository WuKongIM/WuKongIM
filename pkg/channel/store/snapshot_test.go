package store

import "testing"

func TestChannelStoreReplaceSnapshotPayload(t *testing.T) {
	st := newTestChannelStore(t)
	if err := st.StoreSnapshotPayload([]byte("first")); err != nil {
		t.Fatalf("StoreSnapshotPayload(first) error = %v", err)
	}
	if err := st.StoreSnapshotPayload([]byte("second")); err != nil {
		t.Fatalf("StoreSnapshotPayload(second) error = %v", err)
	}
	payload, err := st.LoadSnapshotPayload()
	if err != nil {
		t.Fatalf("LoadSnapshotPayload() error = %v", err)
	}
	if string(payload) != "second" {
		t.Fatalf("payload = %q, want %q", payload, "second")
	}
}
