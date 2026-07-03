package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
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
