package channels

import (
	"bytes"
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

type mqttReleaseStore struct {
	*mqttRecoveryStore
	releases int
	release  func(context.Context, string, uint64) error
}

func (s *mqttReleaseStore) ReleaseMQTTSourceAtAnchor(ctx context.Context, generation string, position uint64) error {
	s.releases++
	return s.release(ctx, generation, position)
}

func mqttReleaseFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttReleaseStore, *mqttCopyFactory, ch.MQTTReplayRecoveryRequest) {
	t.Helper()
	s, m, st, factory, _, q := mqttRecoveryFixture(t)
	st.plan = ch.MQTTReplayRepairPlan{Current: st.page.After, Target: st.proof, Complete: true}
	release := &mqttReleaseStore{mqttRecoveryStore: st}
	release.release = func(ctx context.Context, generation string, position uint64) error {
		require.NoError(t, ctx.Err())
		require.Equal(t, q.Source.Generation, generation)
		require.Equal(t, q.TargetAnchor, position)
		return nil
	}
	factory.handle = release
	q.ReleaseSource = true
	return s, m, release, factory, q
}

func TestMQTTRecoveryReleaseRequiresFreshCompleteExplicitIntent(t *testing.T) {
	for _, mode := range []string{"complete", "ordinary", "partial", "missing_port", "before", "pre_release", "post_release", "removed", "fence_changed", "stable_fence", "learner", "store_error", "cancel", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s, m, st, factory, q := mqttReleaseFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantCalls, wantError := 1, false
			switch mode {
			case "ordinary":
				q.ReleaseSource, wantCalls = false, 0
			case "partial":
				st.plan = ch.MQTTReplayRepairPlan{Current: st.page.Before, Target: st.proof, Next: st.proof, HasNext: true}
				wantCalls = 0
			case "missing_port":
				factory.handle = st.mqttRecoveryStore
				wantCalls, wantError = 0, true
			case "before":
				m.meta.RouteGeneration++
				wantCalls, wantError = 0, true
			case "pre_release", "post_release", "removed", "fence_changed":
				at := 2
				wantCalls, wantError = 0, true
				if mode == "post_release" {
					at, wantCalls = 3, 1
				}
				m.after = func(n int) {
					if n != at {
						return
					}
					switch mode {
					case "removed":
						m.meta.Replicas, m.meta.ISR, m.meta.MinISR = []ch.NodeID{1, 3}, []ch.NodeID{1, 3}, 2
					case "fence_changed":
						m.meta.WriteFence = ch.WriteFence{Token: "new", Version: 1}
					default:
						m.meta.RouteGeneration++
					}
				}
			case "stable_fence":
				m.meta.WriteFence = ch.WriteFence{Token: "migration", Version: 1}
			case "learner":
				m.meta.ISR = []ch.NodeID{1, 3}
				m.meta.Leader = 3
			case "store_error":
				st.release = func(context.Context, string, uint64) error { return ch.ErrLogConflict }
				wantError = true
			case "cancel":
				st.release = func(context.Context, string, uint64) error { cancel(); return nil }
				wantError = true
			case "panic":
				st.release = func(context.Context, string, uint64) error { panic("release") }
			}
			if mode == "panic" {
				require.Panics(t, func() { _, _ = s.StepMQTTReplayRecovery(ctx, q) })
			} else {
				result, err := s.StepMQTTReplayRecovery(ctx, q)
				if wantError {
					require.Error(t, err)
					require.Zero(t, result)
				} else {
					require.NoError(t, err)
					require.True(t, result.ValidFor(q))
					require.Equal(t, q.ReleaseSource && result.Plan.Complete, result.SourceReleased)
				}
			}
			require.Equal(t, wantCalls, st.releases)
			require.Equal(t, factory.opens, st.closed)
			require.Empty(t, s.mqttRepairReceivers)
		})
	}
}

func TestMQTTRecoveryReleaseRPCRequiresVersionedAcknowledgement(t *testing.T) {
	_, _, st, _, q := mqttReleaseFixture(t)
	request, err := encodeMQTTRecoveryRequest(q)
	require.NoError(t, err)
	require.Equal(t, byte(2), request[4])
	decoded, err := decodeMQTTRecoveryRequest(request)
	require.NoError(t, err)
	require.Equal(t, q, decoded)
	result := ch.MQTTReplayRecoveryResult{Plan: st.plan, SourceReleased: true}
	encoded, err := encodeMQTTRecoveryReply(q, result, nil)
	require.NoError(t, err)
	require.Equal(t, byte(2), encoded[4])
	actual, err := decodeMQTTRecoveryReply(encoded, q)
	require.NoError(t, err)
	require.Equal(t, result, actual)
	for cut := 0; cut < len(encoded); cut++ {
		_, err = decodeMQTTRecoveryReply(encoded[:cut], q)
		require.Error(t, err)
	}
	for cut := 0; cut < len(request); cut++ {
		_, err = decodeMQTTRecoveryRequest(request[:cut])
		require.Error(t, err)
	}
	result.SourceReleased = false
	require.False(t, result.ValidFor(q))
	_, err = encodeMQTTRecoveryReply(q, result, nil)
	require.Error(t, err)
	ordinary := q
	ordinary.ReleaseSource = false
	legacy, err := encodeMQTTRecoveryReply(ordinary, result, nil)
	require.NoError(t, err)
	require.Equal(t, byte(1), legacy[4])
	_, err = decodeMQTTRecoveryReply(legacy, q)
	require.Error(t, err)
	_, err = decodeMQTTRecoveryReply(encoded, ordinary)
	require.Error(t, err)
	result.SourceReleased = true
	require.False(t, result.ValidFor(ordinary))
	result.Plan.Complete, result.Plan.HasNext, result.Plan.Next, result.Plan.Current = false, true, st.proof, st.page.Before
	result.Repaired = true
	require.False(t, result.ValidFor(q), "an import cannot acknowledge source release")
	for _, version := range []byte{0, 1, 3, 255} {
		bad := bytes.Clone(encoded)
		bad[4] = version
		_, err = decodeMQTTRecoveryReply(bad, q)
		require.Error(t, err)
	}
}
