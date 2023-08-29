package logicclient

import (
	"fmt"
	"time"

	bproto "github.com/WuKongIM/WuKongIM/internal/gateway/proto"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type clientStatus int

const (
	clientStatusWaitConnect clientStatus = iota
	clientStatusConnected
	clientStatusReady
)

type RemoteClient struct {
	cli       *client.Client
	gatewayID string
	status    clientStatus
	wklog.Log
	stopped bool

	pipeline *wkutil.DataPipeline

	blockSeq   atomic.Uint64
	blockProto *bproto.Proto

	tmpBuff           []byte
	writeFromLogicFnc func(conn *pb.Conn, data []byte) error

	connectedChan chan struct{}
}

func NewRemoteClient(gatewayID string, logicAddr string, nodeMaxTransmissionCapacity int) *RemoteClient {
	cli := client.New(logicAddr, client.WithUID(gatewayID))
	r := &RemoteClient{
		cli:           cli,
		gatewayID:     gatewayID,
		stopped:       false,
		blockProto:    bproto.New(),
		tmpBuff:       make([]byte, 0),
		Log:           wklog.NewWKLog(fmt.Sprintf("RemoteClient[%s]", gatewayID)),
		connectedChan: make(chan struct{}),
	}

	cli.Route("/logic/client/write", r.handleLogicClientWrite)
	r.pipeline = wkutil.NewDataPipeline(nodeMaxTransmissionCapacity, r.deliverData)
	return r

}

func (r *RemoteClient) Start() {
	go r.loopConnect()
	<-r.connectedChan
	r.pipeline.Start()

}
func (r *RemoteClient) Stop() {
	r.stopped = true
	r.pipeline.Stop()
	r.cli.Close()
}

func (r *RemoteClient) handleLogicClientWrite(c *client.Context) {

	r.tmpBuff = append(r.tmpBuff, c.Body()...)

	block, size, err := r.blockProto.Decode(r.tmpBuff)
	if err != nil {
		c.WriteErr(err)
		return
	}
	r.tmpBuff = r.tmpBuff[size:]
	if block == nil {
		c.WriteOk()
		return
	}

	err = r.writeFromLogicFnc(block.Conn, block.Data)
	if err != nil {
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (r *RemoteClient) loopConnect() {
	var err error
	go func() {
		for !r.stopped {
			switch r.status {
			case clientStatusWaitConnect:
				err = r.cli.Connect()
				if err != nil {
					r.Debug("connect is error", zap.Error(err))
					time.Sleep(time.Millisecond * 500)
					continue
				}
				r.status = clientStatusConnected
			case clientStatusConnected:
				err = r.requestGatewayReset()
				if err != nil {
					r.Debug("requestGatewayReset is error", zap.Error(err))
					time.Sleep(time.Millisecond * 500)
					continue
				}
				r.status = clientStatusReady
			case clientStatusReady:
				r.connectedChan <- struct{}{}
				return
			}
		}
	}()
}

func (r *RemoteClient) requestGatewayReset() error {
	req := &pb.ResetReq{
		NodeID: r.gatewayID,
	}
	data, _ := req.Marshal()
	resp, err := r.cli.Request("/gateway/reset", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("resp status is not ok")
	}
	return nil
}

func (r *RemoteClient) ClientAuth(req *pb.AuthReq) (*pb.AuthResp, error) {

	fmt.Println("ClientAuth......", req.String())
	data, _ := req.Marshal()
	resp, err := r.cli.Request("/gateway/client/auth", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, ErrAuthFailed
	}
	authResp := &pb.AuthResp{}
	err = authResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return authResp, nil
}

func (r *RemoteClient) WriteToLogic(conn *pb.Conn, data []byte) (int, error) {
	blockData, err := r.blockProto.Encode(&bproto.Block{
		Seq:  r.blockSeq.Inc(),
		Conn: conn,
		Data: data,
	})
	if err != nil {
		r.Warn("Failed to encode the message", zap.Error(err))
		return 0, nil
	}
	if len(blockData) == 0 {
		return 0, nil
	}
	return r.pipeline.Append(blockData)
}

func (r *RemoteClient) ClientClose(req *pb.ClientCloseReq) error {
	data, _ := req.Marshal()
	resp, err := r.cli.Request("/gateway/client/close", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return ErrRespStatus
	}
	return nil
}

func (r *RemoteClient) deliverData(data []byte) error {
	resp, err := r.cli.Request("/gateway/client/write", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return ErrRespStatus
	}
	return nil
}

func (r *RemoteClient) ListenWriteFromLogic(f func(conn *pb.Conn, data []byte) error) {
	r.writeFromLogicFnc = f
}
