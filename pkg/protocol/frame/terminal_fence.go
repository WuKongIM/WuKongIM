package frame

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// TerminalFenceEventType is the reserved bench-only client request type.
	TerminalFenceEventType = "_wk.bench.terminal_fence.v1"
	// TerminalFenceAckEventType is the reserved server acknowledgement type.
	TerminalFenceAckEventType = "_wk.bench.terminal_fence_ack.v1"
	// TerminalFenceVersion is the only accepted bounded payload version.
	TerminalFenceVersion uint8 = 1
	// TerminalFenceCapabilityMaxBytes bounds the opaque run-scoped capability.
	TerminalFenceCapabilityMaxBytes = 128
	// TerminalFenceNonceBytes fixes the random request nonce at 128 bits.
	TerminalFenceNonceBytes = 16
	// TerminalFenceAckPayloadSize is version + epoch + nonce.
	TerminalFenceAckPayloadSize = 1 + 8 + TerminalFenceNonceBytes
	// TerminalFenceRequestPayloadMaxSize also includes one capability-length byte.
	TerminalFenceRequestPayloadMaxSize = TerminalFenceAckPayloadSize + 1 + TerminalFenceCapabilityMaxBytes
)

var ErrInvalidTerminalFenceEvent = errors.New("wkproto: invalid terminal fence event")

// TerminalFenceGrant authorizes one immutable target-published fence epoch.
// Capability is secret and must never be logged or used as a metric label.
type TerminalFenceGrant struct {
	// Epoch identifies the immutable target-published terminal cut.
	Epoch uint64
	// Capability is the opaque run-scoped authorization secret.
	Capability string
}

// Validate rejects empty or unbounded grants before they reach the wire.
func (g TerminalFenceGrant) Validate() error {
	if g.Epoch == 0 || len(g.Capability) == 0 || len(g.Capability) > TerminalFenceCapabilityMaxBytes {
		return ErrInvalidTerminalFenceEvent
	}
	return nil
}

// String deliberately redacts the capability from ordinary formatting.
func (g TerminalFenceGrant) String() string {
	return fmt.Sprintf("terminal-fence-grant{epoch:%d capability:[redacted]}", g.Epoch)
}

// GoString deliberately redacts the capability from %#v formatting.
func (g TerminalFenceGrant) GoString() string { return g.String() }

// TerminalFenceNonce is the fixed 128-bit request correlation value.
type TerminalFenceNonce [TerminalFenceNonceBytes]byte

// String deliberately omits the nonce bytes from ordinary formatting.
func (TerminalFenceNonce) String() string { return "terminal-fence-nonce{[redacted]}" }

// GoString deliberately omits the nonce bytes from %#v formatting.
func (n TerminalFenceNonce) GoString() string { return n.String() }

// TerminalFenceProof is the fixed-size, frame-independent authorization
// projection handed from the protocol adapter to the terminal use case. The
// digest is sensitive correlation material and must not be logged or labeled.
type TerminalFenceProof struct {
	// Epoch identifies the immutable target-published terminal cut.
	Epoch uint64
	// CapabilitySHA256 proves possession without exposing the wire capability.
	CapabilitySHA256 [sha256.Size]byte
}

// String deliberately omits the capability digest from ordinary formatting.
func (p TerminalFenceProof) String() string {
	return fmt.Sprintf("terminal-fence-proof{epoch:%d capability_sha256:[redacted]}", p.Epoch)
}

// GoString deliberately omits the capability digest from %#v formatting.
func (p TerminalFenceProof) GoString() string { return p.String() }

// TerminalFenceRequest is a validated request with secret fields kept private.
type TerminalFenceRequest struct {
	// Version is the validated bounded wire version.
	Version uint8
	// Epoch is the target-published terminal cut identity.
	Epoch      uint64
	capability string
	nonce      TerminalFenceNonce
}

// AuthorizedBy checks the published grant without exposing the wire secret.
func (r TerminalFenceRequest) AuthorizedBy(grant TerminalFenceGrant) bool {
	return grant.Validate() == nil && r.Version == TerminalFenceVersion && r.Epoch == grant.Epoch &&
		subtle.ConstantTimeCompare([]byte(r.capability), []byte(grant.Capability)) == 1
}

// Proof returns a fixed-size digest projection for entry-agnostic grant
// validation. It never exposes the original capability.
func (r TerminalFenceRequest) Proof() (TerminalFenceProof, error) {
	if r.Version != TerminalFenceVersion || r.Epoch == 0 || len(r.capability) == 0 || len(r.capability) > TerminalFenceCapabilityMaxBytes || zeroTerminalFenceNonce(r.nonce) {
		return TerminalFenceProof{}, ErrInvalidTerminalFenceEvent
	}
	return TerminalFenceProof{
		Epoch:            r.Epoch,
		CapabilitySHA256: sha256.Sum256([]byte(r.capability)),
	}, nil
}

// CopyNonce copies the secret correlation value into an explicit fixed-size
// destination for the authenticated access-to-usecase mapping. Callers must
// not log, format, or use the copied value as a metric label.
func (r TerminalFenceRequest) CopyNonce(dst *TerminalFenceNonce) error {
	if dst == nil || r.Version != TerminalFenceVersion || r.Epoch == 0 || zeroTerminalFenceNonce(r.nonce) {
		return ErrInvalidTerminalFenceEvent
	}
	*dst = r.nonce
	return nil
}

// AckEvent builds the exact identity-free acknowledgement for this request.
func (r TerminalFenceRequest) AckEvent() (*EventPacket, error) {
	if r.Version != TerminalFenceVersion || r.Epoch == 0 || zeroTerminalFenceNonce(r.nonce) {
		return nil, ErrInvalidTerminalFenceEvent
	}
	return newTerminalFenceAck(r.Epoch, r.nonce), nil
}

