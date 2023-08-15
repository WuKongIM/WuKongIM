package transporter

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"go.uber.org/zap"
)

type NodeClient struct {
	cli    *wksdk.Client
	nodeID uint64
	addr   string
	wklog.Log
}

func NewNodeClient(nodeID uint64, addr string, token string) *NodeClient {
	cli := wksdk.NewClient(addr, wksdk.WithUID(fmt.Sprintf("%d", nodeID)), wksdk.WithToken(token))
	return &NodeClient{
		nodeID: nodeID,
		addr:   addr,
		cli:    cli,
		Log:    wklog.NewWKLog("NodeClient"),
	}
}

func (n *NodeClient) Connect() error {

	return n.cli.Connect()
}

func (n *NodeClient) Disconnect() error {
	return n.cli.Disconnect()
}

func (n *NodeClient) Send(dataList ...[]byte) error {
	if len(dataList) == 0 {
		return nil
	}
	var err error
	for _, data := range dataList {
		_, err = n.cli.SendMessage(data, wkproto.Channel{
			ChannelID:   "to",
			ChannelType: wkproto.ChannelTypePerson,
		})
		if err != nil {
			n.Error("failed to send", zap.Error(err))
			return err
		}
	}
	return nil

}
