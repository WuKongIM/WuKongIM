package transport

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
)

func TestTransportCodecUsesRootTypes(t *testing.T) {
	req := runtime.FetchRequestEnvelope{
		ChannelKey:  channel.ChannelKey("channel/1/dTE="),
		Epoch:       3,
		Generation:  7,
		ReplicaID:   2,
		FetchOffset: 11,
		OffsetEpoch: 5,
		MaxBytes:    4096,
	}

	data, err := encodeFetchRequest(req)
	if err != nil {
		t.Fatalf("encodeFetchRequest() error = %v", err)
	}
	got, err := decodeFetchRequest(data)
	if err != nil {
		t.Fatalf("decodeFetchRequest() error = %v", err)
	}
	if got.ChannelKey != req.ChannelKey {
		t.Fatalf("ChannelKey = %q, want %q", got.ChannelKey, req.ChannelKey)
	}
}

func TestFetchRequestCodecRoundTrip(t *testing.T) {
	req := runtime.FetchRequestEnvelope{
		ChannelKey:  channel.ChannelKey("g1"),
		Epoch:       3,
		Generation:  7,
		ReplicaID:   2,
		FetchOffset: 11,
		OffsetEpoch: 5,
		MaxBytes:    4096,
	}

	data, err := encodeFetchRequest(req)
	if err != nil {
		t.Fatalf("encodeFetchRequest() error = %v", err)
	}
	got, err := decodeFetchRequest(data)
	if err != nil {
		t.Fatalf("decodeFetchRequest() error = %v", err)
	}
	if got != req {
		t.Fatalf("request = %+v, want %+v", got, req)
	}
}

func TestFetchResponseCodecRoundTrip(t *testing.T) {
	truncateTo := uint64(9)
	resp := runtime.FetchResponseEnvelope{
		ChannelKey: channel.ChannelKey("g1"),
		Epoch:      3,
		Generation: 7,
		TruncateTo: &truncateTo,
		LeaderHW:   12,
		Records: []channel.Record{
			{Payload: []byte("a"), SizeBytes: 1},
			{Payload: []byte("bc"), SizeBytes: 2},
		},
	}

	data, err := encodeFetchResponse(resp)
	if err != nil {
		t.Fatalf("encodeFetchResponse() error = %v", err)
	}
	got, err := decodeFetchResponse(data)
	if err != nil {
		t.Fatalf("decodeFetchResponse() error = %v", err)
	}
	if got.ChannelKey != resp.ChannelKey || got.Epoch != resp.Epoch || got.Generation != resp.Generation || got.LeaderHW != resp.LeaderHW {
		t.Fatalf("response metadata = %+v, want %+v", got, resp)
	}
	if got.TruncateTo == nil || *got.TruncateTo != truncateTo {
		t.Fatalf("TruncateTo = %+v, want %d", got.TruncateTo, truncateTo)
	}
	if len(got.Records) != len(resp.Records) || string(got.Records[1].Payload) != "bc" {
		t.Fatalf("records = %+v, want %+v", got.Records, resp.Records)
	}
}
