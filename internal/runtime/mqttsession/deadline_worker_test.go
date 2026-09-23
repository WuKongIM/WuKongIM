package mqttsession

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type deadlineSource struct {
	slots   []meta.HashSlot
	listErr error
	read    func(uint16, meta.MQTTRead) (meta.MQTTReadResult, error)
	queries []meta.MQTTRead
	visited []uint16
}

func (s *deadlineSource) LocalLeaderHashSlots(context.Context) ([]meta.HashSlot, error) {
	return s.slots, s.listErr
}
func (s *deadlineSource) ReadMQTTRecovery(_ context.Context, h uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	s.queries = append(s.queries, q)
	s.visited = append(s.visited, h)
	if s.read != nil {
		return s.read(h, q)
	}
	return meta.MQTTReadResult{After: q.After, Done: true}, nil
}

type deadlineHandler func(context.Context, contract.Owner) error

func (f deadlineHandler) ReconcileDeadline(c context.Context, o contract.Owner) error { return f(c, o) }
func deadlineRow(client string, at int64) meta.MQTTSession {
	return meta.MQTTSession{Namespace: "main", ClientID: client, UID: "alice", Generation: 1, Revision: 1, OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 1, State: meta.MQTTSessionActive, SessionExpirySec: 60, LeaseUntilMS: at, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: 1000, QuotaBytes: 1 << 20, WindowLimit: 64, UpdatedAtMS: 1}
}
func deadlineWill(client string, stage meta.MQTTWillStage, at int64) meta.MQTTWill {
	k := meta.MQTTWillKey{Namespace: "main", ClientID: client, SessionGeneration: 1, WillGeneration: 1}
	id, _ := meta.MQTTWillIdempotencyKey(k)
	w := meta.MQTTWill{Key: k, UID: "alice", OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 1, Revision: 2, DecisionRevision: 2, Topic: "topic", TargetID: "group", TargetType: 2, ClientMsgNo: "will", IdempotencyKey: id, QoS: 1, DelaySeconds: 1, Stage: stage, DisconnectedAtMS: at - 1000, DueAtMS: at, UpdatedAtMS: at}
	if stage == meta.MQTTWillExecuting {
		w.ExecutionGeneration = 1
		w.ExecutorNodeID = 1
		w.ExecutorBootID = "exec"
		w.LeaseUntilMS = at + 1000
	}
	return w
}
func sessionScanCursor(r meta.MQTTSession) meta.MQTTReadCursor {
	at := r.LeaseUntilMS
	if r.State == meta.MQTTSessionOffline {
		at = r.OfflineExpiresAtMS
	}
	return meta.MQTTReadCursor{Deadline: meta.MQTTSessionDeadlineCursor{DeadlineMS: at, Namespace: r.Namespace, ClientID: r.ClientID}}
}
func workerFixture(t *testing.T, s *deadlineSource, h deadlineHandler, amend func(*DeadlineWorkerOptions)) *DeadlineWorker {
	t.Helper()
	o := DeadlineWorkerOptions{Source: s, Reconciler: h, Now: func() time.Time { return time.UnixMilli(100000) }, HashSlotCount: 256}
	if amend != nil {
		amend(&o)
	}
	w, e := NewDeadlineWorker(o)
	require.NoError(t, e)
	return w
}

