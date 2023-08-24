package gateway

import (
	"hash/crc32"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type dataPipeline struct {
	ringBuffer *RingBuffer

	sync.RWMutex

	triggerChan chan struct{}
	nodeClient  *client.Client
	wklog.Log
	stopped                     chan struct{}
	doneChan                    chan struct{}
	nodeMaxTransmissionCapacity int
	flushing                    atomic.Bool
}

func newDataPipeline(nodeMaxTransmissionCapacity int) *dataPipeline {
	return &dataPipeline{
		ringBuffer:                  &RingBuffer{},
		nodeMaxTransmissionCapacity: nodeMaxTransmissionCapacity,
		triggerChan:                 make(chan struct{}, 100),
		Log:                         wklog.NewWKLog("dataPipeline"),
		stopped:                     make(chan struct{}),
		doneChan:                    make(chan struct{}),
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
					d.Error("fail to flushToNode", zap.Error(err))
				}
			}
		case <-d.triggerChan:
			d.RLock()
			bufLen := d.ringBuffer.Len()
			d.RUnlock()
			for bufLen > 0 {
				err = d.flushToNode()
				if err != nil {
					d.Error("fail to flushToNode", zap.Error(err))
					time.Sleep(time.Millisecond * 100)
				}
				d.RLock()
				bufLen = d.ringBuffer.Len()
				d.RUnlock()
			}
		case <-d.stopped:
			return
		}
	}
}

func (d *dataPipeline) stop() {
	d.stopped <- struct{}{}
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
	encodeData := d.encode(data)
	resp, err := d.nodeClient.Request("/gateway/conn/write", encodeData)
	if err != nil {
		d.Error("fail to flushToNode", zap.Error(err))
		return err
	}
	if resp.Status != proto.Status_OK {
		d.Error("fail to flushToNode", zap.Error(err), zap.Int32("status", int32(resp.Status)))
		return err
	}
	_, err = d.ringBuffer.Discard(len(data))
	if err != nil {
		d.Warn("fail to discard", zap.Error(err))
	}
	return nil
}

func (d *dataPipeline) encode(data []byte) []byte {
	encoder := wkproto.NewEncoder()
	encoder.WriteUint32(uint32(len(data)))
	crc := crc32.ChecksumIEEE(data)
	encoder.WriteUint32(crc)
	encoder.WriteBytes(data)
	return encoder.Bytes()
}
