package chatlifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"math"
)

const (
	payloadMarkerVersion     = uint8(1)
	payloadMarkerHeaderEnd   = 24
	payloadMarkerRunEnd      = 40
	payloadMarkerSenderEnd   = 56
	payloadMarkerTargetEnd   = 72
	payloadMarkerMessageEnd  = 88
	payloadMarkerChecksumEnd = 104
	payloadMarkerBytes       = payloadMarkerChecksumEnd
	maxPayloadBytes          = 16 * 1_024
	maxMarkerIdentityLen     = 1_024
)

var (
	errTrafficIdentityRequired = errors.New("chat lifecycle traffic: identity space is required")
	errTrafficDistribution     = errors.New("chat lifecycle traffic: distributions must contain positive shares totaling 100")
	errTrafficKind             = errors.New("chat lifecycle payload: traffic kind is invalid")
	errDirectionEndpoints      = errors.New("chat lifecycle direction: endpoints must be nonempty and distinct")
	errPayloadWorker           = errors.New("chat lifecycle payload: worker ID is outside the identity space")
	errPayloadIdentity         = errors.New("chat lifecycle payload: sender and target must be nonempty and at most 1024 bytes")
	errPayloadLogicalIdentity  = errors.New("chat lifecycle payload: client_msg_no does not match the logical send")
	errPayloadSize             = errors.New("chat lifecycle payload: size must fit the versioned marker and not exceed 16 KiB")
	errPayloadLength           = errors.New("chat lifecycle payload: declared or actual length is invalid")
	errPayloadMagic            = errors.New("chat lifecycle payload: marker magic is invalid")
	errPayloadVersion          = errors.New("chat lifecycle payload: marker version is unsupported")
	errPayloadReserved         = errors.New("chat lifecycle payload: reserved marker bits are nonzero")
	errPayloadPadding          = errors.New("chat lifecycle payload: deterministic padding is invalid")
	errPayloadChecksum         = errors.New("chat lifecycle payload: checksum is invalid")
	errPayloadDeclaration      = errors.New("chat lifecycle payload: marker does not match the declared logical send")
)

var payloadMagic = [4]byte{'W', 'K', 'C', 'L'}

// TrafficKind separates primary person and group SEND accounting. The
// very-large-group canary is modeled separately by GroupCatalog.
type TrafficKind uint8

const (
	// TrafficPerson is a person-channel logical SEND.
	TrafficPerson TrafficKind = 1
	// TrafficGroup is a primary fixed-group logical SEND.
	TrafficGroup TrafficKind = 2
)

// PersonDirection is the stable direction policy for one person channel.
type PersonDirection uint8

const (
	// DirectionAlternating switches sender for every successive logical SEND.
	DirectionAlternating PersonDirection = 1
	// DirectionOneWay always sends from the canonical lower endpoint.
	DirectionOneWay PersonDirection = 2
)

// LogicalSend is the attempt-independent identity of one SEND. Retries must
// copy this value and therefore reuse ClientMsgNo exactly.
type LogicalSend struct {
	LogicalSend uint64
	WorkerID    uint32
	Kind        TrafficKind
	Sender      string
	Target      string
	ClientMsgNo string
}

// PayloadMarker is the decoded non-secret v1 marker. Its binary layout is a
// 24-byte header, then 16-byte run, sender, target, message, and checksum
// fields; deterministic padding starts at byte 104. String identities are
// stored only as run-bound fingerprints, never as raw run IDs or UIDs.
type PayloadMarker struct {
	Version           uint8
	Kind              TrafficKind
	PayloadBytes      uint32
	WorkerID          uint32
	LogicalSend       uint64
	RunFingerprint    [16]byte
	SenderFingerprint [16]byte
	TargetFingerprint [16]byte
	MessageIdentity   [16]byte
}

// TrafficModel makes independent exact-cycle traffic, payload, and direction
// decisions and builds self-verifying payloads without mutable random state.
type TrafficModel struct {
	identity        *IdentitySpace
	traffic         [2]int
	payloads        []PayloadShare
	payloadPercents []int
	directions      [2]int
	trafficPhase    uint64
	payloadPhase    uint64
	directionPhase  uint64
	runFingerprint  [16]byte
}

