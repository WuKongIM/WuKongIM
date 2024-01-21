package replica_test

import (
	"fmt"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/stretchr/testify/assert"
)

func TestServerAppendLog(t *testing.T) {

	var shardNo string = "1"
	rootDir := path.Join(os.TempDir(), "replicas")
	defer os.RemoveAll(rootDir)

	fmt.Println("rootDir--->", rootDir)

	trans := &testTransport{
		serverMap: make(map[uint64]*replica.Replica),
	}
	s1 := replica.New(1, shardNo, replica.WithReplicas([]uint64{2, 3}), replica.WithTransport(trans))
	trans.leaderServer = s1
	s1.SetLeaderID(1)
	// start s1
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	s2 := replica.New(2, shardNo, replica.WithTransport(trans))
	// start s2
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	s3 := replica.New(3, shardNo, replica.WithTransport(trans))
	// start s3
	err = s3.Start()
	assert.NoError(t, err)
	defer s3.Stop()

	trans.serverMap[1] = s1
	trans.serverMap[2] = s2
	trans.serverMap[3] = s3

	// append log
	err = s1.AppendLog(replica.Log{
		Index: 1,
		Data:  []byte("hello"),
	})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200) // 等待其他节点同步完毕

	// 验证节点2的数据是否一致
	logs, err := s2.GetLogs(1, 0, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs))
	firstLog := logs[0]
	assert.Equal(t, []byte("hello"), firstLog.Data)

	// 验证节点3的数据是否一致
	logs, err = s3.GetLogs(1, 0, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs))
	firstLog = logs[0]
	assert.Equal(t, []byte("hello"), firstLog.Data)

}

func TestServerProposeAndApply(t *testing.T) {

	var shardNo string = "1"
	rootDir := path.Join(os.TempDir(), "replicas")
	defer os.RemoveAll(rootDir)

	fmt.Println("rootDir--->", rootDir)

	var wg sync.WaitGroup

	wg.Add(3)

	trans := &testTransport{
		serverMap: make(map[uint64]*replica.Replica),
	}
	onApply := func(logs []replica.Log) (uint64, error) {
		if string(logs[0].Data) == "hello" {
			wg.Done()
		}
		return logs[len(logs)-1].Index, nil
	}
	store1 := replica.NewWALStorage(path.Join(rootDir, "1"))
	err := store1.Open()
	assert.NoError(t, err)
	defer store1.Close()
	s1 := replica.New(1, shardNo, replica.WithReplicas([]uint64{2, 3}), replica.WithStorage(store1), replica.WithTransport(trans), replica.WithOnApply(onApply))
	trans.leaderServer = s1
	s1.SetLeaderID(1)
	// start s1
	err = s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	store2 := replica.NewWALStorage(path.Join(rootDir, "2"))
	err = store2.Open()
	assert.NoError(t, err)
	defer store2.Close()
	s2 := replica.New(2, shardNo, replica.WithTransport(trans), replica.WithStorage(store2), replica.WithOnApply(onApply))
	// start s2
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	store3 := replica.NewWALStorage(path.Join(rootDir, "3"))
	err = store3.Open()
	assert.NoError(t, err)
	defer store3.Close()
	s3 := replica.New(3, shardNo, replica.WithTransport(trans), replica.WithStorage(store3), replica.WithOnApply(onApply))
	// start s3
	err = s3.Start()
	assert.NoError(t, err)
	defer s3.Stop()

	trans.serverMap[1] = s1
	trans.serverMap[2] = s2
	trans.serverMap[3] = s3

	// propose data
	err = s1.Propose([]byte("hello"))
	assert.NoError(t, err)
	wg.Wait()

}

type testTransport struct {
	leaderServer itestReplica
	serverMap    map[uint64]*replica.Replica
}

func (t *testTransport) SendSyncNotify(toNodeIDs []uint64, r *replica.SyncNotify) {
	for _, toNodeID := range toNodeIDs {
		t.serverMap[toNodeID].TriggerHandleSyncNotify()
	}
}

func (t *testTransport) SyncLog(toNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp := &replica.SyncRsp{}
	logs, err := t.leaderServer.GetLogs(r.StartLogIndex, 0, r.Limit)
	if err != nil {
		return nil, err
	}
	resp.Logs = logs
	return resp, nil
}

type itestReplica interface {
	GetLogs(startLogIndex, endLogIndex uint64, limit uint32) ([]replica.Log, error)
}
