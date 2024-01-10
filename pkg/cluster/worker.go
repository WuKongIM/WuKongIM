package cluster

import (
	"hash/crc32"
	"sync"

	"github.com/lni/goutils/syncutil"
)

type ChannelEventWorkerManager struct {
	s       *Server
	workers []*ChannelEventWorker
}

func NewChannelEventWorkerManager(s *Server) *ChannelEventWorkerManager {
	workers := make([]*ChannelEventWorker, 0, s.opts.ChannelEventWorkerCount)
	for i := 0; i < s.opts.ChannelEventWorkerCount; i++ {
		workers = append(workers, NewChannelEventWorker(s, i))
	}

	return &ChannelEventWorkerManager{
		s:       s,
		workers: workers,
	}
}

func (w *ChannelEventWorkerManager) Start() error {
	for _, worker := range w.workers {
		err := worker.Start()
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *ChannelEventWorkerManager) Stop() {
	for _, worker := range w.workers {
		worker.Stop()
	}
}

func (w *ChannelEventWorkerManager) AddEvent(event ChannelEvent) {
	index := int(channelWokerIndex(event.Channel.channelID, w.s.opts.ChannelEventWorkerCount))
	w.workers[index].Put(event)
}

func channelWokerIndex(channelID string, workerCount int) uint32 {
	return crc32.ChecksumIEEE([]byte(channelID)) % uint32(workerCount)
}

type ChannelEventType int

const (
	ChannelEventTypeSendMetaNotifySync    ChannelEventType = iota // 发送频道元数据日志同步通知
	ChannelEventTypeSendMessageNotifySync                         // 发送消息元数据同步通知
	ChannelEventTypeSyncMetaLogs                                  // 同步频道元数据日志
	ChannelEventTypeSyncMessageLogs                               // 同步消息日志
)

type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

type ChannelEvent struct {
	Priority  Priority
	Channel   *Channel
	index     int
	EventType ChannelEventType
	retry     int // 重试次数
}

type ChannelEventWorker struct {
	queue   *PriorityQueue
	cond    *sync.Cond
	stopper *syncutil.Stopper
	stopped bool
	s       *Server
	index   int
}

func NewChannelEventWorker(s *Server, index int) *ChannelEventWorker {
	return &ChannelEventWorker{
		queue:   NewPriorityQueue(),
		cond:    sync.NewCond(&sync.Mutex{}),
		stopper: syncutil.NewStopper(),
		s:       s,
		index:   index,
	}
}

func (w *ChannelEventWorker) Start() error {
	w.stopper.RunWorker(w.loop)
	return nil
}

func (w *ChannelEventWorker) Stop() {
	w.stopped = true
	w.cond.L.Lock()
	w.cond.Broadcast()
	w.cond.L.Unlock()

	w.stopper.Stop()

}

func (w *ChannelEventWorker) Put(event ChannelEvent) {
	w.cond.L.Lock()
	defer w.cond.L.Unlock()
	if w.stopped {
		return
	}
	w.queue.Put(event)
	w.cond.Signal()
}

func (w *ChannelEventWorker) Get() ChannelEvent {
	return w.queue.Get()
}

func (w *ChannelEventWorker) IsEmpty() bool {
	return w.queue.IsEmpty()
}

func (w *ChannelEventWorker) loop() {
	for {
		if w.stopped {
			return
		}
		w.cond.L.Lock()
		if w.queue.IsEmpty() && !w.stopped {
			w.cond.Wait()
		}
		if w.stopped {
			w.cond.L.Unlock()
			return
		}
		event := w.queue.Get()
		w.cond.L.Unlock()
		w.process(event)
	}
}

func (w *ChannelEventWorker) process(event ChannelEvent) {
	event.Channel.processEvent(event) // TODO: 假设这里阻塞了，也只会影响同步日志的速度，看是否需要开启协程去处理，现在暂时不开启，观察一下
}
