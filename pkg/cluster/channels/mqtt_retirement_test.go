package channels

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type mqttRetirementRuntime struct {
	mqttSourceRuntime
	commit func(context.Context, ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error)
}

func (r *mqttRetirementRuntime) CommitMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	return r.commit(ctx, q)
}

func mqttRoutedRetirementFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttRetirementRuntime, ch.MQTTReplayRetirementRequest, ch.MQTTReplayRetirementProof) {
	t.Helper()
	_, m, _, anchor, p := mqttRoutedAnchorFixture(t)
	q := ch.MQTTReplayRetirementRequest{Meta: cloneMeta(m.meta), Captured: p, Candidate: p, ConsumerThrough: 2, MessageID: 91, ServerTimestampMS: 1001}
	r, err := q.Retirement()
	require.NoError(t, err)
	manifest := p.Manifest
	manifest.Version = 6
	manifest.BaseOffset = 3
	manifest.LastOffset = 4
	manifest.PreviousIndex = 3
	want := ch.MQTTReplayRetirementProof{Retirement: r, Manifest: manifest}
	runtime := &mqttRetirementRuntime{commit: func(ctx context.Context, actual ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
		require.Equal(t, m.meta, actual.Meta)
		require.Equal(t, q.Captured, actual.Captured)
		require.Equal(t, q.ConsumerThrough, actual.ConsumerThrough)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return want, nil
	}}
	s, err := NewService(Config{LocalNode: anchor.Meta.Leader, MetaSource: m, Runtime: runtime})
	require.NoError(t, err)
	return s, m, runtime, q, want
}

func TestMQTTRetirementRouteRequiresFreshFullAuthority(t *testing.T) {
	for _, mode := range []string{"success", "route_before", "route_after", "members_before", "members_after", "leader_after", "quorum_after", "status_after", "fence_before", "fence_after", "cancel_before", "cancel_after", "read_error", "wrong_proof", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, q, want := mqttRoutedRetirementFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			original := r.commit
			r.commit = func(c context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
				called = true
				p, e := original(c, q)
				if mode == "wrong_proof" {
					p.Retirement.AnchorDigest[0]++
				}
				return p, e
			}
			m.after = func(n int) {
				switch mode {
				case "route_before", "route_after":
					if n == 2 || mode == "route_before" {
						m.meta.RouteGeneration++
					}
				case "members_before", "members_after":
					if n == 2 || mode == "members_before" {
						m.meta.ISR = []ch.NodeID{2, 3}
					}
				case "leader_after":
					if n == 2 {
						m.meta.Leader = 3
					}
				case "quorum_after":
					if n == 2 {
						m.meta.MinISR = 3
					}
				case "status_after":
					if n == 2 {
						m.meta.Status = ch.StatusCreating
					}
				case "fence_before", "fence_after":
					if n == 2 || mode == "fence_before" {
						m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
					}
				case "cancel_before":
					if n == 1 {
						cancel()
					}
				case "cancel_after":
					if n == 2 {
						cancel()
					}
				}
			}
			if mode == "read_error" {
				m.fail = context.DeadlineExceeded
			}
			if mode == "unsupported" {
				s.runtime = &fakeRuntime{}
			}
			q.Meta.LeaseUntil = time.Unix(300, 0)
			q.Meta.RetentionThroughSeq = 99
			p, e := s.CommitMQTTReplayRetirement(ctx, q)
			if mode == "success" {
				require.NoError(t, e)
				require.Equal(t, want, p)
				require.Equal(t, 2, m.calls)
			} else {
				require.Error(t, e)
				require.Zero(t, p)
			}
			if mode == "route_before" || mode == "members_before" || mode == "fence_before" || mode == "cancel_before" || mode == "read_error" || mode == "unsupported" {
				require.False(t, called)
			}
		})
	}
}

func TestMQTTRetirementRouteForwardsOnceAcrossGatewayReplacement(t *testing.T) {
	s, m, _, q, want := mqttRoutedRetirementFixture(t)
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	p, err := origin.CommitMQTTReplayRetirement(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, want, p)
	gateway.Clear()
	_, err = origin.CommitMQTTReplayRetirement(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	gateway.Replace(s)
	_, err = origin.CommitMQTTReplayRetirement(context.Background(), q)
	require.NoError(t, err)
	other, err := NewService(Config{LocalNode: 3, MetaSource: m, Runtime: &fakeRuntime{}})
	require.NoError(t, err)
	gateway.Replace(other)
	_, err = origin.CommitMQTTReplayRetirement(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotLeader)
	noFresh, err := NewService(Config{LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{m.meta}), Runtime: s.runtime})
	require.NoError(t, err)
	_, err = noFresh.CommitMQTTReplayRetirement(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestMQTTRetirementRPCClosedBoundsEchoAndProof(t *testing.T) {
	_, _, _, q, proof := mqttRoutedRetirementFixture(t)
	req := mqttRetirementForward(q)
	body, err := encodeMQTTRetirementRequest(req)
	require.NoError(t, err)
	got, err := decodeMQTTRetirementRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
	for cut := 0; cut < len(body); cut++ {
		_, e := decodeMQTTRetirementRequest(body[:cut])
		require.Error(t, e)
	}
	badVersion := bytes.Clone(body)
	badVersion[4]++
	for _, bad := range [][]byte{badVersion, append(bytes.Clone(body), 0), make([]byte, mqttRetirementRPCMaxBytes+1)} {
		_, e := decodeMQTTRetirementRequest(bad)
		require.Error(t, e)
	}
	for _, operationErr := range append(mqttSourceStatuses[:], errors.New("untrusted text")) {
		b, e := encodeMQTTRetirementReply(req, proof, operationErr)
		require.NoError(t, e)
		p, e := decodeMQTTRetirementReply(b, req)
		if operationErr == nil {
			require.NoError(t, e)
			require.Equal(t, proof, p)
		} else {
			require.Error(t, e)
			require.Zero(t, p)
		}
	}
	reply, err := encodeMQTTRetirementReply(req, proof, nil)
	require.NoError(t, err)
	for cut := 0; cut < len(reply); cut++ {
		_, e := decodeMQTTRetirementReply(reply[:cut], req)
		require.Error(t, e)
	}
	for _, mutate := range []func(*mqttRetirementForwardRequest){func(q *mqttRetirementForwardRequest) { q.MessageID++ }, func(q *mqttRetirementForwardRequest) { q.Authority[0]++ }, func(q *mqttRetirementForwardRequest) { q.Selection.Through-- }, func(q *mqttRetirementForwardRequest) { q.Selection.Captured.Manifest.Digest[0]++ }} {
		other := req
		mutate(&other)
		_, e := decodeMQTTRetirementReply(reply, other)
		require.Error(t, e)
	}
	status := len(mqttRetirementCommitReplyMagic) + 2 + len(body)
	for _, code := range []byte{1, 255} {
		bad := bytes.Clone(reply)
		bad[status] = code
		_, e := decodeMQTTRetirementReply(bad, req)
		require.Error(t, e)
	}
	for _, mutate := range []func(*ch.MQTTReplayRetirementProof){func(p *ch.MQTTReplayRetirementProof) { p.Retirement.AnchorDigest[0]++ }, func(p *ch.MQTTReplayRetirementProof) { p.Manifest.LeaderTerm++ }, func(p *ch.MQTTReplayRetirementProof) { p.Manifest.Version = 5 }} {
		bad := proof
		mutate(&bad)
		_, e := encodeMQTTRetirementReply(req, bad, nil)
		require.Error(t, e)
	}
}
