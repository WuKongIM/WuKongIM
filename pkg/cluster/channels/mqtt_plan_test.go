package channels

import (
	"bytes"
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type mqttPlanRuntime struct {
	mqttSourceRuntime
	plan func(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error)
}

func (r *mqttPlanRuntime) PlanMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	return r.plan(ctx, q)
}
func mqttRoutedPlanFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttPlanRuntime, ch.MQTTReplayPlanRequest, ch.MQTTReplayPlan) {
	t.Helper()
	_, m, _, anchor, proof := mqttRoutedAnchorFixture(t)
	q := ch.MQTTReplayPlanRequest{ChannelID: anchor.Meta.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Generation: anchor.Copy.After.Generation}
	p := ch.MQTTReplayPlan{Source: ch.MQTTSourceSnapshot{Generation: q.Generation, CommittedThrough: 4}, HasAnchor: true, Anchor: proof}
	r := &mqttPlanRuntime{plan: func(ctx context.Context, got ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
		require.Equal(t, q, got)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return p, nil
	}}
	s, err := NewService(Config{LocalNode: 2, MetaSource: m, Runtime: r})
	require.NoError(t, err)
	return s, m, r, q, p
}

func TestMQTTPlanRoutingRejectsAuthorityChangesAndInvalidReplies(t *testing.T) {
	for _, mode := range []string{"success", "stable_fence", "renewed_fence", "cleared_fence", "route_before", "route_after", "members_after", "status_after", "fence_after", "cancel_before", "cancel_after", "read_error", "bad_plan", "runtime_error"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, q, want := mqttRoutedPlanFixture(t)
			if mode == "stable_fence" || mode == "renewed_fence" || mode == "cleared_fence" {
				m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1, Until: time.UnixMilli(1000)}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			original := r.plan
			r.plan = func(c context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
				called = true
				p, e := original(c, q)
				if mode == "bad_plan" {
					p.Source.Generation = "foreign"
				}
				if mode == "runtime_error" {
					e = ch.ErrNotReady
				}
				return p, e
			}
			m.after = func(n int) {
				if n == 1 && mode == "route_before" {
					m.meta.RouteGeneration++
				}
				if n == 1 && mode == "cancel_before" {
					cancel()
				}
				if n != 2 {
					return
				}
				switch mode {
				case "renewed_fence":
					m.meta.WriteFence.Until = m.meta.WriteFence.Until.Add(time.Second)
				case "cleared_fence":
					m.meta.WriteFence = ch.WriteFence{}
				case "route_after":
					m.meta.RouteGeneration++
				case "members_after":
					m.meta.ISR = []ch.NodeID{2, 3}
				case "status_after":
					m.meta.Status = ch.StatusCreating
				case "fence_after":
					m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
				case "cancel_after":
					cancel()
				}
			}
			if mode == "read_error" {
				m.fail = context.DeadlineExceeded
			}
			got, err := s.PlanMQTTReplay(ctx, q)
			if mode == "success" || mode == "stable_fence" {
				require.NoError(t, err)
				require.Equal(t, want, got)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			if mode == "route_before" || mode == "cancel_before" || mode == "read_error" {
				require.False(t, called)
			}
		})
	}
}

func TestMQTTPlanRoutingForwardsThroughStableGateway(t *testing.T) {
	s, m, _, q, want := mqttRoutedPlanFixture(t)
	m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
	network := clusternet.NewLocalNetwork()
	g := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, g)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.PlanMQTTReplay(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, want, got)
	g.Clear()
	_, err = origin.PlanMQTTReplay(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	g.Replace(s)
	_, err = origin.PlanMQTTReplay(context.Background(), q)
	require.NoError(t, err)
	_, err = g.handleForwardMQTTPlan(context.Background(), mqttPlanForwardRequest{Leader: 3, Request: q})
	require.ErrorIs(t, err, ch.ErrNotLeader)
	notFresh, err := NewService(Config{LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{m.meta}), Runtime: s.runtime})
	require.NoError(t, err)
	_, err = notFresh.PlanMQTTReplay(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestMQTTPlanRPCBoundsEchoAndOptionalProof(t *testing.T) {
	_, _, _, q, p := mqttRoutedPlanFixture(t)
	req := mqttPlanForwardRequest{Leader: 2, Request: q}
	body, err := encodeMQTTPlanRequest(req)
	require.NoError(t, err)
	got, err := decodeMQTTPlanRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)
	for cut := 0; cut < len(body); cut++ {
		_, err := decodeMQTTPlanRequest(body[:cut])
		require.Error(t, err)
	}
	badVersion := bytes.Clone(body)
	badVersion[4]++
	for _, bad := range [][]byte{badVersion, append(bytes.Clone(body), 0), make([]byte, mqttPlanRPCMaxBytes+1)} {
		_, err := decodeMQTTPlanRequest(bad)
		require.Error(t, err)
	}
	for _, hasAnchor := range []bool{true, false} {
		plan := p
		if !hasAnchor {
			plan.HasAnchor = false
			plan.Anchor = ch.MQTTReplayAnchorProof{}
		}
		b, err := encodeMQTTPlanReply(req, plan, nil)
		require.NoError(t, err)
		actual, err := decodeMQTTPlanReply(b, req)
		require.NoError(t, err)
		require.Equal(t, plan, actual)
		for cut := 0; cut < len(b); cut++ {
			_, err := decodeMQTTPlanReply(b[:cut], req)
			require.Error(t, err)
		}
		for _, mutate := range []func(*mqttPlanForwardRequest){func(q *mqttPlanForwardRequest) { q.Leader++ }, func(q *mqttPlanForwardRequest) { q.Request.ChannelID.ID += "x" }, func(q *mqttPlanForwardRequest) { q.Request.ExpectedRouteGeneration++ }} {
			other := req
			mutate(&other)
			_, err := decodeMQTTPlanReply(b, other)
			require.Error(t, err)
		}
		badStatus := bytes.Clone(b)
		badStatus[len(mqttPlanReplyMagic)+2+len(body)] = 255
		errorWithPlan := bytes.Clone(b)
		errorWithPlan[len(mqttPlanReplyMagic)+2+len(body)] = 1
		badVersion := bytes.Clone(b)
		badVersion[4]++
		for _, bad := range [][]byte{badStatus, errorWithPlan, badVersion, append(bytes.Clone(b), 0), make([]byte, mqttPlanRPCMaxBytes+1)} {
			_, err := decodeMQTTPlanReply(bad, req)
			require.Error(t, err)
		}
	}
	for _, want := range mqttSourceStatuses[1:] {
		b, err := encodeMQTTPlanReply(req, p, want)
		require.NoError(t, err)
		actual, err := decodeMQTTPlanReply(b, req)
		require.ErrorIs(t, err, want)
		require.Zero(t, actual)
	}
	p.Source.CommittedThrough = 2
	_, err = encodeMQTTPlanReply(req, p, nil)
	require.Error(t, err)
}
