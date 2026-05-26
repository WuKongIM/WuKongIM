package proxy

import (
	"context"
	"errors"
	"fmt"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestPluginBindingRPCServiceIDDoesNotCollideWithSharedRPCServices(t *testing.T) {
	occupied := map[uint8]string{
		3:  "slot-runtime-meta",
		4:  "slot-identity",
		5:  "node-presence",
		6:  "node-delivery-submit",
		7:  "node-delivery-push",
		8:  "node-delivery-ack",
		9:  "node-delivery-offline",
		10: "slot-subscriber",
		11: "slot-user-conversation-state",
		12: "slot-channel",
		13: "node-conversation-facts",
		30: "channel-fetch",
		33: "node-channel-append",
		34: "channel-reconcile-probe",
		35: "channel-long-poll-fetch",
		36: "node-channel-messages",
		37: "node-channel-leader-repair",
		38: "node-channel-leader-evaluate",
		39: "node-runtime-summary",
		40: "node-connections",
		41: "node-connection",
		42: "node-diagnostics",
		43: "node-channel-retention",
		44: "node-delivery-tag",
		45: "node-system-uid-cache",
		46: "node-channel-leader-transfer",
		47: "slot-channel-migration",
		48: "channel-fence-and-drain",
		49: "slot-cmd-conversation-state",
		50: "node-cmd-sync",
		51: "node-diagnostics-tracking",
		52: "node-monitor-metrics",
	}
	if name, exists := occupied[pluginBindingRPCServiceID]; exists {
		t.Fatalf("pluginBindingRPCServiceID = %d collides with %s", pluginBindingRPCServiceID, name)
	}
}

func TestPluginBindingProxyRPCServiceIDsAreUnique(t *testing.T) {
	ids := map[uint8]string{}
	for name, id := range map[string]uint8{
		"runtime_meta":            runtimeMetaRPCServiceID,
		"identity":                identityRPCServiceID,
		"subscriber":              subscriberRPCServiceID,
		"channel":                 channelRPCServiceID,
		"user_conversation_state": userConversationStateRPCServiceID,
		"channel_migration":       channelMigrationRPCServiceID,
		"cmd_conversation_state":  cmdConversationStateRPCServiceID,
		"plugin_binding":          pluginBindingRPCServiceID,
	} {
		if existing, ok := ids[id]; ok {
			t.Fatalf("proxy rpc service id %d is used by both %s and %s", id, existing, name)
		}
		ids[id] = name
	}
}

func TestPluginBindingStoreRoutesBindUnbindAndUIDQueriesToUIDOwner(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	uid := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "plugin-bind")
	hashSlot := mustHashSlotForKey(t, nodes[0].cluster, uid)

	require.NoError(t, nodes[0].store.BindPluginUser(ctx, uid, "bot-a"))

	got, err := nodes[0].store.ListPluginBindingsByUID(ctx, uid)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, metadb.PluginUserBinding{UID: uid, PluginNo: "bot-a"}, pluginBindingWithoutTimes(got[0]))
	require.Positive(t, got[0].CreatedAtMS)
	require.GreaterOrEqual(t, got[0].UpdatedAtMS, got[0].CreatedAtMS)

	exists, err := nodes[0].store.ExistPluginBindingByUID(ctx, uid)
	require.NoError(t, err)
	require.True(t, exists)

	remoteBindings, err := nodes[1].db.ForHashSlot(hashSlot).ListPluginBindingsByUID(ctx, uid)
	require.NoError(t, err)
	require.Len(t, remoteBindings, 1)
	localBindings, err := nodes[0].db.ForHashSlot(hashSlot).ListPluginBindingsByUID(ctx, uid)
	require.NoError(t, err)
	require.Empty(t, localBindings)

	require.NoError(t, nodes[0].store.UnbindPluginUser(ctx, uid, "bot-a"))
	exists, err = nodes[0].store.ExistPluginBindingByUID(ctx, uid)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestPluginBindingStoreUsesUIDHashSlotForProposal(t *testing.T) {
	ctx := context.Background()
	const uid = "u-proposal"
	hashSlot := raftcluster.HashSlotForKey(uid, 8)
	cluster := &proxyPluginBindingTestCluster{
		slotForKeyFunc:     func(key string) multiraft.SlotID { return 1 },
		hashSlotForKeyFunc: func(key string) uint16 { return raftcluster.HashSlotForKey(key, 8) },
		leaders:            map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:              map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID:        1,
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}

	require.NoError(t, store.BindPluginUser(ctx, uid, "bot-a"))
	require.Equal(t, multiraft.SlotID(1), cluster.lastSlotID)
	require.Equal(t, hashSlot, cluster.lastHashSlot)
}

