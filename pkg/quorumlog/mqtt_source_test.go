package quorumlog

import "testing"

func TestMQTTSourceActivationHasExplicitDistinctProposalIdentity(t *testing.T) {
	m := ProposalManifest{Version: MQTTSourceProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 3, CommandID: CommandID{1}, LastOffset: 1}
	r := Record{ID: 1, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: []byte(MQTTSourceActivationPayload)}
	sealed, entries, ok := SealProposalManifest(m, []Record{r})
	if !ok || !VerifyEntry(entries[0], r) || VersionForRecords([]Record{r}) != ProposalManifestVersion {
		t.Fatal("activation must be explicit and independently verifiable")
	}
	for _, version := range []uint16{1, 2, 3} {
		other := m
		other.Version = version
		native, _, ok := SealProposalManifest(other, []Record{r})
		if !ok || native.Digest == sealed.Digest {
			t.Fatal("activation reused an ordinary digest")
		}
	}
	for name, change := range map[string]func(*Record){
		"sync":     func(r *Record) { r.SyncOnce = false },
		"sender":   func(r *Record) { r.FromUID = "sender" },
		"client":   func(r *Record) { r.ClientMsgNo = "client" },
		"setting":  func(r *Record) { r.Setting = 1 },
		"expiry":   func(r *Record) { r.Expire = 1 },
		"metadata": func(r *Record) { r.PublicationMetadata = []byte{1} },
		"payload":  func(r *Record) { r.Payload = []byte("other") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := r
			change(&changed)
			if _, _, ok := SealProposalManifest(m, []Record{changed}); ok || VerifyEntry(entries[0], changed) {
				t.Fatal("malformed activation accepted")
			}
		})
	}
	m.LastOffset = 2
	if _, _, ok := SealProposalManifest(m, []Record{r, r}); ok {
		t.Fatal("activation shared a proposal")
	}
	if MQTTSourceGeneration(CommandID{}) != "" || len(MQTTSourceGeneration(CommandID{1})) > 128 || MQTTSourceGeneration(CommandID{1}) == MQTTSourceGeneration(CommandID{2}) {
		t.Fatal("invalid generation identity")
	}
}
