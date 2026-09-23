package quorumlog

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// MQTTReplayRetirementProposalManifestVersion identifies one explicit decision
// to retire an accepted content anchor after consumer admission checks.
const MQTTReplayRetirementProposalManifestVersion uint16 = 6
const mqttReplayRetirementMagic = "wukongim/mqtt-replay-retirement/v1\x00"
const mqttReplayRetirementSize = len(mqttReplayRetirementMagic) + mqttReplayAnchorSize + 40

var errMQTTReplayRetirement = errors.New("quorumlog: invalid MQTT replay retirement")

// MQTTReplayRetirement preserves the exact accepted prefix as a future recovery
// baseline. The admitting runtime proves consumer completion; storage must match
// this anchor to its own journal before accepting the decision.
type MQTTReplayRetirement struct {
	Anchor MQTTReplayAnchor
	// AnchorPosition and AnchorDigest name the original committed proposal.
	AnchorPosition uint64
	AnchorDigest   EntryDigest
}

func (r MQTTReplayRetirement) Valid() bool {
	return r.Anchor.Valid() && r.AnchorPosition > r.Anchor.Through && r.AnchorPosition < ^uint64(0) && r.AnchorDigest != (EntryDigest{})
}

// MarshalBinary emits a fixed payload; business records never select its format.
func (r MQTTReplayRetirement) MarshalBinary() ([]byte, error) {
	if !r.Valid() {
		return nil, errMQTTReplayRetirement
	}
	a, _ := r.Anchor.MarshalBinary()
	b := append([]byte(mqttReplayRetirementMagic), a...)
	b = binary.BigEndian.AppendUint64(b, r.AnchorPosition)
	return append(b, r.AnchorDigest[:]...), nil
}

func DecodeMQTTReplayRetirement(b []byte) (MQTTReplayRetirement, error) {
	var r MQTTReplayRetirement
	if len(b) != mqttReplayRetirementSize || !bytes.HasPrefix(b, []byte(mqttReplayRetirementMagic)) {
		return r, errMQTTReplayRetirement
	}
	b = b[len(mqttReplayRetirementMagic):]
	var err error
	r.Anchor, err = DecodeMQTTReplayAnchor(b[:mqttReplayAnchorSize])
	if err != nil {
		return MQTTReplayRetirement{}, errMQTTReplayRetirement
	}
	b = b[mqttReplayAnchorSize:]
	r.AnchorPosition = binary.BigEndian.Uint64(b[:8])
	copy(r.AnchorDigest[:], b[8:])
	if !r.Valid() {
		return MQTTReplayRetirement{}, errMQTTReplayRetirement
	}
	return r, nil
}

func validMQTTReplayRetirementRecord(r Record, index uint64) bool {
	if !r.SyncOnce || r.Setting != 0 || r.Expire != 0 || r.FromUID != "" || r.ClientMsgNo != "" || len(r.PublicationMetadata) != 0 {
		return false
	}
	retirement, err := DecodeMQTTReplayRetirement(r.Payload)
	return err == nil && retirement.AnchorPosition < index
}
