package channel

import (
	"strings"
	"testing"
)

func TestWillReceiptRequestBoundsAndResultProof(t *testing.T) {
	q := WillReceiptRequest{ChannelID: ChannelID{ID: "will", Type: 2}, ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch: 2, ExpectedRouteGeneration: 3, FromUID: "sender", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64)}
	if !q.Valid() {
		t.Fatal("valid request rejected")
	}
	for _, change := range []func(*WillReceiptRequest){
		func(q *WillReceiptRequest) { q.ChannelID.ID = "" },
		func(q *WillReceiptRequest) { q.ChannelID.ID = strings.Repeat("x", 1025) },
		func(q *WillReceiptRequest) { q.ChannelID.ID = "a\x00b" },
		func(q *WillReceiptRequest) { q.ChannelID.Type = 0 },
		func(q *WillReceiptRequest) { q.ExpectedChannelEpoch = 0 },
		func(q *WillReceiptRequest) { q.ExpectedLeaderEpoch = 0 },
		func(q *WillReceiptRequest) { q.ExpectedRouteGeneration = 0 },
		func(q *WillReceiptRequest) { q.FromUID = "" },
		func(q *WillReceiptRequest) { q.FromUID = strings.Repeat("u", 65536) },
		func(q *WillReceiptRequest) { q.ServerWillKey = "client-number" },
	} {
		bad := q
		change(&bad)
		if bad.Valid() {
			t.Fatal("invalid request accepted")
		}
	}
	r := WillReceiptResult{CommittedThrough: 2, Found: true, Receipt: WillReceipt{MessageSeq: 2, MessageID: 3, ServerTimestampMS: 1000, ContentHash: [32]byte{1}}}
	if !r.Valid() || !(WillReceiptResult{}).Valid() {
		t.Fatal("valid result rejected")
	}
	for _, change := range []func(*WillReceiptResult){
		func(r *WillReceiptResult) { r.CommittedThrough = 1 },
		func(r *WillReceiptResult) { r.Found = false },
		func(r *WillReceiptResult) { r.Receipt.MessageID = 0 },
	} {
		bad := r
		change(&bad)
		if bad.Valid() {
			t.Fatal("inconsistent result accepted")
		}
	}
}
