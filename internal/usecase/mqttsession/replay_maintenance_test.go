package mqttsession

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestReplayMaintenanceRotatesAllPhasesAndReplicas(t *testing.T) {
	retirement, r, source := newRetirementFixture(t)
	copy, f, _ := newReplayFixture(t)
	f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, 4
	f.copyErr = ch.ErrNotReady // A busy/unavailable copy must not block the next cold phase.
	m, err := NewReplayMaintenance(copy, retirement)
	require.NoError(t, err)
	var targets []ch.NodeID
	for pass := uint64(0); pass < 18; pass++ {
		out, e := m.Step(context.Background(), source, ReplayCursor{Pass: pass})
		require.Equal(t, pass, out.Next.Pass)
		switch pass % 3 {
		case 0:
			require.ErrorIs(t, e, ch.ErrNotReady)
		case 1:
			require.NoError(t, e)
			require.True(t, out.TargetComplete)
			targets = append(targets, out.Target)
		case 2:
			require.NoError(t, e)
			require.True(t, out.RetirementCommitted)
			require.False(t, out.ContinueRetirement)
			require.Empty(t, out.Next.Targets)
		}
	}
	require.Equal(t, []ch.NodeID{1, 2, 3, 1, 2, 3}, targets)
	require.Equal(t, 6, f.copies)
	require.Equal(t, 6, f.repairs)
	require.Len(t, r.writes, 6)
}

func TestReplayMaintenanceKeepsRetirementScanInItsPhase(t *testing.T) {
	retirement, r, source := newRetirementFixture(t)
	copy, f, _ := newReplayFixture(t)
	m, err := NewReplayMaintenance(copy, retirement)
	require.NoError(t, err)
	r.view.Bindings[0].CompletedThrough = 1
	r.selectPage = func(q ch.MQTTReplayRetirementSelectionRequest) ch.MQTTReplayRetirementSelection {
		if q.BeforeAnchor == 0 {
			return ch.MQTTReplayRetirementSelection{Captured: q.Captured, BeforeAnchor: 3}
		}
		return ch.MQTTReplayRetirementSelection{Captured: q.Captured, Done: true}
	}
	one, err := m.Step(context.Background(), source, ReplayCursor{Pass: 2})
	require.NoError(t, err)
	require.True(t, one.ContinueRetirement)
	require.False(t, one.ContinueScan)
	require.Equal(t, source, one.Next.Source)
	require.Equal(t, one.Next.Authority, one.Next.Retirement.Authority)
	require.EqualValues(t, 3, one.Next.Retirement.BeforeAnchor)
	two, err := m.Step(context.Background(), source, one.Next)
	require.NoError(t, err)
	require.False(t, two.ContinueRetirement)
	require.False(t, two.RetirementCommitted)
	require.Zero(t, f.plans, "retirement must not perform a duplicate copy/recovery plan")
	for _, mode := range []string{"wrong_phase", "recovery_targets", "mismatched_authority", "repair_flag"} {
		t.Run(mode, func(t *testing.T) {
			cursor := one.Next
			switch mode {
			case "wrong_phase":
				cursor.Pass = 1
			case "recovery_targets":
				cursor.Targets = []ReplayTargetCursor{{NodeID: 1}}
			case "mismatched_authority":
				cursor.Authority[0]++
			case "repair_flag":
				cursor.RepairNext = true
			}
			_, e := m.Step(context.Background(), source, cursor)
			require.ErrorIs(t, e, ErrEvidence)
		})
	}
}
