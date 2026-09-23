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

type mqttRecoveryStore struct {
	*mqttRepairStore
	plan    ch.MQTTReplayRepairPlan
	planErr error
	plans   int
}

func (s *mqttRecoveryStore) PlanMQTTReplayRepair(_ context.Context, q ch.MQTTReplayRepairScan) (ch.MQTTReplayRepairPlan, error) {
	s.plans++
	return s.plan, s.planErr
}

type mqttRecoveryForward struct {
	*mqttRepairForward
}

func (f *mqttRecoveryForward) ForwardMQTTReplayRecoveryStep(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	panic("local recovery recursively forwarded")
}

func mqttRecoveryFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttRecoveryStore, *mqttCopyFactory, *mqttRecoveryForward, ch.MQTTReplayRecoveryRequest) {
	t.Helper()
	s, m, st, f, forward, repair := mqttRepairFixture(t)
	q := ch.MQTTReplayRecoveryRequest{Target: repair.Target, Source: ch.MQTTReplayPlanRequest{ChannelID: repair.Request.ChannelID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Generation: repair.Request.Range.Generation}, TargetAnchor: 2, ScanLimit: 1}
	store := &mqttRecoveryStore{mqttRepairStore: st, plan: ch.MQTTReplayRepairPlan{Current: st.page.Before, Target: st.proof, Next: st.proof, HasNext: true}}
	f.handle = store
	fw := &mqttRecoveryForward{forward}
	fw.fetch = func(c context.Context, got ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
		require.Equal(t, q.Target, got.Target)
		require.Equal(t, st.proof.Manifest.LastOffset, got.AnchorPosition)
		require.Equal(t, st.page.After.Generation, got.Request.Range.Generation)
		require.Equal(t, uint64(1), got.Request.Range.From)
		require.Equal(t, uint64(1), got.Request.Range.Through)
		deadline, ok := c.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), time.Second)
		return st.page, nil
	}
	s.forward = fw
	return s, m, store, f, fw, q
}

func TestMQTTRecoveryStepPlansImportsAndRotatesDonors(t *testing.T) {
	s, m, st, f, forward, q := mqttRecoveryFixture(t)
	m.meta.Replicas = []ch.NodeID{1, 2, 3, 4, 5, 6, 7}
	m.meta.ISR = []ch.NodeID{1, 2, 3, 4, 5, 6, 7}
	m.meta.MinISR = 4
	var attempted []ch.NodeID
	forward.fetch = func(_ context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
		attempted = append(attempted, q.Donor)
		return ch.MQTTReplayPage{}, ch.ErrNotReady
	}
	first, err := s.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	require.True(t, first.ValidFor(q))
	require.False(t, first.Repaired)
	require.Equal(t, ch.NodeID(5), first.DonorAfter)
	require.Equal(t, []ch.NodeID{1, 3, 4, 5}, attempted)
	require.Zero(t, st.imports)
	q.DonorAfter = first.DonorAfter
	forward.fetch = func(_ context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
		attempted = append(attempted, q.Donor)
		return st.page, nil
	}
	next, err := s.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	require.True(t, next.ValidFor(q))
	require.True(t, next.Repaired)
	require.Zero(t, next.DonorAfter)
	require.Equal(t, ch.NodeID(6), attempted[len(attempted)-1])
	require.Equal(t, 1, st.imports)
	// The next fresh plan, not import's return value, establishes target coverage.
	st.plan.Current = st.page.After
	st.plan.Next = ch.MQTTReplayAnchorProof{}
	st.plan.HasNext = false
	st.plan.Complete = true
	done, err := s.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	require.True(t, done.ValidFor(q))
	require.True(t, done.Plan.Complete)
	require.False(t, done.Repaired)
	require.Equal(t, 1, st.imports)
	require.Len(t, attempted, 5)
	require.Equal(t, f.opens, st.closed)
}

func TestMQTTRecoveryStepSkipsBadDonorAndSurvivesMissingHint(t *testing.T) {
	for _, hint := range []ch.NodeID{0, 99, 2} {
		t.Run(string(rune('a'+hint)), func(t *testing.T) {
			s, _, st, _, forward, q := mqttRecoveryFixture(t)
			q.DonorAfter = hint
			var attempted []ch.NodeID
			forward.fetch = func(_ context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
				attempted = append(attempted, q.Donor)
				if q.Donor == 1 {
					return ch.MQTTReplayPage{}, nil
				}
				return st.page, nil
			}
			got, err := s.StepMQTTReplayRecovery(context.Background(), q)
			require.NoError(t, err)
			require.True(t, got.Repaired)
			require.Equal(t, []ch.NodeID{1, 3}, attempted)
			require.Equal(t, 1, st.imports)
		})
	}
}

