package quorumlog

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// MQTTReplayAnchorProposalManifestVersion identifies one explicit immutable
// full-content checkpoint. Business format selection never emits this version.
const MQTTReplayAnchorProposalManifestVersion uint16 = 5
const mqttReplayAnchorMagic = "wukongim/mqtt-replay-anchor/v1\x00"
const mqttReplayAnchorSize = len(mqttReplayAnchorMagic) + 96

var errMQTTReplayAnchor = errors.New("quorumlog: invalid MQTT replay anchor")

// MQTTReplayAnchor binds a shared prefix to the original activation command.
// Durability and copy quorum are established by the admitting Channel runtime,
// never by decoding this value or trusting a transfer sender's own digest.
type MQTTReplayAnchor struct {
	SourceCommand                CommandID
	StartAfter, Through          uint64
	TotalBytes, TotalStoredBytes uint64
	Digest                       EntryDigest
}

// Valid requires a nonempty canonical prefix and bounded cumulative arithmetic.
func (a MQTTReplayAnchor) Valid() bool {
	return a.SourceCommand != (CommandID{}) && a.Through > a.StartAfter && a.TotalStoredBytes > 0 && a.TotalBytes <= a.TotalStoredBytes && a.Digest != (EntryDigest{})
}

// MarshalBinary emits the fixed, closed version-1 payload with no optional fields.
func (a MQTTReplayAnchor) MarshalBinary() ([]byte, error) {
	if !a.Valid() {
		return nil, errMQTTReplayAnchor
	}
	b := append([]byte(mqttReplayAnchorMagic), a.SourceCommand[:]...)
	for _, v := range []uint64{a.StartAfter, a.Through, a.TotalBytes, a.TotalStoredBytes} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	return append(b, a.Digest[:]...), nil
}

// DecodeMQTTReplayAnchor rejects alternate versions, incomplete or trailing bytes.
func DecodeMQTTReplayAnchor(b []byte) (MQTTReplayAnchor, error) {
	var a MQTTReplayAnchor
	if len(b) != mqttReplayAnchorSize || !bytes.HasPrefix(b, []byte(mqttReplayAnchorMagic)) {
		return a, errMQTTReplayAnchor
	}
	b = b[len(mqttReplayAnchorMagic):]
	copy(a.SourceCommand[:], b[:32])
	b = b[32:]
	a.StartAfter = binary.BigEndian.Uint64(b[:8])
	a.Through = binary.BigEndian.Uint64(b[8:16])
	a.TotalBytes = binary.BigEndian.Uint64(b[16:24])
	a.TotalStoredBytes = binary.BigEndian.Uint64(b[24:32])
	copy(a.Digest[:], b[32:])
	if !a.Valid() {
		return MQTTReplayAnchor{}, errMQTTReplayAnchor
	}
	return a, nil
}

func validMQTTReplayAnchorRecord(r Record, index uint64) bool {
	if !r.SyncOnce || r.Setting != 0 || r.Expire != 0 || r.FromUID != "" || r.ClientMsgNo != "" || len(r.PublicationMetadata) != 0 {
		return false
	}
	a, err := DecodeMQTTReplayAnchor(r.Payload)
	return err == nil && a.Through < index
}
