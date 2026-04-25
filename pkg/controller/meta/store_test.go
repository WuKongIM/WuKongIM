package meta

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	"github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

func TestStoreAssignmentAndTaskRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{
		SlotID:         7,
		DesiredPeers:   []uint64{3, 1, 2, 2},
		ConfigEpoch:    11,
		BalanceVersion: 3,
	}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{
		SlotID:     7,
		Kind:       TaskKindRepair,
		Step:       TaskStepAddLearner,
		SourceNode: 4,
		TargetNode: 2,
		Attempt:    1,
	}))

	assignment, err := store.GetAssignment(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	task, err := store.GetTask(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, TaskKindRepair, task.Kind)
}

func TestStoreSnapshotRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertControllerMembership(ctx, ControllerMembership{
		Peers: []uint64{3, 1, 2},
	}))
	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(11, 0),
		CapacityWeight:  1,
	}))
	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:              1,
		CurrentPeers:        []uint64{1, 2, 3},
		LeaderID:            2,
		HasQuorum:           true,
		ObservedConfigEpoch: 1,
		LastReportAt:        time.Unix(12, 0),
	}))
	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	snap, err := store.ExportSnapshot(ctx)
	require.NoError(t, err)
	entries, err := decodeSnapshot(snap)
	require.NoError(t, err)
	require.Len(t, entries, 5)

	restored := openTestStore(t)
	require.NoError(t, restored.ImportSnapshot(ctx, snap))
	assignment, err := restored.GetAssignment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	membership, err := restored.GetControllerMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, membership.Peers)

	node, err := restored.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, time.Unix(11, 0), node.LastHeartbeatAt)
	require.Equal(t, 1, node.CapacityWeight)

	view, err := restored.GetRuntimeView(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), view.LeaderID)
	require.Equal(t, uint64(1), view.ObservedConfigEpoch)
	require.Equal(t, time.Unix(12, 0), view.LastReportAt)

	restoredTable, err := restored.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.NotNil(t, restoredTable.GetMigration(3))
}

func TestStoreHashSlotTableRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	table := hashslot.NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, hashslot.PhaseDelta)

	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	loaded, err := store.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, table.Version(), loaded.Version())
	require.Equal(t, table.Lookup(3), loaded.Lookup(3))

	migration := loaded.GetMigration(3)
	require.NotNil(t, migration)
	require.Equal(t, hashslot.PhaseDelta, migration.Phase)
	require.Equal(t, uint64(1), uint64(migration.Source))
	require.Equal(t, uint64(2), uint64(migration.Target))
}

func TestStoreListsControllerStateForPlannerQueries(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          2,
		Addr:            "127.0.0.1:7001",
		Status:          NodeStatusDraining,
		LastHeartbeatAt: time.Unix(19, 0),
		CapacityWeight:  2,
	}))
	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(20, 0),
		CapacityWeight:  1,
	}))
	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{SlotID: 2, DesiredPeers: []uint64{2, 3, 1}, ConfigEpoch: 3}))
	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 2}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:              2,
		CurrentPeers:        []uint64{3, 2, 1},
		HasQuorum:           false,
		ObservedConfigEpoch: 3,
		LastReportAt:        time.Unix(22, 0),
	}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:              1,
		CurrentPeers:        []uint64{1, 2, 3},
		HasQuorum:           true,
		ObservedConfigEpoch: 2,
		LastReportAt:        time.Unix(21, 0),
	}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{SlotID: 2, Kind: TaskKindRebalance, Step: TaskStepTransferLeader}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{SlotID: 1, Kind: TaskKindRepair, Step: TaskStepAddLearner}))

	nodes, err := store.ListNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	require.Equal(t, uint64(1), nodes[0].NodeID)
	require.Equal(t, uint64(2), nodes[1].NodeID)
	require.Equal(t, time.Unix(20, 0), nodes[0].LastHeartbeatAt)
	require.Equal(t, 1, nodes[0].CapacityWeight)

	assignments, err := store.ListAssignments(ctx)
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	require.Equal(t, uint32(1), assignments[0].SlotID)
	require.Equal(t, uint32(2), assignments[1].SlotID)
	require.Equal(t, []uint64{1, 2, 3}, assignments[1].DesiredPeers)

	views, err := store.ListRuntimeViews(ctx)
	require.NoError(t, err)
	require.Len(t, views, 2)
	require.Equal(t, uint32(1), views[0].SlotID)
	require.Equal(t, uint32(2), views[1].SlotID)
	require.Equal(t, uint64(2), views[0].ObservedConfigEpoch)
	require.Equal(t, time.Unix(21, 0), views[0].LastReportAt)
	require.Equal(t, []uint64{1, 2, 3}, views[1].CurrentPeers)

	tasks, err := store.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, uint32(1), tasks[0].SlotID)
	require.Equal(t, uint32(2), tasks[1].SlotID)
}

func TestStoreControllerMembershipRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertControllerMembership(ctx, ControllerMembership{
		Peers: []uint64{3, 1, 2, 2},
	}))

	membership, err := store.GetControllerMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, membership.Peers)
}

func TestStoreDeleteOperations(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(30, 0),
	}))
	require.NoError(t, store.UpsertControllerMembership(ctx, ControllerMembership{
		Peers: []uint64{1, 2, 3},
	}))
	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 2, 3},
	}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
	}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{
		SlotID: 1,
		Kind:   TaskKindRepair,
		Step:   TaskStepAddLearner,
	}))

	require.NoError(t, store.DeleteNode(ctx, 1))
	require.NoError(t, store.DeleteControllerMembership(ctx))
	require.NoError(t, store.DeleteAssignment(ctx, 1))
	require.NoError(t, store.DeleteRuntimeView(ctx, 1))
	require.NoError(t, store.DeleteTask(ctx, 1))

	_, err := store.GetNode(ctx, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.GetControllerMembership(ctx)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.GetAssignment(ctx, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.GetRuntimeView(ctx, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.GetTask(ctx, 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestImportSnapshotRejectsCorruptValues(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(40, 0),
	}))

	snap, err := store.ExportSnapshot(ctx)
	require.NoError(t, err)

	entries, err := decodeSnapshot(snap)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entries[0].Value = []byte{recordVersion}

	restored := openTestStore(t)
	err = restored.ImportSnapshot(ctx, encodeSnapshot(entries))
	require.ErrorIs(t, err, ErrCorruptValue)

	_, err = restored.GetNode(ctx, 1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestImportSnapshotRejectsOversizedEntryCount(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	body := make([]byte, 0, 32)
	body = append(body, snapshotMagic[:]...)
	body = binary.BigEndian.AppendUint16(body, snapshotVersion)
	body = binary.BigEndian.AppendUint64(body, ^uint64(0))
	sum := crc32.ChecksumIEEE(body)
	data := binary.BigEndian.AppendUint32(body, sum)

	err := store.ImportSnapshot(ctx, data)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestImportSnapshotRejectsInvalidSemanticValues(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	validNode := snapshotEntry{
		Key: encodeNodeKey(1),
		Value: encodeClusterNode(ClusterNode{
			NodeID:          1,
			Addr:            "127.0.0.1:7000",
			Status:          NodeStatusAlive,
			LastHeartbeatAt: time.Unix(60, 0),
		}),
	}
	validTask := snapshotEntry{
		Key: encodeGroupKey(recordPrefixTask, 1),
		Value: encodeReconcileTask(ReconcileTask{
			SlotID: 1,
			Kind:   TaskKindRepair,
			Step:   TaskStepAddLearner,
		}),
	}

	tests := []struct {
		name  string
		entry snapshotEntry
	}{
		{
			name: "empty node address",
			entry: snapshotEntry{
				Key: encodeNodeKey(1),
				Value: encodeClusterNode(ClusterNode{
					NodeID:          1,
					Addr:            "",
					Status:          NodeStatusAlive,
					LastHeartbeatAt: time.Unix(61, 0),
				}),
			},
		},
		{
			name: "unknown node status",
			entry: snapshotEntry{
				Key: encodeNodeKey(1),
				Value: encodeClusterNode(ClusterNode{
					NodeID:          1,
					Addr:            "127.0.0.1:7000",
					Status:          NodeStatusUnknown,
					LastHeartbeatAt: time.Unix(62, 0),
				}),
			},
		},
		{
			name: "unknown task kind",
			entry: snapshotEntry{
				Key: encodeGroupKey(recordPrefixTask, 1),
				Value: encodeReconcileTask(ReconcileTask{
					SlotID: 1,
					Kind:   TaskKindUnknown,
					Step:   TaskStepAddLearner,
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entries []snapshotEntry
			if tt.entry.Key[0] == recordPrefixNode {
				entries = []snapshotEntry{tt.entry, validTask}
			} else {
				entries = []snapshotEntry{validNode, tt.entry}
			}

			err := store.ImportSnapshot(ctx, encodeSnapshot(entries))
			require.ErrorIs(t, err, ErrCorruptValue)
		})
	}
}

func TestDecodeRejectsInvalidPersistedEnums(t *testing.T) {
	nodeValue := encodeClusterNode(ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(50, 0),
	})
	nodeValue[16] = 99
	_, err := decodeClusterNode(encodeNodeKey(1), nodeValue)
	require.ErrorIs(t, err, ErrCorruptValue)

	viewValue := encodeGroupRuntimeView(SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		HasQuorum:    true,
	})
	viewValue[13] = 2
	_, err = decodeGroupRuntimeView(encodeGroupKey(recordPrefixRuntimeView, 1), viewValue)
	require.ErrorIs(t, err, ErrCorruptValue)

	taskValue := encodeReconcileTask(ReconcileTask{
		SlotID: 1,
		Kind:   TaskKindRepair,
		Step:   TaskStepAddLearner,
	})
	taskValue[1] = 99
	_, err = decodeReconcileTask(encodeGroupKey(recordPrefixTask, 1), taskValue)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestUpsertRejectsUnknownEnums(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusUnknown,
		LastHeartbeatAt: time.Unix(70, 0),
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	err = store.UpsertTask(ctx, ReconcileTask{
		SlotID: 1,
		Kind:   TaskKindUnknown,
		Step:   TaskStepAddLearner,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	err = store.UpsertTask(ctx, ReconcileTask{
		SlotID: 1,
		Kind:   TaskKindRepair,
		Step:   TaskStepUnknown,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestUpsertTaskRejectsInconsistentTaskState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpsertTask(ctx, ReconcileTask{
		SlotID: 1,
		Kind:   TaskKindRepair,
		Step:   TaskStepAddLearner,
		Status: TaskStatusRetrying,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	err = store.UpsertTask(ctx, ReconcileTask{
		SlotID: 1,
		Kind:   TaskKindRepair,
		Step:   TaskStepAddLearner,
		Status: TaskStatusFailed,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestStoreUpsertAssignmentTaskIsAtomic(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpsertAssignmentTask(ctx, SlotAssignment{
		SlotID:       3,
		DesiredPeers: []uint64{1, 2, 3},
		ConfigEpoch:  2,
	}, ReconcileTask{
		SlotID: 3,
		Kind:   TaskKindRepair,
		Step:   TaskStepUnknown,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = store.GetAssignment(ctx, 3)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = store.GetTask(ctx, 3)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestUpsertRejectsInvalidPeerSets(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpsertAssignment(ctx, SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{0, 1, 2},
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	err = store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{0, 2, 3},
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	err = store.UpsertControllerMembership(ctx, ControllerMembership{})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestUpsertRejectsInvalidRuntimeViewState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	err := store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LeaderID:     9,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	err = store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:        1,
		CurrentPeers:  []uint64{1, 2, 3},
		HealthyVoters: 4,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestStoreCanonicalizesPeerOrdering(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{
		SlotID:       9,
		DesiredPeers: []uint64{3, 1, 2, 2},
	}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{
		SlotID:       9,
		CurrentPeers: []uint64{5, 3, 5, 4},
	}))

	assignment, err := store.GetAssignment(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	view, err := store.GetRuntimeView(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 4, 5}, view.CurrentPeers)
}

func TestStoreListMethodsReturnDeterministicOrder(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, ClusterNode{NodeID: 9, Addr: "127.0.0.1:7009", Status: NodeStatusAlive, CapacityWeight: 1}))
	require.NoError(t, store.UpsertNode(ctx, ClusterNode{NodeID: 3, Addr: "127.0.0.1:7003", Status: NodeStatusDraining, CapacityWeight: 1}))
	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{SlotID: 8, DesiredPeers: []uint64{8, 9, 10}}))
	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{SlotID: 2, DesiredPeers: []uint64{2, 3, 4}}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{SlotID: 7, CurrentPeers: []uint64{7, 8, 9}}))
	require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{SlotID: 1, CurrentPeers: []uint64{1, 2, 3}}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{SlotID: 5, Kind: TaskKindRepair, Step: TaskStepAddLearner}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{SlotID: 4, Kind: TaskKindRebalance, Step: TaskStepTransferLeader}))

	nodes, err := store.ListNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	require.Equal(t, uint64(3), nodes[0].NodeID)
	require.Equal(t, uint64(9), nodes[1].NodeID)

	assignments, err := store.ListAssignments(ctx)
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	require.Equal(t, uint32(2), assignments[0].SlotID)
	require.Equal(t, uint32(8), assignments[1].SlotID)

	views, err := store.ListRuntimeViews(ctx)
	require.NoError(t, err)
	require.Len(t, views, 2)
	require.Equal(t, uint32(1), views[0].SlotID)
	require.Equal(t, uint32(7), views[1].SlotID)

	tasks, err := store.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, uint32(4), tasks[0].SlotID)
	require.Equal(t, uint32(5), tasks[1].SlotID)
}

func TestImportSnapshotRejectsInvalidPeerSets(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		entries []snapshotEntry
	}{
		{
			name: "assignment with zero peer",
			entries: []snapshotEntry{
				{
					Key: encodeGroupKey(recordPrefixAssignment, 1),
					Value: encodeGroupAssignment(SlotAssignment{
						SlotID:       1,
						DesiredPeers: []uint64{0, 1, 2},
					}),
				},
			},
		},
		{
			name: "empty controller membership",
			entries: []snapshotEntry{
				{
					Key:   membershipKey(),
					Value: encodeControllerMembership(ControllerMembership{}),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.ImportSnapshot(ctx, encodeSnapshot(tt.entries))
			require.ErrorIs(t, err, ErrCorruptValue)
		})
	}
}

func TestImportSnapshotRejectsInvalidRuntimeViewState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		entry snapshotEntry
	}{
		{
			name: "leader not in peers",
			entry: snapshotEntry{
				Key: encodeGroupKey(recordPrefixRuntimeView, 1),
				Value: encodeGroupRuntimeView(SlotRuntimeView{
					SlotID:       1,
					CurrentPeers: []uint64{1, 2, 3},
					LeaderID:     9,
				}),
			},
		},
		{
			name: "healthy voters exceed peers",
			entry: snapshotEntry{
				Key: encodeGroupKey(recordPrefixRuntimeView, 1),
				Value: encodeGroupRuntimeView(SlotRuntimeView{
					SlotID:        1,
					CurrentPeers:  []uint64{1, 2, 3},
					HealthyVoters: 4,
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.ImportSnapshot(ctx, encodeSnapshot([]snapshotEntry{tt.entry}))
			require.ErrorIs(t, err, ErrCorruptValue)
		})
	}
}

func TestImportSnapshotRejectsDuplicateKeys(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	entries := []snapshotEntry{
		{
			Key: encodeNodeKey(1),
			Value: encodeClusterNode(ClusterNode{
				NodeID:          1,
				Addr:            "127.0.0.1:7000",
				Status:          NodeStatusAlive,
				LastHeartbeatAt: time.Unix(80, 0),
			}),
		},
		{
			Key: encodeNodeKey(1),
			Value: encodeClusterNode(ClusterNode{
				NodeID:          1,
				Addr:            "127.0.0.1:7001",
				Status:          NodeStatusAlive,
				LastHeartbeatAt: time.Unix(81, 0),
			}),
		},
	}

	err := store.ImportSnapshot(ctx, encodeSnapshot(entries))
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestImportSnapshotHonorsCancellation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 2, 3},
	}))

	snap := encodeSnapshot([]snapshotEntry{
		{
			Key: encodeGroupKey(recordPrefixAssignment, 2),
			Value: encodeGroupAssignment(SlotAssignment{
				SlotID:       2,
				DesiredPeers: []uint64{2, 3, 4},
			}),
		},
	})

	cancelCtx := &stepCancelContext{cancelAfter: 2}
	err := store.ImportSnapshot(cancelCtx, snap)
	require.ErrorIs(t, err, context.Canceled)

	assignment, err := store.GetAssignment(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	_, err = store.GetAssignment(context.Background(), 2)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestExportSnapshotRejectsCorruptStoredValues(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	store.mu.Lock()
	err := store.db.Set(encodeNodeKey(1), []byte{recordVersion}, pebble.Sync)
	store.mu.Unlock()
	require.NoError(t, err)

	_, err = store.ExportSnapshot(ctx)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestExportSnapshotRejectsMalformedMembershipKey(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	store.mu.Lock()
	err := store.db.Set([]byte{'m', 'x'}, encodeControllerMembership(ControllerMembership{Peers: []uint64{1, 2, 3}}), pebble.Sync)
	store.mu.Unlock()
	require.NoError(t, err)

	_, err = store.ExportSnapshot(ctx)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestExportSnapshotRejectsNonCanonicalPeerSets(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	value := []byte{recordVersion}
	value = binary.BigEndian.AppendUint64(value, 1)
	value = binary.BigEndian.AppendUint64(value, 0)
	value = appendRawUint64Slice(value, []uint64{3, 1, 1})

	store.mu.Lock()
	err := store.db.Set(encodeGroupKey(recordPrefixAssignment, 1), value, pebble.Sync)
	store.mu.Unlock()
	require.NoError(t, err)

	_, err = store.GetAssignment(ctx, 1)
	require.ErrorIs(t, err, ErrCorruptValue)

	_, err = store.ExportSnapshot(ctx)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestExportSnapshotRejectsZeroWeightNodeRecords(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	value := []byte{recordVersion}
	value = appendString(value, "127.0.0.1:7000")
	value = append(value, byte(NodeStatusAlive))
	value = appendInt64(value, time.Unix(90, 0).UnixNano())
	value = appendInt64(value, 0)

	store.mu.Lock()
	err := store.db.Set(encodeNodeKey(1), value, pebble.Sync)
	store.mu.Unlock()
	require.NoError(t, err)

	_, err = store.GetNode(ctx, 1)
	require.ErrorIs(t, err, ErrCorruptValue)

	_, err = store.ExportSnapshot(ctx)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestStoreRejectsOperationsAfterClose(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Close())

	tests := []struct {
		name string
		run  func() error
	}{
		{"GetNode", func() error { _, err := store.GetNode(ctx, 0); return err }},
		{"DeleteNode", func() error { return store.DeleteNode(ctx, 0) }},
		{"ListNodes", func() error { _, err := store.ListNodes(ctx); return err }},
		{"UpsertNode", func() error { return store.UpsertNode(ctx, ClusterNode{}) }},
		{"GetAssignment", func() error { _, err := store.GetAssignment(ctx, 0); return err }},
		{"DeleteAssignment", func() error { return store.DeleteAssignment(ctx, 0) }},
		{"ListAssignments", func() error { _, err := store.ListAssignments(ctx); return err }},
		{"UpsertAssignment", func() error { return store.UpsertAssignment(ctx, SlotAssignment{}) }},
		{"GetRuntimeView", func() error { _, err := store.GetRuntimeView(ctx, 0); return err }},
		{"DeleteRuntimeView", func() error { return store.DeleteRuntimeView(ctx, 0) }},
		{"ListRuntimeViews", func() error { _, err := store.ListRuntimeViews(ctx); return err }},
		{"UpsertRuntimeView", func() error { return store.UpsertRuntimeView(ctx, SlotRuntimeView{}) }},
		{"GetControllerMembership", func() error { _, err := store.GetControllerMembership(ctx); return err }},
		{"DeleteControllerMembership", func() error { return store.DeleteControllerMembership(ctx) }},
		{"UpsertControllerMembership", func() error { return store.UpsertControllerMembership(ctx, ControllerMembership{}) }},
		{"GetTask", func() error { _, err := store.GetTask(ctx, 0); return err }},
		{"DeleteTask", func() error { return store.DeleteTask(ctx, 0) }},
		{"ListTasks", func() error { _, err := store.ListTasks(ctx); return err }},
		{"UpsertTask", func() error { return store.UpsertTask(ctx, ReconcileTask{}) }},
		{"ExportSnapshot", func() error { _, err := store.ExportSnapshot(ctx); return err }},
		{"ImportSnapshot", func() error { return store.ImportSnapshot(ctx, []byte{0x01}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.run(), ErrClosed)
		})
	}
}

func TestStoreCloseIsConcurrentSafe(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, store.Close())
		}()
	}
	wg.Wait()
}

func appendRawUint64Slice(dst []byte, values []uint64) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = binary.BigEndian.AppendUint64(dst, value)
	}
	return dst
}

type stepCancelContext struct {
	cancelAfter int
	calls       int
}

func (c *stepCancelContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (c *stepCancelContext) Done() <-chan struct{} {
	return nil
}

func (c *stepCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAfter {
		return context.Canceled
	}
	return nil
}

func (c *stepCancelContext) Value(key interface{}) interface{} {
	return nil
}

func openTestStore(tb testing.TB) *Store {
	tb.Helper()

	store, err := Open(filepath.Join(tb.TempDir(), "db"))
	require.NoError(tb, err)

	tb.Cleanup(func() {
		require.NoError(tb, store.Close())
	})
	return store
}
