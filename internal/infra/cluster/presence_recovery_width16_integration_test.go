//go:build integration

package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// Independent recovery rounds share one node-wide admission bound. A canceled
// waiter must not read an owner or retain a permit after the active rounds exit.
func TestPresenceRecoveryOwnerAdmissionAllowsSixteenRoundsAndCancelsSeventeenth(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	node := &recoveryCluster{fakePresenceCluster: &fakePresenceCluster{nodeID: 1, route: cluster.Route{HashSlot: 101, SlotID: 5, Leader: 1, LeaderTerm: 4, ConfigEpoch: 1}}}
	node.snapshot.Nodes = []control.Node{{NodeID: 1, JoinState: control.NodeJoinStateActive}}
	activeCtx, cancelActive := context.WithCancel(context.Background())
	entered := make(chan string, 16)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var waitingReads atomic.Int32
	reader := recoveryReaderFunc(func(_ context.Context, got presence.RouteTarget, uids []string) (presence.OwnerRouteSnapshot, error) {
		if got != target || len(uids) != 1 {
			return presence.OwnerRouteSnapshot{}, errors.New("unexpected recovery target or UID page")
		}
		snapshot := presence.OwnerRouteSnapshot{OwnerNodeID: 1, OwnerBootID: 11}
		switch uids[0] {
		case "waiting-round":
			waitingReads.Add(1)
			return snapshot, nil
		case "after-cancellation":
			return snapshot, nil
		default:
			entered <- uids[0]
			// Keep each admitted invocation held until this test releases it.
			// The adapter's 250ms child deadline cannot free admission while
			// an owner reader has not returned; cancellation still joins below.
			select {
			case <-release:
			case <-activeCtx.Done():
			}
			if err := activeCtx.Err(); err != nil {
				return presence.OwnerRouteSnapshot{}, err
			}
			return snapshot, nil
		}
	})
	source := NewPresenceRecoveryOwners(node, reader)
	completed := make(chan error, 16)
	var rounds sync.WaitGroup
	for i := range 16 {
		rounds.Add(1)
		go func(uid string) {
			defer rounds.Done()
			_, err := source.RecoverRoutes(activeCtx, target, []string{uid})
			completed <- err
		}(fmt.Sprintf("active-round-%02d", i))
	}
	joined := make(chan struct{})
	go func() {
		rounds.Wait()
		close(joined)
	}()
	defer func() {
		cancelActive()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Error("active recovery rounds did not join after cancellation and release")
		}
	}()

	admissionDeadline := time.NewTimer(2 * time.Second)
	defer admissionDeadline.Stop()
	seen := make(map[string]struct{}, 16)
	for len(seen) < 16 {
		select {
		case uid := <-entered:
			if _, duplicate := seen[uid]; duplicate {
				t.Fatalf("owner read entered twice for independent round %q", uid)
			}
			seen[uid] = struct{}{}
		case err := <-completed:
			t.Fatalf("an active recovery round exited before release: %v", err)
		case <-admissionDeadline.C:
			t.Fatalf("only %d independent recovery rounds entered owner reads, want 16", len(seen))
		}
	}

	// All sixteen reads remain held. The next public call must wait for
	// admission and return its own cancellation without invoking the reader.
	waitingCtx, cancelWaiting := context.WithTimeout(context.Background(), 100*time.Millisecond)
	waitingDone := make(chan error, 1)
	waitingJoined := make(chan struct{})
	go func() {
		defer close(waitingJoined)
		_, err := source.RecoverRoutes(waitingCtx, target, []string{"waiting-round"})
		waitingDone <- err
	}()
	defer func() {
		cancelWaiting()
		select {
		case <-waitingJoined:
		case <-time.After(2 * time.Second):
			t.Error("canceled seventeenth recovery round did not join")
		}
	}()
	select {
	case err := <-waitingDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("seventeenth recovery round = %v, want its admission deadline", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("seventeenth recovery round did not return after cancellation")
	}
	if got := waitingReads.Load(); got != 0 {
		t.Fatalf("canceled seventeenth recovery round read an owner %d times", got)
	}

	cancelActive()
	releaseOnce.Do(func() { close(release) })
	for range 16 {
		select {
		case err := <-completed:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("released active recovery round = %v, want parent cancellation", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("active recovery round did not complete after cancellation and release")
		}
	}
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("completed recovery rounds did not join")
	}

	controlCtx, cancelControl := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelControl()
	routes, err := source.RecoverRoutes(controlCtx, target, []string{"after-cancellation"})
	if err != nil || len(routes) != 0 {
		t.Fatalf("recovery after canceled rounds = routes:%+v err:%v, want complete empty proof", routes, err)
	}
}
