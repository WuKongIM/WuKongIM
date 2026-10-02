package channels

import (
	"bytes"
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

func mqttOriginalsFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttPlanRuntime, *consumerReadStore, ch.MQTTReplayOriginalRequest, ch.MQTTReplayOriginalResult) {
	t.Helper()
	s, m, r, planRequest, plan := mqttRoutedPlanFixture(t)
	q := ch.MQTTReplayOriginalRequest{Request: ch.MQTTReplayRequest{ChannelID: planRequest.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Range: ch.MQTTReplayRange{Generation: planRequest.Generation, From: 1, Through: 1, Limit: 1, MaxBytes: 4096}}, AccountedThrough: 2}
	page := typedConsumerFixture(t, q.Request)
	st := &consumerReadStore{page: page}
	s.store = consumerReadFactory{s: st}
	return s, m, r, st, q, ch.MQTTReplayOriginalResult{Plan: plan, Page: page}
}

// The fallback exercises the existing real Service chain before the optional
// compound port exists, making the read-budget regression fail on five reads.
func readOriginalsForBudget(ctx context.Context, s *Service, q ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error) {
	if reader, ok := any(s).(ch.MQTTReplayOriginalReader); ok {
		return reader.ReadMQTTOriginals(ctx, q)
	}
	p, e := s.PlanMQTTReplay(ctx, q.PlanRequest())
	if e != nil {
		return ch.MQTTReplayOriginalResult{}, e
	}
	page, e := s.ReadMQTTReplay(ctx, ch.MQTTReplayConsumerRequest{Request: q.Request, AnchorPosition: p.Anchor.Manifest.LastOffset})
	return ch.MQTTReplayOriginalResult{Plan: p, Page: page}, e
}

func TestMQTTOriginalsShareFreshAuthorityAcrossPlanAndContent(t *testing.T) {
	s, m, _, st, q, want := mqttOriginalsFixture(t)
	refreshes := 0
	s.replicaCommitRefresh = mqttRetirementRefresh(func(ctx context.Context, a replication.Authority) error {
		refreshes++
		require.Zero(t, st.calls)
		require.NoError(t, ctx.Err())
		return nil
	})
	got, e := readOriginalsForBudget(context.Background(), s, q)
	require.NoError(t, e)
	require.Equal(t, want, got)
	require.Equal(t, 2, m.calls, "one fresh read before planning and one after refresh/content")
	require.Equal(t, 1, refreshes)
	require.Equal(t, 1, st.calls)
	require.Empty(t, s.mqttConsumerReads)
}

func TestMQTTOriginalsFailClosedAcrossEveryStage(t *testing.T) {
	for _, mode := range []string{"stable_fence", "route_before", "route_after", "leader_after", "members_after", "status_after", "fence_after", "renewed_fence", "cleared_fence", "metadata_error", "cancel_before", "cancel_plan", "cancel_refresh", "refresh_error", "missing_refresh", "bad_plan", "no_anchor", "short_anchor", "bad_page", "wrong_start", "wrong_prefix", "store_error", "backpressure"} {
		t.Run(mode, func(t *testing.T) {
			s, m, r, st, q, _ := mqttOriginalsFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "stable_fence" || mode == "renewed_fence" || mode == "cleared_fence" {
				m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1, Until: time.UnixMilli(1000)}
			}
			m.after = func(n int) {
				if n == 1 && mode == "route_before" {
					m.meta.RouteGeneration++
				}
				if n != 2 {
					return
				}
				switch mode {
				case "route_after":
					m.meta.RouteGeneration++
				case "leader_after":
					m.meta.Leader = 3
				case "members_after":
					m.meta.ISR = []ch.NodeID{2, 3}
				case "status_after":
					m.meta.Status = ch.StatusCreating
				case "fence_after":
					m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
				case "renewed_fence":
					m.meta.WriteFence.Until = m.meta.WriteFence.Until.Add(time.Second)
				case "cleared_fence":
					m.meta.WriteFence = ch.WriteFence{}
				}
			}
			original := r.plan
			r.plan = func(c context.Context, pr ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
				p, e := original(c, pr)
				switch mode {
				case "cancel_plan":
					cancel()
				case "bad_plan":
					p.Source.Generation = "foreign"
				case "no_anchor":
					p.HasAnchor = false
					p.Anchor = ch.MQTTReplayAnchorProof{}
				}
				return p, e
			}
			switch mode {
			case "cancel_before":
				cancel()
			case "metadata_error":
				m.fail = context.DeadlineExceeded
			case "cancel_refresh", "refresh_error":
				s.replicaCommitRefresh = mqttRetirementRefresh(func(context.Context, replication.Authority) error {
					if mode == "cancel_refresh" {
						cancel()
						return nil
					}
					return ch.ErrBackpressured
				})
			case "missing_refresh":
				s.replicaCommitRefresh = nil
			case "short_anchor":
				q.AccountedThrough = 3
			case "store_error":
				st.failure = ch.ErrNotReady
			case "bad_page":
				st.page.Records[0].Message.Version = 1
			case "wrong_start":
				q.StartAfter = 1
				q.Request.Range.From = 2
				q.Request.Range.Through = 2
				st.page.Before.StartAfter = 1
			case "wrong_prefix":
				q.Request.Range.Through = 2
				st.page.After.Through = 2
			case "backpressure":
				for range cap(s.mqttConsumerReads) {
					s.mqttConsumerReads <- struct{}{}
				}
			}
			reader, ok := any(s).(ch.MQTTReplayOriginalReader)
			require.True(t, ok)
			got, e := reader.ReadMQTTOriginals(ctx, q)
			if mode == "stable_fence" {
				require.NoError(t, e)
				require.True(t, got.ValidFor(q))
			} else {
				require.Error(t, e)
				require.Zero(t, got)
				if mode == "store_error" {
					require.ErrorIs(t, e, ch.ErrNotReady)
				}
			}
			if mode == "cancel_before" || mode == "cancel_plan" || mode == "cancel_refresh" || mode == "metadata_error" || mode == "route_before" || mode == "refresh_error" || mode == "missing_refresh" || mode == "bad_plan" || mode == "no_anchor" || mode == "short_anchor" || mode == "backpressure" {
				require.Zero(t, st.calls)
			}
		})
	}
}