// NewTrafficModel copies bounded distribution inputs. Each semantic decision
// has an independent run-keyed phase, so adding one cannot shift another.
func NewTrafficModel(identity *IdentitySpace, workload WorkloadConfig) (TrafficModel, error) {
	if identity == nil {
		return TrafficModel{}, errTrafficIdentityRequired
	}
	traffic := [2]int{workload.Traffic.PersonPercent, workload.Traffic.GroupPercent}
	directions := [2]int{workload.PersonDirection.AlternatingPercent, workload.PersonDirection.OneWayPercent}
	if !validPositiveDistribution(traffic[:]) || !validPositiveDistribution(directions[:]) {
		return TrafficModel{}, errTrafficDistribution
	}
	if len(workload.Payloads) == 0 || len(workload.Payloads) > 100 {
		return TrafficModel{}, errTrafficDistribution
	}
	payloadPercents := make([]int, len(workload.Payloads))
	for i, share := range workload.Payloads {
		payloadPercents[i] = share.Percent
		if share.Bytes < payloadMarkerBytes || share.Bytes > maxPayloadBytes {
			return TrafficModel{}, errPayloadSize
		}
	}
	if !validPositiveDistribution(payloadPercents) {
		return TrafficModel{}, errTrafficDistribution
	}
	trafficPhase, err := identity.decisionBelow("traffic-kind-ordinal-phase/v1", distributionCycle)
	if err != nil {
		return TrafficModel{}, err
	}
	payloadPhase, err := identity.decisionBelow("payload-size-ordinal-phase/v1", distributionCycle)
	if err != nil {
		return TrafficModel{}, err
	}
	directionPhase, err := identity.decisionBelow("person-direction-ordinal-phase/v1", distributionCycle)
	if err != nil {
		return TrafficModel{}, err
	}
	return TrafficModel{
		identity:        identity,
		traffic:         traffic,
		payloads:        append([]PayloadShare(nil), workload.Payloads...),
		payloadPercents: append([]int(nil), payloadPercents...),
		directions:      directions,
		trafficPhase:    trafficPhase,
		payloadPhase:    payloadPhase,
		directionPhase:  directionPhase,
		runFingerprint:  identityFingerprint(identity, "run/v1", ""),
	}, nil
}

// TrafficFor returns an exact-cycle primary person/group choice.
func (m TrafficModel) TrafficFor(logicalOrdinal uint64) (TrafficKind, error) {
	choice, err := exactCycleChoice(logicalOrdinal, m.trafficPhase, m.traffic[:])
	if err != nil {
		return 0, err
	}
	if choice == 0 {
		return TrafficPerson, nil
	}
	return TrafficGroup, nil
}

// PayloadSizeFor returns an exact-cycle configured payload size.
func (m TrafficModel) PayloadSizeFor(logicalOrdinal uint64) (int, error) {
	choice, err := exactCycleChoice(logicalOrdinal, m.payloadPhase, m.payloadPercents)
	if err != nil {
		return 0, err
	}
	return m.payloads[choice].Bytes, nil
}

// DirectionFor returns the stable exact-cycle direction assigned to a person channel.
func (m TrafficModel) DirectionFor(personChannelOrdinal uint64) (PersonDirection, error) {
	choice, err := exactCycleChoice(personChannelOrdinal, m.directionPhase, m.directions[:])
	if err != nil {
		return 0, err
	}
	if choice == 0 {
		return DirectionAlternating, nil
	}
	return DirectionOneWay, nil
}

// SenderFor resolves a direction policy against canonical lower/higher endpoints.
func SenderFor(direction PersonDirection, messageOrdinal uint64, lower, higher string) (string, error) {
	if lower == "" || higher == "" || lower == higher {
		return "", errDirectionEndpoints
	}
	switch direction {
	case DirectionAlternating:
		if messageOrdinal%2 == 0 {
			return lower, nil
		}
		return higher, nil
	case DirectionOneWay:
		return lower, nil
	default:
		return "", errDirectionEndpoints
	}
}

// NewLogicalSend derives one bounded client_msg_no from attempt-independent fields.
func (m TrafficModel) NewLogicalSend(workerID, logicalOrdinal uint64, kind TrafficKind, sender, target string) (LogicalSend, error) {
	if workerID >= m.identity.workers || workerID > math.MaxUint32 {
		return LogicalSend{}, errPayloadWorker
	}
	if !validTrafficKind(kind) {
		return LogicalSend{}, errTrafficKind
	}
	if !validMarkerIdentity(sender) || !validMarkerIdentity(target) {
		return LogicalSend{}, errPayloadIdentity
	}
	logical := LogicalSend{LogicalSend: logicalOrdinal, WorkerID: uint32(workerID), Kind: kind, Sender: sender, Target: target}
	logical.ClientMsgNo = m.clientMessageNo(logical)
	return logical, nil
}

