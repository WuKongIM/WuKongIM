package mqttsession

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type replayHandler func(context.Context, meta.MQTTBindingOwner, contract.ReplayCursor) (contract.ReplayStepResult, error)

func (f replayHandler) Step(c context.Context, s meta.MQTTBindingOwner, p contract.ReplayCursor) (contract.ReplayStepResult, error) {
	return f(c, s, p)
}
func replayOwner(n int) meta.MQTTBindingOwner {
	return meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: fmt.Sprintf("2:source-%06d", n), Generation: "mqtt-log-v1:01" + strings.Repeat("00", 31)}
}
func replayPage(q meta.MQTTRead, rows []meta.MQTTBindingOwner, done bool) meta.MQTTReadResult {
	after := q.After
	if len(rows) > 0 {
		after.SourceOwner = rows[len(rows)-1]
	}
	return meta.MQTTReadResult{SourceOwners: rows, After: after, Done: done}
}
func replayWorkerFixture(t *testing.T, s DeadlineSource, h replayHandler, amend func(*ReplayWorkerOptions)) *ReplayWorker {
	t.Helper()
	o := ReplayWorkerOptions{Source: s, Stepper: h}
	if amend != nil {
		amend(&o)
	}
	w, err := NewReplayWorker(o)
	require.NoError(t, err)
	return w
}

func TestReplayWorkerRotates256SlotsAndOwnsOnlySlotState(t *testing.T) {
	s := &deadlineSource{}
	for i := 255; i >= 0; i-- {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	w := replayWorkerFixture(t, s, func(context.Context, meta.MQTTBindingOwner, contract.ReplayCursor) (contract.ReplayStepResult, error) {
		t.Fatal("empty source dispatched")
		return contract.ReplayStepResult{}, nil
	}, nil)
	var state replayScanState
	for range 32 {
		o := w.sweep(context.Background(), &state)
		require.Equal(t, 8, o.Pages)
		require.Zero(t, o.Failures)
	}
	seen := map[uint16]bool{}
	for _, slot := range s.visited {
		seen[slot] = true
	}
	require.Len(t, seen, 256)
	require.Len(t, state.slots, 256)
	require.Equal(t, meta.HashSlot(255), s.slots[0])
	for _, q := range s.queries {
		require.Equal(t, meta.MQTTReadSourceOwners, q.Kind)
	}
}

func TestReplayWorkerManySourcesDoNotAllocateSourceCacheOrStarveAfterErrors(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	const count = 4096
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		start := 0
		if q.After.SourceOwner != (meta.MQTTBindingOwner{}) {
			_, err := fmt.Sscanf(q.After.SourceOwner.ID, "2:source-%06d", &start)
			require.NoError(t, err)
			start++
		}
		var rows []meta.MQTTBindingOwner
		for n := start; n < min(start+q.Limit, count); n++ {
			rows = append(rows, replayOwner(n))
		}
		return replayPage(q, rows, start+len(rows) == count), nil
	}
	calls := 0
	seen := make(map[meta.MQTTBindingOwner]uint64)
	w := replayWorkerFixture(t, s, func(_ context.Context, source meta.MQTTBindingOwner, c contract.ReplayCursor) (contract.ReplayStepResult, error) {
		require.Empty(t, c.Targets)
		seen[source] = c.Pass
		calls++
		return contract.ReplayStepResult{}, ch.ErrNotReady
	}, func(o *ReplayWorkerOptions) { o.PageSize = 64; o.MaxVisitsPerTurn = 64; o.PagesPerTurn = 1 })
	var state replayScanState
	for range 2 * count / 64 {
		out := w.sweep(context.Background(), &state)
		require.Equal(t, 64, out.Attempts)
		require.Equal(t, 64, out.Failures)
		require.Len(t, state.slots, 1)
	}
	require.Equal(t, 2*count, calls)
	require.Len(t, seen, count)
	for _, pass := range seen {
		require.Equal(t, uint64(1), pass)
	}
}

