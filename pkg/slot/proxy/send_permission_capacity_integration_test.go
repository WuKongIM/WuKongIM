//go:build integration

package proxy

import (
	"context"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases: independent local or remote callers queue behind slow fresh
// barriers until their SEND deadline expires; capacity must not reuse old facts,
// admit a late caller to an already-started barrier, or leak admission.
func TestSendPermissionBurstWithSlowFreshBarriers(t *testing.T) {
	for _, remote := range []bool{false, true} {
		name := "local"
		if remote {
			name = "remote"
		}
		t.Run(name, func(t *testing.T) {
			db := boundaryDB(t)
			wb := db.NewWriteBatch()
			_, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
			require.NoError(t, err)
			require.NoError(t, wb.Commit())
			require.NoError(t, wb.Close())
			owner := newBoundaryCluster(2)
			owner.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
				owner.barriers.Add(1)
				timer := time.NewTimer(100 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			serving := New(owner, db)
			caller := serving
			if remote {
				caller = New(&sendPermissionTestCluster{node: 1, remote: serving}, db)
			}
			const burst = 64
			start := make(chan struct{})
			results := make([]PermissionMetadataReadResult, burst)
			var wg sync.WaitGroup
			for i := range results {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
					defer cancel()
					results[i] = caller.ReadSendPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}})[0]
				}(i)
			}
			close(start)
			wg.Wait()
			failed := 0
			for _, result := range results {
				if result.Err != nil {
					failed++
					continue
				}
				require.True(t, result.Found)
				require.EqualValues(t, 1, result.UserPolicy.SendBan)
			}
			require.Zero(t, serving.permissionInflight.Load())
			require.Zero(t, serving.permissionWaiting.Load())
			require.Zero(t, owner.admitted.Load())
			require.Zero(t, failed, "slow fresh barriers must not exhaust the independent burst's caller budgets")
			require.Positive(t, owner.barriers.Load())
			require.Less(t, owner.barriers.Load(), int64(burst), "independent callers must share only pre-sealed fresh barriers")
		})
	}
}
