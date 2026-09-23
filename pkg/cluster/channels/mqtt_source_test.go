package channels

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttFreshMeta struct {
	meta  ch.Meta
	calls int
	fail  error
	after func(int)
}

func (m *mqttFreshMeta) ResolveChannelMeta(context.Context, ch.ChannelID) (ch.Meta, error) {
	return ch.Meta{}, errors.New("ordinary metadata path must not serve source activation")
}
func (m *mqttFreshMeta) ResolveChannelMetaFresh(_ context.Context, _ ch.ChannelID) (ch.Meta, error) {
	m.calls++
	if m.after != nil {
		m.after(m.calls)
	}
	return cloneMeta(m.meta), m.fail
}

type mqttSourceRuntime struct {
	fakeRuntime
	ensureCalls int
	ensure      func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error)
}

func (r *mqttSourceRuntime) ApplyMetaContext(_ context.Context, m ch.Meta) error {
	return r.ApplyMeta(m)
}
func (r *mqttSourceRuntime) EnsureMQTTSource(ctx context.Context, q ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	r.ensureCalls++
	return r.ensure(ctx, q)
}

func mqttRoutedSourceFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttSourceRuntime, ch.MQTTSourceRequest) {
	t.Helper()
	id := ch.ChannelID{ID: "source", Type: 2}
	m := &mqttFreshMeta{meta: ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 2, LeaderEpoch: 3, RouteGeneration: 4, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}}
	r := &mqttSourceRuntime{ensure: func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
		return ch.MQTTSourceSnapshot{Generation: quorumlog.MQTTSourceGeneration(ch.CommandID{1}), StartAfter: 4, CommittedThrough: 8}, nil
	}}
	s, err := NewService(Config{LocalNode: 2, MetaSource: m, Runtime: r})
	require.NoError(t, err)
	q := ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, MessageID: 99, ServerTimestampMS: 1000}
	return s, m, r, q
}

func TestMQTTSourceRouteRechecksAuthorityAndCancellation(t *testing.T) {
	for _, mode := range []string{"success", "route-before", "route-after", "fence-after", "deleted-after", "read-error", "cancel", "runtime-error", "invalid-source"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, q := mqttRoutedSourceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "route-before":
				m.meta.RouteGeneration++
			case "route-after":
				m.after = func(n int) {
					if n == 2 {
						m.meta.RouteGeneration++
					}
				}
			case "fence-after":
				m.after = func(n int) {
					if n == 2 {
						m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
					}
				}
			case "deleted-after":
				m.after = func(n int) {
					if n == 2 {
						m.meta.Status = ch.StatusDeleted
					}
				}
			case "read-error":
				m.fail = context.DeadlineExceeded
			case "cancel":
				original := r.ensure
				r.ensure = func(c context.Context, q ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
					cancel()
					return original(c, q)
				}
			case "runtime-error":
				r.ensure = func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
					return ch.MQTTSourceSnapshot{}, ch.ErrLogConflict
				}
			case "invalid-source":
				r.ensure = func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
					return ch.MQTTSourceSnapshot{Generation: "wrong", StartAfter: 8, CommittedThrough: 8}, nil
				}
			}
			got, err := s.EnsureMQTTSource(ctx, q)
			if mode == "success" {
				require.NoError(t, err)
				require.Equal(t, uint64(8), got.CommittedThrough)
				require.Equal(t, 2, m.calls)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			if mode == "route-before" || mode == "read-error" {
				require.Zero(t, r.ensureCalls)
			}
		})
	}
}

func TestMQTTSourceRouteForwardingAndGatewayReplacement(t *testing.T) {
	s, m, r, q := mqttRoutedSourceFixture(t)
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.EnsureMQTTSource(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, uint64(8), got.CommittedThrough)
	require.Equal(t, 1, r.ensureCalls)
	old := gateway.current.Load()
	gateway.Clear()
	_, err = origin.EnsureMQTTSource(context.Background(), q)
	require.Error(t, err)
	gateway.Replace(old)
	_, err = origin.EnsureMQTTSource(context.Background(), q)
	require.NoError(t, err)
	// A forwarded request addressed to another leader must never forward again.
	_, err = gateway.handleForwardMQTTSource(context.Background(), mqttSourceForwardRequest{Leader: 3, Request: q})
	require.ErrorIs(t, err, ch.ErrNotLeader)
	noFresh, err := NewService(Config{LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{m.meta}), Runtime: r})
	require.NoError(t, err)
	_, err = noFresh.EnsureMQTTSource(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestMQTTSourceRPCClosedCodecAndExactEcho(t *testing.T) {
	_, _, _, q := mqttRoutedSourceFixture(t)
	req := mqttSourceForwardRequest{Leader: 2, Request: q}
	body, err := encodeMQTTSourceRequest(req)
	require.NoError(t, err)
	got, err := decodeMQTTSourceRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
	for cut := 0; cut < len(body); cut++ {
		_, err = decodeMQTTSourceRequest(body[:cut])
		require.Error(t, err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), body...), 0), make([]byte, mqttSourceRPCMaxBytes+1)} {
		_, err = decodeMQTTSourceRequest(bad)
		require.Error(t, err)
	}
	badVersion := append([]byte(nil), body...)
	badVersion[4]++
	_, err = decodeMQTTSourceRequest(badVersion)
	require.Error(t, err)
	source := ch.MQTTSourceSnapshot{Generation: quorumlog.MQTTSourceGeneration(ch.CommandID{1}), StartAfter: 4, CommittedThrough: 8}
	for _, want := range []error{nil, ch.ErrNotLeader, ch.ErrNotReady, ch.ErrWriteFenced, ch.ErrStaleMeta, ch.ErrLogConflict, ch.ErrBackpressured, ch.ErrChannelNotFound, context.Canceled, context.DeadlineExceeded} {
		b, err := encodeMQTTSourceReply(req, source, want)
		require.NoError(t, err)
		actual, err := decodeMQTTSourceReply(b, req)
		require.ErrorIs(t, err, want)
		if want == nil {
			require.Equal(t, source, actual)
		} else {
			require.Zero(t, actual)
		}
		mismatch := req
		mismatch.Request.MessageID++
		_, err = decodeMQTTSourceReply(b, mismatch)
		require.Error(t, err)
		_, err = decodeMQTTSourceReply(append(b, 0), req)
		require.Error(t, err)
	}
}
