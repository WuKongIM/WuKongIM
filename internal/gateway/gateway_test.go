package gateway_test

import (
	"os"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/logic"
	"github.com/WuKongIM/WuKongIM/internal/options"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"github.com/stretchr/testify/assert"
)

func TestGatewayStartAndStop(t *testing.T) {
	opts := options.New()
	opts.Addr = "tcp://127.0.0.1:0"
	opts.WSAddr = "ws://127.0.0.1:0"
	g := gateway.New(opts)
	err := g.Start()
	assert.NoError(t, err)

	g.Stop()
}

func TestGatewayAuthForSingle(t *testing.T) {

	// --------- gateway start ------------
	opts := options.New()
	opts.Addr = "tcp://127.0.0.1:0"
	opts.WSAddr = "ws://127.0.0.1:0"
	g := gateway.New(opts)
	err := g.Start()
	assert.NoError(t, err)

	defer g.Stop()

	// --------- logic start ------------
	logicOpts := options.New()
	logicOpts.RootDir = os.TempDir()
	logicOpts.DataDir = os.TempDir()
	logicOpts.Logger.Dir = os.TempDir()
	ls := logic.NewServer("tcp://127.0.0.1:0", logicOpts)
	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// --------- connect------------
	cli := wksdk.NewClient(g.TCPAddr().String(), wksdk.WithUID("test"), wksdk.WithToken("test"))
	err = cli.Connect()
	assert.NoError(t, err)

	defer func() {
		_ = cli.Disconnect()
	}()

	ack, err := cli.SendMessage([]byte("hi"), wkproto.Channel{
		ChannelID:   "u1",
		ChannelType: 1,
	})
	assert.NoError(t, err)
	assert.NotNil(t, ack)

}

func TestGatewayAuthForRemote(t *testing.T) {

	// --------- logic start ------------
	logicOpts := options.New()
	logicOpts.Cluster.NodeID = 1
	logicOpts.RootDir = os.TempDir()
	logicOpts.DataDir = os.TempDir()
	logicOpts.Logger.Dir = os.TempDir()
	ls := logic.NewServer("tcp://127.0.0.1:12345", logicOpts)
	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// --------- gateway start ------------
	opts := options.New()
	opts.Addr = "tcp://127.0.0.1:0"
	opts.WSAddr = "ws://127.0.0.1:0"
	opts.RootDir = os.TempDir()
	opts.DataDir = os.TempDir()
	opts.Logger.Dir = os.TempDir()
	opts.Cluster.NodeID = 1
	opts.Cluster.LogicAddr = "127.0.0.1:12345"
	g := gateway.New(opts)
	err = g.Start()
	assert.NoError(t, err)

	defer g.Stop()

	// --------- connect------------
	cli := wksdk.NewClient(g.TCPAddr().String(), wksdk.WithUID("test"), wksdk.WithToken("test"))
	err = cli.Connect()
	assert.NoError(t, err)

	defer func() {
		_ = cli.Disconnect()
	}()

	ack, err := cli.SendMessage([]byte("hi"), wkproto.Channel{
		ChannelID:   "u1",
		ChannelType: 1,
	})
	assert.NoError(t, err)
	assert.NotNil(t, ack)
}