func TestMQTTRecoveryStepPreservesScanContinuation(t *testing.T) {
	s, _, st, _, forward, q := mqttRecoveryFixture(t)
	q.TargetAnchor = 4
	target := st.proof
	target.Manifest.BaseOffset = 3
	target.Manifest.LastOffset = 4
	target.Manifest.PreviousIndex = 3
	target.Anchor.Through = 3
	target.Anchor.TotalBytes *= 3
	target.Anchor.TotalStoredBytes *= 3
	target.Anchor.Digest = [32]byte{3}
	st.plan = ch.MQTTReplayRepairPlan{Current: st.page.After, Target: target, ScanAfter: 2}
	got, err := s.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	require.True(t, got.ValidFor(q))
	require.False(t, got.Plan.HasNext)
	require.False(t, got.Plan.Complete)
	require.Equal(t, uint64(2), got.Plan.ScanAfter)
	require.Zero(t, forward.calls)
	require.Zero(t, st.imports)
}

func TestMQTTRecoveryStepRejectsUnsafeWorkAndCleansUp(t *testing.T) {
	for _, mode := range []string{"plan_error", "bad_plan", "future_target", "future_next", "stale_before", "stale_preimport", "stale_after", "cancel_before", "cancel_after", "import_error", "panic", "saturated", "no_donor", "migration_fence", "learner"} {
		t.Run(mode, func(t *testing.T) {
			s, m, st, f, forward, q := mqttRecoveryFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "plan_error":
				st.planErr = ch.ErrNotReady
			case "bad_plan":
				st.plan.Current.Generation = "foreign"
			case "future_target":
				st.plan.Target.Manifest.LeaderTerm++
			case "future_next":
				st.plan.Next.Manifest.LeaderTerm++
			case "stale_before":
				m.meta.RouteGeneration++
			case "stale_preimport", "stale_after":
				m.after = func(n int) {
					at := 3
					if mode == "stale_after" {
						at = 4
					}
					if n == at {
						m.meta.RouteGeneration++
					}
				}
			case "cancel_before":
				cancel()
			case "cancel_after":
				st.importFn = func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) {
					cancel()
					return st.page.After, nil
				}
			case "import_error":
				st.importFn = func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) {
					return ch.MQTTReplayPrefix{}, ch.ErrLogConflict
				}
			case "panic":
				st.importFn = func(context.Context, uint64, ch.MQTTReplayPage) (ch.MQTTReplayPrefix, error) { panic("recovery") }
			case "saturated":
				for range cap(s.mqttRepairReceivers) {
					s.mqttRepairReceivers <- struct{}{}
				}
			case "no_donor":
				m.meta.Replicas = []ch.NodeID{2}
				m.meta.ISR = []ch.NodeID{2}
				m.meta.MinISR = 1
			case "migration_fence":
				m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
			case "learner":
				m.meta.ISR = []ch.NodeID{1, 3}
				m.meta.Leader = 3
			}
			if mode == "panic" {
				require.Panics(t, func() { _, _ = s.StepMQTTReplayRecovery(ctx, q) })
				require.Equal(t, f.opens, st.closed)
				require.Empty(t, s.mqttRepairReceivers)
				return
			}
			got, err := s.StepMQTTReplayRecovery(ctx, q)
			if mode == "migration_fence" || mode == "learner" {
				require.NoError(t, err)
				require.True(t, got.Repaired)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
			}
			require.Equal(t, f.opens, st.closed)
			if mode == "plan_error" || mode == "bad_plan" || mode == "future_target" || mode == "future_next" || mode == "no_donor" {
				require.Zero(t, forward.calls)
			}
			if mode == "stale_preimport" {
				require.Zero(t, st.imports)
			}
			if mode == "stale_after" || mode == "cancel_after" {
				require.Equal(t, 1, st.imports)
			}
		})
	}
}

