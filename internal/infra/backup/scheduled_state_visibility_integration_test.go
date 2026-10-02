//go:build integration

package backup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// A forwarded CAS may complete before this follower publishes its local mirror.
// Returning its pre-CAS state can make restore cleanup overlook its own lease.
func TestScheduledStateReadObservesCompletedForwardedWrite(t *testing.T) {
	runtime := &laggedBackupController{fakeScheduledBackupController: fakeScheduledBackupController{state: controller.ClusterState{Revision: 7}}}
	store, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	next := scheduledSystemState()
	if err := store.CompareAndSwap(context.Background(), 7, next); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision < 8 || got.ActiveArchiveOperation == nil {
		t.Fatalf("completed CAS was hidden by follower lag: revision=%d operation=%v", got.Revision, got.ActiveArchiveOperation)
	}
}

func TestScheduledStateLagWaitHonorsCancellation(t *testing.T) {
	runtime := &laggedBackupController{fakeScheduledBackupController: fakeScheduledBackupController{state: controller.ClusterState{Revision: 7}}, forever: true}
	store, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwap(context.Background(), 7, scheduledSystemState()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = store.Load(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lag wait error = %v", err)
	}
}

func TestScheduledStateNoopDoesNotWaitForNonexistentRevision(t *testing.T) {
	runtime := &laggedBackupController{fakeScheduledBackupController: fakeScheduledBackupController{state: controller.ClusterState{Revision: 7}}, forever: true, noop: true}
	store, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	next := scheduledSystemState()
	next.Revision = 7
	if err := store.CompareAndSwap(context.Background(), 7, next); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := store.Load(ctx); err != nil {
		t.Fatalf("no-op load: %v", err)
	}
}

type laggedBackupController struct {
	fakeScheduledBackupController
	reads   int
	forever bool
	noop    bool
}

func (c *laggedBackupController) ReplaceScheduledBackupState(ctx context.Context, expected uint64, next controller.ScheduledBackupState) error {
	if c.noop {
		return nil
	}
	return c.fakeScheduledBackupController.ReplaceScheduledBackupState(ctx, expected, next)
}

func (c *laggedBackupController) LocalState(context.Context) (controller.ClusterState, error) {
	c.reads++
	if c.reads < 3 || c.forever {
		return controller.ClusterState{Revision: 7}, nil
	}
	return c.state.Clone(), nil
}
