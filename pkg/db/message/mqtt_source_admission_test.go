package message

import (
	"context"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTSourceAdmissionRequiresCommittedControlEvidence(t *testing.T) {
	ctx := context.Background()
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	defer s.Close()
	_, found, err := s.LoadCommittedMQTTSourceState(ctx, 0)
	require.NoError(t, err)
	require.False(t, found)
	m, records := mqttActivationProposal(t, DurableProposalManifest{}, 1)
	appendMQTTActivation(t, s, m, records, 0)
	_, found, err = s.LoadCommittedMQTTSourceState(ctx, 0)
	require.NoError(t, err)
	require.False(t, found)
	_, _, err = s.LoadCommittedMQTTSourceState(ctx, 1)
	require.Error(t, err, "unpersisted HW must fail")
	require.NoError(t, s.StoreCheckpointHWMonotonic(ctx, 1))
	_, found, err = s.LoadCommittedMQTTSourceState(ctx, 0)
	require.NoError(t, err)
	require.False(t, found, "later activation cannot satisfy an earlier captured boundary")
	got, found, err := s.LoadCommittedMQTTSourceState(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(0), got.StartAfter)
	require.NoError(t, s.Close())
	_, _, err = s.LoadCommittedMQTTSourceState(ctx, 1)
	require.Error(t, err)
}

func TestMQTTSourceAdmissionRejectsMissingProjectionAndLocalCAS(t *testing.T) {
	for _, missing := range []string{"marker", "source", "checkpoint", "local-cas"} {
		t.Run(missing, func(t *testing.T) {
			ctx := context.Background()
			e := openCompatEngine(t)
			s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
			defer s.Close()
			if missing == "local-cas" {
				require.NoError(t, s.log.ApplyMQTTSourceState(ctx, 0, MQTTSourceState{Generation: "only-local", Revision: 1}))
			} else {
				m, records := mqttActivationProposal(t, DurableProposalManifest{}, 1)
				appendMQTTActivation(t, s, m, records, 1)
				key := mqttActivationKey(s.log.key)
				if missing == "source" {
					key = mqttSourceKey(s.log.key)
				}
				if missing == "checkpoint" {
					key = encodeCheckpointKey(s.log.key)
				}
				deletePhysicalTestKey(t, e, key)
			}
			_, _, err := s.LoadCommittedMQTTSourceState(ctx, 0)
			require.Error(t, err)
		})
	}
}
