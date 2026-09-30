//go:build integration

package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases before the idle-path repair: cancellation/Close must retain
// ownership until the real reader joins, and a later caller needs fresh facts.
// The process-level sequential timeline test covers the actual delay symptom.
func TestSendPermissionIdleOwnership(t *testing.T) {
	for _, closeStore := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[closeStore], func(t *testing.T) {
			db := boundaryDB(t)
			cluster := newBoundaryCluster(2)
			entered := make(chan struct{}, 1)
			canceled := make(chan struct{}, 1)
			gate := boundaryGate(t)
			cluster.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
				entered <- struct{}{}
				<-ctx.Done()
				canceled <- struct{}{}
				<-gate // delayed dependency shutdown must remain owned
				return ctx.Err()
			}
			store := New(cluster, db)
			t.Cleanup(store.CloseSendPermissionReads)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
			result := launchCohortRead(store, ctx, reads)
			awaitBoundaryEvent(t, entered)
			closed := make(chan struct{}, 1)
			if closeStore {
				go func() { store.CloseSendPermissionReads(); closed <- struct{}{} }()
			} else {
				cancel()
			}
			awaitBoundaryEvent(t, canceled)
			store.permissionCohortMu.Lock()
			calls, bytes, cohorts := store.permissionCohortRequests, store.permissionCohortBytes, len(store.permissionCohorts)
			store.permissionCohortMu.Unlock()
			require.Equal(t, 1, calls)
			require.Greater(t, bytes, 1024)
			require.Equal(t, 1, cohorts)
			select {
			case <-result:
				t.Fatal("cancellation returned before the owned dependency joined")
			case <-closed:
				t.Fatal("Close returned before the owned dependency joined")
			default:
			}
			closeBoundaryGate(gate)
			out := awaitBoundaryResult(t, result)
			require.Error(t, out[0].Err)
			if !closeStore {
				require.ErrorIs(t, out[0].Err, context.Canceled)
			}
			if closeStore {
				awaitBoundaryEvent(t, closed)
				require.ErrorIs(t, store.ReadSendPermissionMetadataBatch(context.Background(), reads)[0].Err, context.Canceled)
			}
			requireCohortDrained(t, store)
		})
	}
}

func TestSendPermissionIdleLaterReadIsFresh(t *testing.T) {
	db := boundaryDB(t)
	cluster := newBoundaryCluster(2)
	gate := boundaryGate(t)
	entered := make(chan struct{}, 2)
	cluster.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
		n := cluster.barriers.Add(1)
		entered <- struct{}{}
		if n == 1 {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	store := New(cluster, db)
	t.Cleanup(store.CloseSendPermissionReads)
	reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
	first := startBoundaryRead(t, store, reads)
	awaitBoundaryEvent(t, entered)
	wb := db.NewWriteBatch()
	_, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
	require.NoError(t, err)
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	later := startBoundaryRead(t, store, reads)
	awaitBoundaryEvent(t, entered)
	out := awaitBoundaryResult(t, later)
	require.NoError(t, out[0].Err)
	require.EqualValues(t, 1, out[0].UserPolicy.SendBan)
	require.EqualValues(t, 2, cluster.barriers.Load())
	closeBoundaryGate(gate)
	require.NoError(t, awaitBoundaryResult(t, first)[0].Err)
	requireCohortDrained(t, store)
}
