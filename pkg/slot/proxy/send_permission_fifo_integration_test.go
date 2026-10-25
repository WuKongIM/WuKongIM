//go:build integration

package proxy

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Each fixture owns 64 real, independently blocked fresh barriers. Admission,
// reservation and result transfer use public reads; only the existing controlled
// collection window is sealed explicitly, without changing accounting fields.
func TestSendPermissionReadyFIFO(t *testing.T) {
	t.Run("spare-budget-queues-and-merges-at-a-new-barrier", func(t *testing.T) {
		f := newPermissionFIFOFixture(t, false)
		pending := make([]<-chan []PermissionMetadataReadResult, 32)
		for i := range pending {
			pending[i] = f.read(context.Background(), 1)
		}
		f.awaitOwned(96, pending)
		require.EqualValues(t, 64, f.enteredCount.Load())
		require.Zero(t, f.observer.busy.Load())
		wb := f.store.db.NewWriteBatch()
		_, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
		require.NoError(t, err)
		require.NoError(t, wb.Commit())
		require.NoError(t, wb.Close())
		close(f.gates[0])
		f.awaitBarrier(65)
		require.EqualValues(t, 96, f.observer.selected.Load(), "one freed execution must select all 32 ready callers")
		require.EqualValues(t, 64, f.observer.cohorts.Load())
		for _, p := range pending {
			select {
			case <-p:
				t.Fatal("queued call reused an old barrier result")
			default:
			}
		}
		close(f.gates[64])
		for _, p := range pending {
			out := awaitBoundaryResult(t, p)
			require.NoError(t, out[0].Err)
			require.EqualValues(t, 1, out[0].UserPolicy.SendBan)
		}
		require.EqualValues(t, 65, f.enteredCount.Load())
	})
	t.Run("fifo-prefix-bounds-original-calls", func(t *testing.T) {
		f := newPermissionFIFOFixture(t, false)
		pending := make([]<-chan []PermissionMetadataReadResult, 65)
		for i := range pending {
			pending[i] = f.read(context.Background(), 1)
			f.awaitOwned(65+i, []<-chan []PermissionMetadataReadResult{pending[i]})
		}
		close(f.gates[0])
		f.awaitBarrier(65)
		require.EqualValues(t, 128, f.observer.selected.Load())
		close(f.gates[64])
		for _, p := range pending[:64] {
			require.NoError(t, awaitBoundaryResult(t, p)[0].Err)
		}
		f.awaitBarrier(66)
		select {
		case <-pending[64]:
			t.Fatal("FIFO tail completed before its fresh barrier")
		default:
		}
		close(f.gates[65])
		require.NoError(t, awaitBoundaryResult(t, pending[64])[0].Err)
		require.EqualValues(t, 129, f.observer.selected.Load())
	})
	t.Run("dedup-does-not-remove-original-input-bound", func(t *testing.T) {
		f := newPermissionFIFOFixture(t, false)
		first := f.read(context.Background(), 2048)
		f.awaitOwned(65, []<-chan []PermissionMetadataReadResult{first})
		second := f.read(context.Background(), 2049)
		f.awaitOwned(66, []<-chan []PermissionMetadataReadResult{second})
		tail := f.read(context.Background(), 1)
		f.awaitOwned(67, []<-chan []PermissionMetadataReadResult{tail})
		close(f.gates[0])
		f.awaitBarrier(65)
		require.EqualValues(t, 65, f.observer.selected.Load(), "must not skip the 2049-input FIFO head to fill a 4096-input batch")
		close(f.gates[64])
		out := awaitBoundaryResult(t, first)
		require.Len(t, out, 2048)
		for _, r := range out {
			require.NoError(t, r.Err)
		}
		f.awaitBarrier(66)
		require.EqualValues(t, 67, f.observer.selected.Load())
		select {
		case <-tail:
			t.Fatal("small tail bypassed the large FIFO head")
		default:
		}
		close(f.gates[65])
		out = awaitBoundaryResult(t, second)
		require.Len(t, out, 2049)
		for _, r := range out {
			require.NoError(t, r.Err)
		}
		require.NoError(t, awaitBoundaryResult(t, tail)[0].Err)
	})
	t.Run("pending-cancel-refunds-without-starting-work", func(t *testing.T) {
		f := newPermissionFIFOFixture(t, false)
		ctx, cancel := context.WithCancel(context.Background())
		first := f.read(ctx, 1)
		f.awaitOwned(65, []<-chan []PermissionMetadataReadResult{first})
		long := f.read(context.Background(), 1)
		f.awaitOwned(66, []<-chan []PermissionMetadataReadResult{long})
		cancel()
		require.ErrorIs(t, awaitBoundaryResult(t, first)[0].Err, context.Canceled)
		f.awaitOwned(65, nil)
		require.EqualValues(t, 65*1282, f.observer.bytes.Load())
		close(f.gates[0])
		f.awaitBarrier(65)
		require.EqualValues(t, 65, f.observer.selected.Load())
		close(f.gates[64])
		require.NoError(t, awaitBoundaryResult(t, long)[0].Err)
	})
	t.Run("pending-deadline-does-not-poison-no-deadline-follower", func(t *testing.T) {
		f := newPermissionFIFOFixture(t, false)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		first := f.read(ctx, 1)
		f.awaitOwned(65, []<-chan []PermissionMetadataReadResult{first})
		long := f.read(context.Background(), 1)
		f.awaitOwned(66, []<-chan []PermissionMetadataReadResult{long})
		require.ErrorIs(t, awaitBoundaryResult(t, first)[0].Err, context.DeadlineExceeded)
		f.awaitOwned(65, nil)
		close(f.gates[0])
		f.awaitBarrier(65)
		close(f.gates[64])
		require.NoError(t, awaitBoundaryResult(t, long)[0].Err)
	})
	t.Run("close-refunds-pending-but-joins-actual-executions", func(t *testing.T) {
		f := newPermissionFIFOFixture(t, true)
		pending := f.read(context.Background(), 1)
		f.awaitOwned(65, []<-chan []PermissionMetadataReadResult{pending})
		joined := make(chan struct{})
		go func() { f.store.CloseSendPermissionReads(); close(joined) }()
		require.ErrorIs(t, awaitBoundaryResult(t, pending)[0].Err, context.Canceled)
		f.awaitOwned(64, nil)
		select {
		case <-joined:
			t.Fatal("Close discarded a real blocked provider")
		default:
		}
		out := f.store.ReadSendPermissionMetadataBatch(context.Background(), fifoReads(1))
		require.ErrorIs(t, out[0].Err, context.Canceled)
		closeBoundaryGate(f.releaseAll)
		awaitBoundaryEvent(t, joined)
		require.Eventually(t, func() bool { return f.observer.calls.Load() == 0 && f.observer.bytes.Load() == 0 }, time.Second, time.Millisecond)
		requireCohortDrained(t, f.store)
	})
}

