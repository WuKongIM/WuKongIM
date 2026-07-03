package devsim

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestStatusTransitionsAndCounters(t *testing.T) {
	status := NewStatus("dev-sim-run")

	initial := status.Snapshot()
	require.Equal(t, StateStarting, initial.State)
	require.Equal(t, "dev-sim-run", initial.RunID)
	require.NotZero(t, initial.LastTransitionAt)

	status.SetRunning(20, 5, 2)
	status.AddMessagesSent(3)
	status.AddSendErrors(1)
	status.SetLastError("temporary failure")

	snapshot := status.Snapshot()
	require.Equal(t, StateRunning, snapshot.State)
	require.Equal(t, 20, snapshot.ConnectedUsers)
	require.Equal(t, 20, snapshot.ActiveUsers)
	require.Equal(t, 5, snapshot.PersonChannels)
	require.Equal(t, 2, snapshot.GroupChannels)
	require.Equal(t, uint64(3), snapshot.MessagesSent)
	require.Equal(t, uint64(1), snapshot.SendErrors)
	require.Equal(t, "temporary failure", snapshot.LastError)
	require.Zero(t, snapshot.ReconnectedUsers)

	snapshot.LastError = "mutated copy"
	require.Equal(t, "temporary failure", status.Snapshot().LastError)
}

func TestStatusTracksConnectionFluctuations(t *testing.T) {
	status := NewStatus("dev-sim-run")

	status.SetRunning(20, 5, 2)
	status.SetConnectionStats(17, 3)
	snapshot := status.Snapshot()
	require.Equal(t, 17, snapshot.ActiveUsers)
	require.Equal(t, uint64(3), snapshot.ReconnectedUsers)

	status.SetRunning(20, 5, 2)
	snapshot = status.Snapshot()
	require.Equal(t, 20, snapshot.ActiveUsers)
	require.Equal(t, uint64(3), snapshot.ReconnectedUsers)
}

func TestStatusClonesConfigSnapshot(t *testing.T) {
	status := NewStatus("dev-sim-run")
	status.SetConfig(ConfigSnapshot{
		TargetAPIAddrs:  []string{"http://wk-node1:5001"},
		GatewayTCPAddrs: []string{"wk-node1:5100"},
		UIDPrefix:       "sim-u",
		Warmup:          "10s",
	})

	snapshot := status.Snapshot()
	require.NotNil(t, snapshot.Config)
	require.Equal(t, "sim-u", snapshot.Config.UIDPrefix)
	snapshot.Config.UIDPrefix = "mutated"
	snapshot.Config.TargetAPIAddrs[0] = "http://changed"

	fresh := status.Snapshot()
	require.Equal(t, "sim-u", fresh.Config.UIDPrefix)
	require.Equal(t, "http://wk-node1:5001", fresh.Config.TargetAPIAddrs[0])
}

func TestConfigSnapshotFormatsRatesAndDurations(t *testing.T) {
	cfg := Config{
		Target: TargetConfig{
			APIAddrs:        []string{"http://wk-node1:5001"},
			GatewayTCPAddrs: []string{"wk-node1:5100"},
		},
		Identity: IdentityConfig{
			UIDPrefix:       "sim-u",
			DevicePrefix:    "sim-d",
			ClientMsgPrefix: "sim-msg",
			TokenMode:       "bench_api",
		},
		Online: OnlineConfig{
			TotalUsers:  20,
			ConnectRate: model.Rate{PerSecond: 12.5},
		},
		Profiles: ProfilesConfig{
			PersonChannels: 5,
			GroupChannels:  2,
			GroupMembers:   10,
		},
		Traffic: TrafficConfig{
			PayloadSizeBytes:     128,
			PersonRatePerChannel: model.Rate{PerSecond: 0.25},
			GroupRatePerChannel:  model.Rate{PerSecond: 0.5},
			Concurrency:          16,
			VerifyRecv:           "sampled",
			Warmup:               10 * time.Second,
			Window:               20 * time.Second,
			Cooldown:             time.Second,
		},
		Retry: RetryConfig{
			ReadinessTimeout: 2 * time.Minute,
			RestartBackoff:   5 * time.Second,
		},
	}

	snapshot := cfg.Snapshot()
	require.Equal(t, "12.5/s", snapshot.ConnectRate)
	require.Equal(t, "0.25/s", snapshot.PersonRatePerChannel)
	require.Equal(t, "0.5/s", snapshot.GroupRatePerChannel)
	require.Equal(t, "10s", snapshot.Warmup)
	require.Equal(t, "20s", snapshot.Window)
	require.Equal(t, "1s", snapshot.Cooldown)
	require.Equal(t, "2m0s", snapshot.ReadinessTimeout)
	require.Equal(t, "5s", snapshot.RestartBackoff)
}
