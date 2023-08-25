package gateway

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	sproto "github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type dataPipeline struct {
	ringBuffer *RingBuffer

	sync.RWMutex

	triggerChan chan struct{}
	nodeClient  *client.Client
	wklog.Log
	stoppedChan                 chan struct{}
	stopped                     bool
	doneChan                    chan struct{}
	nodeMaxTransmissionCapacity int
	flushing                    atomic.Bool
}

func newDataPipeline(nodeMaxTransmissionCapacity int, nodeID string, nodeClient *client.Client) *dataPipeline {
	return &dataPipeline{
		ringBuffer:                  &RingBuffer{},
		nodeMaxTransmissionCapacity: nodeMaxTransmissionCapacity,
		triggerChan:                 make(chan struct{}, 100),
		Log:                         wklog.NewWKLog(fmt.Sprintf("dataPipeline[%s]", nodeID)),
		stoppedChan:                 make(chan struct{}),
		doneChan:                    make(chan struct{}),
		nodeClient:                  nodeClient,
		stopped:                     false,
	}
}

func (d *dataPipeline) append(data []byte) (int, error) {
	d.Lock()
	defer d.Unlock()
	n, err := d.ringBuffer.Write(data)
	if err != nil {
		return n, err
	}
	d.triggerChan <- struct{}{}
	return n, nil
}

func (d *dataPipeline) start() {
	go d.run()
}

func (d *dataPipeline) run() {
	tick := time.NewTicker(time.Second)
	var err error
	defer d.onStop()
	for {
		select {
		case <-tick.C:
			if !d.flushing.Load() {
				err = d.flushToNode()
				if err != nil {
					d.Warn("fail to flushToNode", zap.Error(err))
				}
			}
		case <-d.triggerChan:
			d.RLock()
			bufLen := d.ringBuffer.Len()
			d.RUnlock()
			for bufLen > 0 && !d.stopped {
				err = d.flushToNode()
				if err != nil {
					d.Warn("fail to flushToNode", zap.Error(err))
					time.Sleep(time.Millisecond * 100)
				}
				d.RLock()
				bufLen = d.ringBuffer.Len()
				d.RUnlock()
			}
		case <-d.stoppedChan:
			return
		}
	}
}

func (d *dataPipeline) stop() {
	d.stopped = true
	d.stoppedChan <- struct{}{}
	<-d.doneChan
}

func (d *dataPipeline) onStop() {
	close(d.doneChan)
}

func (d *dataPipeline) flushToNode() error {
	d.Lock()
	defer d.Unlock()

	d.flushing.Store(true)
	defer d.flushing.Store(false)

	head, tail := d.ringBuffer.Peek(d.nodeMaxTransmissionCapacity)
	if len(head) == 0 && len(tail) == 0 {
		return nil
	}

	data := append(head, tail...)
	resp, err := d.nodeClient.Request("/node/conn/write", data)
	if err != nil {
		d.Warn("fail to flushToNode", zap.Error(err))
		return err
	}
	if resp.Status != sproto.Status_OK {
		d.Warn("fail to flushToNode", zap.Error(err), zap.Int32("status", int32(resp.Status)))
		return err
	}
	_, err = d.ringBuffer.Discard(len(data))
	if err != nil {
		d.Warn("fail to discard", zap.Error(err))
	}
	return nil
}
