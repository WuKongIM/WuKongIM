package channels

import (
	"bytes"
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttRetirementRecoveryStore struct {
	*mqttReleaseStore
	latest             ch.MQTTReplayRetirementProof
	found              bool
	reads, retires     int
	readErr, retireErr error
	retire             func(context.Context) ch.MQTTReplayRetirementResult
}

func (s *mqttRetirementRecoveryStore) LoadLatestMQTTReplayRetirement(context.Context, string) (ch.MQTTReplayRetirementProof, bool, error) {
	s.reads++
	return s.latest, s.found, s.readErr
}
func (s *mqttRetirementRecoveryStore) RetireMQTTReplay(ctx context.Context, generation string, position uint64, limit int) (ch.MQTTReplayRetirementResult, error) {
	s.retires++
	if generation != s.page.After.Generation || position != s.latest.Manifest.LastOffset || limit != 64 {
		panic("unbounded or foreign retirement")
	}
	return s.retire(ctx), s.retireErr
}

func TestMQTTRecoveryAppliesCommittedRetirementBeforePlanning(t *testing.T) {
	for _, mode := range []string{"complete", "pending", "absent", "ordinary", "missing_port", "read_error", "bad_absence", "foreign", "future", "pre_apply", "post_apply", "stable_fence", "retire_error", "bad_result", "cancel", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s, m, base, factory, q := mqttReleaseFixture(t)
			q.ApplyRetirement = true
			manifest := base.proof.Manifest
			manifest.Version, manifest.BaseOffset, manifest.LastOffset, manifest.PreviousIndex = 6, 2, 3, 2
			manifest.CommandID, manifest.PreviousDigest = ch.CommandID{3}, manifest.Digest
			manifest.Digest = ch.EntryDigest{5}
			st := &mqttRetirementRecoveryStore{mqttReleaseStore: base, found: true, latest: ch.MQTTReplayRetirementProof{Manifest: manifest, Retirement: quorumlog.MQTTReplayRetirement{Anchor: base.proof.Anchor, AnchorPosition: 2, AnchorDigest: base.proof.Manifest.Digest}}}
			factory.handle = st
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st.retire = func(context.Context) ch.MQTTReplayRetirementResult {
				require.Zero(t, st.plans, "retirement must precede planning and donor I/O")
				return ch.MQTTReplayRetirementResult{RetirementPosition: 3, Retired: base.page.After, DeletedThrough: 1, Deleted: 1, Done: true}
			}
			wantError, wantRetire := false, 1
			switch mode {
			case "pending":
				st.retire = func(context.Context) ch.MQTTReplayRetirementResult {
					return ch.MQTTReplayRetirementResult{RetirementPosition: 3, Retired: base.page.After}
				}
			case "absent":
				st.latest, st.found, wantRetire = ch.MQTTReplayRetirementProof{}, false, 0
			case "ordinary":
				q.ApplyRetirement, wantRetire = false, 0
			case "missing_port":
				factory.handle, wantRetire, wantError = base, 0, true
			case "read_error":
				st.readErr, wantRetire, wantError = ch.ErrNotReady, 0, true
			case "bad_absence":
				st.found, wantRetire, wantError = false, 0, true
			case "foreign":
				st.latest.Retirement.Anchor.SourceCommand, wantRetire, wantError = ch.CommandID{99}, 0, true
			case "future":
				st.latest.Manifest.ChannelEpoch++
				wantRetire, wantError = 0, true
			case "pre_apply", "post_apply":
				at := 2
				if mode == "post_apply" {
					at = 3
				} else {
					wantRetire = 0
				}
				m.after = func(n int) {
					if n == at {
						m.meta.RouteGeneration++
					}
				}
				wantError = true
			case "stable_fence":
				m.meta.WriteFence = ch.WriteFence{Token: "migration", Version: 1}
			case "retire_error":
				st.retireErr, wantError = ch.ErrLogConflict, true
			case "bad_result":
				st.retire = func(context.Context) ch.MQTTReplayRetirementResult { return ch.MQTTReplayRetirementResult{} }
				wantError = true
			case "cancel":
				st.retire = func(context.Context) ch.MQTTReplayRetirementResult { cancel(); return ch.MQTTReplayRetirementResult{} }
				wantError = true
			case "panic":
				st.retire = func(context.Context) ch.MQTTReplayRetirementResult { panic("retirement") }
			}
			if mode == "panic" {
				require.Panics(t, func() { _, _ = s.StepMQTTReplayRecovery(ctx, q) })
			} else {
				out, err := s.StepMQTTReplayRecovery(ctx, q)
				if wantError {
					require.Error(t, err)
					require.Zero(t, out)
				} else {
					require.NoError(t, err)
					require.True(t, out.ValidFor(q))
					require.Equal(t, mode == "pending", out.RetirementPending)
				}
			}
			require.Equal(t, wantRetire, st.retires)
			require.Equal(t, factory.opens, st.closed)
			require.Empty(t, s.mqttRepairReceivers)
		})
	}
}

func TestMQTTRetirementRecoveryRPCIsExplicitAndClosed(t *testing.T) {
	_, _, st, _, q := mqttReleaseFixture(t)
	q.ApplyRetirement = true
	for _, release := range []bool{false, true} {
		q.ReleaseSource = release
		request, err := encodeMQTTRecoveryRequest(q)
		require.NoError(t, err)
		require.Equal(t, byte(3), request[4])
		actual, err := decodeMQTTRecoveryRequest(request)
		require.NoError(t, err)
		require.Equal(t, q, actual)
		result := ch.MQTTReplayRecoveryResult{Plan: st.plan, SourceReleased: release, RetirementPending: true}
		reply, err := encodeMQTTRecoveryReply(q, result, nil)
		require.NoError(t, err)
		require.Equal(t, byte(3), reply[4])
		got, err := decodeMQTTRecoveryReply(reply, q)
		require.NoError(t, err)
		require.Equal(t, result, got)
		for cut := range len(request) {
			_, err = decodeMQTTRecoveryRequest(request[:cut])
			require.Error(t, err)
		}
		for cut := range len(reply) {
			_, err = decodeMQTTRecoveryReply(reply[:cut], q)
			require.Error(t, err)
		}
		bad := bytes.Clone(request)
		bad[len(bad)-1] |= 128
		_, err = decodeMQTTRecoveryRequest(bad)
		require.Error(t, err)
		_, err = decodeMQTTRecoveryReply(append(bytes.Clone(reply), 0), q)
		require.Error(t, err)
		legacy := q
		legacy.ApplyRetirement = false
		require.False(t, result.ValidFor(legacy))
		for _, version := range []byte{1, 2, 4} {
			bad = bytes.Clone(reply)
			bad[4] = version
			_, err = decodeMQTTRecoveryReply(bad, q)
			require.Error(t, err)
		}
	}
}
