package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttReplayRuntime struct {
	mqttSourceRuntime
	prepareCalls int
	prepare      func(context.Context, ch.MQTTReplayRequest) (ch.MQTTReplayPage, error)
}

func (r *mqttReplayRuntime) PrepareMQTTReplay(ctx context.Context, q ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
	r.prepareCalls++
	return r.prepare(ctx, q)
}
func mqttRoutedReplayFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttReplayRuntime, ch.MQTTReplayRequest, ch.MQTTReplayPage) {
	t.Helper()
	_, m, _, source := mqttRoutedSourceFixture(t)
	gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
	q := ch.MQTTReplayRequest{ChannelID: source.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4,
		Range: ch.MQTTReplayRange{Generation: gen, From: 1, Through: 4, Limit: 256, MaxBytes: 1 << 20}}
	body := bytes.Repeat([]byte("replay-content"), 1024)
	size := uint64(len(body))
	p := ch.MQTTReplayPage{Before: ch.MQTTReplayPrefix{Generation: gen}, After: ch.MQTTReplayPrefix{Generation: gen, Through: 1, TotalBytes: size, TotalStoredBytes: size, Digest: [32]byte{1}},
		Records: []ch.MQTTReplayRecord{{Position: 1, ContentVersion: 1, MessageID: 42, AccountedBytes: size, TotalBytes: size, TotalStoredBytes: size, ContentHash: [32]byte{2}, Digest: [32]byte{1}, Content: body}}}
	r := &mqttReplayRuntime{prepare: func(ctx context.Context, actual ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
		require.Equal(t, q, actual)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return p, nil
	}}
	s, err := NewService(Config{LocalNode: 2, MetaSource: m, Runtime: r})
	require.NoError(t, err)
	return s, m, r, q, p
}

func TestMQTTReplayRouteRequiresFreshStableAuthority(t *testing.T) {
	for _, mode := range []string{"success", "route_before", "route_after", "leader_after", "isr_after", "replicas_after", "fence_after", "deleted", "duplicate_isr", "duplicate_replica", "zero_replica", "read_error", "cancel_before", "cancel_after", "runtime_error", "bad_page"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, q, want := mqttRoutedReplayFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.after = func(n int) {
				if n == 1 && mode == "cancel_before" {
					cancel()
				}
				if n != 2 {
					return
				}
				switch mode {
				case "route_after":
					m.meta.RouteGeneration++
				case "leader_after":
					m.meta.Leader = 3
				case "isr_after":
					m.meta.ISR = []ch.NodeID{2, 3}
				case "replicas_after":
					m.meta.Replicas = append(m.meta.Replicas, 4)
				case "fence_after":
					m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
				case "cancel_after":
					cancel()
				}
			}
			switch mode {
			case "route_before":
				m.meta.RouteGeneration++
			case "deleted":
				m.meta.Status = ch.StatusDeleted
			case "duplicate_isr":
				m.meta.ISR = []ch.NodeID{2, 2}
			case "duplicate_replica":
				m.meta.Replicas = append(m.meta.Replicas, 2)
			case "zero_replica":
				m.meta.Replicas = append(m.meta.Replicas, 0)
			case "read_error":
				m.fail = context.DeadlineExceeded
			case "runtime_error":
				r.prepare = func(context.Context, ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
					return ch.MQTTReplayPage{}, ch.ErrLogConflict
				}
			case "bad_page":
				r.prepare = func(context.Context, ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
					p := want
					p.After.Generation = "foreign"
					return p, nil
				}
			}
			got, err := s.PrepareMQTTReplay(ctx, q)
			if mode == "success" {
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.Equal(t, 2, m.calls)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			if mode == "route_before" || mode == "deleted" || mode == "read_error" || mode == "cancel_before" {
				require.Zero(t, r.prepareCalls)
			}
		})
	}
}

