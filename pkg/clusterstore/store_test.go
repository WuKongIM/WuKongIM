package clusterstore_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/stretchr/testify/assert"
)

func TestAddSubscribers(t *testing.T) {
	s1, t1, s2, t2 := newTestClusterServerGroupTwo()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	// 节点1添加订阅者
	err := s1.AddSubscribers(channelID, channelType, []string{"123", "34"})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 100)

	// 节点2获取订阅者
	subscribers, err := s2.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"123", "34"}, subscribers)

}

func TestAppendMessage(t *testing.T) {
	s1, t1, s2, t2 := newTestClusterServerGroupTwo()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	channelID := "test"
	var channelType uint8 = 2

	// 节点1添加消息
	msg := &testMessage{
		data: []byte("hello"),
	}
	err := s1.AppendMessage(channelID, channelType, msg)
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	// 节点2获取消息
	messages, err := s2.LoadNextRangeMsgs(channelID, channelType, 0, 0, 10)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(messages))

	assert.Equal(t, msg.data, messages[0].(*testMessage).data)
}

func newTestClusterServerGroupTwo() (*clusterstore.Store, *testClusterServer, *clusterstore.Store, *testClusterServer) {
	initNodes := map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
	}

	opts1 := clusterstore.NewOptions(1)
	opts1.DecodeMessageFnc = func(msg []byte) (wkstore.Message, error) {
		var err error
		m := &testMessage{}
		err = m.Decode(msg)
		return m, err
	}
	opts1.DataDir = path.Join(os.TempDir(), "cluster", "1")
	s1 := clusterstore.NewStore(opts1)
	ts1 := newTestClusterServer(1, cluster.WithListenAddr("127.0.0.1:10001"), cluster.WithMessageLogStorage(s1.GetMessageShardLogStorage()), cluster.WithInitNodes(initNodes), cluster.WithOnChannelMetaApply(func(channelID string, channelType uint8, logs []replica.Log) error {

		return s1.OnMetaApply(channelID, channelType, logs)
	}))
	opts1.Cluster = ts1

	opts2 := clusterstore.NewOptions(2)
	opts2.DecodeMessageFnc = func(msg []byte) (wkstore.Message, error) {
		var err error
		m := &testMessage{}
		err = m.Decode(msg)
		return m, err
	}
	opts2.DataDir = path.Join(os.TempDir(), "cluster", "2")
	s2 := clusterstore.NewStore(opts2)
	ts2 := newTestClusterServer(2, cluster.WithListenAddr("127.0.0.1:10002"), cluster.WithMessageLogStorage(s2.GetMessageShardLogStorage()), cluster.WithInitNodes(initNodes), cluster.WithOnChannelMetaApply(func(channelID string, channelType uint8, logs []replica.Log) error {

		return s2.OnMetaApply(channelID, channelType, logs)
	}))
	opts2.Cluster = ts2

	err := s1.Open()
	if err != nil {
		panic(err)
	}

	err = s2.Open()
	if err != nil {
		panic(err)
	}

	err = ts1.Start()
	if err != nil {
		panic(err)
	}
	err = ts2.Start()
	if err != nil {
		panic(err)
	}
	return s1, ts1, s2, ts2
}

type testClusterServer struct {
	server *cluster.Server
}

func newTestClusterServer(nodeID uint64, opts ...cluster.Option) *testClusterServer {

	rootDir := path.Join(os.TempDir(), "cluster")
	dataDir := path.Join(rootDir, fmt.Sprintf("%d", nodeID))
	fmt.Println("dataDir--->", dataDir)
	newOpts := make([]cluster.Option, 0)
	newOpts = append(newOpts, cluster.WithDataDir(dataDir))
	newOpts = append(newOpts, opts...)
	newOpts = append(newOpts, cluster.WithHeartbeat(200*time.Millisecond))
	return &testClusterServer{
		server: cluster.NewServer(
			nodeID,
			newOpts...,
		),
	}
}

func (t *testClusterServer) Start() error {
	return t.server.Start()
}

func (t *testClusterServer) Stop() {
	t.server.Stop()

	os.RemoveAll(t.server.Options().DataDir)
}

func (t *testClusterServer) ProposeMetaToChannel(channelID string, channelType uint8, data []byte) error {

	return t.server.ProposeMetaToChannel(channelID, channelType, data)
}

func (t *testClusterServer) ProposeMessageToChannel(channelID string, channelType uint8, data []byte) error {

	return t.server.ProposeMessageToChannel(channelID, channelType, data)
}

func (t *testClusterServer) ProposeMessagesToChannel(channelID string, channelType uint8, data [][]byte) error {
	return nil
}

type testMessage struct {
	seq  uint32
	data []byte
}

func (t *testMessage) GetMessageID() int64 {
	return 0
}
func (t *testMessage) SetSeq(seq uint32) {
	t.seq = seq
}
func (t *testMessage) GetSeq() uint32 {
	return t.seq
}
func (t *testMessage) Encode() []byte {
	return wkstore.EncodeMessage(t.seq, t.data)
}
func (t *testMessage) Decode(msg []byte) error {
	var err error
	t.seq, t.data, err = wkstore.DecodeMessage(msg)
	return err
}
