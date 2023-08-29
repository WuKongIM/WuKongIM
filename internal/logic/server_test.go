package logic

import (
	"os"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/stretchr/testify/assert"
)

func TestServerNodeConnAndClose(t *testing.T) {
	opts := options.New()
	s := NewServer("tcp://127.0.0.1:0", opts)
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	nodeID := "node1@gateway"
	cli := client.New(s.Addr().String(), client.WithUID(nodeID))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()
	node := s.logic.gatewayManager.get(nodeID)

	assert.NotNil(t, node)
	// assert.Equal(t, nodeID, node.gatewayID())

}

func TestClientAuthAndClose(t *testing.T) {
	opts := options.New()
	opts.RootDir = os.TempDir()
	opts.DataDir = os.TempDir()
	s := NewServer("tcp://127.0.0.1:0", opts)
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	nodeID := "node1@gateway"
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

	_, err = cli.Request("/gateway/client/auth", data)
	assert.NoError(t, err)

	// node := s.gatewayManager.get(nodeID)
	// assert.Equal(t, 1, len(node.getAllClientConn()))

	clientCloseReq := &pb.ClientCloseReq{
		Uid:    "test",
		ConnID: 1,
	}
	data, err = clientCloseReq.Marshal()
	assert.NoError(t, err)
	_, err = cli.Request("/gateway/client/close", data)
	assert.NoError(t, err)

	// assert.Equal(t, 0, len(node.getAllClientConn()))
}

func TestNodeReset(t *testing.T) {
	opts := options.New()
	opts.RootDir = os.TempDir()
	opts.DataDir = os.TempDir()
	s := NewServer("tcp://127.0.0.1:0", opts)
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	nodeID := "node1@gateway"
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

	_, err = cli.Request("/gateway/client/auth", data)
	assert.NoError(t, err)

	// node := s.gatewayManager.get(nodeID)
	// assert.Equal(t, 1, len(node.getAllClientConn()))

	resetReq := &pb.ResetReq{}
	resetReq.NodeID = nodeID
	resetData, _ := resetReq.Marshal()
	_, err = cli.Request("/gateway/reset", resetData)
	assert.NoError(t, err)

	// assert.Equal(t, 0, len(node.getAllClientConn()))
}
