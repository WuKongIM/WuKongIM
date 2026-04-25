package transport

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestLongPollFetchRequestOpenRoundTrip(t *testing.T) {
	req := LongPollFetchRequest{
		PeerID:          2,
		LaneID:          3,
		LaneCount:       8,
		SessionID:       41,
		SessionEpoch:    7,
		Op:              LanePollOpOpen,
		ProtocolVersion: 1,
		Capabilities:    LongPollCapabilityQuorumAck | LongPollCapabilityLocalAck,
		MaxWaitMs:       1,
		MaxBytes:        64 * 1024,
		MaxChannels:     64,
		FullMembership: []LongPollMembership{
			{ChannelKey: "g1", ChannelEpoch: 11},
			{ChannelKey: "g2", ChannelEpoch: 12},
		},
	}

	data, err := encodeLongPollFetchRequest(req)
	if err != nil {
		t.Fatalf("encodeLongPollFetchRequest() error = %v", err)
	}
	got, err := decodeLongPollFetchRequest(data)
	if err != nil {
		t.Fatalf("decodeLongPollFetchRequest() error = %v", err)
	}
	assertLongPollFetchRequestEqual(t, got, req)
}

func TestLongPollFetchRequestPollRoundTrip(t *testing.T) {
	req := LongPollFetchRequest{
		PeerID:                2,
		LaneID:                1,
		LaneCount:             8,
		SessionID:             99,
		SessionEpoch:          5,
		Op:                    LanePollOpPoll,
		ProtocolVersion:       1,
		Capabilities:          LongPollCapabilityQuorumAck,
		MaxWaitMs:             2,
		MaxBytes:              128 * 1024,
		MaxChannels:           16,
		MembershipVersionHint: 77,
		CursorDelta: []LongPollCursorDelta{
			{ChannelKey: "g-hot", ChannelEpoch: 21, MatchOffset: 100, OffsetEpoch: 3},
			{ChannelKey: "g-cold", ChannelEpoch: 22, MatchOffset: 9, OffsetEpoch: 2},
		},
	}

	data, err := encodeLongPollFetchRequest(req)
	if err != nil {
		t.Fatalf("encodeLongPollFetchRequest() error = %v", err)
	}
	got, err := decodeLongPollFetchRequest(data)
	if err != nil {
		t.Fatalf("decodeLongPollFetchRequest() error = %v", err)
	}
	assertLongPollFetchRequestEqual(t, got, req)
}

func TestLongPollFetchRequestCloseRoundTrip(t *testing.T) {
	req := LongPollFetchRequest{
		PeerID:          2,
		LaneID:          4,
		LaneCount:       8,
		SessionID:       1001,
		SessionEpoch:    9,
		Op:              LanePollOpClose,
		ProtocolVersion: 1,
	}

	data, err := encodeLongPollFetchRequest(req)
	if err != nil {
		t.Fatalf("encodeLongPollFetchRequest() error = %v", err)
	}
	got, err := decodeLongPollFetchRequest(data)
	if err != nil {
		t.Fatalf("decodeLongPollFetchRequest() error = %v", err)
	}
	assertLongPollFetchRequestEqual(t, got, req)
}

func TestLongPollFetchResponseTimedOutRoundTrip(t *testing.T) {
	resp := LongPollFetchResponse{
		Status:       LanePollStatusOK,
		SessionID:    7,
		SessionEpoch: 3,
		TimedOut:     true,
	}

	data, err := encodeLongPollFetchResponse(resp)
	if err != nil {
		t.Fatalf("encodeLongPollFetchResponse() error = %v", err)
	}
	got, err := decodeLongPollFetchResponse(data)
	if err != nil {
		t.Fatalf("decodeLongPollFetchResponse() error = %v", err)
	}
	assertLongPollFetchResponseEqual(t, got, resp)
}

func TestLongPollFetchResponseNeedResetPreservesReason(t *testing.T) {
	resp := LongPollFetchResponse{
		Status:        LanePollStatusNeedReset,
		SessionID:     55,
		SessionEpoch:  8,
		ResetRequired: true,
		ResetReason:   LongPollResetReasonLaneLayoutMismatch,
	}

	data, err := encodeLongPollFetchResponse(resp)
	if err != nil {
		t.Fatalf("encodeLongPollFetchResponse() error = %v", err)
	}
	got, err := decodeLongPollFetchResponse(data)
	if err != nil {
		t.Fatalf("decodeLongPollFetchResponse() error = %v", err)
	}
	assertLongPollFetchResponseEqual(t, got, resp)
}

