//go:build integration

package presence

import (
	"context"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"testing"
	"time"
)

func TestAuthorityRecoveryProvenLookupDoesNotWaitForColdOwner(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}
	directory.BecomeAuthority(target)
	row := Route{UID: "hot", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 3}
	if err := directory.InstallRecoveredRoutes(target, []string{"hot"}, []Route{row}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	recovery := NewAuthorityRecovery(directory, recoveryOwnersFunc(func(ctx context.Context, _ RouteTarget, uids []string) ([]Route, error) {
		if len(uids) != 1 || uids[0] != "cold" {
			t.Error("hot proof queried owners")
			return nil, authority.ErrRouteNotReady
		}
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	coldCtx, coldCancel := context.WithTimeout(context.Background(), time.Second)
	coldDone := make(chan struct{})
	go func() { defer close(coldDone); _, _ = recovery.Endpoints(coldCtx, target, []string{"cold"}) }()
	t.Cleanup(func() {
		coldCancel()
		select {
		case <-coldDone:
		case <-time.After(time.Second):
			t.Error("cold lookup cleanup did not join")
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cold owner did not enter")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := recovery.Endpoints(ctx, target, []string{"hot"})
	if err != nil || len(rows) != 1 || rows[0] != row {
		t.Fatalf("hot lookup blocked by cold owner: rows=%+v err=%v", rows, err)
	}
}