// String deliberately omits the capability and nonce.
func (r TerminalFenceRequest) String() string {
	return fmt.Sprintf("terminal-fence-request{version:%d epoch:%d capability:[redacted] nonce:[redacted]}", r.Version, r.Epoch)
}

// GoString deliberately omits the capability and nonce from %#v formatting.
func (r TerminalFenceRequest) GoString() string { return r.String() }

// TerminalFenceAck is a validated ACK with its nonce kept private.
type TerminalFenceAck struct {
	// Version is the validated bounded wire version.
	Version uint8
	// Epoch is the acknowledged terminal cut identity.
	Epoch uint64
	nonce TerminalFenceNonce
}

// Matches verifies the exact client cut without exposing the nonce.
func (a TerminalFenceAck) Matches(epoch uint64, nonce TerminalFenceNonce) bool {
	return a.Version == TerminalFenceVersion && a.Epoch == epoch && subtle.ConstantTimeCompare(a.nonce[:], nonce[:]) == 1
}

// String deliberately omits the nonce.
func (a TerminalFenceAck) String() string {
	return fmt.Sprintf("terminal-fence-ack{version:%d epoch:%d nonce:[redacted]}", a.Version, a.Epoch)
}

// GoString deliberately omits the nonce from %#v formatting.
func (a TerminalFenceAck) GoString() string { return a.String() }

// NewTerminalFenceRequest builds one strictly bounded client request EVENT.
func NewTerminalFenceRequest(grant TerminalFenceGrant, nonce TerminalFenceNonce) (*EventPacket, error) {
	if err := grant.Validate(); err != nil || zeroTerminalFenceNonce(nonce) {
		return nil, ErrInvalidTerminalFenceEvent
	}
	payload := make([]byte, 1+8+1+len(grant.Capability)+TerminalFenceNonceBytes)
	payload[0] = TerminalFenceVersion
	binary.BigEndian.PutUint64(payload[1:9], grant.Epoch)
	payload[9] = byte(len(grant.Capability))
	copy(payload[10:10+len(grant.Capability)], grant.Capability)
	copy(payload[len(payload)-TerminalFenceNonceBytes:], nonce[:])
	return &EventPacket{Type: TerminalFenceEventType, Data: payload}, nil
}

func newTerminalFenceAck(epoch uint64, nonce TerminalFenceNonce) *EventPacket {
	payload := make([]byte, TerminalFenceAckPayloadSize)
	payload[0] = TerminalFenceVersion
	binary.BigEndian.PutUint64(payload[1:9], epoch)
	copy(payload[9:], nonce[:])
	return &EventPacket{Type: TerminalFenceAckEventType, Data: payload}
}

// IsTerminalFenceEvent reports whether f claims either reserved terminal type.
func IsTerminalFenceEvent(f Frame) bool {
	pkt, ok := f.(*EventPacket)
	return ok && pkt != nil && (pkt.Type == TerminalFenceEventType || pkt.Type == TerminalFenceAckEventType)
}

// ParseTerminalFenceRequest validates the complete request envelope and bound.
func ParseTerminalFenceRequest(pkt *EventPacket) (TerminalFenceRequest, error) {
	if !validTerminalFenceEnvelope(pkt, TerminalFenceEventType) || len(pkt.Data) < 1+8+1+1+TerminalFenceNonceBytes || len(pkt.Data) > TerminalFenceRequestPayloadMaxSize {
		return TerminalFenceRequest{}, ErrInvalidTerminalFenceEvent
	}
	capabilitySize := int(pkt.Data[9])
	if capabilitySize == 0 || capabilitySize > TerminalFenceCapabilityMaxBytes || len(pkt.Data) != 1+8+1+capabilitySize+TerminalFenceNonceBytes {
		return TerminalFenceRequest{}, ErrInvalidTerminalFenceEvent
	}
	request := TerminalFenceRequest{
		Version:    pkt.Data[0],
		Epoch:      binary.BigEndian.Uint64(pkt.Data[1:9]),
		capability: string(pkt.Data[10 : 10+capabilitySize]),
	}
	copy(request.nonce[:], pkt.Data[len(pkt.Data)-TerminalFenceNonceBytes:])
	if request.Version != TerminalFenceVersion || request.Epoch == 0 || zeroTerminalFenceNonce(request.nonce) {
		return TerminalFenceRequest{}, ErrInvalidTerminalFenceEvent
	}
	return request, nil
}

// ParseTerminalFenceAck validates the fixed acknowledgement envelope.
func ParseTerminalFenceAck(pkt *EventPacket) (TerminalFenceAck, error) {
	if !validTerminalFenceEnvelope(pkt, TerminalFenceAckEventType) || len(pkt.Data) != TerminalFenceAckPayloadSize {
		return TerminalFenceAck{}, ErrInvalidTerminalFenceEvent
	}
	ack := TerminalFenceAck{Version: pkt.Data[0], Epoch: binary.BigEndian.Uint64(pkt.Data[1:9])}
	copy(ack.nonce[:], pkt.Data[9:])
	if ack.Version != TerminalFenceVersion || ack.Epoch == 0 || zeroTerminalFenceNonce(ack.nonce) {
		return TerminalFenceAck{}, ErrInvalidTerminalFenceEvent
	}
	return ack, nil
}

func validTerminalFenceEnvelope(pkt *EventPacket, eventType string) bool {
	return pkt != nil && pkt.Type == eventType && pkt.Id == "" && pkt.Timestamp == 0
}

func zeroTerminalFenceNonce(nonce TerminalFenceNonce) bool {
	var zero TerminalFenceNonce
	return subtle.ConstantTimeCompare(nonce[:], zero[:]) == 1
}
