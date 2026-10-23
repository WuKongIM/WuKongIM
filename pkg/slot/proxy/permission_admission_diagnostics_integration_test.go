//go:build integration

package proxy

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Public reads own real input reservations and block only at the existing
// cluster barrier port. No test changes accounting fields or capacity limits.
func TestPermissionAdmissionRejectionSnapshot(t *testing.T) {
	small := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
	large := make([]PermissionMetadataRead, 32)
	for i := range large {
		large[i] = PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: strings.Repeat("x", 32<<10)}
	}
	for _, tc := range []struct {
		name                          string
		cohorts, width                int
		reads, rejected               []PermissionMetadataRead
		wantReasons                   uint8
		ownedCallBytes, rejectedBytes int
	}{
		{"calls", 16, 64, small, small, 2, 1282, 1282},
		{"bytes", 7, 1, large, large, 4, 2130176, 2130176},
		{"calls-and-bytes", 64, 16, small, []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: strings.Repeat("y", 15<<19)}}, 6, 1282, 15729920},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := boundaryDB(t)
			cluster := newBoundaryCluster(2)
			gate := boundaryGate(t)
			entered := make(chan struct{}, 64)
			cluster.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
				entered <- struct{}{}
				select {
				case <-gate:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			store := New(cluster, db)
			store.permissionCohortWindow = time.Hour // existing controlled seal seam
			t.Cleanup(store.CloseSendPermissionReads)
			observer := &rejectionSnapshotObserver{}
			store.SetSendPermissionObserver(observer)
			var pending []<-chan []PermissionMetadataReadResult
			for i := 0; i < tc.cohorts; i++ {
				for j := 0; j < tc.width; j++ {
					pending = append(pending, startBoundaryRead(t, store, tc.reads))
				}
				if tc.width != 64 {
					sealTestCohort(t, store, len(pending))
				}
				awaitBoundaryEvent(t, entered)
			}
			out := store.ReadSendPermissionMetadataBatch(context.Background(), tc.rejected)
			require.ErrorIs(t, out[0].Err, ErrPermissionBusy)
			require.Equal(t, ErrPermissionBusy.Error(), out[0].Err.Error())
			snapshot, ok := PermissionAdmissionRejectionFromError(fmt.Errorf("outer: %w", out[0].Err))
			require.True(t, ok, "public admission rejection lost its measured reservation snapshot")
			require.Equal(t, tc.wantReasons, snapshot.Reasons)
			require.Equal(t, tc.cohorts, snapshot.ActiveCohorts)
			require.Equal(t, len(pending), snapshot.OwnedCalls)
			require.Equal(t, len(pending)*tc.ownedCallBytes, snapshot.OwnedBytes)
			require.Equal(t, tc.rejectedBytes, snapshot.RequestBytes)
			require.EqualValues(t, 1, observer.busy.Load(), "observing a rejection must not count it twice")
			closeBoundaryGate(gate)
			for _, p := range pending {
				require.NoError(t, awaitBoundaryResult(t, p)[0].Err)
			}
			requireCohortDrained(t, store)
			after, ok := PermissionAdmissionRejectionFromError(out[0].Err)
			require.True(t, ok)
			require.Equal(t, snapshot, after, "later returns must not change a past rejection")
			after.OwnedCalls = -1
			again, _ := PermissionAdmissionRejectionFromError(out[0].Err)
			require.Equal(t, snapshot, again, "each reader owns an immutable value copy")
		})
	}

	t.Run("unobserved-errors-stay-absent", func(t *testing.T) {
		store := New(newBoundaryCluster(2), boundaryDB(t))
		t.Cleanup(store.CloseSendPermissionReads)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		oversized := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: strings.Repeat("z", 8<<20)}}
		errs := []error{nil, ErrPermissionBusy, context.DeadlineExceeded,
			store.ReadSendPermissionMetadataBatch(ctx, small)[0].Err,
			store.ReadSendPermissionMetadataBatch(context.Background(), oversized)[0].Err,
			store.ReadSendPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy}})[0].Err}
		for _, err := range errs {
			_, ok := PermissionAdmissionRejectionFromError(err)
			require.False(t, ok)
		}
	})
}

type rejectionSnapshotObserver struct{ busy atomic.Int64 }

func (o *rejectionSnapshotObserver) ObserveSendPermissionCount(kind string, n int) {
	if kind == "cohort_busy" {
		o.busy.Add(int64(n))
	}
}
func (*rejectionSnapshotObserver) ObserveSendPermissionStage(string, string, time.Duration) {}
func (*rejectionSnapshotObserver) ObserveSendPermissionInflight(int)                        {}
