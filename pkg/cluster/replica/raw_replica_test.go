package replica_test

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/stretchr/testify/assert"
)

func TestRawReplicaProposeNoReplica(t *testing.T) {
	rootDir := path.Join(os.TempDir(), "replicas")

	// store
	store := replica.NewWALStorage(path.Join(rootDir, "1"))
	err := store.Open()
	assert.NoError(t, err)
	defer os.RemoveAll(rootDir)
	defer store.Close()

	// replica1
	rc := replica.NewRawReplica(1, "test", replica.WithStorage(store))
	rc.BecomeLeader()
	_, err = rc.Propose([]byte("hello"))
	assert.NoError(t, err)

	lastLog, err := rc.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), lastLog)
}

func TestRawReplicaProposeThreeReplica(t *testing.T) {
	rootDir := path.Join(os.TempDir(), "replicas")

	// store

	replicas := []uint64{1, 2, 3}

	leaderID := uint64(1)

	// 1
	trans1 := &testRawTransport{
		nodeID:    1,
		leaderID:  leaderID,
		serverMap: make(map[uint64]*replica.RawReplica),
	}
	store := replica.NewWALStorage(path.Join(rootDir, "1"))
	err := store.Open()
	assert.NoError(t, err)
	defer os.RemoveAll(rootDir)
	defer store.Close()

	// 2
	trans2 := &testRawTransport{
		nodeID:    2,
		leaderID:  leaderID,
		serverMap: make(map[uint64]*replica.RawReplica),
	}
	store2 := replica.NewWALStorage(path.Join(rootDir, "2"))
	err = store2.Open()
	assert.NoError(t, err)
	defer store2.Close()

	// 3
	store3 := replica.NewWALStorage(path.Join(rootDir, "3"))
	err = store3.Open()
	assert.NoError(t, err)
	defer store3.Close()
	trans3 := &testRawTransport{
		nodeID:    3,
		leaderID:  leaderID,
		serverMap: make(map[uint64]*replica.RawReplica),
	}

	// replica1
	rc := replica.NewRawReplica(1, "test", replica.WithStorage(store), replica.WithReplicas(replicas), replica.WithTransport(trans1))
	rc.BecomeLeader()

	// replica2
	rc2 := replica.NewRawReplica(2, "test", replica.WithStorage(store2), replica.WithReplicas(replicas), replica.WithTransport(trans2))
	rc2.BecomeFollower(leaderID)

	// replica3
	rc3 := replica.NewRawReplica(3, "test", replica.WithStorage(store3), replica.WithReplicas(replicas), replica.WithTransport(trans3))
	rc3.BecomeFollower(leaderID)

	trans1.serverMap[1] = rc
	trans1.serverMap[2] = rc2
	trans1.serverMap[3] = rc3

	trans2.serverMap[1] = rc
	trans2.serverMap[2] = rc2
	trans2.serverMap[3] = rc3

	trans3.serverMap[1] = rc
	trans3.serverMap[2] = rc2
	trans3.serverMap[3] = rc3

	_, err = rc.Propose([]byte("hello"))
	assert.NoError(t, err)

	logs, err := rc.GetLogs(1, 0, 10)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(logs))
	assert.Equal(t, []byte("hello"), logs[0].Data)

}

type testRawTransport struct {
	nodeID    uint64 // 当前id
	leaderID  uint64 // 领导id
	serverMap map[uint64]*replica.RawReplica
}

func (t *testRawTransport) SendSyncNotify(toNodeIDs []uint64, r *replica.SyncNotify) {
	fmt.Println("toNodeIDs----->", toNodeIDs)
	for _, toNodeID := range toNodeIDs {
		go func(nID uint64) {
			_ = t.serverMap[nID].RequestSyncLogs(r)
		}(toNodeID)

	}
}

func (t *testRawTransport) SyncLog(leaderID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	fmt.Println("SyncLog----->", leaderID, r)
	logs, err := t.serverMap[t.leaderID].SyncLogs(t.nodeID, r.StartLogIndex, r.Limit)
	if err != nil {
		return nil, err
	}
	return &replica.SyncRsp{
		Logs: logs,
	}, nil
}
