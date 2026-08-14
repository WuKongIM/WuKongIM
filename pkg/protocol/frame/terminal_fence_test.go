package frame

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTerminalFenceEventsUseBoundedRedactedWireContract(t *testing.T) {
	grant := TerminalFenceGrant{Epoch: 2, Capability: "top-secret-7"}
	nonce := TerminalFenceNonce{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	pkt, err := NewTerminalFenceRequest(grant, nonce)
	if err != nil {
		t.Fatalf("NewTerminalFenceRequest() error = %v", err)
	}
	if pkt.Type != TerminalFenceEventType || pkt.Id != "" || pkt.Timestamp != 0 {
		t.Fatalf("terminal event envelope = %#v, want fixed redacted envelope", pkt)
	}
	want := []byte{
		1,
		0, 0, 0, 0, 0, 0, 0, 2,
		12, 't', 'o', 'p', '-', 's', 'e', 'c', 'r', 'e', 't', '-', '7',
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	}
	if string(pkt.Data) != string(want) {
		t.Fatalf("terminal request payload = %v, want %v", pkt.Data, want)
	}

	request, err := ParseTerminalFenceRequest(pkt)
	if err != nil {
		t.Fatalf("ParseTerminalFenceRequest() error = %v", err)
	}
	if request.Version != TerminalFenceVersion || request.Epoch != 2 || !request.AuthorizedBy(grant) {
		t.Fatalf("parsed terminal request did not match grant")
	}
	ackEvent, err := request.AckEvent()
	if err != nil {
		t.Fatalf("AckEvent() error = %v", err)
	}
	if ackEvent.Type != TerminalFenceAckEventType || len(ackEvent.Data) != TerminalFenceAckPayloadSize {
		t.Fatalf("terminal ACK envelope = %#v", ackEvent)
	}
	ack, err := ParseTerminalFenceAck(ackEvent)
	if err != nil {
		t.Fatalf("ParseTerminalFenceAck() error = %v", err)
	}
	if !ack.Matches(grant.Epoch, nonce) {
		t.Fatal("terminal ACK did not match exact epoch and nonce")
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", grant), fmt.Sprintf("%#v", grant),
		fmt.Sprintf("%v", request), fmt.Sprintf("%#v", request),
		fmt.Sprintf("%v", ack), fmt.Sprintf("%#v", ack),
		fmt.Sprintf("%v", pkt), fmt.Sprintf("%#v", pkt),
		fmt.Sprintf("%v", ackEvent), fmt.Sprintf("%#v", ackEvent),
	} {
		if strings.Contains(rendered, grant.Capability) || strings.Contains(rendered, "1 2 3 4") {
			t.Fatalf("formatted terminal value exposed secret material: %q", rendered)
		}
	}
}

func TestTerminalFenceEventsRejectUnboundedOrAmbiguousValues(t *testing.T) {
	grant := TerminalFenceGrant{Epoch: 7, Capability: "secret"}
	nonce := TerminalFenceNonce{1}
	valid, err := NewTerminalFenceRequest(grant, nonce)
	if err != nil {
		t.Fatalf("NewTerminalFenceRequest() error = %v", err)
	}
	tests := []struct {
		name string
		pkt  *EventPacket
	}{
		{name: "wrong request type", pkt: &EventPacket{Type: "other", Data: valid.Data}},
		{name: "identity field", pkt: &EventPacket{Id: "uid", Type: valid.Type, Data: valid.Data}},
		{name: "timestamp field", pkt: &EventPacket{Type: valid.Type, Timestamp: 1, Data: valid.Data}},
		{name: "short request", pkt: &EventPacket{Type: valid.Type, Data: valid.Data[:len(valid.Data)-1]}},
		{name: "long request", pkt: &EventPacket{Type: valid.Type, Data: append(append([]byte(nil), valid.Data...), 0)}},
		{name: "wrong version", pkt: &EventPacket{Type: valid.Type, Data: append([]byte{2}, valid.Data[1:]...)}},
		{name: "zero epoch", pkt: &EventPacket{Type: valid.Type, Data: append([]byte{1, 0, 0, 0, 0, 0, 0, 0, 0}, valid.Data[9:]...)}},
		{name: "zero capability", pkt: &EventPacket{Type: valid.Type, Data: append([]byte{1, 0, 0, 0, 0, 0, 0, 0, 7, 0}, make([]byte, 16)...)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTerminalFenceRequest(tc.pkt); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
				t.Fatalf("ParseTerminalFenceRequest() error = %v, want %v", err, ErrInvalidTerminalFenceEvent)
			}
		})
	}

	if err := (TerminalFenceGrant{Capability: "secret"}).Validate(); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
		t.Fatalf("zero epoch grant error = %v", err)
	}
	if err := (TerminalFenceGrant{Epoch: 1}).Validate(); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
		t.Fatalf("empty capability grant error = %v", err)
	}
	if err := (TerminalFenceGrant{Epoch: 1, Capability: strings.Repeat("x", TerminalFenceCapabilityMaxBytes+1)}).Validate(); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
		t.Fatalf("oversized capability grant error = %v", err)
	}
	maxRequest, err := NewTerminalFenceRequest(TerminalFenceGrant{Epoch: 1, Capability: strings.Repeat("x", TerminalFenceCapabilityMaxBytes)}, nonce)
	if err != nil {
		t.Fatalf("maximum bounded capability error = %v", err)
	}
	if len(maxRequest.Data) != TerminalFenceRequestPayloadMaxSize {
		t.Fatalf("maximum request payload = %d, want %d", len(maxRequest.Data), TerminalFenceRequestPayloadMaxSize)
	}
	if _, err := NewTerminalFenceRequest(grant, TerminalFenceNonce{}); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
		t.Fatalf("zero nonce request error = %v", err)
	}

	request, err := ParseTerminalFenceRequest(valid)
	if err != nil {
		t.Fatalf("ParseTerminalFenceRequest() error = %v", err)
	}
	validAck, err := request.AckEvent()
	if err != nil {
		t.Fatalf("AckEvent() error = %v", err)
	}
	ackTests := []struct {
		name string
		pkt  *EventPacket
	}{
		{name: "wrong ACK type", pkt: &EventPacket{Type: TerminalFenceEventType, Data: validAck.Data}},
		{name: "ACK identity field", pkt: &EventPacket{Id: "uid", Type: validAck.Type, Data: validAck.Data}},
		{name: "ACK timestamp field", pkt: &EventPacket{Type: validAck.Type, Timestamp: 1, Data: validAck.Data}},
		{name: "short ACK", pkt: &EventPacket{Type: validAck.Type, Data: validAck.Data[:len(validAck.Data)-1]}},
		{name: "long ACK", pkt: &EventPacket{Type: validAck.Type, Data: append(append([]byte(nil), validAck.Data...), 0)}},
		{name: "wrong ACK version", pkt: &EventPacket{Type: validAck.Type, Data: append([]byte{2}, validAck.Data[1:]...)}},
		{name: "zero ACK epoch", pkt: &EventPacket{Type: validAck.Type, Data: append([]byte{1, 0, 0, 0, 0, 0, 0, 0, 0}, validAck.Data[9:]...)}},
		{name: "zero ACK nonce", pkt: &EventPacket{Type: validAck.Type, Data: append(append([]byte(nil), validAck.Data[:9]...), make([]byte, TerminalFenceNonceBytes)...)}},
	}
	for _, tc := range ackTests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTerminalFenceAck(tc.pkt); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
				t.Fatalf("ParseTerminalFenceAck() error = %v, want %v", err, ErrInvalidTerminalFenceEvent)
			}
		})
	}
}

