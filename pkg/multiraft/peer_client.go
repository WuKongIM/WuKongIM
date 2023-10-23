package multiraft

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type PeerClient struct {
	peer   Peer
	client *client.Client
}

func NewPeerClient(peer Peer) *PeerClient {
	cli := client.New(strings.ReplaceAll(peer.Addr, "tcp://", ""), client.WithUID(fmt.Sprintf("%d", peer.ID)))

	return &PeerClient{
		peer:   peer,
		client: cli,
	}
}

func (p *PeerClient) Connect() error {
	return p.client.Connect()
}

func (p *PeerClient) Close() error {

	return p.client.Close()
}

func (p *PeerClient) NeedConnect() bool {
	return p.client.ConnectStatus() == client.DISCONNECTED || p.client.ConnectStatus() == client.CLOSED
}

func (p *PeerClient) SendCMD(cmd *CMDReq) (*CMDResp, error) {
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

func (p *PeerClient) SendRaftMessage(req *RaftMessageReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := p.client.Request("/raftmessage", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return ErrCMDRespStatus
	}
	return nil
}
