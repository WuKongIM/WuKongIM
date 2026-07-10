package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestChannelDataPlaneLeaseGuardAllowsFreshAppendAdmission(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, 30*time.Second)
	guard.MarkVisible(now.Add(-10 * time.Second))

	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); err != nil {
		t.Fatalf("AllowChannelAppend() error = %v, want nil for fresh lease", err)
	}
}

func TestChannelDataPlaneLeaseGuardRejectsMissingOrExpiredLease(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, 30*time.Second)

	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("AllowChannelAppend() missing lease error = %v, want ErrNotReady", err)
	}

	guard.MarkVisible(now.Add(-31 * time.Second))
	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("AllowChannelAppend() expired lease error = %v, want ErrNotReady", err)
	}
}

func TestChannelDataPlaneLeaseGuardReportsStableFailureReasons(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	tests := []struct {
		name       string
		mark       *time.Time
		wantReason channelDataPlaneLeaseFailureReason
	}{
		{
			name:       "missing",
			wantReason: channelDataPlaneLeaseReasonMissing,
		},
		{
			name:       "expired",
			mark:       timePtr(now.Add(-31 * time.Second)),
			wantReason: channelDataPlaneLeaseReasonExpired,
		},
		{
			name:       "clock invalid",
			mark:       timePtr(now.Add(time.Second)),
			wantReason: channelDataPlaneLeaseReasonClockInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, 30*time.Second)
			if test.mark != nil {
				guard.MarkVisible(*test.mark)
			}

			err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{})
			if !errors.Is(err, ch.ErrNotReady) {
				t.Fatalf("AllowChannelAppend() error = %v, want ErrNotReady", err)
			}
			var leaseErr *channelDataPlaneLeaseError
			if !errors.As(err, &leaseErr) {
				t.Fatalf("AllowChannelAppend() error = %T, want *channelDataPlaneLeaseError", err)
			}
			if leaseErr.reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", leaseErr.reason, test.wantReason)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestChannelDataPlaneLeaseGuardHonorsContextCancellation(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, 30*time.Second)
	guard.MarkVisible(now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := guard.AllowChannelAppend(ctx, ch.AppendAdmissionRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AllowChannelAppend() canceled error = %v, want context.Canceled", err)
	}
}

func TestChannelDataPlaneLeaseGuardKeepsNewestConcurrentVisibilityMark(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	olderVisible := now.Add(-31 * time.Second)
	newerVisible := now.Add(-time.Second)
	guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, 30*time.Second)
	releaseOlder := make(chan struct{})
	olderDone := make(chan struct{})

	go func() {
		defer close(olderDone)
		<-releaseOlder
		guard.MarkVisible(olderVisible)
	}()

	guard.MarkVisible(newerVisible)
	close(releaseOlder)
	<-olderDone

	snapshot := guard.snapshot()
	if !snapshot.lastVisibleAt.Equal(newerVisible) {
		t.Fatalf("lastVisibleAt = %s, want newest concurrent evidence %s", snapshot.lastVisibleAt, newerVisible)
	}
	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); err != nil {
		t.Fatalf("AllowChannelAppend() error = %v, want nil for newest concurrent evidence", err)
	}
}

func TestChannelDataPlaneLeaseGuardFailsClosedAndRecoversAfterClockRollback(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	guard := newChannelDataPlaneLeaseGuard(func() time.Time { return now }, 30*time.Second)

	guard.MarkVisible(now.Add(100 * time.Second))
	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("AllowChannelAppend() future evidence error = %v, want ErrNotReady", err)
	}

	guard.MarkVisible(now)
	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); err != nil {
		t.Fatalf("AllowChannelAppend() post-rollback evidence error = %v, want nil", err)
	}

	now = now.Add(31 * time.Second)
	if err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{}); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("AllowChannelAppend() expired post-rollback evidence error = %v, want ErrNotReady", err)
	}
}

func TestChannelDataPlaneLeaseSnapshotUsesOneObservedLease(t *testing.T) {
	oldVisible := time.Unix(100, 0).UTC()
	freshVisible := oldVisible.Add(10 * time.Second)
	var guard *channelDataPlaneLeaseGuard
	guard = newChannelDataPlaneLeaseGuard(func() time.Time {
		guard.MarkVisible(freshVisible)
		return freshVisible
	}, 3*time.Second)
	guard.MarkVisible(oldVisible)

	snapshot := guard.snapshot()

	if !snapshot.lastVisibleAt.Equal(oldVisible) {
		t.Fatalf("lastVisibleAt = %s, want originally observed %s", snapshot.lastVisibleAt, oldVisible)
	}
	if snapshot.ready {
		t.Fatal("ready = true, want readiness computed from the same observed lease timestamp")
	}
}
