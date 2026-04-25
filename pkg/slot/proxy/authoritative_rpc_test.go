package proxy

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestHandleRuntimeMetaRPCBeforeClusterStartReturnsNoLeader(t *testing.T) {
	db := openTestDB(t)
	raftDB := openTestRaftDBAt(t, filepath.Join(t.TempDir(), "raft"))

	cluster, err := raftcluster.NewCluster(raftcluster.Config{
		NodeID:             1,
		ListenAddr:         "127.0.0.1:9090",
		SlotCount:          1,
		ControllerReplicaN: 1,
		SlotReplicaN:       1,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(db),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(db),
		Nodes: []raftcluster.NodeConfig{{
			NodeID: 1,
			Addr:   "127.0.0.1:9090",
		}},
	})
	require.NoError(t, err)

	store := New(cluster, db)
	body, err := json.Marshal(runtimeMetaRPCRequest{
		Op:     runtimeMetaRPCList,
		SlotID: 1,
	})
	require.NoError(t, err)

	var respBody []byte
	require.NotPanics(t, func() {
		respBody, err = store.handleRuntimeMetaRPC(context.Background(), body)
	})
	require.NoError(t, err)

	resp, err := decodeRuntimeMetaRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusNoLeader, resp.Status)
}
