package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type routeFencedRuntime struct {
	fakeRuntime
	single ch.AppendRequest
	batch  ch.AppendBatchRequest
}

func (r *routeFencedRuntime) ApplyMetaContext(ctx context.Context, m ch.Meta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.ApplyMeta(m)
}
func (r *routeFencedRuntime) Append(ctx context.Context, q ch.AppendRequest) (ch.AppendResult, error) {
	r.single = q
	return r.fakeRuntime.Append(ctx, q)
}
func (r *routeFencedRuntime) AppendBatch(ctx context.Context, q ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	r.batch = q
	return r.fakeRuntime.AppendBatch(ctx, q)
}

func routeFencedAppendFixture(t *testing.T) (*Service, *mqttFreshMeta, *routeFencedRuntime, ch.AppendRequest) {
	t.Helper()
	_, source, _, sourceReq := mqttRoutedSourceFixture(t)
	r := &routeFencedRuntime{}
	s, err := NewService(Config{LocalNode: 2, MetaSource: source, Runtime: r})
	require.NoError(t, err)
	q := ch.AppendRequest{ChannelID: sourceReq.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Message: codecContractMessage(1), CommitMode: ch.CommitModeQuorum}
	return s, source, r, q
}

func routeAppendBatch(q ch.AppendRequest) ch.AppendBatchRequest {
	return ch.AppendBatchRequest{ChannelID: q.ChannelID, ExpectedChannelEpoch: q.ExpectedChannelEpoch,
		ExpectedLeaderEpoch: q.ExpectedLeaderEpoch, ExpectedRouteGeneration: q.ExpectedRouteGeneration,
		CommitMode: q.CommitMode, Messages: []ch.Message{q.Message}}
}

func callRouteAppend(s *Service, ctx context.Context, q ch.AppendRequest, batch bool) error {
	if batch {
		_, err := s.AppendBatch(ctx, routeAppendBatch(q))
		return err
	}
	_, err := s.Append(ctx, q)
	return err
}

func TestAppendRouteFenceFreshAuthority(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, mode := range []string{"exact", "stale", "missing-epoch", "missing-leader-epoch", "local-commit", "missing", "deleted", "foreign", "fenced", "read-failure", "cancel-before", "cancel-read", "no-fresh", "no-context-apply"} {
			t.Run(mode+map[bool]string{false: "/single", true: "/batch"}[batch], func(t *testing.T) {
				s, source, r, q := routeFencedAppendFixture(t)
				// A warm, valid ordinary cache must never replace the fresh Slot read.
				require.NoError(t, s.ApplyMeta(source.meta))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				want := error(nil)
				switch mode {
				case "stale":
					source.meta.RouteGeneration++
					want = ch.ErrStaleMeta
				case "missing-epoch":
					q.ExpectedChannelEpoch = 0
					want = ch.ErrInvalidConfig
				case "missing-leader-epoch":
					q.ExpectedLeaderEpoch = 0
					want = ch.ErrInvalidConfig
				case "local-commit":
					q.CommitMode = ch.CommitModeLocal
					want = ch.ErrInvalidConfig
				case "missing":
					source.fail = ch.ErrChannelNotFound
					want = source.fail
				case "deleted":
					source.meta.Status = ch.StatusDeleted
					want = ch.ErrNotReady
				case "foreign":
					source.meta.ID.ID = "foreign"
					want = ch.ErrNotReady
				case "fenced":
					source.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
					want = ch.ErrWriteFenced
				case "read-failure":
					source.fail = context.DeadlineExceeded
					want = source.fail
				case "cancel-before":
					cancel()
					want = context.Canceled
				case "cancel-read":
					source.after = func(int) { cancel() }
					want = context.Canceled
				case "no-fresh":
					s.metaSource = NewStaticMetaSource([]ch.Meta{source.meta})
					want = ch.ErrInvalidConfig
				case "no-context-apply":
					s.runtime = &r.fakeRuntime
					want = ch.ErrInvalidConfig
				}
				err := callRouteAppend(s, ctx, q, batch)
				require.ErrorIs(t, err, want)
				if want != nil {
					require.Zero(t, r.appendCalls+r.appendBatchCalls)
					return
				}
				require.Equal(t, 1, source.calls)
				require.Equal(t, 1, r.appendCalls+r.appendBatchCalls)
				if batch {
					require.Equal(t, routeAppendBatch(q), r.batch)
				} else {
					require.Equal(t, q, r.single)
				}
			})
		}
	}
}

