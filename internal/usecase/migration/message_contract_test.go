package migration_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMigrationUsesExistingMessageProposalFormat(t *testing.T) {
	source := channelcompat.Message{MessageID: 9007199254740999, MessageSeq: 1, ChannelID: "room", ChannelType: 2, Timestamp: 1700000000, ServerTimestampMS: 1700000000000, FromUID: "alice", ClientMsgNo: "original", Payload: []byte{0, 1, 255}}
	native, record, err := migration.PrepareMessageRecord(source)
	require.NoError(t, err)
	got, err := message.DecodeMessageRecord(native)
	require.NoError(t, err)
	want := source
	want.Timestamp = 0 // Existing native recovery uses server milliseconds.
	require.Equal(t, want, got)
	manifest := quorumlog.ProposalManifest{Version: quorumlog.ProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{1}, LastOffset: 1}
	_, entries, ok := quorumlog.SealProposalManifest(manifest, []quorumlog.Record{record})
	require.True(t, ok)
	require.Equal(t, uint16(1), entries[0].Version)
	for _, version := range []uint16{0, 3} {
		manifest.Version = version
		_, _, ok = quorumlog.SealProposalManifest(manifest, []quorumlog.Record{record})
		require.False(t, ok, "migration must reject unsupported native versions")
	}
}

func TestMigrationRejectsUnrepresentableMessageFieldsWithoutMutatingSource(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*channelcompat.Message)
	}{
		{"sync_once", func(m *channelcompat.Message) { m.Framer.SyncOnce = true }},
		{"stream_no", func(m *channelcompat.Message) { m.StreamNo = "private-stream-identity" }},
		{"topic", func(m *channelcompat.Message) { m.Topic = "private-topic" }},
		{"timestamp", func(m *channelcompat.Message) { m.Timestamp = 0; m.ServerTimestampMS = 0 }},
		{"timestamp", func(m *channelcompat.Message) { m.Timestamp = -1; m.ServerTimestampMS = -1000 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := channelcompat.Message{MessageID: 9007199254740999, MessageSeq: 1093, ChannelID: "private-channel", ChannelType: 2, Timestamp: 1700000000, ServerTimestampMS: 1700000000000, Payload: []byte("private-payload")}
			test.change(&source)
			before := source
			_, _, err := migration.PrepareMessageRecord(source)
			require.ErrorContains(t, err, "incompatible with existing v3")
			require.ErrorContains(t, err, test.name)
			require.NotContains(t, err.Error(), "private-")
			require.Equal(t, before, source)
		})
	}
}

func TestMigrationPreservesRedDotInExistingNativeFlag(t *testing.T) {
	for _, redDot := range []bool{false, true} {
		source := channelcompat.Message{MessageID: 91, MessageSeq: 1, ChannelID: "room", ChannelType: 2, Timestamp: 1700000000, ServerTimestampMS: 1700000000000, Payload: []byte("body")}
		source.Framer.RedDot = redDot
		before := source
		native, _, err := migration.PrepareMessageRecord(source)
		require.NoError(t, err)
		got, err := message.DecodeMessageRecord(native)
		require.NoError(t, err)
		require.Equal(t, redDot, got.Framer.RedDot)
		require.Equal(t, before, source)
		require.NotContains(t, migration.UnsupportedMessageFields(source), "red_dot")
	}
}

func TestMigrationPreservesOriginalExpireAndBindsNativeProposal(t *testing.T) {
	for _, expire := range []uint32{0, 3600, ^uint32(0)} {
		source := channelcompat.Message{MessageID: 91, MessageSeq: 1, ChannelID: "room", ChannelType: 2, Timestamp: 1700000000, ServerTimestampMS: 1700000000000, Expire: expire, Payload: []byte("body")}
		before := source
		native, record, err := migration.PrepareMessageRecord(source)
		require.NoError(t, err)
		got, err := message.DecodeMessageRecord(native)
		require.NoError(t, err)
		require.Equal(t, expire, got.Expire)
		require.Equal(t, expire, record.Expire)
		require.Equal(t, before, source)
		manifest := quorumlog.ProposalManifest{Version: quorumlog.VersionForRecords([]quorumlog.Record{record}), ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{1}, LastOffset: 1}
		_, entries, ok := quorumlog.SealProposalManifest(manifest, []quorumlog.Record{record})
		require.True(t, ok)
		require.True(t, quorumlog.VerifyEntry(entries[0], record))
		if expire != 0 {
			require.Equal(t, quorumlog.ExpirationProposalManifestVersion, entries[0].Version)
			record.Expire = 0
			require.False(t, quorumlog.VerifyEntry(entries[0], record))
		}
	}
}
