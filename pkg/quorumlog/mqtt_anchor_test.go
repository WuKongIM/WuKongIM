package quorumlog

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTReplayAnchorPayloadAndControlIdentity(t *testing.T) {
	a := MQTTReplayAnchor{SourceCommand: CommandID{1}, StartAfter: 3, Through: 7, TotalBytes: 100, TotalStoredBytes: 200, Digest: EntryDigest{3}}
	b, err := a.MarshalBinary()
	require.NoError(t, err)
	got, err := DecodeMQTTReplayAnchor(b)
	require.NoError(t, err)
	require.Equal(t, a, got)
	for i := 0; i < len(b); i++ {
		_, err = DecodeMQTTReplayAnchor(b[:i])
		require.Error(t, err)
	}
	for _, bad := range [][]byte{append(bytes.Clone(b), 0), bytes.Repeat([]byte{1}, 4096)} {
		_, err = DecodeMQTTReplayAnchor(bad)
		require.Error(t, err)
	}
	bad := bytes.Clone(b)
	bad[len(mqttReplayAnchorMagic)-1]++
	_, err = DecodeMQTTReplayAnchor(bad)
	require.Error(t, err)
	for _, change := range []func(*MQTTReplayAnchor){func(v *MQTTReplayAnchor) { v.SourceCommand = CommandID{} }, func(v *MQTTReplayAnchor) { v.Through = v.StartAfter }, func(v *MQTTReplayAnchor) { v.TotalBytes = v.TotalStoredBytes + 1 }, func(v *MQTTReplayAnchor) { v.TotalStoredBytes = 0 }, func(v *MQTTReplayAnchor) { v.Digest = EntryDigest{} }} {
		other := a
		change(&other)
		_, err = other.MarshalBinary()
		require.Error(t, err)
	}
	r := Record{ID: 9, Epoch: 2, Index: 8, ServerTimestampMS: 100, SyncOnce: true, Payload: b}
	m := ProposalManifest{Version: MQTTReplayAnchorProposalManifestVersion, ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4, CommandID: CommandID{9}, BaseOffset: 7, LastOffset: 8, PreviousTerm: 3, PreviousIndex: 7, PreviousDigest: EntryDigest{5}}
	sealed, entries, ok := SealProposalManifest(m, []Record{r})
	require.True(t, ok)
	require.True(t, VerifyEntry(entries[0], r))
	require.True(t, sealed.StructurallyValid())
	require.Equal(t, ProposalManifestVersion, VersionForRecords([]Record{r}), "payload must not select internal control semantics")
	for _, version := range []uint16{1, 2, 3} {
		other := m
		other.Version = version
		native, _, ok := SealProposalManifest(other, []Record{r})
		require.True(t, ok)
		require.NotEqual(t, sealed.Digest, native.Digest)
	}
	for _, change := range []func(*Record){func(v *Record) { v.SyncOnce = false }, func(v *Record) { v.FromUID = "sender" }, func(v *Record) { v.Expire = 1 }, func(v *Record) { v.Setting = 1 }, func(v *Record) { v.ClientMsgNo = "client" }, func(v *Record) { v.PublicationMetadata = []byte{1} }, func(v *Record) { other := a; other.Through = 8; v.Payload, _ = other.MarshalBinary() }} {
		other := r
		change(&other)
		_, _, ok := SealProposalManifest(m, []Record{other})
		require.False(t, ok)
		require.False(t, VerifyEntry(entries[0], other))
	}
	pair := m
	pair.LastOffset++
	_, _, ok = SealProposalManifest(pair, []Record{r, r})
	require.False(t, ok)
}