func TestPluginBindingListByPluginNoMergesAuthoritativeSlotsWithCursor(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	uids := []string{
		findUIDForSlot(t, nodes[0].cluster, 1, "u-plugin-page-a"),
		findUIDForSlot(t, nodes[0].cluster, 2, "u-plugin-page-b"),
		findUIDForSlot(t, nodes[0].cluster, 1, "u-plugin-page-c"),
		findUIDForSlot(t, nodes[0].cluster, 2, "u-plugin-page-d"),
	}
	for _, uid := range uids {
		require.NoError(t, nodes[0].store.BindPluginUser(ctx, uid, "bot-page"))
	}

	page1, cursor, hasMore, err := nodes[0].store.ListPluginBindingsByPluginNo(ctx, "bot-page", "", 2)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, page1, 2)
	require.NotEmpty(t, cursor)

	page2, nextCursor, hasMore, err := nodes[0].store.ListPluginBindingsByPluginNo(ctx, "bot-page", cursor, 10)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, nextCursor)

	all := append(page1, page2...)
	require.Len(t, all, 4)
	require.True(t, pluginBindingsSortedByPluginThenUID(all))
	require.ElementsMatch(t, uids, pluginBindingUIDs(all))

	_, _, _, err = nodes[0].store.ListPluginBindingsByPluginNo(ctx, "bot-page", "bad-cursor", 2)
	require.Error(t, err)
}

func TestPluginBindingListByPluginNoContinuesWithinLocalHashSlot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID: 1,
	}
	store := &Store{cluster: cluster, db: db}
	for _, uid := range []string{"u1", "u2", "u3"} {
		require.NoError(t, db.ForHashSlot(3).BindPluginUser(ctx, metadb.PluginUserBinding{
			UID: uid, PluginNo: "bot-local", CreatedAtMS: 1, UpdatedAtMS: 1,
		}))
	}

	page1, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-local", "", 2)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Equal(t, []string{"u1", "u2"}, pluginBindingUIDs(page1))

	page2, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-local", cursor, 2)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, cursor)
	require.Equal(t, []string{"u3"}, pluginBindingUIDs(page2))
}

func TestPluginBindingListByPluginNoContinuesDuplicateTupleAcrossHashSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3, 4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID: 1,
	}
	store := &Store{cluster: cluster, db: db}
	binding := metadb.PluginUserBinding{UID: "same-uid", PluginNo: "bot-dup", CreatedAtMS: 1, UpdatedAtMS: 1}
	require.NoError(t, db.ForHashSlot(3).BindPluginUser(ctx, binding))
	require.NoError(t, db.ForHashSlot(4).BindPluginUser(ctx, binding))

	page1, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-dup", "", 1)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, page1, 1)
	require.NotEmpty(t, cursor)
	decoded, err := decodePluginBindingPageCursor(cursor)
	require.NoError(t, err)
	require.Equal(t, uint16(3), decoded.HashSlot)

	page2, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-dup", cursor, 1)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, cursor)
	require.Equal(t, []metadb.PluginUserBinding{binding}, page2)
}

