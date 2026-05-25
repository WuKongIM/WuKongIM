package proxy

import (
	"encoding/json"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	benchmarkRuntimeMetaBytesSink []byte
	benchmarkRuntimeMetaSink      runtimeMetaRPCResponse
	benchmarkIdentitySink         identityRPCResponse
	benchmarkSubscriberSink       subscriberRPCResponse
	benchmarkUserConversationSink userConversationStateRPCResponse
)

func TestRuntimeMetaRPCBinaryBenchmarkCodecRoundTrip(t *testing.T) {
	resp := benchmarkRuntimeMetaRPCResponse()
	body, err := encodeRuntimeMetaRPCResponse(resp)
	if err != nil {
		t.Fatalf("encode runtime meta binary failed: %v", err)
	}
	decoded, err := decodeRuntimeMetaRPCResponse(body)
	if err != nil {
		t.Fatalf("decode runtime meta binary failed: %v", err)
	}
	if decoded.Status != resp.Status || len(decoded.Metas) != len(resp.Metas) {
		t.Fatalf("unexpected runtime meta round trip: status=%q metas=%d", decoded.Status, len(decoded.Metas))
	}
}

func BenchmarkRuntimeMetaRPCResponseCodec(b *testing.B) {
	resp := benchmarkRuntimeMetaRPCResponse()
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		b.Fatalf("marshal runtime meta json failed: %v", err)
	}
	binaryBody, err := encodeRuntimeMetaRPCResponse(resp)
	if err != nil {
		b.Fatalf("marshal runtime meta binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(resp)
			if err != nil {
				b.Fatalf("marshal runtime meta json failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeRuntimeMetaRPCResponse(resp)
			if err != nil {
				b.Fatalf("marshal runtime meta binary failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out runtimeMetaRPCResponse
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal runtime meta json failed: %v", err)
			}
			benchmarkRuntimeMetaSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeRuntimeMetaRPCResponse(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal runtime meta binary failed: %v", err)
			}
			benchmarkRuntimeMetaSink = out
		}
	})
}

func BenchmarkIdentityRPCResponseCodec(b *testing.B) {
	resp := benchmarkIdentityRPCResponse()
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		b.Fatalf("marshal identity json failed: %v", err)
	}
	binaryBody, err := encodeIdentityRPCResponse(resp)
	if err != nil {
		b.Fatalf("marshal identity binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(resp)
			if err != nil {
				b.Fatalf("marshal identity json failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeIdentityRPCResponse(resp)
			if err != nil {
				b.Fatalf("marshal identity binary failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out identityRPCResponse
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal identity json failed: %v", err)
			}
			benchmarkIdentitySink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeIdentityRPCResponse(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal identity binary failed: %v", err)
			}
			benchmarkIdentitySink = out
		}
	})
}

func BenchmarkSubscriberRPCResponseCodec(b *testing.B) {
	resp := benchmarkSubscriberRPCResponse()
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		b.Fatalf("marshal subscriber json failed: %v", err)
	}
	binaryBody, err := encodeSubscriberRPCResponse(resp)
	if err != nil {
		b.Fatalf("marshal subscriber binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(resp)
			if err != nil {
				b.Fatalf("marshal subscriber json failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeSubscriberRPCResponse(resp)
			if err != nil {
				b.Fatalf("marshal subscriber binary failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out subscriberRPCResponse
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal subscriber json failed: %v", err)
			}
			benchmarkSubscriberSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeSubscriberRPCResponse(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal subscriber binary failed: %v", err)
			}
			benchmarkSubscriberSink = out
		}
	})
}

func BenchmarkUserConversationStateRPCResponseCodec(b *testing.B) {
	resp := benchmarkUserConversationStateRPCResponse()
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		b.Fatalf("marshal user conversation json failed: %v", err)
	}
	binaryBody, err := encodeUserConversationStateRPCResponse(resp)
	if err != nil {
		b.Fatalf("marshal user conversation binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(resp)
			if err != nil {
				b.Fatalf("marshal user conversation json failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeUserConversationStateRPCResponse(resp)
			if err != nil {
				b.Fatalf("marshal user conversation binary failed: %v", err)
			}
			benchmarkRuntimeMetaBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out userConversationStateRPCResponse
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal user conversation json failed: %v", err)
			}
			benchmarkUserConversationSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeUserConversationStateRPCResponse(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal user conversation binary failed: %v", err)
			}
			benchmarkUserConversationSink = out
		}
	})
}

func benchmarkRuntimeMetaRPCResponse() runtimeMetaRPCResponse {
	metas := make([]metadb.ChannelRuntimeMeta, 16)
	for i := range metas {
		metas[i] = metadb.ChannelRuntimeMeta{
			ChannelID:            fmt.Sprintf("channel-%03d", i),
			ChannelType:          2,
			ChannelEpoch:         10 + uint64(i),
			LeaderEpoch:          20 + uint64(i),
			Replicas:             []uint64{1, 2, 3},
			ISR:                  []uint64{1, 2},
			Leader:               uint64(i%3 + 1),
			MinISR:               2,
			Status:               1,
			Features:             7,
			LeaseUntilMS:         1777777777000 + int64(i),
			RetentionThroughSeq:  1000 + uint64(i),
			RetentionUpdatedAtMS: 1777777700000 + int64(i),
		}
	}
	return runtimeMetaRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 1,
		Metas:    metas,
		Cursor:   metadb.ChannelRuntimeMetaCursor{ChannelID: "channel-015", ChannelType: 2},
		Done:     true,
	}
}

func benchmarkIdentityRPCResponse() identityRPCResponse {
	return identityRPCResponse{
		Status: rpcStatusOK,
		User:   &metadb.User{UID: "uid-001", Token: "identity-token", DeviceFlag: 1, DeviceLevel: 2},
		Device: &metadb.Device{UID: "uid-001", DeviceFlag: 1, Token: "device-token", DeviceLevel: 2},
	}
}

func benchmarkSubscriberRPCResponse() subscriberRPCResponse {
	uids := make([]string, 64)
	for i := range uids {
		uids[i] = fmt.Sprintf("uid-%03d", i)
	}
	return subscriberRPCResponse{
		Status:     rpcStatusOK,
		UIDs:       uids,
		NextCursor: uids[len(uids)-1],
		Done:       false,
	}
}

func benchmarkUserConversationStateRPCResponse() userConversationStateRPCResponse {
	states := make([]metadb.UserConversationState, 32)
	for i := range states {
		states[i] = metadb.UserConversationState{
			UID:          "uid-001",
			ChannelID:    fmt.Sprintf("channel-%03d", i),
			ChannelType:  2,
			ReadSeq:      100 + uint64(i),
			DeletedToSeq: uint64(i),
			ActiveAt:     1777777700000 + int64(i),
			UpdatedAt:    1777777800000 + int64(i),
		}
	}
	return userConversationStateRPCResponse{
		Status: rpcStatusOK,
		State:  &states[0],
		States: states,
		Cursor: metadb.ConversationCursor{ChannelID: states[len(states)-1].ChannelID, ChannelType: 2},
		Done:   true,
	}
}
