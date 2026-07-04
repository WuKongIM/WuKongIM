package transport

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
	baseTransport "github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"github.com/stretchr/testify/require"
)

func TestMigrationControlClientDrainsRemoteLeader(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	server.HandleRPCMux(mux)
	require.NoError(t, server.Start("127.0.0.1:0"))
	defer server.Stop()

	service := &migrationRuntimeStub{
		result: channel.DrainResult{
			ChannelKey:        "g-drain",
			LEO:               12,
			HW:                11,
			CheckpointHW:      10,
			ChannelEpoch:      7,
			LeaderEpoch:       9,
			WriteFenceVersion: 3,
		},
	}
	_, err := New(Options{
		LocalNode: 2,
		Client: baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
			addrs: map[uint64]string{},
		}, 1, time.Second)),
		RPCMux:       mux,
		FetchService: service,
	})
	require.NoError(t, err)

	client := baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()
	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    baseTransport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(context.Context, runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
		RPCTimeout: time.Second,
	})
	require.NoError(t, err)

	req := channel.FenceAndDrainRequest{
		ChannelKey:           "g-drain",
		TaskID:               "task-drain",
		WriteFenceToken:      "task-drain",
		WriteFenceVersion:    3,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  9,
		ExpectedLeader:       2,
	}
	got, err := adapter.FenceAndDrain(context.Background(), 2, req)
	require.NoError(t, err)
	require.Equal(t, service.result, got)
	require.Equal(t, []channel.FenceAndDrainRequest{req}, service.callSnapshot())
}

func TestMigrationControlRejectsDrainOnNonLeaderOrFenceMismatch(t *testing.T) {
	adapter, err := New(Options{
		LocalNode: 2,
		Client: baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
			addrs: map[uint64]string{},
		}, 1, time.Second)),
		RPCMux: baseTransport.NewRPCMux(),
		FetchService: &migrationRuntimeStub{
			err: channel.ErrStaleMeta,
		},
	})
	require.NoError(t, err)

	_, err = adapter.handleMigrationControlDrainRPC(context.Background(), mustEncodeFenceAndDrainRequest(t, channel.FenceAndDrainRequest{
		ChannelKey:           "g-drain",
		WriteFenceToken:      "wrong",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeader:       2,
	}))
	require.ErrorIs(t, err, channel.ErrStaleMeta)

	adapter.BindFetchService(&migrationRuntimeStub{err: channel.ErrNotLeader})
	_, err = adapter.handleMigrationControlDrainRPC(context.Background(), mustEncodeFenceAndDrainRequest(t, channel.FenceAndDrainRequest{
		ChannelKey:           "g-drain",
		WriteFenceToken:      "task",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeader:       2,
	}))
	require.ErrorIs(t, err, channel.ErrNotLeader)
}

func TestMigrationControlClientNormalizesRemoteDrainErrors(t *testing.T) {
	err := normalizeMigrationControlError(fmt.Errorf("remote error: %s", channel.ErrStaleMeta))
	require.ErrorIs(t, err, channel.ErrStaleMeta)
	err = normalizeMigrationControlError(fmt.Errorf("remote error: %s", channel.ErrLeaseExpired))
	require.ErrorIs(t, err, channel.ErrLeaseExpired)
	err = normalizeMigrationControlError(fmt.Errorf("remote error: %s", channel.ErrChannelNotFound))
	require.ErrorIs(t, err, channel.ErrChannelNotFound)
}

func mustEncodeFenceAndDrainRequest(t *testing.T, req channel.FenceAndDrainRequest) []byte {
	t.Helper()
	body, err := encodeFenceAndDrainRequest(req)
	require.NoError(t, err)
	return body
}

type migrationRuntimeStub struct {
	mu     sync.Mutex
	calls  []channel.FenceAndDrainRequest
	result channel.DrainResult
	err    error
}

func (s *migrationRuntimeStub) ServeFetch(context.Context, runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
	return runtime.FetchResponseEnvelope{}, nil
}

func (s *migrationRuntimeStub) FenceAndDrain(_ context.Context, req channel.FenceAndDrainRequest) (channel.DrainResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	return s.result, s.err
}

func (s *migrationRuntimeStub) callSnapshot() []channel.FenceAndDrainRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]channel.FenceAndDrainRequest(nil), s.calls...)
}
