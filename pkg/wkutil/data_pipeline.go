package wkutil

import (
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

var (
	ErrDataNotEnough = errors.New("data not enough")
)

type DataPipeline struct {
	ringBuffer *RingBuffer

	sync.RWMutex

	triggerChan chan struct{}
	wklog.Log
	stoppedChan chan struct{}
	stopped     bool
	doneChan    chan struct{}
	maxPeekByte int
	flushing    atomic.Bool

	deliverDataFunc func(data []byte) error
}

func NewDataPipeline(maxPeekByte int, deliverDataFunc func(data []byte) error) *DataPipeline {
	return &DataPipeline{
		ringBuffer:      &RingBuffer{},
		maxPeekByte:     maxPeekByte,
		triggerChan:     make(chan struct{}, 100),
		Log:             wklog.NewWKLog("dataPipeline"),
		stoppedChan:     make(chan struct{}),
		doneChan:        make(chan struct{}),
		stopped:         false,
		deliverDataFunc: deliverDataFunc,
	}
}

func (d *DataPipeline) Append(data []byte) (int, error) {
	d.Lock()
	defer d.Unlock()
	n, err := d.ringBuffer.Write(data)
	if err != nil {
		return n, err
	}
	d.triggerChan <- struct{}{}
	return n, nil
}

func (d *DataPipeline) Start() {
	go d.run()
}

func (d *DataPipeline) run() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
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

func (d *DataPipeline) Stop() {
	d.stopped = true
	d.stoppedChan <- struct{}{}
	<-d.doneChan
}

func (d *DataPipeline) onStop() {
	close(d.doneChan)
}

func (d *DataPipeline) flushToNode() error {
	d.Lock()
	defer d.Unlock()

	d.flushing.Store(true)
	defer d.flushing.Store(false)

	head, tail := d.ringBuffer.Peek(d.maxPeekByte)
	if len(head) == 0 && len(tail) == 0 {
		return nil
	}

	data := append(head, tail...)
	err := d.deliverDataFunc(data)
	if err != nil {
		if err == ErrDataNotEnough {
			d.Debug("data not enough")
			return nil
		}
		d.Warn("fail to deliver data", zap.Error(err))
		return err
	}
	_, err = d.ringBuffer.Discard(len(data))
	if err != nil {
		d.Warn("fail to discard", zap.Error(err))
	}
	return nil
}