func TestMQTTRecoveryStepRPCRoutesViaStableGateways(t *testing.T) {
	target, m, st, _, _, q := mqttRecoveryFixture(t)
	donor, _, _, _, _, _ := mqttRepairFixture(t)
	donor.localNode = 1
	network := clusternet.NewLocalNetwork()
	gate := NewServiceGateway(target)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gate)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 1}, NewServiceGateway(donor))
	target.forward = NewTransportClient(network)
	origin, err := NewService(Config{LocalNode: 3, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	got, err := origin.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	require.True(t, got.Repaired)
	require.Equal(t, st.plan, got.Plan)
	gate.Clear()
	_, err = origin.StepMQTTReplayRecovery(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	gate.Replace(target)
	_, err = origin.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	q.Target = 3
	_, err = gate.handleMQTTReplayRecoveryStep(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReplica)
}

func TestMQTTRecoveryStepRPCClosedFramingAndOutcomes(t *testing.T) {
	_, _, st, _, _, q := mqttRecoveryFixture(t)
	body, err := encodeMQTTRecoveryRequest(q)
	require.NoError(t, err)
	got, err := decodeMQTTRecoveryRequest(body)
	require.NoError(t, err)
	require.Equal(t, q, got)
	for cut := 0; cut < len(body); cut++ {
		_, err = decodeMQTTRecoveryRequest(body[:cut])
		require.Error(t, err)
	}
	badVersion := bytes.Clone(body)
	badVersion[4] = 255
	for _, b := range [][]byte{badVersion, append(bytes.Clone(body), 0), make([]byte, mqttRecoveryRPCMaxBytes+1)} {
		_, err = decodeMQTTRecoveryRequest(b)
		require.Error(t, err)
	}
	for _, copied := range []bool{false, true} {
		result := ch.MQTTReplayRecoveryResult{Plan: st.plan, Repaired: copied}
		if !copied {
			result.DonorAfter = 1
		}
		reply, err := encodeMQTTRecoveryReply(q, result, nil)
		require.NoError(t, err)
		actual, err := decodeMQTTRecoveryReply(reply, q)
		require.NoError(t, err)
		require.Equal(t, result, actual)
		for cut := 0; cut < len(reply); cut++ {
			_, err = decodeMQTTRecoveryReply(reply[:cut], q)
			require.Error(t, err)
		}
		for _, mutate := range []func(*ch.MQTTReplayRecoveryRequest){func(r *ch.MQTTReplayRecoveryRequest) { r.Target++ }, func(r *ch.MQTTReplayRecoveryRequest) { r.TargetAnchor++ }, func(r *ch.MQTTReplayRecoveryRequest) { r.DonorAfter++ }, func(r *ch.MQTTReplayRecoveryRequest) { r.AfterAnchor++ }, func(r *ch.MQTTReplayRecoveryRequest) { r.Source.ExpectedRouteGeneration++ }} {
			other := q
			mutate(&other)
			_, err = decodeMQTTRecoveryReply(reply, other)
			require.Error(t, err)
		}
		badStatus := bytes.Clone(reply)
		badStatus[len(mqttRecoveryReplyMagic)+2+len(body)] = 255
		for _, b := range [][]byte{badStatus, append(bytes.Clone(reply), 0), make([]byte, mqttRecoveryRPCMaxBytes+1)} {
			_, err = decodeMQTTRecoveryReply(b, q)
			require.Error(t, err)
		}
		for _, want := range mqttSourceStatuses[1:] {
			b, err := encodeMQTTRecoveryReply(q, result, want)
			require.NoError(t, err)
			empty, err := decodeMQTTRecoveryReply(b, q)
			require.ErrorIs(t, err, want)
			require.Zero(t, empty)
		}
	}
	bad := ch.MQTTReplayRecoveryResult{Plan: st.plan}
	require.False(t, bad.ValidFor(q), "an unimported interval needs a donor continuation")
	_, err = encodeMQTTRecoveryReply(q, bad, nil)
	require.Error(t, err)
}

func TestMQTTRecoveryStepContractRejectsAmbiguity(t *testing.T) {
	_, _, st, _, _, q := mqttRecoveryFixture(t)
	for _, mutate := range []func(*ch.MQTTReplayRecoveryRequest){
		func(r *ch.MQTTReplayRecoveryRequest) { r.Target = 0 },
		func(r *ch.MQTTReplayRecoveryRequest) { r.TargetAnchor = 0 },
		func(r *ch.MQTTReplayRecoveryRequest) { r.AfterAnchor = r.TargetAnchor + 1 },
		func(r *ch.MQTTReplayRecoveryRequest) { r.ScanLimit = 0 },
		func(r *ch.MQTTReplayRecoveryRequest) { r.ScanLimit = 65 },
		func(r *ch.MQTTReplayRecoveryRequest) { r.Source.Generation = "foreign" },
	} {
		bad := q
		mutate(&bad)
		require.False(t, bad.Valid())
		_, err := encodeMQTTRecoveryRequest(bad)
		require.Error(t, err)
	}
	result := ch.MQTTReplayRecoveryResult{Plan: st.plan, Repaired: true}
	for _, mutate := range []func(*ch.MQTTReplayRecoveryResult){
		func(p *ch.MQTTReplayRecoveryResult) { p.DonorAfter = 1 },
		func(p *ch.MQTTReplayRecoveryResult) { p.Repaired = false; p.DonorAfter = q.Target },
		func(p *ch.MQTTReplayRecoveryResult) { p.Plan.Complete = true },
		func(p *ch.MQTTReplayRecoveryResult) { p.Plan.Target.Manifest.ChannelEpoch++ },
		func(p *ch.MQTTReplayRecoveryResult) { p.Plan.Next.Manifest.FenceVersion++ },
	} {
		bad := result
		mutate(&bad)
		require.False(t, bad.ValidFor(q))
		_, err := encodeMQTTRecoveryReply(q, bad, nil)
		require.Error(t, err)
	}
	result.Plan.Current = st.page.After
	result.Plan.Next = ch.MQTTReplayAnchorProof{}
	result.Plan.HasNext = false
	result.Plan.Complete = true
	result.Repaired = false
	reply, err := encodeMQTTRecoveryReply(q, result, nil)
	require.NoError(t, err)
	decoded, err := decodeMQTTRecoveryReply(reply, q)
	require.NoError(t, err)
	require.Equal(t, result, decoded)
	// A completed target needs neither a forwarding client nor a donor.
	s, m, st, _, _, q := mqttRecoveryFixture(t)
	st.plan = result.Plan
	s.forward = nil
	m.meta.Replicas = []ch.NodeID{2}
	m.meta.ISR = []ch.NodeID{2}
	m.meta.MinISR = 1
	done, err := s.StepMQTTReplayRecovery(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, result, done)
}
