package channels

import (
	"context"
	"strings"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type willReceiptRuntime struct {
	mqttSourceRuntime
	read  func(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error)
	calls int
}

func (r *willReceiptRuntime) ReadWillReceipt(ctx context.Context, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
	r.calls++
	return r.read(ctx, q)
}
func routedWillFixture(t *testing.T) (*Service, *mqttFreshMeta, *willReceiptRuntime, ch.WillReceiptRequest) {
	_, m, base, q := mqttRoutedSourceFixture(t)
	req := ch.WillReceiptRequest{ChannelID: q.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, FromUID: "sender", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64)}
	r := &willReceiptRuntime{mqttSourceRuntime: *base, read: func(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
		return ch.WillReceiptResult{CommittedThrough: 8, Found: true, Receipt: ch.WillReceipt{MessageID: 99, MessageSeq: 7, ServerTimestampMS: 1000, ContentHash: [32]byte{1}}}, nil
	}}
	s, err := NewService(Config{LocalNode: 2, MetaSource: m, Runtime: r})
	require.NoError(t, err)
	return s, m, r, req
}

func TestWillReceiptRouteFreshAuthorityAndBoundedAdmission(t *testing.T) {
	for _, mode := range []string{"success", "absent", "stable_fence", "route_before", "route_after", "fence_after", "members_after", "deleted_after", "read_error", "runtime_error", "invalid_result", "cancel", "backpressure", "unsupported_runtime", "unsupported_meta"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, q := routedWillFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.after = func(n int) {
				if n != 2 {
					return
				}
				switch mode {
				case "route_after":
					m.meta.RouteGeneration++
				case "fence_after":
					m.meta.WriteFence = ch.WriteFence{Token: "changed", Version: 2}
				case "members_after":
					m.meta.Replicas = append(m.meta.Replicas, 4)
				case "deleted_after":
					m.meta.Status = ch.StatusDeleted
				}
			}
			switch mode {
			case "absent":
				r.read = func(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
					return ch.WillReceiptResult{CommittedThrough: 8}, nil
				}
			case "stable_fence":
				m.meta.WriteFence = ch.WriteFence{Token: "move", Version: 1}
			case "route_before":
				q.ExpectedRouteGeneration++
			case "read_error":
				m.fail = context.DeadlineExceeded
			case "runtime_error":
				r.read = func(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
					return ch.WillReceiptResult{}, ch.ErrNotReady
				}
			case "invalid_result":
				r.read = func(context.Context, ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
					return ch.WillReceiptResult{Found: true}, nil
				}
			case "cancel":
				original := r.read
				r.read = func(c context.Context, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
					cancel()
					return original(c, q)
				}
			case "backpressure":
				for range cap(s.willReceiptReads) {
					s.willReceiptReads <- struct{}{}
				}
			case "unsupported_runtime":
				s.runtime = &fakeRuntime{}
			case "unsupported_meta":
				s.metaSource = NewStaticMetaSource([]ch.Meta{m.meta})
			}
			got, err := s.ReadWillReceipt(ctx, q)
			if mode == "success" || mode == "absent" || mode == "stable_fence" {
				require.NoError(t, err)
				require.True(t, got.Valid())
				require.Equal(t, mode != "absent", got.Found)
				require.Equal(t, 2, m.calls)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			if mode == "route_before" || mode == "read_error" || mode == "backpressure" || mode == "unsupported_meta" {
				require.Zero(t, r.calls)
			}
		})
	}
}

func TestWillReceiptForwardingGatewayAndExactReply(t *testing.T) {
	s, m, r, q := routedWillFixture(t)
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.ReadWillReceipt(context.Background(), q)
	require.NoError(t, err)
	require.True(t, got.Found)
	require.EqualValues(t, 99, got.Receipt.MessageID)
	require.Equal(t, 1, r.calls)
	gateway.Clear()
	_, err = origin.ReadWillReceipt(context.Background(), q)
	require.Error(t, err)
	gateway.Replace(s)
	_, err = origin.ReadWillReceipt(context.Background(), q)
	require.NoError(t, err)
	_, err = gateway.handleForwardWillReceipt(context.Background(), willReceiptForwardRequest{Leader: 3, Request: q})
	require.ErrorIs(t, err, ch.ErrNotLeader)
	request := willReceiptForwardRequest{Leader: 2, Request: q}
	body, err := encodeWillReceiptRequest(request)
	require.NoError(t, err)
	decoded, err := decodeWillReceiptRequest(body)
	require.NoError(t, err)
	require.Equal(t, request, decoded)
	for i := range body {
		_, err = decodeWillReceiptRequest(body[:i])
		require.Error(t, err)
	}
	_, err = decodeWillReceiptRequest(append(body, 0))
	require.Error(t, err)
	for _, opErr := range []error{nil, ch.ErrNotReady, ch.ErrStaleMeta, ch.ErrBackpressured, ch.ErrChannelNotFound, context.Canceled, context.DeadlineExceeded} {
		reply, err := encodeWillReceiptReply(request, got, opErr)
		require.NoError(t, err)
		actual, err := decodeWillReceiptReply(reply, request)
		require.ErrorIs(t, err, opErr)
		if opErr == nil {
			require.Equal(t, got, actual)
		} else {
			require.Zero(t, actual)
		}
		for i := range reply {
			_, err = decodeWillReceiptReply(reply[:i], request)
			require.Error(t, err)
		}
		_, err = decodeWillReceiptReply(append(reply, 0), request)
		require.Error(t, err)
		wrong := request
		wrong.Request.FromUID = "other"
		_, err = decodeWillReceiptReply(reply, wrong)
		require.Error(t, err)
	}
	request.Request.FromUID = strings.Repeat("u", 65535)
	body, err = encodeWillReceiptRequest(request)
	require.NoError(t, err)
	decoded, err = decodeWillReceiptRequest(body)
	require.NoError(t, err)
	require.Equal(t, request, decoded)
	reply, err := encodeWillReceiptReply(request, got, nil)
	require.NoError(t, err)
	actual, err := decodeWillReceiptReply(reply, request)
	require.NoError(t, err)
	require.Equal(t, got, actual)
	reply[4]++
	_, err = decodeWillReceiptReply(reply, request)
	require.Error(t, err)
	body[4]++
	_, err = decodeWillReceiptRequest(body)
	require.Error(t, err)
}