func TestPluginBindingListByPluginNoRejectsMismatchedCursorPlugin(t *testing.T) {
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 3,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-a", UID: "u1"},
	})
	require.NoError(t, err)
	store := &Store{cluster: &proxyPluginBindingTestCluster{slotIDs: []multiraft.SlotID{1}}, db: openTestDB(t)}

	_, _, _, err = store.ListPluginBindingsByPluginNo(context.Background(), "bot-b", cursor, 10)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestPluginBindingListByPluginNoRejectsMismatchedCursorShard(t *testing.T) {
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 8,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-a", UID: "u1"},
	})
	require.NoError(t, err)
	store := &Store{
		cluster: &proxyPluginBindingTestCluster{
			slotIDs:   []multiraft.SlotID{1},
			hashSlots: map[multiraft.SlotID][]uint16{1: {7}},
		},
		db: openTestDB(t),
	}

	_, _, _, err = store.ListPluginBindingsByPluginNo(context.Background(), "bot-a", cursor, 10)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestPluginBindingListByPluginNoContinuesWithoutRescanningLowerUIDsOnLaterShards(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3, 4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID: 1,
	}
	store := &Store{cluster: cluster, db: db}
	for _, uid := range []string{"u1", "u2", "u3"} {
		require.NoError(t, db.ForHashSlot(3).BindPluginUser(ctx, metadb.PluginUserBinding{
			UID: uid, PluginNo: "bot-no-rescan", CreatedAtMS: 1, UpdatedAtMS: 1,
		}))
	}
	for _, uid := range []string{"u0", "u2", "u4"} {
		require.NoError(t, db.ForHashSlot(4).BindPluginUser(ctx, metadb.PluginUserBinding{
			UID: uid, PluginNo: "bot-no-rescan", CreatedAtMS: 1, UpdatedAtMS: 1,
		}))
	}

	page1, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-no-rescan", "", 3)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Equal(t, []string{"u0", "u1", "u2"}, pluginBindingUIDs(page1))
	decoded, err := decodePluginBindingPageCursor(cursor)
	require.NoError(t, err)
	require.Equal(t, uint16(3), decoded.HashSlot)

	page2, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-no-rescan", cursor, 10)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, cursor)
	require.Equal(t, []string{"u2", "u3", "u4"}, pluginBindingUIDs(page2))
}

func TestPluginBindingListByPluginNoContinuesAcrossBeforeSameAndAfterShards(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {2, 3, 4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID: 1,
	}
	store := &Store{cluster: cluster, db: db}
	seed := map[uint16][]string{
		2: {"u2", "u5"},
		3: {"u2", "u3"},
		4: {"u2", "u4"},
	}
	for hashSlot, uids := range seed {
		for _, uid := range uids {
			require.NoError(t, db.ForHashSlot(hashSlot).BindPluginUser(ctx, metadb.PluginUserBinding{
				UID: uid, PluginNo: "bot-before", CreatedAtMS: 1, UpdatedAtMS: 1,
			}))
		}
	}
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 3,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-before", UID: "u2"},
	})
	require.NoError(t, err)

	page, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-before", cursor, 10)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, cursor)
	require.Equal(t, []string{"u2", "u3", "u4", "u5"}, pluginBindingUIDs(page))
}

func TestPluginBindingListByPluginNoUsesCursorBoundForLaterRemoteShards(t *testing.T) {
	ctx := context.Background()
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 3,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-efficient", UID: "u2"},
	})
	require.NoError(t, err)

	ops := make([]string, 0, 2)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3}, 2: {4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2}},
		localNodeID: 1,
		rpcService: func(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
			req, err := decodePluginBindingRPCRequest(payload)
			require.NoError(t, err)
			ops = append(ops, req.Op)
			switch req.Op {
			case pluginBindingRPCGetInHashSlot:
				require.Equal(t, "u2", req.UID)
				return encodePluginBindingRPCResponse(pluginBindingRPCResponse{Status: rpcStatusOK})
			case pluginBindingRPCScanByPluginNo:
				require.NotNil(t, req.After)
				require.Equal(t, pluginBindingRPCCursor{PluginNo: "bot-efficient", UID: "u2"}, *req.After)
				return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
					Status: rpcStatusOK,
					Bindings: []metadb.PluginUserBinding{{
						UID: "u4", PluginNo: "bot-efficient", CreatedAtMS: 1, UpdatedAtMS: 1,
					}},
					Cursor: pluginBindingRPCCursor{PluginNo: "bot-efficient", UID: "u4"},
					Done:   true,
				})
			default:
				t.Fatalf("unexpected plugin binding rpc op %s", req.Op)
				return nil, nil
			}
		},
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}

	page, nextCursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-efficient", cursor, 1)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, nextCursor)
	require.Equal(t, []string{"u4"}, pluginBindingUIDs(page))
	require.Equal(t, []string{pluginBindingRPCGetInHashSlot, pluginBindingRPCScanByPluginNo}, ops)
}

