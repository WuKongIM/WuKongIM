package gateway_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/logicclient/pb"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"github.com/stretchr/testify/assert"
)

func TestGatewayStartAndStop(t *testing.T) {
	opts := gateway.NewOptions()
	opts.Addr = "tcp://127.0.0.1:0"
	opts.WSAddr = "ws://127.0.0.1:0"
	g := gateway.New(opts)
	err := g.Start()
	assert.NoError(t, err)

	err = g.Stop()
	assert.NoError(t, err)
}

func TestGatewayAuth(t *testing.T) {
	opts := gateway.NewOptions()
	opts.Addr = "tcp://127.0.0.1:0"
	opts.WSAddr = "ws://127.0.0.1:0"
	g := gateway.New(opts)
	err := g.Start()
	assert.NoError(t, err)

	g.SetLogic(&testLogic{})

	defer func() {
		_ = g.Stop()
	}()

	// send auth
	cli := wksdk.NewClient(g.TCPAddr().String(), wksdk.WithUID("test"), wksdk.WithToken("test"))
	err = cli.Connect()
	assert.NoError(t, err)

	defer func() {
		_ = cli.Disconnect()
	}()
}

type testLogic struct {
}

func (t *testLogic) Auth(req *pb.AuthReq) (*pb.AuthResp, error) {

	return &pb.AuthResp{
		DeviceLevel: 1,
	}, nil
}
