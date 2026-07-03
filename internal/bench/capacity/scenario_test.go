package capacity

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestBuildScenarioMixedSplitsOfferedQPS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Profile = ProfileMixed
	cfg.Duration = time.Minute
	cfg.Warmup = time.Second
	cfg.Cooldown = time.Second
	cfg.GroupMembers = 10

	s := BuildScenario(cfg, Attempt{Index: 2, OfferedQPS: 100})

	require.Equal(t, "wkbench/v1", s.Version)
	require.Contains(t, s.Run.ID, "capacity-send")
	require.Equal(t, time.Minute, s.Run.Duration)
	require.Len(t, s.Channels.Profiles, 2)
	require.Equal(t, "person-chat", s.Channels.Profiles[0].Name)
	require.Equal(t, 50, s.Channels.Profiles[0].Count)
	require.Equal(t, model.ChannelTypePerson, s.Channels.Profiles[0].ChannelType)
	require.Equal(t, "small-group", s.Channels.Profiles[1].Name)
	require.Equal(t, 50, s.Channels.Profiles[1].Count)
	require.Equal(t, 10, s.Channels.Profiles[1].Members.Count)
	require.Len(t, s.Messages.Traffic, 2)
	require.Equal(t, 1.0, s.Messages.Traffic[0].RatePerChannel.PerSecond)
	require.Equal(t, 1.0, s.Messages.Traffic[1].RatePerChannel.PerSecond)
	require.Equal(t, "none", s.Messages.Traffic[0].Verify.Recv.Mode)
}

func TestBuildScenarioPersonOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Profile = ProfilePerson

	s := BuildScenario(cfg, Attempt{Index: 0, OfferedQPS: 12})

	require.Len(t, s.Channels.Profiles, 1)
	require.Equal(t, model.ChannelTypePerson, s.Channels.Profiles[0].ChannelType)
	require.Equal(t, 12, s.Channels.Profiles[0].Count)
	require.Equal(t, 24, s.Online.TotalUsers)
}

func TestBuildScenarioGroupOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Profile = ProfileGroup
	cfg.GroupMembers = 7

	s := BuildScenario(cfg, Attempt{Index: 0, OfferedQPS: 12})

	require.Len(t, s.Channels.Profiles, 1)
	require.Equal(t, model.ChannelTypeGroup, s.Channels.Profiles[0].ChannelType)
	require.Equal(t, 12, s.Channels.Profiles[0].Count)
	require.Equal(t, 7, s.Channels.Profiles[0].Members.Count)
	require.Equal(t, 84, s.Online.TotalUsers)
}

func TestBuildScenarioUsesValidHeartbeat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}

	s := BuildScenario(cfg, Attempt{Index: 0, OfferedQPS: 100})

	require.True(t, s.Online.Heartbeat.Enabled)
	require.Greater(t, s.Online.Heartbeat.Interval, time.Duration(0))
	require.Greater(t, s.Online.Heartbeat.Timeout, time.Duration(0))
}

func TestBuildHotChannelScenarioKeepsOneGroupChannelWithSenderFanIn(t *testing.T) {
	cfg := DefaultHotChannelConfig()
	cfg.Config.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Senders = 16
	cfg.Duration = time.Minute
	cfg.Warmup = time.Second
	cfg.Cooldown = time.Second

	s := BuildHotChannelScenario(cfg, Attempt{Index: 3, OfferedQPS: 1200})

	require.Equal(t, "wkbench/v1", s.Version)
	require.Contains(t, s.Run.ID, "capacity-hot-channel")
	require.Equal(t, time.Minute, s.Run.Duration)
	require.Equal(t, 16, s.Online.TotalUsers)
	require.Len(t, s.Channels.Profiles, 1)
	profile := s.Channels.Profiles[0]
	require.Equal(t, "hot-group", profile.Name)
	require.Equal(t, model.ChannelTypeGroup, profile.ChannelType)
	require.Equal(t, 1, profile.Count)
	require.Equal(t, 16, profile.Members.Count)
	require.Equal(t, "disallowed", profile.Members.Overlap)
	require.Equal(t, 1.0, profile.Online.MemberRatio)
	require.Len(t, s.Messages.Traffic, 1)
	traffic := s.Messages.Traffic[0]
	require.Equal(t, "hot-group-send", traffic.Name)
	require.Equal(t, "hot-group", traffic.ChannelRef)
	require.Equal(t, 1200.0, traffic.RatePerChannel.PerSecond)
	require.Equal(t, "round_robin", traffic.SenderPick)
	require.Equal(t, "none", traffic.Verify.Recv.Mode)
	require.GreaterOrEqual(t, traffic.Concurrency, 16)
}