func TestPluginBindingListByPluginNoFallsBackWhenUnsupportedIsOverwrittenByLaterPeerError(t *testing.T) {
	ctx := context.Background()
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 3,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-compat-loss", UID: "u2"},
	})
	require.NoError(t, err)

	getCalls := 0
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3}, 2: {4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2, 3}},
		localNodeID: 1,
		rpcService: func(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
			req, err := decodePluginBindingRPCRequest(payload)
			require.NoError(t, err)
			switch req.Op {
			case pluginBindingRPCGetInHashSlot:
				getCalls++
				if nodeID == 2 {
					return nil, fmt.Errorf("metastore: unknown plugin binding rpc op id 6")
				}
				return nil, fmt.Errorf("temporary peer failure")
			case pluginBindingRPCScanByPluginNo:
				if req.After == nil || req.After.UID == "" {
					return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
						Status: rpcStatusOK,
						Bindings: []metadb.PluginUserBinding{{
							UID: "u1", PluginNo: "bot-compat-loss", CreatedAtMS: 1, UpdatedAtMS: 1,
						}},
						Cursor: pluginBindingRPCCursor{PluginNo: "bot-compat-loss", UID: "u1"},
					})
				}
				return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
					Status: rpcStatusOK,
					Bindings: []metadb.PluginUserBinding{{
						UID: "u2", PluginNo: "bot-compat-loss", CreatedAtMS: 1, UpdatedAtMS: 1,
					}},
					Cursor: pluginBindingRPCCursor{PluginNo: "bot-compat-loss", UID: "u2"},
					Done:   true,
				})
			default:
				t.Fatalf("unexpected plugin binding rpc op %s", req.Op)
				return nil, nil
			}
		},
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}

	page, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-compat-loss", cursor, 1)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, cursor)
	require.Equal(t, []string{"u2"}, pluginBindingUIDs(page))
	require.Equal(t, 2, getCalls)
}

func TestPluginBindingListByPluginNoFallsBackWhenGetInHashSlotUnsupported(t *testing.T) {
	ctx := context.Background()
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 3,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-compat", UID: "u2"},
	})
	require.NoError(t, err)

	ops := make([]string, 0, 3)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3}, 2: {4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2}},
		localNodeID: 1,
		rpcService: func(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
			req, err := decodePluginBindingRPCRequest(payload)
			require.NoError(t, err)
			ops = append(ops, req.Op)
			switch req.Op {
			case pluginBindingRPCGetInHashSlot:
				return nil, fmt.Errorf("metastore: unknown plugin binding rpc op id 6")
			case pluginBindingRPCScanByPluginNo:
				var binding metadb.PluginUserBinding
				done := false
				if req.After == nil || req.After.UID == "" {
					binding = metadb.PluginUserBinding{UID: "u1", PluginNo: "bot-compat", CreatedAtMS: 1, UpdatedAtMS: 1}
				} else {
					binding = metadb.PluginUserBinding{UID: "u2", PluginNo: "bot-compat", CreatedAtMS: 1, UpdatedAtMS: 1}
					done = true
				}
				return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
					Status:   rpcStatusOK,
					Bindings: []metadb.PluginUserBinding{binding},
					Cursor:   pluginBindingRPCCursor{PluginNo: binding.PluginNo, UID: binding.UID},
					Done:     done,
				})
			default:
				t.Fatalf("unexpected plugin binding rpc op %s", req.Op)
				return nil, nil
			}
		},
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}

	page, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-compat", cursor, 1)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, cursor)
	require.Equal(t, []string{"u2"}, pluginBindingUIDs(page))
	require.Equal(t, []string{pluginBindingRPCGetInHashSlot, pluginBindingRPCScanByPluginNo, pluginBindingRPCScanByPluginNo}, ops)
}

func TestPluginBindingListByPluginNoCompatRejectsNonAdvancingCursor(t *testing.T) {
	ctx := context.Background()
	cursor, err := encodePluginBindingPageCursor(pluginBindingPageCursor{
		SlotID:   1,
		HashSlot: 3,
		Binding:  metadb.PluginUserBindingCursor{PluginNo: "bot-stuck", UID: "u2"},
	})
	require.NoError(t, err)

	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {3}, 2: {4}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2}},
		localNodeID: 1,
		rpcService: func(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
			req, err := decodePluginBindingRPCRequest(payload)
			require.NoError(t, err)
			switch req.Op {
			case pluginBindingRPCGetInHashSlot:
				return nil, fmt.Errorf("metastore: unknown plugin binding rpc op id 6")
			case pluginBindingRPCScanByPluginNo:
				return encodePluginBindingRPCResponse(pluginBindingRPCResponse{
					Status: rpcStatusOK,
					Bindings: []metadb.PluginUserBinding{{
						UID: "u1", PluginNo: "bot-stuck", CreatedAtMS: 1, UpdatedAtMS: 1,
					}},
					Cursor: pluginBindingRPCCursor{PluginNo: "bot-stuck", UID: ""},
				})
			default:
				t.Fatalf("unexpected plugin binding rpc op %s", req.Op)
				return nil, nil
			}
		},
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}

	_, _, _, err = store.ListPluginBindingsByPluginNo(ctx, "bot-stuck", cursor, 1)
	require.ErrorContains(t, err, "cursor did not advance")
}

