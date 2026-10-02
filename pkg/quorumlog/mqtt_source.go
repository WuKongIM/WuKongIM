package quorumlog

import "encoding/hex"

// MQTTSourceProposalManifestVersion identifies a single internal source
// activation record. Ordinary business format selection never emits it.
const MQTTSourceProposalManifestVersion uint16 = 4

// MQTTSourceActivationPayload is the only content permitted in format 4.
// Its bytes alone do not confer control semantics on a business proposal.
const MQTTSourceActivationPayload = "wukongim/mqtt-source-activation/v1\x00"

// MQTTSourceGeneration binds an activation to its retry-stable command, not a
// leader term or process boot. The enclosing Channel identifies the log.
func MQTTSourceGeneration(command CommandID) string {
	if command == (CommandID{}) {
		return ""
	}
	return "mqtt-log-v1:" + hex.EncodeToString(command[:])
}

// validMQTTSourceRecord closes the control format against business semantics.
func validMQTTSourceRecord(r Record) bool {
	return r.SyncOnce && r.Setting == 0 && r.Expire == 0 && r.FromUID == "" &&
		r.ClientMsgNo == "" && len(r.PublicationMetadata) == 0 && string(r.Payload) == MQTTSourceActivationPayload
}