func TestDeadlineWorkerRotatesBothIndexesAcross256Slots(t *testing.T) {
	s := &deadlineSource{}
	for i := 255; i >= 0; i-- {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	w := workerFixture(t, s, func(context.Context, contract.Owner) error { t.Fatal("empty source dispatched"); return nil }, nil)
	var state deadlineScanState
	for i := 0; i < 64; i++ {
		o := w.sweep(context.Background(), &state)
		require.Equal(t, 8, o.Pages)
		require.Zero(t, o.Failures)
	}
	seen := map[string]bool{}
	for i, q := range s.queries {
		seen[fmt.Sprintf("%d/%d", s.visited[i], q.Kind)] = true
	}
	require.Len(t, seen, 512)
	require.Equal(t, meta.HashSlot(255), s.slots[0], "do not sort the provider's buffer in place")
}

func TestDeadlineWorkerBudgetNeverSkipsUnvisitedRowsOrStarvesWill(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{7}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadWillRecovery {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		rows := []meta.MQTTSession{deadlineRow("aa", 1000), deadlineRow("bbb", 1000)}
		if q.After.Deadline.ClientID == "aa" {
			rows = rows[1:]
		}
		return meta.MQTTReadResult{Sessions: rows, After: sessionScanCursor(rows[len(rows)-1]), Done: false}, nil
	}
	var calls []string
	w := workerFixture(t, s, func(_ context.Context, o contract.Owner) error {
		calls = append(calls, o.Key.ClientID)
		return errors.New("retry")
	}, func(o *DeadlineWorkerOptions) { o.MaxVisitsPerTurn = 1; o.PagesPerTurn = 1 })
	var state deadlineScanState
	w.sweep(context.Background(), &state)
	w.sweep(context.Background(), &state)
	w.sweep(context.Background(), &state)
	require.Equal(t, []string{"aa", "bbb"}, calls)
	require.Equal(t, meta.MQTTReadWillRecovery, s.queries[1].Kind)
	require.Equal(t, "aa", s.queries[2].After.Deadline.ClientID)
}

func TestDeadlineWorkerSkipsDetachedWillAndResetsAtFutureBoundary(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{1}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadSessionDeadlines {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		rows := []meta.MQTTWill{deadlineWill("ready", meta.MQTTWillReady, 5000), deadlineWill("executing", meta.MQTTWillExecuting, 6000), deadlineWill("waiting", meta.MQTTWillWaiting, 8000), deadlineWill("future", meta.MQTTWillWaiting, 200000)}
		last := rows[len(rows)-1]
		return meta.MQTTReadResult{Wills: rows, After: meta.MQTTReadCursor{Will: meta.MQTTWillRecoveryCursor{RecoveryAtMS: last.DueAtMS, Key: last.Key}}, Done: false}, nil
	}
	var calls []string
	w := workerFixture(t, s, func(_ context.Context, o contract.Owner) error { calls = append(calls, o.Key.ClientID); return nil }, func(o *DeadlineWorkerOptions) { o.PagesPerTurn = 2 })
	var state deadlineScanState
	w.sweep(context.Background(), &state)
	w.sweep(context.Background(), &state)
	require.Equal(t, []string{"waiting", "waiting"}, calls)
	require.Equal(t, meta.MQTTReadCursor{}, s.queries[3].After)
}

func TestDeadlineWorkerCancellationPreservesUnstartedCursorAndFailedScan(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadWillRecovery {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		rows := []meta.MQTTSession{deadlineRow("a", 1000), deadlineRow("b", 1000)}
		return meta.MQTTReadResult{Sessions: rows, After: sessionScanCursor(rows[1]), Done: false}, nil
	}
	w := workerFixture(t, s, func(context.Context, contract.Owner) error { cancel(); return context.Canceled }, nil)
	var state deadlineScanState
	o := w.sweep(ctx, &state)
	require.Equal(t, 1, o.Visited)
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		return meta.MQTTReadResult{}, errors.New("unavailable")
	}
	w.sweep(context.Background(), &state)
	var sessionQuery meta.MQTTRead
	for _, q := range s.queries[1:] {
		if q.Kind == meta.MQTTReadSessionDeadlines {
			sessionQuery = q
		}
	}
	require.Equal(t, "a", sessionQuery.After.Deadline.ClientID)
	w.sweep(context.Background(), &state)
	require.Equal(t, "a", s.queries[len(s.queries)-1].After.Deadline.ClientID)
}

func TestDeadlineWorkerDropsLostSlotCursorsAndRejectsMalformedLists(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{2}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadWillRecovery {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		r := deadlineRow("a", 1000)
		return meta.MQTTReadResult{Sessions: []meta.MQTTSession{r}, After: sessionScanCursor(r), Done: false}, nil
	}
	w := workerFixture(t, s, func(context.Context, contract.Owner) error { return nil }, func(o *DeadlineWorkerOptions) { o.PagesPerTurn = 1 })
	var state deadlineScanState
	w.sweep(context.Background(), &state)
	s.slots = nil
	w.sweep(context.Background(), &state)
	s.slots = []meta.HashSlot{2}
	w.sweep(context.Background(), &state)
	w.sweep(context.Background(), &state)
	require.Equal(t, meta.MQTTReadCursor{}, s.queries[len(s.queries)-1].After)
	for _, slots := range [][]meta.HashSlot{{2, 2}, {256}, make([]meta.HashSlot, 257)} {
		s.slots = slots
		n := len(s.queries)
		o := w.sweep(context.Background(), &state)
		require.Positive(t, o.Failures)
		require.Len(t, s.queries, n)
	}
}

