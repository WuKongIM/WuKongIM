package transport

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
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

func TestMigrationControlRPCServiceIDDoesNotCollideWithNodeServices(t *testing.T) {
	// Keep this list in sync with internal/access/node/service_ids.go because both
	// node access and channel transport register handlers on the shared RPC mux.
	occupiedNodeServices := map[uint8]string{
		5:  "presence",
		6:  "delivery-submit",
		7:  "delivery-push",
		8:  "delivery-ack",
		9:  "delivery-offline",
		13: "conversation-facts",
		33: "channel-append",
		36: "channel-messages",
		37: "channel-leader-repair",
		38: "channel-leader-evaluate",
		39: "runtime-summary",
		40: "connections",
		41: "connection",
		42: "diagnostics",
		43: "channel-retention",
		44: "delivery-tag",
		45: "system-uid-cache",
		46: "channel-leader-transfer",
		47: "slot-channel-migration",
		49: "slot-cmd-conversation-state",
		50: "cmd-sync",
	}
	if name, exists := occupiedNodeServices[RPCServiceFenceAndDrain]; exists {
		t.Fatalf("RPCServiceFenceAndDrain = %d collides with internal/access/node %s service", RPCServiceFenceAndDrain, name)
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
			{ID: 101, Index: 10, Epoch: 3, Payload: []byte("a"), SizeBytes: 1},
			{ID: 102, Index: 11, Epoch: 3, Payload: []byte("bc"), SizeBytes: 2},
		},
	}

	data, err := encodeFetchResponse(resp)
	if err != nil {
		t.Fatalf("encodeFetchResponse() error = %v", err)
	}
	if data[0] != fetchResponseCodecVersion {
		t.Fatalf("fetch response version = %d, want %d", data[0], fetchResponseCodecVersion)
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
	if len(got.Records) != len(resp.Records) ||
		got.Records[1].ID != 102 ||
		got.Records[1].Index != 11 ||
		got.Records[1].Epoch != 3 ||
		string(got.Records[1].Payload) != "bc" {
		t.Fatalf("records = %+v, want %+v", got.Records, resp.Records)
	}
}

func TestWriteRecordAvoidsPerFieldHeapChurn(t *testing.T) {
	record := channel.Record{ID: 101, Index: 10, Epoch: 3, Payload: []byte("payload"), SizeBytes: 7}

	allocs := testing.AllocsPerRun(1000, func() {
		buf := bytes.NewBuffer(make([]byte, 0, recordEncodedSize(record)))
		if err := writeRecord(buf, record); err != nil {
			t.Fatalf("writeRecord() error = %v", err)
		}
		if buf.Len() != recordEncodedSize(record) {
			t.Fatalf("encoded record size = %d, want %d", buf.Len(), recordEncodedSize(record))
		}
	})

	if allocs > 1 {
		t.Fatalf("writeRecord allocs = %v, want <= 1", allocs)
	}
}

func TestReadRecordAvoidsPerFieldHeapChurn(t *testing.T) {
	record := channel.Record{ID: 101, Index: 10, Epoch: 3, Payload: []byte("payload"), SizeBytes: 7}
	buf := bytes.NewBuffer(make([]byte, 0, recordEncodedSize(record)))
	if err := writeRecord(buf, record); err != nil {
		t.Fatalf("writeRecord() error = %v", err)
	}
	encoded := buf.Bytes()

	allocs := testing.AllocsPerRun(1000, func() {
		reader := bytes.NewReader(encoded)
		got, err := readRecord(reader)
		if err != nil {
			t.Fatalf("readRecord() error = %v", err)
		}
		if got.ID != record.ID || string(got.Payload) != string(record.Payload) {
			t.Fatalf("record = %+v, want %+v", got, record)
		}
	})

	if allocs > 1 {
		t.Fatalf("readRecord allocs = %v, want <= 1", allocs)
	}
}

