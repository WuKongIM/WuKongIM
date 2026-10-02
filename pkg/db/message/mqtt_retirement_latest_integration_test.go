//go:build integration

package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTLatestRetirementPinsCommittedProofAndSurvivesRestart(t *testing.T) {
	f := retiredReplayFixture(t)
	ctx := context.Background()
	for _, hw := range []uint64{7, 8, 9} {
		setPhysicalTestValue(t, f.targetEngine, encodeCheckpointKey(f.target.log.key), encodeCheckpoint(Checkpoint{HW: hw}))
		p, found, err := f.target.LoadLatestMQTTReplayRetirement(ctx, f.generation)
		require.NoError(t, err)
		require.Equal(t, hw > 7, found)
		if found {
			require.Equal(t, hw, p.Manifest.LastOffset)
		} else {
			require.Zero(t, p)
		}
	}
	want, _, err := f.target.LoadLatestMQTTReplayRetirement(ctx, f.generation)
	require.NoError(t, err)
	_, present, err := f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, present, "discovery must not materialize replay or cleanup")
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	got, found, err := f.target.LoadLatestMQTTReplayRetirement(ctx, f.generation)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, found, err = f.target.LoadLatestMQTTReplayRetirement(canceled, f.generation)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, found)
	require.Zero(t, got)
}

func TestMQTTLatestRetirementRejectsBrokenEvidence(t *testing.T) {
	for _, fault := range []string{"source", "activation", "anchor", "journal_at_hw", "entry", "pair", "foreign", "empty_generation", "baseline_ahead", "closed"} {
		t.Run(fault, func(t *testing.T) {
			f := retiredReplayFixture(t)
			key, generation := f.target.log.key, f.generation
			switch fault {
			case "source":
				deletePhysicalTestKey(t, f.targetEngine, mqttSourceKey(key))
			case "activation":
				deletePhysicalTestKey(t, f.targetEngine, mqttActivationKey(key))
			case "anchor":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayAnchorKey(key, 7))
			case "journal_at_hw":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayRetirementKey(key, 9))
			case "entry":
				deletePhysicalTestKey(t, f.targetEngine, encodeEntryIdentityKey(key, 9))
			case "pair":
				deletePhysicalTestKey(t, f.targetEngine, encodeProposalByCommandKey(key, quorumlog.CommandID{7}))
			case "foreign":
				generation = quorumlog.MQTTSourceGeneration(quorumlog.CommandID{99})
			case "empty_generation":
				generation = ""
			case "baseline_ahead":
				_, err := f.target.RetireMQTTReplay(context.Background(), generation, 9, 1)
				require.NoError(t, err)
				setPhysicalTestValue(t, f.targetEngine, encodeCheckpointKey(key), encodeCheckpoint(Checkpoint{HW: 8}))
			case "closed":
				require.NoError(t, f.target.Close())
			}
			got, found, err := f.target.LoadLatestMQTTReplayRetirement(context.Background(), generation)
			require.Error(t, err)
			require.False(t, found)
			require.Zero(t, got)
		})
	}
}
