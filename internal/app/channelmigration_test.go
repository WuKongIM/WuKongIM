package app

import (
	"context"
	"testing"

	applifecycle "github.com/WuKongIM/WuKongIM/internal/app/lifecycle"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
	"github.com/stretchr/testify/require"
)

func TestBuildWiresChannelMigrationAfterChannelMeta(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.ApplyDefaultsAndValidate())

	executor, lifecycle := newAppChannelMigrationExecutor(cfg, cfg.Node.ID, nil, nil, nil, nil, nil)
	require.NotNil(t, executor)
	require.NotNil(t, lifecycle)

	app := &App{
		channelMetaSync:           &channelMetaSync{},
		channelMigrationExecutor:  executor,
		channelMigrationLifecycle: lifecycle,
	}
	names := appLifecycleComponentNames(app.lifecycleComponents(false))
	requireLifecycleBefore(t, names, appLifecycleManagedSlotsReady, appLifecycleChannelMeta)
	requireLifecycleBefore(t, names, appLifecycleChannelMeta, appLifecycleChannelMigration)
	requireLifecycleBefore(t, names, appLifecycleChannelMigration, appLifecycleGateway)
}

func TestChannelMigrationProbeReportUsesRuntimeReadinessFields(t *testing.T) {
	got := channelMigrationProbeReportFromResponse(3, channel.Meta{
		Key:         "g1",
		Epoch:       5,
		LeaderEpoch: 7,
		Leader:      2,
	}, channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:     "g1",
		Epoch:          5,
		LeaderEpoch:    7,
		Generation:     9,
		ReplicaID:      3,
		Leader:         2,
		Role:           channel.ReplicaRoleFollower,
		OffsetEpoch:    5,
		LogStartOffset: 4,
		LogEndOffset:   12,
		CheckpointHW:   10,
		CommitReady:    true,
	})

	require.Equal(t, channel.NodeID(2), got.Leader)
	require.Equal(t, channel.ReplicaRoleFollower, got.Role)
	require.Equal(t, uint64(4), got.LogStartOffset)
	require.True(t, got.CommitReady)
}

func TestChannelMigrationProbeReportFailsClosedForLegacyReadiness(t *testing.T) {
	got := channelMigrationProbeReportFromResponse(3, channel.Meta{
		Key:         "g1",
		Epoch:       5,
		LeaderEpoch: 7,
		Leader:      2,
	}, channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:   "g1",
		Epoch:        5,
		LeaderEpoch:  7,
		ReplicaID:    3,
		OffsetEpoch:  5,
		LogEndOffset: 10,
		CheckpointHW: 10,
	})

	require.Zero(t, got.Leader)
	require.Zero(t, got.Role)
	require.False(t, got.CommitReady)
}

func TestChannelMigrationProbeClientRequestsExtendedProbe(t *testing.T) {
	stub := &recordingChannelMigrationProbeTransport{resp: channelruntime.ReconcileProbeResponseEnvelope{
		ChannelKey:     "g1",
		Epoch:          5,
		LeaderEpoch:    7,
		ReplicaID:      3,
		Leader:         2,
		Role:           channel.ReplicaRoleFollower,
		OffsetEpoch:    5,
		LogStartOffset: 1,
		LogEndOffset:   10,
		CheckpointHW:   10,
		CommitReady:    true,
	}}
	client := appChannelMigrationProbeClient{client: stub, localNode: 9}

	_, err := client.ProbeChannel(context.Background(), 3, channel.Meta{
		Key:         "g1",
		Epoch:       5,
		LeaderEpoch: 7,
		Leader:      2,
	})

	require.NoError(t, err)
	require.Equal(t, channel.NodeID(9), stub.req.ReplicaID)
	require.True(t, stub.req.RequireExtendedResponse)
}

func appLifecycleComponentNames(components []applifecycle.Component) []string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, component.Name())
	}
	return names
}

func requireLifecycleBefore(t *testing.T, names []string, before, after string) {
	t.Helper()
	beforeIndex, afterIndex := -1, -1
	for i, name := range names {
		if name == before {
			beforeIndex = i
		}
		if name == after {
			afterIndex = i
		}
	}
	require.NotEqualf(t, -1, beforeIndex, "missing lifecycle component %s in %v", before, names)
	require.NotEqualf(t, -1, afterIndex, "missing lifecycle component %s in %v", after, names)
	require.Lessf(t, beforeIndex, afterIndex, "expected %s before %s in %v", before, after, names)
}

type recordingChannelMigrationProbeTransport struct {
	req  channelruntime.ReconcileProbeRequestEnvelope
	resp channelruntime.ReconcileProbeResponseEnvelope
}

func (t *recordingChannelMigrationProbeTransport) Probe(_ context.Context, _ channel.NodeID, req channelruntime.ReconcileProbeRequestEnvelope) (channelruntime.ReconcileProbeResponseEnvelope, error) {
	t.req = req
	return t.resp, nil
}
