//go:build integration

package presence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

type recoveryQueuedAdmissionContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *recoveryQueuedAdmissionContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestAuthorityRecoveryQueuedCancellationPreservesUnjoinedReadbackFailure(t *testing.T) {
	target := RouteTarget{HashSlot: 79, SlotID: 4, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 2}
	cold, proven := target, target
	cold.HashSlot++
	proven.HashSlot += 2
	directory := &recoveryProofPublishedDirectory{Directory: authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})}
	for _, tgt := range []RouteTarget{target, cold, proven} {
		directory.BecomeAuthority(tgt)
	}
	known := Route{UID: "known", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 42}
	if err := directory.InstallRecoveredRoutes(proven, []string{"known"}, []Route{known}); err != nil {
		t.Fatal(err)
	}
	directory.beforeRecoveryUIDs = func(got RouteTarget, uids []string) error {
		if got == target {
			return directory.InstallRecoveredRoutes(got, uids, nil)
		}
		return nil
	}
	readbackErr := fmt.Errorf("proof became unavailable: %w", authority.ErrRouteNotReady)
	reads := 0
	directory.readEndpoints = func(got RouteTarget, uids []string) ([]Route, error) {
		if got == target {
			reads++
			if reads == 2 {
				return nil, readbackErr
			}
		}
		return directory.Directory.EndpointsByUIDs(got, uids)
	}
	entered, release := make(chan struct{}, 16), make(chan struct{})
	var ownerCalls atomic.Int32
	owners := recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
		ownerCalls.Add(1)
		entered <- struct{}{}
		<-release
		return nil, nil
	})
	recovery := NewAuthorityRecovery(directory, owners)
	held := make(chan error, 16)
	t.Cleanup(func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := recovery.Close(ctx); err != nil {
			t.Errorf("recovery ownership did not join: %v", err)
		}
		for i := 0; i < 16; i++ {
			select {
			case <-held:
			case <-ctx.Done():
				t.Error("held caller did not join")
				return
			}
		}
		if got := ownerCalls.Load(); got != 16 {
			t.Errorf("canceled sibling executed owner work: calls=%d", got)
		}
	})
	for i := 0; i < 16; i++ {
		tgt := target
		tgt.HashSlot = uint16(i)
		directory.BecomeAuthority(tgt)
		go func() {
			_, err := recovery.Endpoints(context.Background(), tgt, []string{fmt.Sprintf("held-%d", tgt.HashSlot)})
			held <- err
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("owner capacity was not occupied")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queued := &recoveryQueuedAdmissionContext{Context: ctx, waiting: make(chan struct{})}
	done := make(chan []EndpointLookupResult, 1)
	go func() {
		done <- recovery.EndpointsByTargets(queued, []EndpointLookupGroup{
			{Target: target, UIDs: []string{"bob"}},
			{Target: cold, UIDs: []string{"cold"}},
			{Target: proven, UIDs: []string{"known"}},
		})
	}()
	select {
	case <-queued.waiting:
	case <-time.After(time.Second):
		t.Fatal("cold sibling did not wait for occupied capacity")
	}
	cancel()
	var results []EndpointLookupResult
	select {
	case results = <-done:
	case <-time.After(time.Second):
		t.Fatal("queued cancellation did not return")
	}
	if len(results) != 3 || !errors.Is(results[0].Err, readbackErr) || ActivationFailurePhase(results[0].Err) != ActivationPhaseRecoveryReadback {
		t.Fatalf("unjoined readback failure was replaced by sibling admission: %+v", results)
	}
	if !errors.Is(results[1].Err, context.Canceled) || ActivationFailurePhase(results[1].Err) != ActivationPhaseRecoveryAdmission {
		t.Fatalf("joined cold sibling lost original cancellation: %+v", results[1])
	}
	if results[2].Err != nil || !reflect.DeepEqual(results[2].Routes, []Route{known}) {
		t.Fatalf("proven sibling was changed by queued cancellation: %+v", results[2])
	}
}
