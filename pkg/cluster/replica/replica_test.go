package replica_test

import (
	"fmt"
	"os"
	"path"
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
	s1 := replica.New(1, shardNo, replica.WithReplicas([]uint64{2, 3}), replica.WithTransport(trans), replica.WithDataDir(path.Join(rootDir, "1")))
	trans.leaderServer = s1
	s1.SetLeaderID(1)
	// start s1
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	s2 := replica.New(2, shardNo, replica.WithTransport(trans), replica.WithDataDir(path.Join(rootDir, "2")))
	// start s2
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	s3 := replica.New(3, shardNo, replica.WithTransport(trans), replica.WithDataDir(path.Join(rootDir, "3")))
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
	logs, err := s2.GetLogs(1, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs))
	firstLog := logs[0]
	assert.Equal(t, []byte("hello"), firstLog.Data)

	// 验证节点3的数据是否一致
	logs, err = s3.GetLogs(1, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs))
	firstLog = logs[0]
	assert.Equal(t, []byte("hello"), firstLog.Data)

}

type testTransport struct {
	leaderServer *replica.Replica
	serverMap    map[uint64]*replica.Replica
}

func (t *testTransport) SendSyncNotify(toNodeID uint64, shardNo string, r *replica.SyncNotify) error {
	t.serverMap[toNodeID].TriggerHandleSyncNotify(r)
	return nil
}

func (t *testTransport) SyncLog(toNodeID uint64, r *replica.SyncReq) (*replica.SyncRsp, error) {
	resp := &replica.SyncRsp{}
	logs, err := t.leaderServer.GetLogs(r.StartLogIndex, r.Limit)
	if err != nil {
		return nil, err
	}
	resp.Logs = logs
	return resp, nil
}