func TestFetchResponseCodecRoundTripPreservesRetentionReset(t *testing.T) {
	resp := runtime.FetchResponseEnvelope{
		ChannelKey: "g-retention",
		Epoch:      3,
		Generation: 7,
		LeaderHW:   12,
		RetentionReset: &channel.RetentionReset{
			RetentionThroughSeq:   5,
			RetainedThroughOffset: 5,
			MinAvailableSeq:       6,
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
	if got.RetentionReset == nil {
		t.Fatal("RetentionReset = nil, want reset")
	}
	if *got.RetentionReset != *resp.RetentionReset {
		t.Fatalf("RetentionReset = %+v, want %+v", got.RetentionReset, resp.RetentionReset)
	}
}

func TestReconcileProbeCodecVersionsIncludeLeaderEpoch(t *testing.T) {
	req := runtime.ReconcileProbeRequestEnvelope{
		ChannelKey:  "g1",
		Epoch:       3,
		LeaderEpoch: 9,
		Generation:  7,
		ReplicaID:   2,
	}
	reqData, err := encodeReconcileProbeRequest(req)
	if err != nil {
		t.Fatalf("encodeReconcileProbeRequest() error = %v", err)
	}
	if reqData[0] != 2 {
		t.Fatalf("reconcile probe request version = %d, want 2", reqData[0])
	}
	gotReq, err := decodeReconcileProbeRequest(reqData)
	if err != nil {
		t.Fatalf("decodeReconcileProbeRequest() error = %v", err)
	}
	if gotReq != req {
		t.Fatalf("request = %+v, want %+v", gotReq, req)
	}

	resp := runtime.ReconcileProbeResponseEnvelope{
		ChannelKey:     "g1",
		Epoch:          3,
		LeaderEpoch:    9,
		Generation:     8,
		ReplicaID:      4,
		Leader:         2,
		Role:           channel.ReplicaRoleFollower,
		OffsetEpoch:    3,
		LogStartOffset: 5,
		LogEndOffset:   12,
		CheckpointHW:   11,
		CommitReady:    true,
	}
	respData, err := encodeReconcileProbeResponse(resp)
	if err != nil {
		t.Fatalf("encodeReconcileProbeResponse() error = %v", err)
	}
	if respData[0] != 3 {
		t.Fatalf("reconcile probe response version = %d, want 3", respData[0])
	}
	gotResp, err := decodeReconcileProbeResponse(respData)
	if err != nil {
		t.Fatalf("decodeReconcileProbeResponse() error = %v", err)
	}
	if gotResp != resp {
		t.Fatalf("response = %+v, want %+v", gotResp, resp)
	}
}

func TestReconcileProbeRequestCodecCanRequestExtendedResponse(t *testing.T) {
	req := runtime.ReconcileProbeRequestEnvelope{
		ChannelKey:              "g1",
		Epoch:                   3,
		LeaderEpoch:             9,
		Generation:              7,
		ReplicaID:               2,
		RequireExtendedResponse: true,
	}

	data, err := encodeReconcileProbeRequest(req)
	if err != nil {
		t.Fatalf("encodeReconcileProbeRequest() error = %v", err)
	}
	if data[0] != 3 {
		t.Fatalf("reconcile probe request version = %d, want 3", data[0])
	}
	got, err := decodeReconcileProbeRequest(data)
	if err != nil {
		t.Fatalf("decodeReconcileProbeRequest() error = %v", err)
	}
	if got != req {
		t.Fatalf("request = %+v, want %+v", got, req)
	}
}

func TestReconcileProbeResponseDecoderAcceptsVersion2LegacyPayload(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	buf.WriteByte(2)
	if err := writeChannelKey(buf, "g-legacy"); err != nil {
		t.Fatalf("writeChannelKey() error = %v", err)
	}
	for _, value := range []uint64{3, 9, 8, 4, 3, 12, 11} {
		if err := binary.Write(buf, binary.BigEndian, value); err != nil {
			t.Fatalf("binary.Write() error = %v", err)
		}
	}

	got, err := decodeReconcileProbeResponse(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeReconcileProbeResponse() error = %v", err)
	}
	want := runtime.ReconcileProbeResponseEnvelope{
		ChannelKey:   "g-legacy",
		Epoch:        3,
		LeaderEpoch:  9,
		Generation:   8,
		ReplicaID:    4,
		OffsetEpoch:  3,
		LogEndOffset: 12,
		CheckpointHW: 11,
	}
	if got != want {
		t.Fatalf("response = %+v, want %+v", got, want)
	}
}

func TestReconcileProbeResponseEncoderDowngradesForVersion2Request(t *testing.T) {
	resp := runtime.ReconcileProbeResponseEnvelope{
		ChannelKey:     "g1",
		Epoch:          3,
		LeaderEpoch:    9,
		Generation:     8,
		ReplicaID:      4,
		Leader:         2,
		Role:           channel.ReplicaRoleFollower,
		OffsetEpoch:    3,
		LogStartOffset: 5,
		LogEndOffset:   12,
		CheckpointHW:   11,
		CommitReady:    true,
	}

	data, err := encodeReconcileProbeResponseForRequest(resp, runtime.ReconcileProbeRequestEnvelope{})
	if err != nil {
		t.Fatalf("encodeReconcileProbeResponseForRequest() error = %v", err)
	}
	if data[0] != 2 {
		t.Fatalf("legacy reconcile probe response version = %d, want 2", data[0])
	}
	got, err := decodeReconcileProbeResponse(data)
	if err != nil {
		t.Fatalf("decodeReconcileProbeResponse() error = %v", err)
	}
	want := runtime.ReconcileProbeResponseEnvelope{
		ChannelKey:   resp.ChannelKey,
		Epoch:        resp.Epoch,
		LeaderEpoch:  resp.LeaderEpoch,
		Generation:   resp.Generation,
		ReplicaID:    resp.ReplicaID,
		OffsetEpoch:  resp.OffsetEpoch,
		LogEndOffset: resp.LogEndOffset,
		CheckpointHW: resp.CheckpointHW,
	}
	if got != want {
		t.Fatalf("response = %+v, want %+v", got, want)
	}
}
