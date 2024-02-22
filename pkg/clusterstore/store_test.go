package clusterstore_test

import (
	"fmt"
	"os"
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
)

func newTestClusterServerGroupThree() (*clusterstore.Store, *testClusterServer, *clusterstore.Store, *testClusterServer, *clusterstore.Store, *testClusterServer) {
	initNodes := map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
		3: "127.0.0.1:10003",
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
	ts1 := newTestClusterServer(1, cluster.WithAddr("127.0.0.1:10001"), cluster.WithMessageLogStorage(s1.GetMessageShardLogStorage()), cluster.WithInitNodes(initNodes), cluster.WithOnSlotApply(func(slotId uint32, logs []replica.Log) error {

		return s1.OnMetaApply(slotId, logs)
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
	ts2 := newTestClusterServer(2, cluster.WithAddr("127.0.0.1:10002"), cluster.WithMessageLogStorage(s2.GetMessageShardLogStorage()), cluster.WithInitNodes(initNodes), cluster.WithOnSlotApply(func(slotId uint32, logs []replica.Log) error {

		return s2.OnMetaApply(slotId, logs)
	}))
	opts2.Cluster = ts2

	opts3 := clusterstore.NewOptions(3)
	opts3.DecodeMessageFnc = func(msg []byte) (wkstore.Message, error) {
		var err error
		m := &testMessage{}
		err = m.Decode(msg)
		return m, err
	}
	opts3.DataDir = path.Join(os.TempDir(), "cluster", "3")
	s3 := clusterstore.NewStore(opts3)
	ts3 := newTestClusterServer(3, cluster.WithAddr("127.0.0.1:10003"), cluster.WithMessageLogStorage(s3.GetMessageShardLogStorage()), cluster.WithInitNodes(initNodes), cluster.WithOnSlotApply(func(slotId uint32, logs []replica.Log) error {

		return s3.OnMetaApply(slotId, logs)
	}))
	opts3.Cluster = ts3

	err := s1.Open()
	if err != nil {
		panic(err)
	}

	err = s2.Open()
	if err != nil {
		panic(err)
	}

	err = s3.Open()
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

	err = ts3.Start()
	if err != nil {
		panic(err)
	}
	return s1, ts1, s2, ts2, s3, ts3
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
	return &testClusterServer{
		server: cluster.New(
			nodeID,
			cluster.NewOptions(newOpts...),
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

func (t *testClusterServer) ProposeChannelMeta(channelID string, channelType uint8, data []byte) error {

	return t.server.ProposeChannelMeta(channelID, channelType, data)
}

func (t *testClusterServer) ProposeChannelMessage(channelID string, channelType uint8, data []byte) (uint64, error) {

	return t.server.ProposeChannelMessage(channelID, channelType, data)
}

func (t *testClusterServer) ProposeChannelMessages(channelID string, channelType uint8, data [][]byte) ([]uint64, error) {
	return t.server.ProposeChannelMessages(channelID, channelType, data)
}
func (t *testClusterServer) ProposeToSlot(slotId uint32, data []byte) error {
	return t.server.ProposeToSlot(slotId, data)
}

type testMessage struct {
	seq  uint32
	data []byte
	term uint64
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

func (t *testMessage) SetTerm(term uint64) {
	t.term = term
}

func (t *testMessage) GetTerm() uint64 {
	return t.term
}

func (t *testMessage) Encode() []byte {
	return wkstore.EncodeMessage(t.seq, t.term, t.data)
}
func (t *testMessage) Decode(msg []byte) error {
	var err error
	t.seq, t.term, t.data, err = wkstore.DecodeMessage(msg)
	return err
}
