package quorumlog

import "encoding/binary"

// RecoveryBarrierProposalManifestVersion identifies one authenticated authority
// maintenance entry. Ordinary format selection never emits this control format.
const RecoveryBarrierProposalManifestVersion uint16 = 7

// InternalProposalVersion classifies explicit native maintenance formats only.
// SyncOnce and payload lookalikes never grant control or uncharged body semantics.
func InternalProposalVersion(version uint16) bool {
	return version == MQTTSourceProposalManifestVersion || version == MQTTReplayAnchorProposalManifestVersion || version == MQTTReplayRetirementProposalManifestVersion || version == RecoveryBarrierProposalManifestVersion
}

func validRecoveryBarrierRecord(r Record, epoch, term, fence uint64) bool {
	return r.SyncOnce && r.Setting == 0 && r.Expire == 0 && r.FromUID == "" && r.ClientMsgNo == "" && len(r.PublicationMetadata) == 0 && len(r.Payload) == 25 && r.Payload[0] == 1 && binary.BigEndian.Uint64(r.Payload[1:9]) == epoch && binary.BigEndian.Uint64(r.Payload[9:17]) == term && binary.BigEndian.Uint64(r.Payload[17:25]) == fence
}
