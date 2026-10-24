//go:build integration

package cluster

import (
	"context"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestRecoveryCapacityCensusIncludesAccumulatorWhileNextOwnerWaits(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "complete"
		if malformed {
			name = "malformed"
		}
		t.Run(name, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			local := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				return recoveryCallsPage(1, 11, c, start, start+1, 1), nil
			}}
			remote := recoveryCallReader{page: func(ctx context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return presence.OwnerRouteCallPage{}, ctx.Err()
				}
				page := recoveryCallsPage(2, 22, c, start, len(c), 1)
				if malformed {
					page.Results[0][0].Snapshot.Routes[0].UID = "foreign"
				}
				return page, nil
			}}
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: remote})}}
			var mu sync.Mutex
			var latest presence.RecoveryResultUsage
			var pageHighWater uint64
			observe := func(u presence.RecoveryResultUsage) {
				mu.Lock()
				latest = u
				pageHighWater = max(pageHighWater, u.PageRows)
				mu.Unlock()
			}
			out := make(chan [][]presence.EndpointLookupResult, 1)
			go func() {
				out <- NewPresenceRecoveryOwners(node, local).RecoverRouteCallsObserved(context.Background(), calls, observe)
			}()
			defer func() {
				unblock()
				select {
				case <-out:
				case <-time.After(time.Second):
					t.Error("owner census worker not joined")
				}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("next owner was not reached")
			}
			mu.Lock()
			paused, pages := latest, pageHighWater
			mu.Unlock()
			if paused.ResultRows != 2 || paused.ResultDecodedBytes == 0 || paused.PageRows != 0 || paused.PageDecodedBytes != 0 || pages != 1 {
				t.Fatalf("partial accumulator missing while next owner waits: %+v page_high_water=%d", paused, pages)
			}
			unblock()
			var got [][]presence.EndpointLookupResult
			select {
			case got = <-out:
			case <-time.After(time.Second):
				t.Fatal("owner read did not complete")
			}
			// Return the join receipt for the failure-safe deferred cleanup.
			out <- got
			mu.Lock()
			final := latest
			mu.Unlock()
			if malformed {
				requireRecoveryCallErrors(t, got)
				if final.ResultRows != 0 || final.PageRows != 0 {
					t.Fatalf("malformed round retained proof census: %+v", final)
				}
			} else if final.ResultRows != 4 || final.PageRows != 0 || len(got[0][0].Routes) != 2 {
				t.Fatalf("final ownership=%+v", final)
			}
		})
	}
}
