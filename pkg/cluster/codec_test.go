package cluster

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestRaftBodyRoundTrip(t *testing.T) {
	data := []byte("raft-data")
	body := encodeRaftBody(7, data)
	slotID, decoded, err := decodeRaftBody(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if slotID != 7 || !bytes.Equal(decoded, data) {
		t.Fatalf("mismatch: slotID=%d", slotID)
	}
}

func TestEncodeDecodeRaftBatchBodyRoundTrip(t *testing.T) {
	items := []raftBatchItem{
		{slotID: 7, data: []byte("raft-data-7")},
		{slotID: 11, data: []byte("raft-data-11")},
	}

	body := encodeRaftBatchBody(items)
	decoded, err := decodeRaftBatchBody(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != len(items) {
		t.Fatalf("decoded item count = %d, want %d", len(decoded), len(items))
	}
	for i := range items {
		if decoded[i].slotID != items[i].slotID || !bytes.Equal(decoded[i].data, items[i].data) {
			t.Fatalf("decoded[%d] = {slotID:%d data:%q}, want {slotID:%d data:%q}",
				i, decoded[i].slotID, decoded[i].data, items[i].slotID, items[i].data)
		}
	}
}

func TestDecodeRaftBatchBodyRejectsTruncatedPayload(t *testing.T) {
	body := encodeRaftBatchBody([]raftBatchItem{
		{slotID: 7, data: []byte("raft-data")},
	})
	for n := 0; n < len(body); n++ {
		if _, err := decodeRaftBatchBody(body[:n]); err == nil {
			t.Fatalf("decodeRaftBatchBody(%d bytes) error = nil, want truncated payload error", n)
		}
	}
}

func TestForwardPayloadRoundTrip(t *testing.T) {
	cmd := []byte("test-command")
	payload := encodeForwardPayload(7, cmd)
	slotID, decoded, err := decodeForwardPayload(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if slotID != 7 || !bytes.Equal(decoded, cmd) {
		t.Fatalf("mismatch: slotID=%d", slotID)
	}
}

func TestProposalPayloadRoundTrip(t *testing.T) {
	cmd := []byte("proposal-command")
	before := time.Now().UTC().UnixMilli()
	payload := encodeProposalPayload(23, cmd)
	after := time.Now().UTC().UnixMilli()
	if len(payload) != 10+len(cmd) {
		t.Fatalf("encoded proposal payload length = %d, want %d", len(payload), 10+len(cmd))
	}
	createdAtMS := int64(binary.BigEndian.Uint64(payload[2:10]))
	if createdAtMS < before || createdAtMS > after {
		t.Fatalf("encoded proposal createdAtMS = %d, want between %d and %d", createdAtMS, before, after)
	}
	hashSlot, decoded, err := decodeProposalPayload(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hashSlot != 23 || !bytes.Equal(decoded, cmd) {
		t.Fatalf("mismatch: hashSlot=%d", hashSlot)
	}
}

func TestForwardRespRoundTrip(t *testing.T) {
	data := []byte("result")
	resp := encodeForwardResp(errCodeOK, data)
	code, decoded, err := decodeForwardResp(resp)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code != errCodeOK || !bytes.Equal(decoded, data) {
		t.Fatalf("mismatch: code=%d", code)
	}
}