func TestPluginBindingListByPluginNoSkipsEmptyPhysicalSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   map[multiraft.SlotID][]uint16{2: {3}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 1},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {1}},
		localNodeID: 1,
	}
	store := &Store{cluster: cluster, db: db}
	want := metadb.PluginUserBinding{UID: "u1", PluginNo: "bot-empty-slot", CreatedAtMS: 1, UpdatedAtMS: 1}
	require.NoError(t, db.ForHashSlot(3).BindPluginUser(ctx, want))

	page, _, hasMore, err := store.ListPluginBindingsByPluginNo(ctx, "bot-empty-slot", "", 10)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, []metadb.PluginUserBinding{want}, page)
}

func TestPluginBindingRPCRejectsUnownedHashSlotScan(t *testing.T) {
	ctx := context.Background()
	store := &Store{
		cluster: &proxyPluginBindingTestCluster{
			hashSlots:   map[multiraft.SlotID][]uint16{1: {7}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
			peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
			localNodeID: 1,
		},
		db: openTestDB(t),
	}
	body, err := encodePluginBindingRPCRequestBinary(pluginBindingRPCRequest{
		Op:       pluginBindingRPCScanByPluginNo,
		SlotID:   1,
		HashSlot: 8,
		PluginNo: "bot-a",
		Limit:    10,
	})
	require.NoError(t, err)

	_, err = store.handlePluginBindingRPC(ctx, body)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestPluginBindingRPCRejectsUnownedHashSlotGetInHashSlot(t *testing.T) {
	ctx := context.Background()
	store := &Store{
		cluster: &proxyPluginBindingTestCluster{
			hashSlots:   map[multiraft.SlotID][]uint16{1: {7}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
			peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
			localNodeID: 1,
		},
		db: openTestDB(t),
	}
	body, err := encodePluginBindingRPCRequestBinary(pluginBindingRPCRequest{
		Op:       pluginBindingRPCGetInHashSlot,
		SlotID:   1,
		HashSlot: 8,
		UID:      "u1",
		PluginNo: "bot-a",
	})
	require.NoError(t, err)

	_, err = store.handlePluginBindingRPC(ctx, body)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestPluginBindingRPCRejectsOversizedScanLimit(t *testing.T) {
	ctx := context.Background()
	store := &Store{
		cluster: &proxyPluginBindingTestCluster{
			hashSlots:   map[multiraft.SlotID][]uint16{1: {7}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
			peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
			localNodeID: 1,
		},
		db: openTestDB(t),
	}
	body, err := encodePluginBindingRPCRequestBinary(pluginBindingRPCRequest{
		Op:       pluginBindingRPCScanByPluginNo,
		SlotID:   1,
		HashSlot: 7,
		PluginNo: "bot-a",
		Limit:    pluginBindingScanMaxLimit + 1,
	})
	require.NoError(t, err)

	_, err = store.handlePluginBindingRPC(ctx, body)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestPluginBindingStoreRejectsOverlongInputBeforeProposal(t *testing.T) {
	ctx := context.Background()
	cluster := &proxyPluginBindingTestCluster{
		slotForKeyFunc:     func(key string) multiraft.SlotID { return 1 },
		hashSlotForKeyFunc: func(key string) uint16 { return 1 },
		leaders:            map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:              map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID:        1,
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}
	overlong := string(make([]byte, 1<<16))

	require.ErrorIs(t, store.BindPluginUser(ctx, overlong, "bot-a"), metadb.ErrInvalidArgument)
	require.Empty(t, cluster.lastCommand)
	require.ErrorIs(t, store.BindPluginUser(ctx, "u1", overlong), metadb.ErrInvalidArgument)
	require.Empty(t, cluster.lastCommand)
	require.ErrorIs(t, store.UnbindPluginUser(ctx, overlong, "bot-a"), metadb.ErrInvalidArgument)
	require.Empty(t, cluster.lastCommand)
	require.ErrorIs(t, store.UnbindPluginUser(ctx, "u1", overlong), metadb.ErrInvalidArgument)
	require.Empty(t, cluster.lastCommand)
}

func TestPluginBindingStoreRejectsOverlongReadInputBeforeRPC(t *testing.T) {
	ctx := context.Background()
	cluster := &proxyPluginBindingTestCluster{
		slotForKeyFunc:     func(key string) multiraft.SlotID { return 2 },
		hashSlotForKeyFunc: func(key string) uint16 { return 1 },
		leaders:            map[multiraft.SlotID]multiraft.NodeID{2: 2},
		peers:              map[multiraft.SlotID][]multiraft.NodeID{2: {2}},
		localNodeID:        1,
		rpcService: func(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
			t.Fatal("overlong input should be rejected before RPC")
			return nil, nil
		},
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}
	overlong := string(make([]byte, 1<<16))

	_, err := store.ListPluginBindingsByUID(ctx, overlong)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	_, err = store.ExistPluginBindingByUID(ctx, overlong)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	_, _, _, err = store.ListPluginBindingsByPluginNo(ctx, overlong, "", 10)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestPluginBindingRPCRejectsOverlongInputBeforeProposal(t *testing.T) {
	ctx := context.Background()
	cluster := &proxyPluginBindingTestCluster{
		hashSlots:   map[multiraft.SlotID][]uint16{1: {7}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID: 1,
	}
	store := &Store{cluster: cluster, db: openTestDB(t)}
	overlong := string(make([]byte, 1<<16))

	for _, req := range []pluginBindingRPCRequest{
		{Op: pluginBindingRPCBind, SlotID: 1, HashSlot: 7, UID: overlong, PluginNo: "bot-a"},
		{Op: pluginBindingRPCBind, SlotID: 1, HashSlot: 7, UID: "u1", PluginNo: overlong},
		{Op: pluginBindingRPCUnbind, SlotID: 1, HashSlot: 7, UID: "u1", PluginNo: overlong},
		{Op: pluginBindingRPCListByUID, SlotID: 1, HashSlot: 7, UID: overlong},
		{Op: pluginBindingRPCExistsByUID, SlotID: 1, HashSlot: 7, UID: overlong},
		{Op: pluginBindingRPCScanByPluginNo, SlotID: 1, HashSlot: 7, PluginNo: overlong, Limit: 10},
		{Op: pluginBindingRPCGetInHashSlot, SlotID: 1, HashSlot: 7, UID: overlong, PluginNo: "bot-a"},
		{Op: pluginBindingRPCGetInHashSlot, SlotID: 1, HashSlot: 7, UID: "u1", PluginNo: overlong},
	} {
		body, err := encodePluginBindingRPCRequestBinary(req)
		require.NoError(t, err)
		_, err = store.handlePluginBindingRPC(ctx, body)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
		require.Empty(t, cluster.lastCommand)
	}
}

func TestPluginBindingRPCRejectsMisroutedUIDRequest(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotForKeyFunc:     func(key string) multiraft.SlotID { return 2 },
		hashSlotForKeyFunc: func(key string) uint16 { return 7 },
		leaders:            map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
		peers:              map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2}},
		localNodeID:        1,
	}
	store := &Store{cluster: cluster, db: db}
	body, err := encodePluginBindingRPCRequestBinary(pluginBindingRPCRequest{
		Op:       pluginBindingRPCListByUID,
		SlotID:   1,
		HashSlot: 1,
		UID:      "misrouted",
	})
	require.NoError(t, err)

	respBody, err := store.handlePluginBindingRPC(ctx, body)
	require.NoError(t, err)
	resp, err := decodePluginBindingRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusNotLeader, resp.Status)
	require.Equal(t, uint64(2), resp.LeaderID)
}

func TestPluginBindingRPCComputesHashSlotFromUID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyPluginBindingTestCluster{
		slotForKeyFunc:     func(key string) multiraft.SlotID { return 1 },
		hashSlotForKeyFunc: func(key string) uint16 { return 7 },
		leaders:            map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:              map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		localNodeID:        1,
	}
	store := &Store{cluster: cluster, db: db}
	want := metadb.PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 1, UpdatedAtMS: 1}
	require.NoError(t, db.ForHashSlot(7).BindPluginUser(ctx, want))

	body, err := encodePluginBindingRPCRequestBinary(pluginBindingRPCRequest{
		Op:       pluginBindingRPCListByUID,
		SlotID:   1,
		HashSlot: 1,
		UID:      "u1",
	})
	require.NoError(t, err)
	respBody, err := store.handlePluginBindingRPC(ctx, body)
	require.NoError(t, err)
	resp, err := decodePluginBindingRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, []metadb.PluginUserBinding{want}, resp.Bindings)
}

func pluginBindingWithoutTimes(binding metadb.PluginUserBinding) metadb.PluginUserBinding {
	binding.CreatedAtMS = 0
	binding.UpdatedAtMS = 0
	return binding
}

func pluginBindingsSortedByPluginThenUID(bindings []metadb.PluginUserBinding) bool {
	for i := 1; i < len(bindings); i++ {
		prev := bindings[i-1]
		cur := bindings[i]
		if prev.PluginNo > cur.PluginNo || (prev.PluginNo == cur.PluginNo && prev.UID > cur.UID) {
			return false
		}
	}
	return true
}

func pluginBindingUIDs(bindings []metadb.PluginUserBinding) []string {
	uids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		uids = append(uids, binding.UID)
	}
	return uids
}