// BuildPayload writes a fixed binary marker and deterministic checked padding.
func (m TrafficModel) BuildPayload(logical LogicalSend, payloadBytes int) ([]byte, error) {
	if err := m.validateLogicalSend(logical); err != nil {
		return nil, err
	}
	if payloadBytes < payloadMarkerBytes || payloadBytes > maxPayloadBytes {
		return nil, errPayloadSize
	}
	payload := make([]byte, payloadBytes)
	copy(payload[:4], payloadMagic[:])
	payload[4] = payloadMarkerVersion
	payload[5] = byte(logical.Kind)
	binary.BigEndian.PutUint32(payload[8:12], uint32(payloadBytes))
	binary.BigEndian.PutUint32(payload[12:16], logical.WorkerID)
	binary.BigEndian.PutUint64(payload[16:24], logical.LogicalSend)
	copy(payload[payloadMarkerHeaderEnd:payloadMarkerRunEnd], m.runFingerprint[:])
	sender := identityFingerprint(m.identity, "sender/v1", logical.Sender)
	target := identityFingerprint(m.identity, "target/v1", logical.Target)
	message := messageFingerprint(logical.ClientMsgNo)
	copy(payload[payloadMarkerRunEnd:payloadMarkerSenderEnd], sender[:])
	copy(payload[payloadMarkerSenderEnd:payloadMarkerTargetEnd], target[:])
	copy(payload[payloadMarkerTargetEnd:payloadMarkerMessageEnd], message[:])
	fillPayloadPadding(payload[payloadMarkerBytes:])
	checksum := payloadChecksum(payload)
	copy(payload[payloadMarkerMessageEnd:payloadMarkerChecksumEnd], checksum[:])
	return payload, nil
}

// VerifyPayload strictly decodes payload and matches every declared identity field.
func (m TrafficModel) VerifyPayload(payload []byte, logical LogicalSend) error {
	if err := m.validateLogicalSend(logical); err != nil {
		return err
	}
	marker, err := DecodePayloadMarker(payload)
	if err != nil {
		return err
	}
	sender := identityFingerprint(m.identity, "sender/v1", logical.Sender)
	target := identityFingerprint(m.identity, "target/v1", logical.Target)
	message := messageFingerprint(logical.ClientMsgNo)
	if marker.Kind != logical.Kind || marker.WorkerID != logical.WorkerID || marker.LogicalSend != logical.LogicalSend ||
		marker.RunFingerprint != m.runFingerprint || marker.SenderFingerprint != sender || marker.TargetFingerprint != target || marker.MessageIdentity != message {
		return errPayloadDeclaration
	}
	return nil
}

// DecodePayloadMarker validates length, version, reserved bytes, padding, and checksum.
func DecodePayloadMarker(payload []byte) (PayloadMarker, error) {
	if len(payload) < payloadMarkerBytes || len(payload) > maxPayloadBytes {
		return PayloadMarker{}, errPayloadLength
	}
	if !bytes.Equal(payload[:4], payloadMagic[:]) {
		return PayloadMarker{}, errPayloadMagic
	}
	if payload[4] != payloadMarkerVersion {
		return PayloadMarker{}, errPayloadVersion
	}
	kind := TrafficKind(payload[5])
	if !validTrafficKind(kind) {
		return PayloadMarker{}, errTrafficKind
	}
	if payload[6] != 0 || payload[7] != 0 {
		return PayloadMarker{}, errPayloadReserved
	}
	declared := binary.BigEndian.Uint32(payload[8:12])
	if uint64(declared) != uint64(len(payload)) {
		return PayloadMarker{}, errPayloadLength
	}
	if !validPayloadPadding(payload[payloadMarkerBytes:]) {
		return PayloadMarker{}, errPayloadPadding
	}
	wantChecksum := payloadChecksum(payload)
	if !bytes.Equal(payload[payloadMarkerMessageEnd:payloadMarkerChecksumEnd], wantChecksum[:]) {
		return PayloadMarker{}, errPayloadChecksum
	}
	marker := PayloadMarker{
		Version:      payload[4],
		Kind:         kind,
		PayloadBytes: declared,
		WorkerID:     binary.BigEndian.Uint32(payload[12:16]),
		LogicalSend:  binary.BigEndian.Uint64(payload[16:24]),
	}
	copy(marker.RunFingerprint[:], payload[payloadMarkerHeaderEnd:payloadMarkerRunEnd])
	copy(marker.SenderFingerprint[:], payload[payloadMarkerRunEnd:payloadMarkerSenderEnd])
	copy(marker.TargetFingerprint[:], payload[payloadMarkerSenderEnd:payloadMarkerTargetEnd])
	copy(marker.MessageIdentity[:], payload[payloadMarkerTargetEnd:payloadMarkerMessageEnd])
	return marker, nil
}