type permissionFIFOObserver struct{ calls, bytes, cohorts, selected, busy atomic.Int64 }

func (o *permissionFIFOObserver) ObserveSendPermissionCount(kind string, n int) {
	if kind == "cohort_requests" {
		o.selected.Add(int64(n))
	}
	if kind == "cohort_busy" {
		o.busy.Add(int64(n))
	}
}
func (*permissionFIFOObserver) ObserveSendPermissionStage(string, string, time.Duration) {}
func (*permissionFIFOObserver) ObserveSendPermissionInflight(int)                        {}
func (o *permissionFIFOObserver) ObserveSendPermissionCohortOwned(kind string, n int) {
	switch kind {
	case "calls":
		o.calls.Add(int64(n))
	case "budget_bytes":
		o.bytes.Add(int64(n))
	case "cohorts":
		o.cohorts.Add(int64(n))
	}
}

type permissionFIFOFixture struct {
	t            *testing.T
	store        *Store
	observer     *permissionFIFOObserver
	gates        [128]chan struct{}
	releaseAll   chan struct{}
	entered      chan int
	enteredCount atomic.Int64
	joins        []<-chan struct{}
}

func newPermissionFIFOFixture(t *testing.T, ignoreCancel bool) *permissionFIFOFixture {
	f := &permissionFIFOFixture{t: t, observer: &permissionFIFOObserver{}, releaseAll: make(chan struct{}), entered: make(chan int, 128)}
	for i := range f.gates {
		f.gates[i] = make(chan struct{})
	}
	c := newBoundaryCluster(2)
	c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
		n := int(f.enteredCount.Add(1))
		f.entered <- n
		if n > len(f.gates) {
			return context.Canceled
		}
		if ignoreCancel {
			select {
			case <-f.gates[n-1]:
			case <-f.releaseAll:
			}
			return ctx.Err()
		}
		select {
		case <-f.gates[n-1]:
			return nil
		case <-f.releaseAll:
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.store = New(c, boundaryDB(t))
	f.store.permissionCohortWindow = time.Hour
	f.store.SetSendPermissionObserver(f.observer)
	t.Cleanup(func() {
		closeBoundaryGate(f.releaseAll)
		f.store.CloseSendPermissionReads()
		for _, joined := range f.joins {
			awaitBoundaryEvent(t, joined)
		}
		requireCohortDrained(t, f.store)
	})
	for i := 0; i < 64; i++ {
		p := f.read(context.Background(), 1)
		f.awaitOwned(i+1, []<-chan []PermissionMetadataReadResult{p})
		sealTestCohort(t, f.store, i+1)
		f.awaitBarrier(i + 1)
	}
	return f
}
func fifoReads(n int) []PermissionMetadataRead {
	reads := make([]PermissionMetadataRead, n)
	for i := range reads {
		reads[i] = PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}
	}
	return reads
}
func (f *permissionFIFOFixture) read(ctx context.Context, n int) <-chan []PermissionMetadataReadResult {
	done := make(chan []PermissionMetadataReadResult, 1)
	joined := make(chan struct{})
	f.joins = append(f.joins, joined)
	go func() { defer close(joined); done <- f.store.ReadSendPermissionMetadataBatch(ctx, fifoReads(n)) }()
	return done
}
func (f *permissionFIFOFixture) awaitOwned(n int, pending []<-chan []PermissionMetadataReadResult) {
	f.t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for f.observer.calls.Load() != int64(n) {
		for _, p := range pending {
			select {
			case out := <-p:
				f.t.Fatalf("call rejected before spare budget could queue it: %v", out[0].Err)
			default:
			}
		}
		select {
		case <-deadline.C:
			f.t.Fatalf("owned calls: got %d, want %d", f.observer.calls.Load(), n)
		case <-tick.C:
		}
	}
}
func (f *permissionFIFOFixture) awaitBarrier(want int) {
	f.t.Helper()
	select {
	case n := <-f.entered:
		require.Equal(f.t, want, n)
	case <-time.After(2 * time.Second):
		f.t.Fatal("fresh FIFO barrier did not start")
	}
}
