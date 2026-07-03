package transport

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	baseTransport "github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

type traceEventSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *traceEventSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *traceEventSink) Snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

func TestLongPollRequestTraceRecordsTimeoutDetails(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	started := make(chan struct{}, 1)
	mux.Handle(RPCServiceLongPollFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	server.HandleRPCMux(mux)
	require.NoError(t, server.Start("127.0.0.1:0"))
	defer server.Stop()

	client := baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode:         1,
		Client:            client,
		RPCMux:            baseTransport.NewRPCMux(),
		RPCTimeout:        20 * time.Millisecond,
		LongPollLaneCount: 4,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	require.NoError(t, err)
	defer adapter.Close()

	sink := &traceEventSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.SessionManager().Session(2).Send(runtime.Envelope{
			Peer:       2,
			Kind:       runtime.MessageKindLanePollRequest,
			ChannelKey: channel.ChannelKey("lane-parent"),
			LanePollRequest: &runtime.LanePollRequestEnvelope{
				LaneID:          1,
				LaneCount:       4,
				Op:              runtime.LanePollOpPoll,
				ProtocolVersion: 1,
				MaxWait:         time.Second,
				MaxBytes:        64 * 1024,
				MaxChannels:     64,
				CursorDelta: []runtime.LaneCursorDelta{{
					ChannelKey:        channel.ChannelKey("delta-key"),
					ChannelEpoch:      9,
					ChannelGeneration: 3,
					MatchOffset:       11,
					OffsetEpoch:       4,
				}},
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for long poll rpc handler to block")
	}

	select {
	case err := <-errCh:
		require.True(t, errors.Is(err, context.DeadlineExceeded), "err=%v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("expected configured rpc timeout to abort long poll request")
	}

	events := sink.Snapshot()
	require.Len(t, events, 2)
	require.Equal(t, sendtrace.StageRuntimeLanePollRequestSend, events[0].Stage)
	require.Equal(t, sendtrace.StageRuntimeLaneCursorDeltaSend, events[1].Stage)
	for _, event := range events {
		require.Equal(t, sendtrace.ResultTimeout, event.Result)
		require.Equal(t, "deadline_exceeded", event.ErrorCode)
		require.Equal(t, context.DeadlineExceeded.Error(), event.Error)
		require.Equal(t, uint64(1), event.NodeID)
		require.Equal(t, uint64(2), event.PeerNodeID)
	}
	require.Equal(t, "long_poll_fetch", events[0].Service)
	require.Equal(t, "lane-parent", events[0].ChannelKey)
	require.Equal(t, "lane_cursor_delta", events[1].Service)
	require.Equal(t, "delta-key", events[1].ChannelKey)
}