func (m TrafficModel) validateLogicalSend(logical LogicalSend) error {
	if uint64(logical.WorkerID) >= m.identity.workers {
		return errPayloadWorker
	}
	if !validTrafficKind(logical.Kind) {
		return errTrafficKind
	}
	if !validMarkerIdentity(logical.Sender) || !validMarkerIdentity(logical.Target) {
		return errPayloadIdentity
	}
	if logical.ClientMsgNo == "" || logical.ClientMsgNo != m.clientMessageNo(logical) {
		return errPayloadLogicalIdentity
	}
	return nil
}

func (m TrafficModel) clientMessageNo(logical LogicalSend) string {
	return logicalClientMessageNo(m.identity, logical)
}

func logicalClientMessageNo(identity *IdentitySpace, logical LogicalSend) string {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/client-msg-no/v1"))
	_, _ = h.Write(identity.rootKey[:])
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(logical.WorkerID))
	_, _ = h.Write(encoded[:])
	binary.BigEndian.PutUint64(encoded[:], logical.LogicalSend)
	_, _ = h.Write(encoded[:])
	_, _ = h.Write([]byte{byte(logical.Kind)})
	sender := identityFingerprint(identity, "sender/v1", logical.Sender)
	target := identityFingerprint(identity, "target/v1", logical.Target)
	_, _ = h.Write(sender[:])
	_, _ = h.Write(target[:])
	sum := h.Sum(nil)
	return "wkm-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:20])
}

func validPositiveDistribution(shares []int) bool {
	if len(shares) == 0 || len(shares) > 100 {
		return false
	}
	total := 0
	for _, share := range shares {
		if share <= 0 || share > 100 {
			return false
		}
		total += share
	}
	return total == 100
}

func exactCycleChoice(ordinal, phase uint64, shares []int) (int, error) {
	if !validPositiveDistribution(shares) {
		return 0, errTrafficDistribution
	}
	position := int((ordinal%distributionCycle + phase) % distributionCycle)
	boundary := 0
	for index, share := range shares {
		boundary += share
		if position < boundary {
			return index, nil
		}
	}
	return 0, errTrafficDistribution
}

func validTrafficKind(kind TrafficKind) bool { return kind == TrafficPerson || kind == TrafficGroup }

func validMarkerIdentity(value string) bool {
	return len(value) > 0 && len(value) <= maxMarkerIdentityLen
}

// identityFingerprint is a correctness correlation fingerprint, not an
// authentication primitive. At three million identifiers, a 128-bit birthday
// collision is approximately 1.32e-26; raw identities remain outside payloads.
func identityFingerprint(identity *IdentitySpace, purpose, value string) [16]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/marker-fingerprint/v1"))
	_, _ = h.Write(identity.rootKey[:])
	_, _ = h.Write([]byte(purpose))
	_, _ = h.Write([]byte(value))
	var fingerprint [16]byte
	copy(fingerprint[:], h.Sum(nil))
	return fingerprint
}

func messageFingerprint(clientMsgNo string) [16]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/message-identity/v1"))
	_, _ = h.Write([]byte(clientMsgNo))
	var fingerprint [16]byte
	copy(fingerprint[:], h.Sum(nil))
	return fingerprint
}

func fillPayloadPadding(padding []byte) {
	for index := range padding {
		padding[index] = byte((uint64(index)*131 + 0x5a) % 251)
	}
}

func validPayloadPadding(padding []byte) bool {
	for index, value := range padding {
		if value != byte((uint64(index)*131+0x5a)%251) {
			return false
		}
	}
	return true
}

func payloadChecksum(payload []byte) [16]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/payload-checksum/v1"))
	_, _ = h.Write(payload[:payloadMarkerMessageEnd])
	_, _ = h.Write(payload[payloadMarkerBytes:])
	var checksum [16]byte
	copy(checksum[:], h.Sum(nil))
	return checksum
}
