package transporter

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/WuKongIMGoSDK/pkg/wksdk"
	"go.uber.org/zap"
)

type NodeClient struct {
	cli    *wksdk.Client
	nodeID uint64
	addr   string
	wklog.Log
	onRecv func(msg *wksdk.Message)
}

func NewNodeClient(nodeID uint64, addr string, token string, onRecv func(msg *wksdk.Message)) *NodeClient {
	cli := wksdk.NewClient(addr, wksdk.WithUID(wkutil.GenUUID()), wksdk.WithToken(token))
	cli.OnMessage(onRecv)
	return &NodeClient{
		nodeID: nodeID,
		addr:   addr,
		cli:    cli,
		Log:    wklog.NewWKLog("NodeClient"),
		onRecv: onRecv,
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
		err = n.cli.SendMessageAsync(data, wkproto.Channel{
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
