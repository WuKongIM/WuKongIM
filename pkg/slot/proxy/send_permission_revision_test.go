package proxy

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure inventory: unrelated Controller revisions must not turn unchanged
// Slot authority into a stale read. Fresh barriers and receiver-owned facts
// remain required. Every hard authority field and hash mapping must still be
// checked both before and after the read; revision tolerance cannot grant one
// successful stale result or increase the existing bounded retry count.
func TestSendPermissionRevisionOnlyUsesFreshAuthority(t *testing.T) {
	for _, tc := range []struct {
		name             string
		originRevision   uint64
		remoteRevision   uint64
		advanceAtBarrier bool
	}{
		{name: "origin-behind", originRevision: 10, remoteRevision: 11},
		{name: "origin-ahead", originRevision: 11, remoteRevision: 10},
		{name: "advance-at-barrier", originRevision: 10, remoteRevision: 10, advanceAtBarrier: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPermissionRevisionFixture(t)
			f.origin.route.Fence.RouteRevision = tc.originRevision
			f.serving.route.Fence.RouteRevision = tc.remoteRevision
			f.serving.barrier = func() error {
				if err := setPermissionRevisionPolicy(f.servingDB, 0); err != nil {
					return err
				}
				if tc.advanceAtBarrier {
					f.serving.route.Fence.RouteRevision++
				}
				return nil
			}
			out := f.read()
			require.Len(t, out, 1)
			require.NoError(t, out[0].Err)
			require.True(t, out[0].Found)
			require.EqualValues(t, 0, out[0].UserPolicy.SendBan)
			require.EqualValues(t, 2, out[0].UserPolicy.Version)
			require.EqualValues(t, 1, f.origin.calls.Load(), "revision-only skew must not need the stale retry")
			require.EqualValues(t, 1, f.serving.barriers.Load(), "a fresh barrier remains mandatory")
			require.Zero(t, f.serving.admitted.Load())
		})
	}
}

func TestSendPermissionHardAuthorityChangesStayFenced(t *testing.T) {
	changes := []struct {
		name string
		edit func(*SendPermissionRoute)
	}{
		{name: "hash-slot", edit: func(r *SendPermissionRoute) { r.HashSlot++ }},
		{name: "physical-slot", edit: func(r *SendPermissionRoute) { r.Fence.SlotID++ }},
		{name: "leader-node", edit: func(r *SendPermissionRoute) { r.Fence.LeaderNodeID++ }},
		{name: "leader-term", edit: func(r *SendPermissionRoute) { r.Fence.LeaderTerm++ }},
		{name: "config-epoch", edit: func(r *SendPermissionRoute) { r.Fence.ConfigEpoch++ }},
	}
	for _, change := range changes {
		for _, afterBarrier := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after-barrier=%t", change.name, afterBarrier), func(t *testing.T) {
				f := newPermissionRevisionFixture(t)
				if afterBarrier {
					f.serving.barrier = func() error {
						change.edit(&f.serving.route)
						return nil
					}
				} else {
					change.edit(&f.serving.route)
				}
				out := f.read()
				require.Len(t, out, 1)
				require.ErrorIs(t, out[0].Err, ErrReadStaleRoute)
				require.False(t, out[0].Found)
				require.EqualValues(t, 2, f.origin.calls.Load(), "only the existing stale-group retry is allowed")
				wantBarriers := int64(0)
				if afterBarrier {
					wantBarriers = 1
				}
				require.Equal(t, wantBarriers, f.serving.barriers.Load())
				require.Zero(t, f.serving.admitted.Load())
			})
		}
	}
}

func TestSendPermissionLocalAuthorityRejectsMappingABA(t *testing.T) {
	for _, everyAttempt := range []bool{false, true} {
		t.Run(fmt.Sprintf("every-attempt=%t", everyAttempt), func(t *testing.T) {
			f := newPermissionRevisionFixture(t)
			f.serving.barrier = func() error {
				if everyAttempt || f.serving.barriers.Load() == 1 {
					// The last visible hard fence is unchanged, but two real local
					// topology publications occurred inside this admitted read.
					f.serving.route.Fence.SlotID = 2
					f.serving.route.LocalAuthorityEpoch++
					f.serving.route.Fence.RouteRevision++
					f.serving.route.Fence.SlotID = 1
					f.serving.route.LocalAuthorityEpoch++
					f.serving.route.Fence.RouteRevision++
					// Model ordinary caller catch-up before its already-allowed
					// retry, not a new application retry or a reused read proof.
					f.origin.route.Fence.RouteRevision = f.serving.route.Fence.RouteRevision
					return nil
				}
				return setPermissionRevisionPolicy(f.servingDB, 0)
			}
			out := f.read()
			require.Len(t, out, 1)
			require.EqualValues(t, 2, f.origin.calls.Load())
			require.EqualValues(t, 2, f.serving.barriers.Load(), "ABA invalidates the first barrier; only a new attempt can serve")
			if everyAttempt {
				require.ErrorIs(t, out[0].Err, ErrReadStaleRoute)
				require.False(t, out[0].Found)
			} else {
				require.NoError(t, out[0].Err)
				require.True(t, out[0].Found)
				require.EqualValues(t, 0, out[0].UserPolicy.SendBan)
				require.EqualValues(t, 2, out[0].UserPolicy.Version)
			}
			require.Zero(t, f.serving.admitted.Load())
		})
	}
}