func TestMQTTOriginalsForwardOnceAndPreserveGatewayFences(t *testing.T) {
	s, m, _, _, q, want := mqttOriginalsFixture(t)
	network := clusternet.NewLocalNetwork()
	g := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, g)
	origin, e := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, e)
	origin.replicaCommitRefresh = mqttRetirementRefresh(func(context.Context, replication.Authority) error {
		t.Fatal("origin must not refresh replicas")
		return nil
	})
	reader, ok := any(origin).(ch.MQTTReplayOriginalReader)
	require.True(t, ok)
	got, e := reader.ReadMQTTOriginals(context.Background(), q)
	require.NoError(t, e)
	require.Equal(t, want, got)
	require.Equal(t, 4, m.calls)
	g.Clear()
	_, e = reader.ReadMQTTOriginals(context.Background(), q)
	require.ErrorIs(t, e, ch.ErrNotReady)
	g.Replace(s)
	_, e = reader.ReadMQTTOriginals(context.Background(), q)
	require.NoError(t, e)
	_, e = g.handleForwardMQTTOriginals(context.Background(), mqttOriginalsForwardRequest{Leader: 3, Request: q})
	require.ErrorIs(t, e, ch.ErrNotLeader)
}

func TestMQTTOriginalsRPCBindsWholeRequestAndOwnsContent(t *testing.T) {
	_, _, _, _, request, want := mqttOriginalsFixture(t)
	q := mqttOriginalsForwardRequest{Leader: 2, Request: request}
	b, e := encodeMQTTOriginalsRequest(q)
	require.NoError(t, e)
	got, e := decodeMQTTOriginalsRequest(b)
	require.NoError(t, e)
	require.Equal(t, q, got)
	for cut := range len(b) {
		_, e = decodeMQTTOriginalsRequest(b[:cut])
		require.Error(t, e)
	}
	_, e = decodeMQTTOriginalsRequest(append(bytes.Clone(b), 0))
	require.Error(t, e)
	for _, failure := range []error{nil, ch.ErrNotReady, ch.ErrStaleMeta, ch.ErrBackpressured, context.Canceled, context.DeadlineExceeded} {
		reply, e := encodeMQTTOriginalsReply(q, want, failure)
		require.NoError(t, e)
		decoded, e := decodeMQTTOriginalsReply(reply, q)
		require.ErrorIs(t, e, failure)
		if failure == nil {
			require.Equal(t, want, decoded)
		} else {
			require.Zero(t, decoded)
		}
		for _, mutate := range []func(*mqttOriginalsForwardRequest){func(q *mqttOriginalsForwardRequest) { q.Leader++ }, func(q *mqttOriginalsForwardRequest) { q.Request.AccountedThrough++ }, func(q *mqttOriginalsForwardRequest) { q.Request.StartAfter++ }, func(q *mqttOriginalsForwardRequest) { q.Request.Request.Range.MaxBytes++ }, func(q *mqttOriginalsForwardRequest) { q.Request.Request.ExpectedRouteGeneration++ }} {
			other := q
			mutate(&other)
			_, e = decodeMQTTOriginalsReply(reply, other)
			require.Error(t, e)
		}
		for _, cut := range []int{0, 1, 8, len(reply) - 1} {
			_, e = decodeMQTTOriginalsReply(reply[:cut], q)
			require.Error(t, e)
		}
		_, e = decodeMQTTOriginalsReply(append(bytes.Clone(reply), 0), q)
		require.Error(t, e)
		clear(reply)
		if failure == nil {
			require.Equal(t, want, decoded, "decoder must own payload and publication metadata")
		}
	}
	bad := want
	bad.Plan.Source.StartAfter = 1
	_, e = encodeMQTTOriginalsReply(q, bad, nil)
	require.Error(t, e)
}
