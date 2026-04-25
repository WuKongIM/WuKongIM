package cluster

import (
	"bytes"
	"testing"
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
	payload := encodeProposalPayload(23, cmd)
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