func TestReplayWorkerBudgetPreservesUnstartedSources(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	a, b := replayOwner(0), replayOwner(1)
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		rows := []meta.MQTTBindingOwner{a, b}
		if q.After.SourceOwner == a {
			rows = rows[1:]
		}
		return replayPage(q, rows, true), nil
	}
	var seen []meta.MQTTBindingOwner
	var passes []uint64
	w := replayWorkerFixture(t, s, func(_ context.Context, source meta.MQTTBindingOwner, c contract.ReplayCursor) (contract.ReplayStepResult, error) {
		seen = append(seen, source)
		passes = append(passes, c.Pass)
		return contract.ReplayStepResult{}, nil
	}, func(o *ReplayWorkerOptions) { o.MaxVisitsPerTurn = 1 })
	var state replayScanState
	for range 3 {
		w.sweep(context.Background(), &state)
	}
	require.Equal(t, []meta.MQTTBindingOwner{a, b, a}, seen)
	require.Equal(t, []uint64{0, 0, 1}, passes)
	require.Equal(t, a, s.queries[1].After.SourceOwner)
}

func scanReplayResult(source meta.MQTTBindingOwner, pass uint64, after uint64) contract.ReplayStepResult {
	return contract.ReplayStepResult{Target: 1, ContinueScan: true, Next: contract.ReplayCursor{Source: source, Pass: pass, Authority: [32]byte{1}, NextTarget: 1, Targets: []contract.ReplayTargetCursor{{NodeID: 1, AnchorPosition: 100, AfterAnchor: after}, {NodeID: 2}}}}
}
func TestReplayWorkerPinsScanAndYieldsToOtherSlots(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0, 1}}
	s.read = func(slot uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		return replayPage(q, []meta.MQTTBindingOwner{replayOwner(int(slot))}, true), nil
	}
	calls := map[meta.MQTTBindingOwner]int{}
	w := replayWorkerFixture(t, s, func(_ context.Context, source meta.MQTTBindingOwner, c contract.ReplayCursor) (contract.ReplayStepResult, error) {
		calls[source]++
		if source == replayOwner(0) {
			switch calls[source] {
			case 1:
				return scanReplayResult(source, c.Pass, 1), nil
			case 2:
				require.True(t, c.RepairNext)
				require.Zero(t, c.NextTarget)
				require.Equal(t, uint64(100), c.Targets[0].AnchorPosition)
				require.Equal(t, uint64(1), c.Targets[0].AfterAnchor)
				return scanReplayResult(source, c.Pass, 2), nil
			case 3:
				require.Equal(t, uint64(2), c.Targets[0].AfterAnchor)
				return contract.ReplayStepResult{Repaired: true, Target: 1}, nil
			}
		}
		return contract.ReplayStepResult{}, ch.ErrNotReady
	}, func(o *ReplayWorkerOptions) { o.PagesPerTurn = 2 })
	var state replayScanState
	for range 3 {
		w.sweep(context.Background(), &state)
	}
	require.Equal(t, 3, calls[replayOwner(0)])
	require.Equal(t, 3, calls[replayOwner(1)])
	require.Zero(t, s.queries[2].After.SourceOwner)
	require.Zero(t, s.queries[4].After.SourceOwner)
}

func TestReplayWorkerDropsChangedSourceAndLostSlotHints(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	current := replayOwner(0)
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		return replayPage(q, []meta.MQTTBindingOwner{current}, true), nil
	}
	var cursors []contract.ReplayCursor
	w := replayWorkerFixture(t, s, func(_ context.Context, source meta.MQTTBindingOwner, c contract.ReplayCursor) (contract.ReplayStepResult, error) {
		cursors = append(cursors, c)
		return scanReplayResult(source, c.Pass, 1), nil
	}, nil)
	var state replayScanState
	w.sweep(context.Background(), &state)
	current = replayOwner(1)
	w.sweep(context.Background(), &state)
	require.Empty(t, cursors[1].Targets)
	s.slots = nil
	w.sweep(context.Background(), &state)
	require.Empty(t, state.slots)
	s.slots = []meta.HashSlot{0}
	w.sweep(context.Background(), &state)
	require.Empty(t, cursors[2].Targets)
	require.Zero(t, cursors[2].Pass)
}