func TestMQTTReplayRouteForwardingAndGatewayReplacement(t *testing.T) {
	s, m, r, q, want := mqttRoutedReplayFixture(t)
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.PrepareMQTTReplay(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, 1, r.prepareCalls)
	clear(got.Records[0].Content)
	require.NotEqual(t, got, want, "forwarded bytes must be independently owned")
	gateway.Clear()
	_, err = origin.PrepareMQTTReplay(context.Background(), q)
	require.Error(t, err)
	gateway.Replace(s)
	_, err = origin.PrepareMQTTReplay(context.Background(), q)
	require.NoError(t, err)
	_, err = gateway.handleForwardMQTTReplay(context.Background(), mqttReplayForwardRequest{Leader: 3, Request: q})
	require.ErrorIs(t, err, ch.ErrNotLeader)
	noFresh, err := NewService(Config{LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{m.meta}), Runtime: r})
	require.NoError(t, err)
	_, err = noFresh.PrepareMQTTReplay(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestMQTTReplayRPCBoundsEchoAndOwnedContent(t *testing.T) {
	_, _, _, q, page := mqttRoutedReplayFixture(t)
	req := mqttReplayForwardRequest{Leader: 2, Request: q}
	body, err := encodeMQTTReplayRequest(req)
	require.NoError(t, err)
	decoded, err := decodeMQTTReplayRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, decoded)
	for cut := 0; cut < len(body); cut++ {
		_, err := decodeMQTTReplayRequest(body[:cut])
		require.Error(t, err)
	}
	for _, bad := range [][]byte{append(bytes.Clone(body), 0), make([]byte, mqttReplayRPCMaxRequestBytes+1)} {
		_, err := decodeMQTTReplayRequest(bad)
		require.Error(t, err)
	}
	bad := bytes.Clone(body)
	bad[4]++
	_, err = decodeMQTTReplayRequest(bad)
	require.Error(t, err)
	for _, change := range []func(*mqttReplayForwardRequest){
		func(r *mqttReplayForwardRequest) { r.Leader++ }, func(r *mqttReplayForwardRequest) { r.Request.ChannelID.ID += "x" },
		func(r *mqttReplayForwardRequest) { r.Request.ExpectedChannelEpoch++ }, func(r *mqttReplayForwardRequest) { r.Request.ExpectedLeaderEpoch++ },
		func(r *mqttReplayForwardRequest) { r.Request.ExpectedRouteGeneration++ }, func(r *mqttReplayForwardRequest) { r.Request.Range.From++ },
		func(r *mqttReplayForwardRequest) { r.Request.Range.Through++ }, func(r *mqttReplayForwardRequest) { r.Request.Range.Limit-- },
		func(r *mqttReplayForwardRequest) { r.Request.Range.MaxBytes-- }, func(r *mqttReplayForwardRequest) {
			r.Request.Range.Generation = quorumlog.MQTTSourceGeneration(ch.CommandID{2})
		},
	} {
		echo, err := encodeMQTTReplayReply(req, page, nil)
		require.NoError(t, err)
		other := req
		change(&other)
		_, err = decodeMQTTReplayReply(echo, other)
		require.Error(t, err)
	}
	for _, want := range []error{nil, ch.ErrNotLeader, ch.ErrNotReady, ch.ErrStaleMeta, ch.ErrWriteFenced, ch.ErrLogConflict, ch.ErrBackpressured, ch.ErrChannelNotFound, context.Canceled, context.DeadlineExceeded, errMQTTSourceRemote} {
		b, err := encodeMQTTReplayReply(req, page, want)
		require.NoError(t, err)
		got, err := decodeMQTTReplayReply(b, req)
		require.ErrorIs(t, err, want)
		if want == nil {
			require.Equal(t, page, got)
			clear(b)
			require.Equal(t, page, got)
		} else {
			require.Zero(t, got)
		}
	}
	b, err := encodeMQTTReplayReply(req, page, nil)
	require.NoError(t, err)
	// Probe all framing truncations plus representative large-body truncations.
	for cut := 0; cut < len(b); cut++ {
		_, err := decodeMQTTReplayReply(b[:cut], req)
		require.Error(t, err)
	}
	for _, bad := range [][]byte{append(bytes.Clone(b), 0), make([]byte, mqttReplayRPCMaxReplyBytes+1)} {
		_, err := decodeMQTTReplayReply(bad, req)
		require.Error(t, err)
	}
	statusOffset := len(mqttReplayReplyMagic) + 2 + len(body)
	bad = bytes.Clone(b)
	bad[statusOffset] = 255
	_, err = decodeMQTTReplayReply(bad, req)
	require.Error(t, err)
	bad = bytes.Clone(b)
	bad[statusOffset] = 1
	_, err = decodeMQTTReplayReply(bad, req)
	require.Error(t, err, "error replies cannot retain page data")
	bad = bytes.Clone(b)
	bad[4]++
	_, err = decodeMQTTReplayReply(bad, req)
	require.Error(t, err)
	// The final length word immediately precedes the only content envelope.
	bad = bytes.Clone(b)
	binary.BigEndian.PutUint32(bad[len(b)-len(page.Records[0].Content)-4:], ^uint32(0))
	_, err = decodeMQTTReplayReply(bad, req)
	require.Error(t, err)
	for _, mutate := range []func(*ch.MQTTReplayPage){
		func(p *ch.MQTTReplayPage) { p.Records = nil }, func(p *ch.MQTTReplayPage) { p.After.Through++ },
		func(p *ch.MQTTReplayPage) { p.Before.TotalBytes++ }, func(p *ch.MQTTReplayPage) { p.After.Digest[0]++ },
	} {
		malformed := page
		mutate(&malformed)
		_, err := encodeMQTTReplayReply(req, malformed, nil)
		require.Error(t, err)
	}
	_, err = encodeMQTTReplayReply(req, page, errors.New("arbitrary peer text"))
	require.NoError(t, err)
}
