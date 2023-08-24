package gateway_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"github.com/stretchr/testify/assert"
)

func TestGatewayStartAndStop(t *testing.T) {
	opts := gateway.NewOptions()
	g := gateway.New(opts)
	err := g.Start()
	assert.NoError(t, err)

	err = g.Stop()
	assert.NoError(t, err)
}

func TestGatewayAuth(t *testing.T) {
	opts := gateway.NewOptions()
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
}

type testLogic struct {
}

func (t *testLogic) Auth(req *logicclient.AuthReq) (*logicclient.AuthResp, error) {

	return &logicclient.AuthResp{
		DeviceLevel: 1,
	}, nil
}