func TestReplayWorkerRejectsWholeMalformedPagesAndSlotLists(t *testing.T) {
	for _, mode := range []string{"duplicate_slot", "foreign_slot", "duplicate_source", "regression", "foreign_kind", "foreign_rows", "foreign_cursor", "oversize", "empty_nonfinal", "wrong_after", "partial_nonfinal", "late_page"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			if mode == "duplicate_slot" {
				s.slots = []meta.HashSlot{0, 0}
			}
			if mode == "foreign_slot" {
				s.slots = []meta.HashSlot{256}
			}
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				a, b := replayOwner(0), replayOwner(1)
				r := replayPage(q, []meta.MQTTBindingOwner{a, b}, true)
				switch mode {
				case "duplicate_source":
					r.SourceOwners[1] = a
					r.After.SourceOwner = a
				case "regression":
					r.SourceOwners = []meta.MQTTBindingOwner{b, a}
					r.After.SourceOwner = a
				case "foreign_kind":
					r.SourceOwners[1].Kind = meta.MQTTBindingUID
					r.After.SourceOwner = r.SourceOwners[1]
				case "foreign_rows":
					r.Sessions = []meta.MQTTSession{deadlineRow("x", 1)}
				case "foreign_cursor":
					r.After.Deadline.DeadlineMS = 1
				case "oversize":
					for range 65 {
						r.SourceOwners = append(r.SourceOwners, b)
					}
				case "empty_nonfinal":
					r = meta.MQTTReadResult{}
				case "wrong_after":
					r.After.SourceOwner = replayOwner(9)
				case "partial_nonfinal":
					r.Done = false
				case "late_page":
					cancel()
				}
				return r, nil
			}
			w := replayWorkerFixture(t, s, func(context.Context, meta.MQTTBindingOwner, contract.ReplayCursor) (contract.ReplayStepResult, error) {
				t.Fatal("invalid evidence dispatched")
				return contract.ReplayStepResult{}, nil
			}, nil)
			var state replayScanState
			out := w.sweep(ctx, &state)
			require.Equal(t, 1, out.Failures)
			require.Zero(t, out.Attempts)
		})
	}
}

func TestReplayWorkerRejectsInvalidOrNonadvancingScanContinuations(t *testing.T) {
	for _, mode := range []string{"oversize", "foreign_source", "flags", "target", "position", "no_progress", "placement_changed"} {
		t.Run(mode, func(t *testing.T) {
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			source := replayOwner(0)
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				return replayPage(q, []meta.MQTTBindingOwner{source}, true), nil
			}
			calls := 0
			w := replayWorkerFixture(t, s, func(_ context.Context, _ meta.MQTTBindingOwner, c contract.ReplayCursor) (contract.ReplayStepResult, error) {
				calls++
				r := scanReplayResult(source, c.Pass, 1)
				if calls == 1 {
					return r, nil
				}
				switch mode {
				case "oversize":
					r.Next.Targets = make([]contract.ReplayTargetCursor, 257)
				case "foreign_source":
					r.Next.Source = replayOwner(2)
				case "flags":
					r.Repaired = true
				case "target":
					r.Target = 99
				case "position":
					r.Next.Targets[0].AfterAnchor = 100
				case "placement_changed":
					r.Next.Authority = [32]byte{2}
					r.Next.Targets[0].AfterAnchor = 2
				}
				return r, nil
			}, nil)
			var state replayScanState
			w.sweep(context.Background(), &state)
			w.sweep(context.Background(), &state)
			w.sweep(context.Background(), &state)
			require.Zero(t, s.queries[2].After.SourceOwner, "ending final source visit wraps the pass")
		})
	}
}

func TestReplayWorkerRequiresBoundedOptions(t *testing.T) {
	s := &deadlineSource{}
	h := replayHandler(func(context.Context, meta.MQTTBindingOwner, contract.ReplayCursor) (contract.ReplayStepResult, error) {
		return contract.ReplayStepResult{}, nil
	})
	for _, mutate := range []func(*ReplayWorkerOptions){func(o *ReplayWorkerOptions) { o.Source = nil }, func(o *ReplayWorkerOptions) { o.Stepper = nil }, func(o *ReplayWorkerOptions) { o.Interval = -1 }, func(o *ReplayWorkerOptions) { o.TurnTimeout = 2 * time.Minute }, func(o *ReplayWorkerOptions) { o.ReadTimeout = 2 * time.Minute }, func(o *ReplayWorkerOptions) { o.StepTimeout = 2 * time.Minute }, func(o *ReplayWorkerOptions) { o.PagesPerTurn = 33 }, func(o *ReplayWorkerOptions) { o.PageSize = 65 }, func(o *ReplayWorkerOptions) { o.MaxVisitsPerTurn = 257 }} {
		o := ReplayWorkerOptions{Source: s, Stepper: h}
		mutate(&o)
		_, err := NewReplayWorker(o)
		require.Error(t, err)
	}
}
