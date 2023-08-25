package gateway

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"go.uber.org/zap"
)

type nodeClient struct {
	cli    *client.Client
	nodeID string
	wklog.Log
	pipeline *dataPipeline
}

func newNodeClient(opts *nodeClientOptions) *nodeClient {
	cli := client.New(opts.addr, client.WithUID(opts.nodeID))
	pipeline := newDataPipeline(opts.nodeMaxTransmissionCapacity, opts.nodeID, cli)
	return &nodeClient{
		cli:      cli,
		nodeID:   opts.nodeID,
		pipeline: pipeline,
		Log:      wklog.NewWKLog(fmt.Sprintf("nodeClient[%s]", opts.nodeID)),
	}
}

func (n *nodeClient) start() error {
	n.loopConnect()
	n.pipeline.start()
	return nil
}

func (n *nodeClient) stop() {
	n.pipeline.stop()
	err := n.cli.Close()
	if err != nil {
		n.Debug("close is error", zap.Error(err))
	}

}

func (n *nodeClient) append(data []byte) (int, error) {
	return n.pipeline.append(data)
}

func (n *nodeClient) loopConnect() {
	go func() {
		for {
			err := n.cli.Connect()
			if err != nil {
				n.Debug("connect is error", zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return
		}
	}()
}

type nodeClientOptions struct {
	nodeMaxTransmissionCapacity int
	addr                        string
	nodeID                      string
}

func newNodeClientOptions() *nodeClientOptions {
	return &nodeClientOptions{
		nodeMaxTransmissionCapacity: 1024 * 1024 * 1024,
	}
}
