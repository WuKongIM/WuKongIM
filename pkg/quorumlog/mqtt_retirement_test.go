package quorumlog

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTReplayRetirementClosedPayloadAndDomain(t *testing.T) {
	r := MQTTReplayRetirement{Anchor: MQTTReplayAnchor{SourceCommand: CommandID{1}, StartAfter: 3, Through: 7, TotalBytes: 100, TotalStoredBytes: 200, Digest: EntryDigest{3}}, AnchorPosition: 8, AnchorDigest: EntryDigest{4}}
	body, err := r.MarshalBinary()
	require.NoError(t, err)
	decoded, err := DecodeMQTTReplayRetirement(body)
	require.NoError(t, err)
	require.Equal(t, r, decoded)
	for n := 0; n < len(body); n++ {
		_, err = DecodeMQTTReplayRetirement(body[:n])
		require.Error(t, err)
	}
	for _, bad := range [][]byte{append(bytes.Clone(body), 0), bytes.Repeat([]byte{0}, len(body)), []byte(MQTTSourceActivationPayload)} {
		_, err = DecodeMQTTReplayRetirement(bad)
		require.Error(t, err)
	}
	for _, change := range []func(*MQTTReplayRetirement){func(r *MQTTReplayRetirement) { r.AnchorPosition = r.Anchor.Through }, func(r *MQTTReplayRetirement) { r.AnchorPosition = ^uint64(0) }, func(r *MQTTReplayRetirement) { r.AnchorDigest = EntryDigest{} }, func(r *MQTTReplayRetirement) { r.Anchor.SourceCommand = CommandID{} }} {
		bad := r
		change(&bad)
		_, err = bad.MarshalBinary()
		require.Error(t, err)
	}
	record := Record{ID: 9, Index: 9, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: body}
	m := ProposalManifest{Version: MQTTReplayRetirementProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 3, CommandID: CommandID{9}, BaseOffset: 8, LastOffset: 9, PreviousIndex: 8, PreviousTerm: 2, PreviousDigest: EntryDigest{8}}
	sealed, entries, ok := SealProposalManifest(m, []Record{record})
	require.True(t, ok)
	require.True(t, sealed.StructurallyValid())
	require.True(t, VerifyEntry(entries[0], record))
	require.Equal(t, ProposalManifestVersion, VersionForRecords([]Record{record}))
	for _, version := range []uint16{1, 2, 3} {
		native := m
		native.Version = version
		other, _, ok := SealProposalManifest(native, []Record{record})
		require.True(t, ok)
		require.NotEqual(t, sealed.Digest, other.Digest)
	}
	for _, change := range []func(*Record){func(r *Record) { r.SyncOnce = false }, func(r *Record) { r.Expire = 1 }, func(r *Record) { r.Setting = 1 }, func(r *Record) { r.FromUID = "alice" }, func(r *Record) { r.ClientMsgNo = "client" }, func(r *Record) { r.PublicationMetadata = []byte{1} }, func(v *Record) { changed := r; changed.AnchorPosition = 9; v.Payload, _ = changed.MarshalBinary() }} {
		bad := record
		change(&bad)
		_, _, ok := SealProposalManifest(m, []Record{bad})
		require.False(t, ok)
		require.False(t, VerifyEntry(entries[0], bad))
	}
	m.LastOffset++
	_, _, ok = SealProposalManifest(m, []Record{record, record})
	require.False(t, ok)
}
