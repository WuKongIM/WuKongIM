package raftgroup

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type nodeClient struct {
	client *client.Client
	nodeID uint64
	wklog.Log
}

func newNodeClient(nodeID uint64, addr string) *nodeClient {
	client := client.New(addr, client.WithUID(strconv.FormatUint(nodeID, 10)))
	return &nodeClient{
		client: client,
		nodeID: nodeID,
		Log:    wklog.NewWKLog(fmt.Sprintf("nodeClient[%d]", nodeID)),
	}
}

func (n *nodeClient) connect() error {
	return n.client.Connect()
}

func (n *nodeClient) close() {
	n.client.Close()
}

func (n *nodeClient) needConnect() bool {
	return n.client.ConnectStatus() == client.DISCONNECTED || n.client.ConnectStatus() == client.CLOSED
}

func (p *nodeClient) SendCMD(cmd *CMDReq) (*CMDResp, error) {
	cmdData, err := cmd.Marshal()
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Request("/cmd", cmdData)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, ErrCMDRespStatus
	}
	cmdResp := &CMDResp{}
	err = cmdResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return cmdResp, nil
}

func (p *nodeClient) Send(ctx context.Context, m *RaftMessageReq) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}

	return p.client.SendNoFlush(&proto.Message{
		MsgType:   MsgTypeRaftMessage,
		Content:   data,
		Timestamp: uint64(time.Now().Unix()),
	})
}

func (p *nodeClient) Flush() error {

	p.client.Flush()

	return nil
}