type proxyPluginBindingTestCluster struct {
	raftcluster.API
	slotForKeyFunc     func(string) multiraft.SlotID
	hashSlotForKeyFunc func(string) uint16
	slotIDs            []multiraft.SlotID
	hashSlots          map[multiraft.SlotID][]uint16
	leaders            map[multiraft.SlotID]multiraft.NodeID
	peers              map[multiraft.SlotID][]multiraft.NodeID
	localNodeID        multiraft.NodeID
	rpcService         func(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error)
	lastSlotID         multiraft.SlotID
	lastHashSlot       uint16
	lastCommand        []byte
}

func (c *proxyPluginBindingTestCluster) SlotForKey(key string) multiraft.SlotID {
	if c.slotForKeyFunc != nil {
		return c.slotForKeyFunc(key)
	}
	return 1
}

func (c *proxyPluginBindingTestCluster) HashSlotForKey(key string) uint16 {
	if c.hashSlotForKeyFunc != nil {
		return c.hashSlotForKeyFunc(key)
	}
	return 0
}

func (c *proxyPluginBindingTestCluster) HashSlotsOf(slotID multiraft.SlotID) []uint16 {
	return append([]uint16(nil), c.hashSlots[slotID]...)
}

func (c *proxyPluginBindingTestCluster) SlotIDs() []multiraft.SlotID {
	return append([]multiraft.SlotID(nil), c.slotIDs...)
}