func TestTerminalFenceRequestCopyNonceUsesExplicitBoundedDestination(t *testing.T) {
	nonce := TerminalFenceNonce{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	pkt, err := NewTerminalFenceRequest(TerminalFenceGrant{Epoch: 9, Capability: "secret"}, nonce)
	if err != nil {
		t.Fatalf("NewTerminalFenceRequest() error = %v", err)
	}
	request, err := ParseTerminalFenceRequest(pkt)
	if err != nil {
		t.Fatalf("ParseTerminalFenceRequest() error = %v", err)
	}

	if err := request.CopyNonce(nil); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
		t.Fatalf("CopyNonce(nil) error = %v, want %v", err, ErrInvalidTerminalFenceEvent)
	}
	var copied TerminalFenceNonce
	if err := request.CopyNonce(&copied); err != nil {
		t.Fatalf("CopyNonce() error = %v", err)
	}
	if copied != nonce {
		t.Fatalf("copied nonce = %v, want exact source nonce", copied)
	}
	for _, rendered := range []string{fmt.Sprintf("%v", copied), fmt.Sprintf("%#v", copied)} {
		if strings.Contains(rendered, "1 2 3 4") || strings.Contains(rendered, "0x1, 0x2, 0x3, 0x4") {
			t.Fatalf("formatted copied nonce exposed secret material: %q", rendered)
		}
	}

	// Mutating the caller's copy must not change the request's matching ACK.
	copied[0] = 99
	ackEvent, err := request.AckEvent()
	if err != nil {
		t.Fatalf("AckEvent() error = %v", err)
	}
	ack, err := ParseTerminalFenceAck(ackEvent)
	if err != nil {
		t.Fatalf("ParseTerminalFenceAck() error = %v", err)
	}
	if !ack.Matches(request.Epoch, nonce) {
		t.Fatal("mutating copied nonce changed the parsed request")
	}
}

func TestTerminalFenceRequestProofIsFixedSizeAndRedacted(t *testing.T) {
	const capability = "proof-capability-secret"
	pkt, err := NewTerminalFenceRequest(
		TerminalFenceGrant{Epoch: 11, Capability: capability},
		TerminalFenceNonce{1},
	)
	if err != nil {
		t.Fatalf("NewTerminalFenceRequest() error = %v", err)
	}
	request, err := ParseTerminalFenceRequest(pkt)
	if err != nil {
		t.Fatalf("ParseTerminalFenceRequest() error = %v", err)
	}

	proof, err := request.Proof()
	if err != nil {
		t.Fatalf("Proof() error = %v", err)
	}
	wantDigest := sha256.Sum256([]byte(capability))
	if proof.Epoch != request.Epoch || proof.CapabilitySHA256 != wantDigest {
		t.Fatalf("Proof() = %#v, want exact epoch and SHA-256", proof)
	}
	digestPrefix := fmt.Sprintf("%x", wantDigest[:4])
	for _, rendered := range []string{fmt.Sprintf("%v", proof), fmt.Sprintf("%#v", proof)} {
		if strings.Contains(rendered, capability) || strings.Contains(rendered, digestPrefix) {
			t.Fatalf("formatted proof exposed capability material: %q", rendered)
		}
	}

	if _, err := (TerminalFenceRequest{}).Proof(); !errors.Is(err, ErrInvalidTerminalFenceEvent) {
		t.Fatalf("zero request Proof() error = %v, want %v", err, ErrInvalidTerminalFenceEvent)
	}
}
