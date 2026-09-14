package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"reflect"
	"testing"
)

func TestMessageUpdateJSONOmitsOnlyDefaultReadFields(t *testing.T) {
	request := messageUpdateReadRPC{Format: 1, SlotID: 2, Reads: []metadb.MessageUpdateRead{{ChannelID: "g", ChannelType: 2, IDs: []uint64{1}}}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Probe", "IncludePending", "After", "Through", "Limit"} {
		if bytes.Contains(raw, []byte(`"`+field+`"`)) {
			t.Fatalf("default field %s should be omitted: %s", field, raw)
		}
	}
	var decoded messageUpdateReadRPC
	if err = json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(request, decoded) {
		t.Fatalf("request roundtrip: %+v %v", decoded, err)
	}
	reply := messageUpdateReadReply{Format: 1, Status: rpcStatusOK, Pages: make([]metadb.MessageUpdatePage, 20)}
	raw, err = json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"Head"`)) || len(raw) > 150 {
		t.Fatalf("empty pages should retain alignment without zero-field bodies: %d bytes", len(raw))
	}
	got, err := decodeMessageUpdateReply(raw)
	if err != nil || !reflect.DeepEqual(reply, got) {
		t.Fatalf("empty reply roundtrip: %+v %v", got, err)
	}
}

func TestMessageUpdateJSONReadsLegacyAndPreservesNonzeroFields(t *testing.T) {
	legacy := []byte(`{"Format":1,"Status":"ok","LeaderID":0,"Pages":[{"Head":{"replica_set":"[1,2,3]","channel_id":"g","channel_type":2,"generation":"generation","update_seq":18446744073709551615},"Updates":null,"Next":42,"Through":18446744073709551615,"More":true}]}`)
	got, err := decodeMessageUpdateReply(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	again, err := decodeMessageUpdateReply(raw)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("legacy reply changed: %v", err)
	}
	req := messageUpdateReadRPC{Format: 1, Probe: true, SlotID: 2, Reads: []metadb.MessageUpdateRead{{ChannelID: "g", ChannelType: 2, IDs: []uint64{^uint64(0)}, IncludePending: true, After: 1, Through: ^uint64(0), Limit: 200}}}
	raw, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded messageUpdateReadRPC
	if err = json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(req, decoded) {
		t.Fatalf("nonzero request changed: %v", err)
	}
}

func BenchmarkMessageUpdateJSONRoundtrip(b *testing.B) {
	for _, edited := range []bool{false, true} {
		b.Run(fmt.Sprint(edited), func(b *testing.B) {
			req := messageUpdateReadRPC{Format: 1, SlotID: 2}
			reply := messageUpdateReadReply{Format: 1, Status: rpcStatusOK}
			for i := 0; i < 16; i++ {
				id := fmt.Sprintf("channel-%d", i)
				req.Reads = append(req.Reads, metadb.MessageUpdateRead{ChannelID: id, ChannelType: 2, IDs: []uint64{1, 2, 3}})
				page := metadb.MessageUpdatePage{}
				if edited {
					page.Head = metadb.MessageUpdateHead{ChannelID: id, ChannelType: 2, Generation: "g", ReplicaSet: "[1,2,3]", UpdateSeq: 1}
					page.Through = 1
					page.Updates = []metadb.MessageUpdate{{ChannelID: id, ChannelType: 2, MessageID: 3, MessageSeq: 3, Version: 1, UpdateSeq: 1, Payload: bytes.Repeat([]byte("e"), 256), UpdatedAtMS: 123}}
				}
				reply.Pages = append(reply.Pages, page)
			}
			b.ReportAllocs()
			b.ResetTimer()
			wireBytes := 0
			for i := 0; i < b.N; i++ {
				requestBody, e := json.Marshal(req)
				if e != nil {
					b.Fatal(e)
				}
				var r messageUpdateReadRPC
				if e = json.Unmarshal(requestBody, &r); e != nil {
					b.Fatal(e)
				}
				responseBody, e := json.Marshal(reply)
				if e != nil {
					b.Fatal(e)
				}
				if _, e = decodeMessageUpdateReply(responseBody); e != nil {
					b.Fatal(e)
				}
				wireBytes = len(requestBody) + len(responseBody)
			}
			b.ReportMetric(float64(wireBytes), "wire_bytes/op")
		})
	}
}