func (c *proxyPluginBindingTestCluster) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	leaderID, ok := c.leaders[slotID]
	if !ok {
		return 0, raftcluster.ErrNoLeader
	}
	return leaderID, nil
}

func (c *proxyPluginBindingTestCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return nodeID == c.localNodeID
}

func (c *proxyPluginBindingTestCluster) PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID {
	return append([]multiraft.NodeID(nil), c.peers[slotID]...)
}

func (c *proxyPluginBindingTestCluster) ProposeWithHashSlot(_ context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	c.lastSlotID = slotID
	c.lastHashSlot = hashSlot
	c.lastCommand = append([]byte(nil), cmd...)
	return nil
}

func (c *proxyPluginBindingTestCluster) RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	if c.rpcService != nil {
		return c.rpcService(ctx, nodeID, slotID, serviceID, payload)
	}
	return nil, errors.New("missing rpc service")
}

func (c *proxyPluginBindingTestCluster) NodeID() multiraft.NodeID {
	return c.localNodeID
}

func (c *proxyPluginBindingTestCluster) Start() error { return nil }

func (c *proxyPluginBindingTestCluster) Stop() {}

func (c *proxyPluginBindingTestCluster) String() string {
	return fmt.Sprintf("proxyPluginBindingTestCluster(node=%d)", c.localNodeID)
}
