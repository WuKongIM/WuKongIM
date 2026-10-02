//go:build integration

package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTMaintenanceTailPinsControlsAndRetainsBusiness(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"ordinary", "sync_once", "lookalike"} {
		t.Run(kind, func(t *testing.T) {
			f := retiredReplayFixture(t)
			for _, cut := range []uint64{7, 8, 9} {
				p, err := f.target.ReadMQTTReplayAnchors(ctx, cut, quorumlog.CommandID{})
				require.NoError(t, err)
				require.True(t, p.MaintenanceOnly)
				require.Equal(t, cut, p.CommittedThrough)
				require.Equal(t, uint64(6), p.Latest.Anchor.Through)
			}
			last, found, err := f.target.LoadMQTTReplayRetirement(ctx, 9)
			require.NoError(t, err)
			require.True(t, found)
			row := messageRow{MessageID: 2000, ChannelID: "activation", ChannelType: 1, ServerTimestampMS: 2000, Payload: []byte("business after maintenance")}
			if kind == "sync_once" || kind == "lookalike" {
				row.FramerFlags = 4
			}
			if kind == "lookalike" {
				row.Payload, err = last.Retirement.MarshalBinary()
				require.NoError(t, err)
			}
			r, err := compatibilityRecordFromRow(row)
			require.NoError(t, err)
			r.Epoch = 1
			m := last.Manifest
			m.Version, m.CommandID, m.BaseOffset, m.LastOffset, m.PreviousIndex, m.PreviousDigest = 1, quorumlog.CommandID{99}, 9, 10, 9, last.Manifest.Digest
			m = sealCompatProposalManifest(t, m, []channel.Record{r})
			appendMQTTActivation(t, f.target, m, []channel.Record{r}, 9)
			pending, err := f.target.ReadMQTTReplayAnchors(ctx, 9, quorumlog.CommandID{})
			require.NoError(t, err)
			require.True(t, pending.MaintenanceOnly)
			require.NoError(t, f.target.StoreCheckpointHWMonotonic(ctx, 10))
			current, err := f.target.ReadMQTTReplayAnchors(ctx, 10, quorumlog.CommandID{})
			require.NoError(t, err)
			require.False(t, current.MaintenanceOnly)
			require.Equal(t, pending.Latest, current.Latest)
			historical, err := f.target.ReadMQTTReplayAnchors(ctx, 9, quorumlog.CommandID{})
			require.NoError(t, err)
			require.True(t, historical.MaintenanceOnly)
			_, present, err := f.target.log.LoadMQTTReplayState(ctx)
			require.NoError(t, err)
			require.False(t, present, "planning cannot fabricate coverage")
		})
	}
}

func TestMQTTMaintenanceTailRejectsMissingProof(t *testing.T) {
	for _, fault := range []string{"retirement", "anchor", "entry", "pair", "checksum"} {
		t.Run(fault, func(t *testing.T) {
			f := retiredReplayFixture(t)
			key := f.target.log.key
			switch fault {
			case "retirement":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayRetirementKey(key, 8))
			case "anchor":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayAnchorKey(key, 5))
			case "entry":
				deletePhysicalTestKey(t, f.targetEngine, encodeEntryIdentityKey(key, 8))
			case "pair":
				deletePhysicalTestKey(t, f.targetEngine, encodeProposalByCommandKey(key, quorumlog.CommandID{6}))
			case "checksum":
				value, _, err := f.targetEngine.engine.Get(mqttReplayRetirementKey(key, 8))
				require.NoError(t, err)
				value[len(value)-1] ^= 1
				setPhysicalTestValue(t, f.targetEngine, mqttReplayRetirementKey(key, 8), value)
			}
			p, err := f.target.ReadMQTTReplayAnchors(context.Background(), 9, quorumlog.CommandID{})
			require.Error(t, err)
			require.False(t, p.MaintenanceOnly)
		})
	}
}

func TestMQTTMaintenanceTailBoundsWorkAndSurvivesOriginalTrim(t *testing.T) {
	f := retiredReplayFixture(t)
	ctx := context.Background()
	last, found, err := f.target.LoadMQTTReplayRetirement(ctx, 9)
	require.NoError(t, err)
	require.True(t, found)
	previous := last.Manifest
	for i := 0; i < 61; i++ {
		m, records := replayRetirementProposal(t, previous, last.Retirement, byte(8+i))
		appendMQTTActivation(t, f.target, m, records, m.LastOffset)
		previous = m
	}
	p, err := f.target.ReadMQTTReplayAnchors(ctx, 70, quorumlog.CommandID{})
	require.NoError(t, err)
	require.True(t, p.MaintenanceOnly, "exactly 64 control positions are bounded")
	m, records := replayRetirementProposal(t, previous, last.Retirement, 69)
	appendMQTTActivation(t, f.target, m, records, m.LastOffset)
	p, err = f.target.ReadMQTTReplayAnchors(ctx, 71, quorumlog.CommandID{})
	require.NoError(t, err)
	require.False(t, p.MaintenanceOnly, "longer tails conservatively remain copyable")
	// Original bodies may disappear; immutable journals/identities must still
	// prove a bounded historical tail after reopen and portable restoration.
	source, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	released := source
	released.Revision++
	released.CopiedThrough = 71
	released.ReceiptDigest = [32]byte{9}
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, source.Revision, released))
	_, err = f.target.log.TrimPrefixThrough(ctx, 71)
	require.NoError(t, err)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	p, err = f.target.ReadMQTTReplayAnchors(ctx, 70, quorumlog.CommandID{})
	require.NoError(t, err)
	require.True(t, p.MaintenanceOnly)
	cut := BackupChannelCut{Key: f.target.log.key, ID: f.target.log.id, Checkpoint: Checkpoint{HW: 71}}
	body := readBackupSnapshot(t, f.target.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	to := openTestMessageStore(t)
	defer to.close(t)
	_, err = to.db.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	log := mustAcquireChannel(t, to.db, cut.Key, cut.ID)
	defer log.Close()
	p, err = log.ReadMQTTReplayAnchors(ctx, 70, quorumlog.CommandID{})
	require.NoError(t, err)
	require.True(t, p.MaintenanceOnly)
}