func TestAppendRouteFenceForwardingRechecksServingAuthority(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, stale := range []bool{false, true} {
			s, source, r, q := routeFencedAppendFixture(t)
			network := clusternet.NewLocalNetwork()
			RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, NewServiceGateway(s))
			origin, err := NewService(Config{LocalNode: 1, MetaSource: source, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
			require.NoError(t, err)
			if stale {
				source.after = func(n int) {
					if n == 2 {
						source.meta.RouteGeneration++
					}
				}
			}
			err = callRouteAppend(origin, context.Background(), q, batch)
			if stale {
				require.ErrorIs(t, err, ch.ErrStaleMeta)
				require.Zero(t, r.appendCalls+r.appendBatchCalls)
			} else {
				require.NoError(t, err)
				if batch {
					require.Equal(t, routeAppendBatch(q), r.batch)
				} else {
					require.Equal(t, q, r.single)
				}
			}
			require.Equal(t, 2, source.calls, "origin and serving node each require a fresh read")
		}
	}
}

func TestAppendRouteFenceCodecAndNoLossyFallback(t *testing.T) {
	_, _, _, q := routeFencedAppendFixture(t)
	q.Message = publicationCodecMessage(t)
	for _, batch := range []bool{false, true} {
		encode := func(q ch.AppendRequest, version uint8) ([]byte, error) {
			if batch {
				return encodeAppendBatchRequestVersion(routeAppendBatch(q), version)
			}
			return encodeAppendRequestVersion(q, version)
		}
		decode := func(b []byte) error {
			if batch {
				_, err := decodeAppendBatchRequest(b)
				return err
			}
			_, err := decodeAppendRequest(b)
			return err
		}
		b, err := encode(q, 12)
		require.NoError(t, err)
		require.Equal(t, byte(12), b[0])
		require.NoError(t, decode(b))
		if batch {
			got, err := decodeAppendBatchRequest(b)
			require.NoError(t, err)
			require.Equal(t, routeAppendBatch(q), got)
		} else {
			got, err := decodeAppendRequest(b)
			require.NoError(t, err)
			require.Equal(t, q, got)
		}
		assertEveryStrictPrefixRejected(t, b, decode)
		require.Error(t, decode(append(append([]byte(nil), b...), 0)))
		for version := uint8(5); version <= 11; version++ {
			_, err := encode(q, version)
			require.Error(t, err)
		}
		plain := q
		plain.ExpectedRouteGeneration = 0
		want, err := encode(plain, 11)
		require.NoError(t, err)
		var got []byte
		if batch {
			got, err = encodeAppendBatchRequest(routeAppendBatch(plain))
		} else {
			got, err = encodeAppendRequest(plain)
		}
		require.NoError(t, err)
		require.Equal(t, want, got)
		// An old peer rejects v12. No second request may discard preparation.
		network := clusternet.NewLocalNetwork()
		calls := 0
		serviceID := clusternet.RPCChannelAppend
		if batch {
			serviceID = clusternet.RPCChannelAppendBatch
		}
		network.Register(2, serviceID, clusternet.HandlerFunc(func(_ context.Context, b []byte) ([]byte, error) {
			calls++
			require.Equal(t, byte(12), b[0])
			return nil, errInvalidCodecFrame
		}))
		client := NewTransportClient(network)
		if batch {
			_, err = client.ForwardAppendBatch(context.Background(), 2, routeAppendBatch(q))
		} else {
			_, err = client.ForwardAppend(context.Background(), 2, q)
		}
		require.Error(t, err)
		require.Equal(t, 1, calls)
	}
}

func TestAppendRouteFenceRejectsLegacySuccessReply(t *testing.T) {
	_, _, _, q := routeFencedAppendFixture(t)
	for _, batch := range []bool{false, true} {
		network := clusternet.NewLocalNetwork()
		serviceID, kind := clusternet.RPCChannelAppend, kindAppendResponse
		var result any = ch.AppendResult{MessageID: q.Message.MessageID, MessageSeq: 1}
		if batch {
			serviceID, kind = clusternet.RPCChannelAppendBatch, kindAppendBatchResponse
			result = ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{MessageID: q.Message.MessageID, MessageSeq: 1}}}
		}
		network.Register(2, serviceID, clusternet.HandlerFunc(func(_ context.Context, b []byte) ([]byte, error) {
			require.Equal(t, byte(12), b[0])
			return encodeRPCResultVersion(10, kind, result, nil)
		}))
		client := NewTransportClient(network)
		var err error
		if batch {
			_, err = client.ForwardAppendBatch(context.Background(), 2, routeAppendBatch(q))
		} else {
			_, err = client.ForwardAppend(context.Background(), 2, q)
		}
		require.Error(t, err, "a legacy success cannot attest prepared-append support")
	}
}

func TestAppendRouteFenceV12RequiresPositiveFence(t *testing.T) {
	_, _, _, q := routeFencedAppendFixture(t)
	q.ExpectedRouteGeneration = 0
	_, err := encodeAppendRequestVersion(q, 12)
	if err == nil {
		t.Error("single v12 encoder accepted an absent fence")
	}
	_, err = encodeAppendBatchRequestVersion(routeAppendBatch(q), 12)
	if err == nil {
		t.Error("batch v12 encoder accepted an absent fence")
	}
}
