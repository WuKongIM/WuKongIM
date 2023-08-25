package logic

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/logicclient/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/stretchr/testify/assert"
)

func TestServerNodeConnAndClose(t *testing.T) {
	s := NewServer("tcp://127.0.0.1:0")
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	nodeID := "node1"
	cli := client.New(s.Addr().String(), client.WithUID(nodeID))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()
	node := s.nodeManager.get(nodeID)

	assert.NotNil(t, node)
	assert.Equal(t, nodeID, node.nodeID())

}

func TestClientAuthAndClose(t *testing.T) {
	s := NewServer("tcp://127.0.0.1:0")
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	nodeID := "node1"
	cli := client.New(s.Addr().String(), client.WithUID(nodeID))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

	authReq := &pb.AuthReq{
		ConnID:     1,
		Uid:        "test",
		DeviceFlag: 2,
	}
	data, err := authReq.Marshal()
	assert.NoError(t, err)

	_, err = cli.Request("/client/auth", data)
	assert.NoError(t, err)

	node := s.nodeManager.get(nodeID)
	assert.Equal(t, 1, len(node.getAllClientConn()))

	clientCloseReq := &pb.ClientCloseReq{
		Uid:    "test",
		ConnID: 1,
	}
	data, err = clientCloseReq.Marshal()
	assert.NoError(t, err)
	_, err = cli.Request("/client/close", data)
	assert.NoError(t, err)

	assert.Equal(t, 0, len(node.getAllClientConn()))
}

func TestNodeReset(t *testing.T) {
	s := NewServer("tcp://127.0.0.1:0")
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	nodeID := "node1"
	cli := client.New(s.Addr().String(), client.WithUID(nodeID))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

	authReq := &pb.AuthReq{
		ConnID:     1,
		Uid:        "test",
		DeviceFlag: 2,
	}
	data, err := authReq.Marshal()
	assert.NoError(t, err)

	_, err = cli.Request("/client/auth", data)
	assert.NoError(t, err)

	node := s.nodeManager.get(nodeID)
	assert.Equal(t, 1, len(node.getAllClientConn()))

	resetReq := &pb.ResetReq{}
	resetReq.NodeID = nodeID
	resetData, _ := resetReq.Marshal()
	_, err = cli.Request("/node/noderest", resetData)
	assert.NoError(t, err)

	assert.Equal(t, 0, len(node.getAllClientConn()))
}
