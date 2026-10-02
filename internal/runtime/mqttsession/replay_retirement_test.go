package mqttsession

import (
	"context"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func retirementScanResult(source meta.MQTTBindingOwner, pass, before uint64) contract.ReplayStepResult {
	proof := ch.MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: ch.CommandID{1}, Through: 99, TotalBytes: 99, TotalStoredBytes: 199, Digest: [32]byte{1}}, Manifest: ch.ProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{2}, BaseOffset: 99, LastOffset: 100, PreviousIndex: 99, PreviousTerm: 1, PreviousDigest: ch.EntryDigest{3}, Digest: ch.EntryDigest{4}}}
	return contract.ReplayStepResult{ContinueRetirement: true, Next: contract.ReplayCursor{Pass: pass, Source: source, Authority: [32]byte{1}, Retirement: contract.ReplayRetirementCursor{Source: source, Authority: [32]byte{1}, Captured: proof, Through: 2, BeforeAnchor: before}}}
}

func TestReplayWorkerRetirementScanIsFiniteAndYieldsAfterCommit(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0, 1}}
	a, b, other := replayOwner(0), replayOwner(1), replayOwner(2)
	s.read = func(slot uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		rows := []meta.MQTTBindingOwner{a, b}
		if slot == 1 {
			rows = []meta.MQTTBindingOwner{other}
		} else if q.After.SourceOwner == a {
			rows = rows[1:]
		}
		return replayPage(q, rows, true), nil
	}
	calls := map[meta.MQTTBindingOwner]int{}
	w := replayWorkerFixture(t, s, func(_ context.Context, source meta.MQTTBindingOwner, cursor contract.ReplayCursor) (contract.ReplayStepResult, error) {
		calls[source]++
		if source == a {
			switch calls[source] {
			case 1:
				return retirementScanResult(a, cursor.Pass, 100), nil
			case 2:
				require.EqualValues(t, 100, cursor.Retirement.BeforeAnchor)
				require.Empty(t, cursor.Targets)
				return retirementScanResult(a, cursor.Pass, 50), nil
			case 3:
				require.EqualValues(t, 50, cursor.Retirement.BeforeAnchor)
				return contract.ReplayStepResult{RetirementCommitted: true}, nil
			}
		}
		return contract.ReplayStepResult{}, ch.ErrNotReady
	}, func(o *ReplayWorkerOptions) { o.PagesPerTurn = 2 })
	var state replayScanState
	for round := 0; round < 3; round++ {
		out := w.sweep(context.Background(), &state)
		if round < 2 {
			require.Equal(t, 1, out.Continuations)
		} else {
			require.Equal(t, 1, out.RetirementCommits)
		}
		require.Len(t, state.slots, 2)
	}
	require.Equal(t, 3, calls[a])
	require.Equal(t, 1, calls[b])
	require.Equal(t, 3, calls[other], "a finite reverse scan must not starve another Slot")
	require.Zero(t, state.slots[0].cursor)
}

func TestReplayWorkerRejectsRetirementContinuationChanges(t *testing.T) {
	for _, mode := range []string{"position", "capture", "floor", "source", "authority", "pass", "recovery", "committed", "target", "outer_authority", "missing_capture", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			source := replayOwner(0)
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				return replayPage(q, []meta.MQTTBindingOwner{source}, true), nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			w := replayWorkerFixture(t, s, func(_ context.Context, _ meta.MQTTBindingOwner, cursor contract.ReplayCursor) (contract.ReplayStepResult, error) {
				calls++
				if calls == 1 {
					return retirementScanResult(source, cursor.Pass, 100), nil
				}
				r := retirementScanResult(source, cursor.Pass, 50)
				switch mode {
				case "position":
					r.Next.Retirement.BeforeAnchor = 100
				case "capture":
					r.Next.Retirement.Captured.Manifest.Digest[0]++
				case "floor":
					r.Next.Retirement.Through++
				case "source":
					r.Next.Retirement.Source = replayOwner(5)
				case "authority":
					r.Next.Retirement.Authority[0]++
				case "pass":
					r.Next.Pass++
				case "recovery":
					r.ContinueScan = true
				case "committed":
					r.RetirementCommitted = true
				case "target":
					r.Next.Targets = []contract.ReplayTargetCursor{{NodeID: 1}}
				case "outer_authority":
					r.Next.Authority[0]++
				case "missing_capture":
					r.Next.Retirement.Captured = ch.MQTTReplayAnchorProof{}
				case "expired":
					cancel()
				}
				return r, nil
			}, nil)
			var state replayScanState
			require.Equal(t, 1, w.sweep(ctx, &state).Continuations)
			out := w.sweep(ctx, &state)
			require.Equal(t, 1, out.Failures)
			require.Zero(t, out.RetirementCommits)
			require.Zero(t, state.slots[0].cursor)
		})
	}
}
