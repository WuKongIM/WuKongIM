package cluster

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

func TestMQTTMetadataNodeForegroundGates(t *testing.T) {
	ctx := context.Background()
	var absent *Node
	maintenance := &Node{}
	maintenance.started.Store(true)
	maintenance.maintenance.Store(true)
	for _, tc := range []struct {
		node *Node
		err  error
	}{{absent, ErrNotStarted}, {&Node{}, ErrNotStarted}, {maintenance, ErrMaintenance}} {
		_, err := tc.node.ReadMQTT(ctx, metadb.MQTTRead{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ReadMQTTRecovery(ctx, 0, metadb.MQTTRead{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTSession(ctx, 0, metadb.MQTTSession{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ApplyMQTTLifecycle(ctx, metadb.MQTTLifecycleMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.BuildMQTTReclamationIndex(ctx, 0)
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ReclaimMQTTSession(ctx, metadb.MQTTSessionReclamation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.MutateMQTTSubscription(ctx, metadb.MQTTSubscriptionMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.MutateMQTTDeliveryCursor(ctx, metadb.MQTTDeliveryCursorMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.MutateMQTTWindow(ctx, metadb.MQTTWindowMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTSourceBinding(ctx, 0, metadb.MQTTSourceBinding{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTInboxAdmission(ctx, 0, metadb.MQTTInboxAdmission{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTWill(ctx, 0, metadb.MQTTWill{})
		require.ErrorIs(t, err, tc.err)
	}
}

func TestMQTTMetadataNodeRereadsAfterOneStaleRoute(t *testing.T) {
	unavailable := errors.New("test read unavailable")
	for _, tc := range []struct {
		name          string
		advanceAlways bool
		cancel        bool
		barrierError  error
		wantError     error
		wantBarriers  int
	}{
		{name: "one route publication", wantBarriers: 2},
		{name: "route keeps changing", advanceAlways: true, wantError: slotproxy.ErrReadStaleRoute, wantBarriers: 2},
		{name: "caller cancels", cancel: true, wantError: context.Canceled, wantBarriers: 1},
		{name: "other read failure", barrierError: unavailable, wantError: unavailable, wantBarriers: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, db := newLocalMetadataScanNode(t)
			uid := keyForNodeHashSlot(t, node.controlSnapshot.HashSlots.Count, 0)
			key := metadb.ChannelKey{ChannelID: "mailbox-channel", ChannelType: 1}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, db.ForHashSlot(0).UpsertUserChannelMembership(ctx, metadb.UserChannelMembership{
				UID: uid, ChannelID: key.ChannelID, ChannelType: key.ChannelType,
			}))
			route := &mqttReadBarrierPort{Node: node, revision: node.HashSlotTableVersion()}
			route.barrier = func() error {
				if route.calls == 1 || tc.advanceAlways {
					route.revision++
				}
				if tc.cancel {
					cancel()
				}
				return tc.barrierError
			}
			node.defaultSlotProxy = slotproxy.NewChannelMetadataStore(route, db)
			got, err := node.ReadMQTT(ctx, metadb.MQTTRead{
				Kind: metadb.MQTTReadInboxDirectory, Owner: metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingUID, ID: uid}, Limit: 2,
			})
			if tc.wantError == nil {
				require.NoError(t, err)
				require.Equal(t, []metadb.ChannelKey{key}, got.Directory)
				require.True(t, got.Done)
			} else {
				require.ErrorIs(t, err, tc.wantError)
				require.Zero(t, got)
			}
			require.Equal(t, tc.wantBarriers, route.calls)
		})
	}
}

type mqttReadBarrierPort struct {
	*Node
	revision uint64
	calls    int
	barrier  func() error
}

func (p *mqttReadBarrierPort) HashSlotTableVersion() uint64 { return p.revision }

func (p *mqttReadBarrierPort) ReadSlotBarrier(context.Context, multiraft.SlotID) error {
	p.calls++
	return p.barrier()
}
