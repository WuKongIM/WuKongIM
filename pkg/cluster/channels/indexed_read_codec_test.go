package channels

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"reflect"
	"testing"
)

func TestIndexedReadCodecCannotSilentlyBecomeRange(t *testing.T) {
	q := CommittedReadsRequest{Items: []CommittedReadRequest{{CommittedRead: CommittedRead{ChannelID: ch.ChannelID{ID: "g", Type: 2}, Request: store.ReadCommittedRequest{ClientMsgNo: "key", MinSeq: 7, MaxSeq: 9, Limit: 10, MaxBytes: 1024}}}}}
	raw, e := encodeCommittedReadsRequestVersion(q, codecVersion)
	if e != nil {
		t.Fatal(e)
	}
	got, e := decodeCommittedReadsRequest(raw)
	if e != nil || !reflect.DeepEqual(got, q) {
		t.Fatalf("round trip: %+v %v", got, e)
	}
	if _, e = decodeFrame(raw, kindCommittedReads); e == nil {
		t.Fatal("old decoder accepts indexed lookup")
	}
	assertEveryStrictPrefixRejected(t, raw, func(b []byte) error { _, e := decodeCommittedReadsRequest(b); return e })
	if _, e = decodeCommittedReadsRequest(append(raw, 0)); e == nil {
		t.Fatal("trailing data accepted")
	}
	q.Items[0].Request.ClientMsgNo = ""
	base := appendCommittedReadsRequest(nil, q)
	old, e := encodeRequestFrame(codecVersion, kindCommittedReads, base)
	if e != nil {
		t.Fatal(e)
	}
	got, e = decodeCommittedReadsRequest(old)
	if e != nil || !reflect.DeepEqual(got, q) {
		t.Fatalf("old range read: %v", e)
	}
	broken, e := encodeRequestFrame(codecVersion, kindIndexedCommittedReads, base)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = decodeCommittedReadsRequest(broken); e == nil {
		t.Fatal("missing selector extension accepted")
	}
}