func TestDeadlineWorkerRejectsWholeMalformedPageBeforeEffects(t *testing.T) {
	for _, mode := range []string{"duplicate", "order", "too-many", "bad-next", "empty-more", "foreign-kind", "ended", "invalid-owner"} {
		t.Run(mode, func(t *testing.T) {
			s := &deadlineSource{slots: []meta.HashSlot{0}}
			s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				a, b := deadlineRow("z", 1000), deadlineRow("aa", 1000)
				r := meta.MQTTReadResult{Sessions: []meta.MQTTSession{a, b}, After: sessionScanCursor(b)}
				switch mode {
				case "duplicate":
					r.Sessions[1] = a
					r.After = sessionScanCursor(a)
				case "order":
					r.Sessions = []meta.MQTTSession{b, a}
					r.After = sessionScanCursor(a)
				case "too-many":
					for i := 0; i < 17; i++ {
						r.Sessions = append(r.Sessions, a)
					}
				case "bad-next":
					r.After = sessionScanCursor(a)
				case "empty-more":
					r.Sessions = nil
					r.After = q.After
				case "foreign-kind":
					r.Wills = []meta.MQTTWill{deadlineWill("will", meta.MQTTWillWaiting, 5000)}
				case "ended":
					r.Sessions[1].State = meta.MQTTSessionEnded
				case "invalid-owner":
					r.Sessions[1].OwnerNodeID = 0
				}
				return r, nil
			}
			w := workerFixture(t, s, func(context.Context, contract.Owner) error { t.Fatal("invalid page partially dispatched"); return nil }, func(o *DeadlineWorkerOptions) { o.PagesPerTurn = 1 })
			var state deadlineScanState
			o := w.sweep(context.Background(), &state)
			require.Equal(t, 1, o.Failures)
			require.Zero(t, o.Visited)
		})
	}
}

func TestDeadlineWorkerRequiresBoundedCompleteOptions(t *testing.T) {
	s := &deadlineSource{}
	h := deadlineHandler(func(context.Context, contract.Owner) error { return nil })
	for _, mutate := range []func(*DeadlineWorkerOptions){
		func(o *DeadlineWorkerOptions) { o.Source = nil }, func(o *DeadlineWorkerOptions) { o.Reconciler = nil },
		func(o *DeadlineWorkerOptions) { o.PageSize = 17 }, func(o *DeadlineWorkerOptions) { o.PagesPerTurn = 33 }, func(o *DeadlineWorkerOptions) { o.MaxVisitsPerTurn = 257 },
		func(o *DeadlineWorkerOptions) { o.Interval = -1 }, func(o *DeadlineWorkerOptions) { o.TurnTimeout = time.Minute + 1 }, func(o *DeadlineWorkerOptions) { o.ItemTimeout = time.Minute },
	} {
		o := DeadlineWorkerOptions{Source: s, Reconciler: h}
		mutate(&o)
		_, err := NewDeadlineWorker(o)
		require.Error(t, err)
	}
}

func TestDeadlineWorkerFutureCandidateConsumesVisitBudget(t *testing.T) {
	s := &deadlineSource{slots: []meta.HashSlot{0}}
	s.read = func(_ uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if q.Kind == meta.MQTTReadWillRecovery {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		r := deadlineRow("future", 200000)
		return meta.MQTTReadResult{Sessions: []meta.MQTTSession{r}, After: sessionScanCursor(r)}, nil
	}
	w := workerFixture(t, s, func(context.Context, contract.Owner) error { t.Fatal("future candidate dispatched"); return nil }, func(o *DeadlineWorkerOptions) { o.MaxVisitsPerTurn = 1; o.PagesPerTurn = 2 })
	var state deadlineScanState
	o := w.sweep(context.Background(), &state)
	require.Equal(t, 1, o.Pages)
	require.Equal(t, 1, o.Visited)
}
