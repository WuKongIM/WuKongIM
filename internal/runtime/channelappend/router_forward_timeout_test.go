package channelappend

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// A live TCP connection can outlast its leader. A forwarding attempt must leave
// time to refresh authority while retaining the original idempotency identity.
func TestRouterRefreshesUnresponsiveAuthority(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				old := routerTarget("paused", 2, 8)
				current := routerTarget("paused", 2, 7)
				resolver := &routerResolverForTest{targets: []AuthorityTarget{old, current}}
				remote := &routerRemoteForTest{waitContextDone: true}
				expected := make([]SendBatchItemResult, count)
				items := make([]SendBatchItem, count)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				for i := range items {
					items[i] = routerItem("sender", "paused", 2)
					items[i].Context = ctx
					items[i].Command.ClientMsgNo = string(rune('a' + i))
					expected[i].Result = SendResult{MessageID: uint64(i + 1), MessageSeq: uint64(i + 1), Reason: ReasonSuccess}
				}
				local := &routerLocalSubmitterForTest{results: expected}
				router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Local: local, Remote: remote})
				got := router.SendBatch(items)
				for i, r := range got {
					if r.Err != nil || r.Result != expected[i].Result {
						t.Fatalf("item %d = %+v, want committed result after route refresh", i, r)
					}
				}
				if ctx.Err() != nil || remote.calls != 1 || local.calls != 1 || len(resolver.invalidated) != 1 {
					t.Fatalf("caller=%v remote=%d local=%d invalidations=%d", ctx.Err(), remote.calls, local.calls, len(resolver.invalidated))
				}
				for i := range items {
					if local.items[i].Command.ClientMsgNo != items[i].Command.ClientMsgNo {
						t.Fatal("retry changed idempotency identity")
					}
				}
			})
		})
	}
}

func TestRouterForwardTimeoutDoesNotReplayUnkeyedOrTransientSend(t *testing.T) {
	for _, transient := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			old := routerTarget("paused", 2, 8)
			resolver := &routerResolverForTest{targets: []AuthorityTarget{old}}
			remote := &routerRemoteForTest{waitContextDone: true}
			item := routerItem("sender", "paused", 2)
			item.Command.ClientMsgNo = ""
			if transient {
				item.Command.NoPersist = true
				item.Command.SyncOnce = true
				item.Command.ClientMsgNo = "transient"
				resolver.targets[0] = routerTarget("paused____cmd", 2, 8)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			item.Context = ctx
			router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Remote: remote})
			got := router.SendBatch([]SendBatchItem{item})
			if len(got) != 1 || !errors.Is(got[0].Err, context.DeadlineExceeded) || ctx.Err() != nil || remote.calls != 1 || len(resolver.invalidated) != 1 {
				t.Fatalf("result=%+v caller=%v calls=%d invalidations=%d", got, ctx.Err(), remote.calls, len(resolver.invalidated))
			}
		})
	}
}

func TestRouterCallerDeadlineInvalidatesWithoutRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old := routerTarget("paused", 2, 8)
		resolver := &routerResolverForTest{targets: []AuthorityTarget{old}}
		remote := &routerRemoteForTest{waitContextDone: true}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		item := routerItem("sender", "paused", 2)
		item.Context = ctx
		router := NewRouter(RouterOptions{LocalNodeID: 7, Resolver: resolver, Remote: remote})
		got := router.SendBatch([]SendBatchItem{item})
		if !errors.Is(got[0].Err, context.DeadlineExceeded) || remote.calls != 1 || len(resolver.invalidated) != 1 {
			t.Fatalf("result=%+v calls=%d invalidations=%d", got, remote.calls, len(resolver.invalidated))
		}
	})
}
