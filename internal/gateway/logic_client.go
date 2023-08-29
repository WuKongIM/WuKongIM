package gateway

import (
	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIM/internal/pb"
)

// type nodeStatus int

// const (
// 	nodeStatusWaitConnect nodeStatus = iota
// 	nodeStatusConnected
// 	nodeStatusReady
// )

// type nodeClient struct {
// 	cli    *client.Client
// 	nodeID string
// 	wklog.Log
// 	pipeline *dataPipeline

// 	status nodeStatus

// 	logic logicclient.Client
// }

// func newNodeClient(opts *nodeClientOptions, logic logicclient.Client) *nodeClient {
// 	cli := client.New(opts.addr, client.WithUID(opts.nodeID))
// 	nc := &nodeClient{
// 		cli:    cli,
// 		nodeID: opts.nodeID,
// 		Log:    wklog.NewWKLog(fmt.Sprintf("nodeClient[%s]", opts.nodeID)),
// 		logic:  logic,
// 	}
// 	nc.pipeline = newDataPipeline(opts.nodeMaxTransmissionCapacity, opts.nodeID, nc.deliverData)
// 	return nc
// }

// func (n *nodeClient) start() error {
// 	n.loopConnect()
// 	n.pipeline.start()
// 	return nil
// }

// func (n *nodeClient) stop() {
// 	n.pipeline.stop()
// 	err := n.cli.Close()
// 	if err != nil {
// 		n.Debug("close is error", zap.Error(err))
// 	}

// }

// func (n *nodeClient) append(data []byte) (int, error) {
// 	return n.pipeline.append(data)
// }

// func (n *nodeClient) loopConnect() {
// 	var err error
// 	go func() {
// 		for {
// 			switch n.status {
// 			case nodeStatusWaitConnect:
// 				err = n.cli.Connect()
// 				if err != nil {
// 					n.Debug("connect is error", zap.Error(err))
// 					time.Sleep(time.Millisecond * 500)
// 					continue
// 				}
// 				n.status = nodeStatusConnected
// 			case nodeStatusConnected:
// 				err = n.requestNodeReset()
// 				if err != nil {
// 					n.Debug("requestNodeReset is error", zap.Error(err))
// 					time.Sleep(time.Millisecond * 500)
// 					continue
// 				}
// 				n.status = nodeStatusReady
// 			case nodeStatusReady:
// 				return
// 			}
// 		}
// 	}()
// }

// func (n *nodeClient) requestNodeReset() error {

// 	req := &pb.ResetReq{
// 		NodeID: n.nodeID,
// 	}
// 	data, _ := req.Marshal()
// 	resp, err := n.cli.Request("/node/noderest", data)
// 	if err != nil {
// 		return err
// 	}
// 	if resp.Status != proto.Status_OK {
// 		return fmt.Errorf("resp status is not ok")
// 	}
// 	return nil
// }

// func (n *nodeClient) deliverData(data []byte) error {
// 	n.logic.DeliverData(data)
// 	return nil
// }

// type nodeClientOptions struct {
// 	nodeMaxTransmissionCapacity int
// 	addr                        string
// 	nodeID                      string
// }

// func newNodeClientOptions() *nodeClientOptions {
// 	return &nodeClientOptions{
// 		nodeMaxTransmissionCapacity: 1024 * 1024 * 1024,
// 	}
// }

// type NodeClient interface {
// 	NodeID() string
// 	Append(data []byte) (int, error)
// }

type logicClient struct {
	cli logicclient.Client
	g   *Gateway
}

func newLogicClient(cli logicclient.Client, g *Gateway) *logicClient {
	cli.ListenWriteFromLogic(g.processor.OnWriteFromLogic)
	return &logicClient{
		cli: cli,
		g:   g,
	}
}

func (n *logicClient) ClientAuth(req *pb.AuthReq) (*pb.AuthResp, error) {

	return n.cli.ClientAuth(req)
}

func (n *logicClient) Write(conn *pb.Conn, data []byte) (int, error) {

	return n.cli.WriteToLogic(conn, data)
}

func (n *logicClient) start() {
	n.cli.Start()
}
func (n *logicClient) stop() {
	n.cli.Stop()
}
