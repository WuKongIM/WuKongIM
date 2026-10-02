package channel

import "testing"

func TestWillReceiptRequiresCompleteImmutableProof(t *testing.T) {
	r := WillReceipt{MessageID: 10, MessageSeq: 2, ServerTimestampMS: 1000, ContentHash: [32]byte{1}}
	if !r.Valid() {
		t.Fatal("complete receipt rejected")
	}
	for _, mutate := range []func(*WillReceipt){
		func(r *WillReceipt) { r.MessageID = 0 },
		func(r *WillReceipt) { r.MessageSeq = 0 },
		func(r *WillReceipt) { r.ServerTimestampMS = 0 },
		func(r *WillReceipt) { r.ServerTimestampMS = -1 },
		func(r *WillReceipt) { r.ContentHash = [32]byte{} },
	} {
		bad := r
		mutate(&bad)
		if bad.Valid() {
			t.Fatalf("incomplete receipt accepted: %+v", bad)
		}
	}
}