func TestLongPollFetchResponseMixedItemsRoundTrip(t *testing.T) {
	truncateTo := uint64(44)
	resp := LongPollFetchResponse{
		Status:       LanePollStatusOK,
		SessionID:    99,
		SessionEpoch: 4,
		MoreReady:    true,
		Items: []LongPollItem{
			{
				ChannelKey:   "g-data",
				ChannelEpoch: 10,
				LeaderEpoch:  20,
				Flags:        LongPollItemFlagData,
				LeaderHW:     51,
				Records: []channel.Record{
					{Payload: []byte("one"), SizeBytes: 3},
					{Payload: []byte("two"), SizeBytes: 3},
				},
			},
			{
				ChannelKey:   "g-truncate",
				ChannelEpoch: 11,
				LeaderEpoch:  21,
				Flags:        LongPollItemFlagTruncate,
				LeaderHW:     52,
				TruncateTo:   &truncateTo,
			},
			{
				ChannelKey:   "g-hw",
				ChannelEpoch: 12,
				LeaderEpoch:  22,
				Flags:        LongPollItemFlagHWOnly,
				LeaderHW:     88,
			},
		},
	}

	data, err := encodeLongPollFetchResponse(resp)
	if err != nil {
		t.Fatalf("encodeLongPollFetchResponse() error = %v", err)
	}
	got, err := decodeLongPollFetchResponse(data)
	if err != nil {
		t.Fatalf("decodeLongPollFetchResponse() error = %v", err)
	}
	assertLongPollFetchResponseEqual(t, got, resp)
}

func assertLongPollFetchRequestEqual(t *testing.T, got, want LongPollFetchRequest) {
	t.Helper()

	if got.PeerID != want.PeerID ||
		got.LaneID != want.LaneID ||
		got.LaneCount != want.LaneCount ||
		got.SessionID != want.SessionID ||
		got.SessionEpoch != want.SessionEpoch ||
		got.Op != want.Op ||
		got.ProtocolVersion != want.ProtocolVersion ||
		got.Capabilities != want.Capabilities ||
		got.MaxWaitMs != want.MaxWaitMs ||
		got.MaxBytes != want.MaxBytes ||
		got.MaxChannels != want.MaxChannels ||
		got.MembershipVersionHint != want.MembershipVersionHint {
		t.Fatalf("request mismatch: got=%+v want=%+v", got, want)
	}
	if len(got.FullMembership) != len(want.FullMembership) {
		t.Fatalf("membership len = %d, want %d", len(got.FullMembership), len(want.FullMembership))
	}
	for i := range got.FullMembership {
		if got.FullMembership[i] != want.FullMembership[i] {
			t.Fatalf("membership[%d] = %+v, want %+v", i, got.FullMembership[i], want.FullMembership[i])
		}
	}
	if len(got.CursorDelta) != len(want.CursorDelta) {
		t.Fatalf("cursor delta len = %d, want %d", len(got.CursorDelta), len(want.CursorDelta))
	}
	for i := range got.CursorDelta {
		if got.CursorDelta[i] != want.CursorDelta[i] {
			t.Fatalf("cursorDelta[%d] = %+v, want %+v", i, got.CursorDelta[i], want.CursorDelta[i])
		}
	}
}

func assertLongPollFetchResponseEqual(t *testing.T, got, want LongPollFetchResponse) {
	t.Helper()

	if got.Status != want.Status ||
		got.SessionID != want.SessionID ||
		got.SessionEpoch != want.SessionEpoch ||
		got.TimedOut != want.TimedOut ||
		got.MoreReady != want.MoreReady ||
		got.ResetRequired != want.ResetRequired ||
		got.ResetReason != want.ResetReason {
		t.Fatalf("response mismatch: got=%+v want=%+v", got, want)
	}
	if len(got.Items) != len(want.Items) {
		t.Fatalf("item len = %d, want %d", len(got.Items), len(want.Items))
	}
	for i := range got.Items {
		assertLongPollItemEqual(t, got.Items[i], want.Items[i], i)
	}
}

func assertLongPollItemEqual(t *testing.T, got, want LongPollItem, idx int) {
	t.Helper()

	if got.ChannelKey != want.ChannelKey ||
		got.ChannelEpoch != want.ChannelEpoch ||
		got.LeaderEpoch != want.LeaderEpoch ||
		got.Flags != want.Flags ||
		got.LeaderHW != want.LeaderHW {
		t.Fatalf("item[%d] mismatch: got=%+v want=%+v", idx, got, want)
	}
	switch {
	case got.TruncateTo == nil && want.TruncateTo == nil:
	case got.TruncateTo != nil && want.TruncateTo != nil && *got.TruncateTo == *want.TruncateTo:
	default:
		t.Fatalf("item[%d].TruncateTo = %v, want %v", idx, got.TruncateTo, want.TruncateTo)
	}
	if len(got.Records) != len(want.Records) {
		t.Fatalf("item[%d] record len = %d, want %d", idx, len(got.Records), len(want.Records))
	}
	for j := range got.Records {
		if got.Records[j].SizeBytes != want.Records[j].SizeBytes || string(got.Records[j].Payload) != string(want.Records[j].Payload) {
			t.Fatalf("item[%d].record[%d] = %+v, want %+v", idx, j, got.Records[j], want.Records[j])
		}
	}
}