func TestSendPermissionLocalAuthorityUnknownFailsClosed(t *testing.T) {
	for _, afterBarrier := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-barrier=%t", afterBarrier), func(t *testing.T) {
			f := newPermissionRevisionFixture(t)
			if afterBarrier {
				f.serving.barrier = func() error {
					f.serving.route.LocalAuthorityEpoch = 0
					return nil
				}
			} else {
				f.serving.route.LocalAuthorityEpoch = 0
			}
			out := f.read()
			require.Len(t, out, 1)
			require.ErrorIs(t, out[0].Err, ErrReadStaleRoute)
			require.False(t, out[0].Found)
			require.EqualValues(t, 2, f.origin.calls.Load())
			wantBarriers := int64(0)
			if afterBarrier {
				wantBarriers = 1
			}
			require.Equal(t, wantBarriers, f.serving.barriers.Load())
			require.Zero(t, f.serving.admitted.Load())
		})
	}
}

// permissionRevisionCluster varies only public cluster ports. Handler discovery
// uses Store.RegisterRPCHandlers, so the test exercises the real request/reply
// codec and public batch entrypoint without reaching a private RPC handler.
type permissionRevisionCluster struct {
	*sendPermissionTestCluster
	route   SendPermissionRoute
	handler func(context.Context, []byte) ([]byte, error)
	barrier func() error
	busy    atomic.Bool
}

func (c *permissionRevisionCluster) SendPermissionRoutes(keys []string) []SendPermissionRoute {
	out := make([]SendPermissionRoute, len(keys))
	for i := range out {
		out[i] = c.route
	}
	return out
}

func (c *permissionRevisionCluster) RPCService(ctx context.Context, node multiraft.NodeID, slot multiraft.SlotID, service uint8, raw []byte) ([]byte, error) {
	c.calls.Add(1)
	if node != 2 || service != clusternet.RPCNodeSendPermissions || c.handler == nil {
		return nil, fmt.Errorf("unexpected permission RPC node=%d slot=%d service=%d", node, slot, service)
	}
	return c.handler(ctx, raw)
}

func (c *permissionRevisionCluster) ReadSlotBarrier(ctx context.Context, slot multiraft.SlotID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if slot != 1 || c.admitted.Load() != 1 || !c.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("invalid barrier ownership for slot %d", slot)
	}
	defer c.busy.Store(false)
	c.barriers.Add(1)
	if c.barrier != nil {
		return c.barrier()
	}
	return nil
}

type permissionRevisionFixture struct {
	origin, serving *permissionRevisionCluster
	servingDB       *metadb.DB
	store           *Store
}

func newPermissionRevisionFixture(t *testing.T) *permissionRevisionFixture {
	t.Helper()
	openDB := func() *metadb.DB {
		db, err := metadb.Open(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		require.NoError(t, setPermissionRevisionPolicy(db, 1))
		return db
	}
	originDB, servingDB := openDB(), openDB()
	route := SendPermissionRoute{HashSlot: 1, LocalAuthorityEpoch: 41, Fence: SendPermissionFence{SlotID: 1, LeaderNodeID: 2, LeaderTerm: 3, ConfigEpoch: 4, RouteRevision: 10}}
	serving := &permissionRevisionCluster{sendPermissionTestCluster: &sendPermissionTestCluster{node: 2}, route: route}
	remote := New(serving, servingDB)
	t.Cleanup(remote.CloseSendPermissionReads)
	origin := &permissionRevisionCluster{sendPermissionTestCluster: &sendPermissionTestCluster{node: 1}, route: route}
	origin.route.LocalAuthorityEpoch = 7 // Local observations are never comparable across nodes.
	remote.RegisterRPCHandlers(func(service uint8, handler func(context.Context, []byte) ([]byte, error)) {
		if service == clusternet.RPCNodeSendPermissions {
			origin.handler = handler
		}
	})
	require.NotNil(t, origin.handler)
	store := New(origin, originDB)
	t.Cleanup(store.CloseSendPermissionReads)
	return &permissionRevisionFixture{origin: origin, serving: serving, servingDB: servingDB, store: store}
}

func (f *permissionRevisionFixture) read() []PermissionMetadataReadResult {
	return f.store.ReadSendPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}})
}

func setPermissionRevisionPolicy(db *metadb.DB, value int64) error {
	wb := db.NewWriteBatch()
	defer wb.Close()
	if _, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: value}); err != nil {
		return err
	}
	return wb.Commit()
}
