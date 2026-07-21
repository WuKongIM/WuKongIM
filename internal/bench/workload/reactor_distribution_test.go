package workload

import (
	"path/filepath"
	"runtime"
	"testing"

	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	benchmodel "github.com/WuKongIM/WuKongIM/pkg/bench/model"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestCloudMediumTrafficBalancesAcrossChannelReactors(t *testing.T) {
	const (
		reactorCount = 4
		// runID preserves the failed Cloud Medium run's group-channel hash seed.
		runID    = "gh-29845692322-1"
		workerID = "simulator-worker"
	)
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	scenario, err := benchconfig.LoadScenario(filepath.Join(filepath.Dir(currentFile), "../../../docker/sim/cloud-medium.yaml"))
	require.NoError(t, err)
	scenario.Run.ID = runID
	plan, err := planner.Build(scenario, []benchmodel.Worker{{ID: workerID, Weight: 1}})
	require.NoError(t, err)
	workerPlan, ok := plan.Workers[workerID]
	require.True(t, ok)
	require.Equal(t, benchmodel.Range{Start: 0, End: 10_000}, workerPlan.IdentityRange)
	profiles := make(map[string]benchmodel.ChannelProfile, len(scenario.Channels.Profiles))
	for _, profile := range scenario.Channels.Profiles {
		profiles[profile.Name] = profile
	}

	router, err := reactor.NewRouter(reactorCount)
	require.NoError(t, err)
	load := make([]float64, reactorCount)
	profileLoad := make(map[string][]float64)

	add := func(profileName, channelID string, channelType uint8, qps float64) {
		key := ch.ChannelKeyForID(ch.ChannelID{ID: channelID, Type: channelType})
		index := router.PickIndex(key)
		load[index] += qps
		if profileLoad[profileName] == nil {
			profileLoad[profileName] = make([]float64, reactorCount)
		}
		profileLoad[profileName][index] += qps
	}
	for _, profileName := range plan.ProfileOrder {
		profile, ok := profiles[profileName]
		require.True(t, ok, "profile %q", profileName)
		shard, ok := workerPlan.Profiles[profileName]
		require.True(t, ok, "profile shard %q", profileName)
		switch profile.ChannelType {
		case benchmodel.ChannelTypePerson:
			require.Equal(t, shard.ChannelRange.Len()*2, shard.ParticipantRange.Len())
			for offset := 0; offset < shard.ChannelRange.Len(); offset++ {
				senderIndex := shard.ParticipantRange.Start + offset*2
				recipientIndex := senderIndex + 1
				senderUID := indexedBenchID(scenario.Identity.UIDPrefix, senderIndex)
				recipientUID := indexedBenchID(scenario.Identity.UIDPrefix, recipientIndex)
				add(profileName, encodeBenchPersonChannel(senderUID, recipientUID), frame.ChannelTypePerson, shard.LocalRate.PerSecond)
			}
		case benchmodel.ChannelTypeGroup:
			for channelIndex := shard.ChannelRange.Start; channelIndex < shard.ChannelRange.End; channelIndex++ {
				channelID := GroupChannelID(runID, profileName, channelIndex)
				if profile.Shard.HashSlotSpread {
					channelID = GroupChannelIDForHashSlot(runID, profileName, channelIndex, profile.Shard.HashSlotCount)
				}
				add(profileName, channelID, frame.ChannelTypeGroup, shard.LocalRate.PerSecond)
			}
		default:
			t.Fatalf("unsupported channel type %q", profile.ChannelType)
		}
	}

	for profileName, qps := range profileLoad {
		t.Logf("%s load=%v", profileName, qps)
	}
	totalQPS := 0.0
	for _, qps := range load {
		totalQPS += qps
	}
	require.Equal(t, 5_000.0, scenario.Objectives.IngressQPS.PerSecond)
	require.InDelta(t, scenario.Objectives.IngressQPS.PerSecond, totalQPS, 0.000001)
	for index, qps := range load {
		require.InDelta(t, 1_250, qps, 1_250*0.05, "reactor %d load=%v all=%v", index, qps, load)
	}
}
