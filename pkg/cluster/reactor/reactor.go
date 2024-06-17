package reactor

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Reactor struct {
	subReactors []*ReactorSub
	opts        *Options
	mu          sync.RWMutex
	taskPool    *ants.Pool
	wklog.Log

	processInitC              chan *initReq          // 处理频道初始化
	processConflictCheckC     chan *conflictCheckReq // 冲突检查请求
	processGetLogC            chan *getLogReq        // 获取日志请求
	processStoreAppendC       chan *storeAppendReq   // 存储追加日志请求
	processApplyLogC          chan *applyLogReq      // 应用日志请求
	processLearnerToFollowerC chan *learnerToFollowerReq
	processLearnerToLeaderC   chan *learnerToLeaderReq

	stopper *syncutil.Stopper

	request IRequest
}

func New(opts *Options) *Reactor {
	r := &Reactor{
		opts:    opts,
		Log:     wklog.NewWKLog(fmt.Sprintf("Reactor[%s]", opts.ReactorType.String())),
		stopper: syncutil.NewStopper(),

		processInitC:              make(chan *initReq, 1024),
		processConflictCheckC:     make(chan *conflictCheckReq, 1024),
		processGetLogC:            make(chan *getLogReq, 1024),
		processStoreAppendC:       make(chan *storeAppendReq, 1024),
		processApplyLogC:          make(chan *applyLogReq, 1024),
		processLearnerToFollowerC: make(chan *learnerToFollowerReq, 1024),
		processLearnerToLeaderC:   make(chan *learnerToLeaderReq, 1024),
		request:                   opts.Request,
	}
	taskPool, err := ants.NewPool(opts.TaskPoolSize, ants.WithPanicHandler(func(err interface{}) {
		stack := debug.Stack()
		r.Panic("执行任务失败", zap.Any("error", err), zap.String("stack", string(stack)))

	}))
	if err != nil {
		r.Panic("create task pool error", zap.Error(err))
	}
	r.taskPool = taskPool

	for i := 0; i < int(r.opts.SubReactorNum); i++ {
		sub := r.newReactorSub(i)
		r.subReactors = append(r.subReactors, sub)
	}
	return r
}

func (r *Reactor) Start() error {

	for i := 0; i < 10; i++ {
		r.stopper.RunWorker(r.processInitLoop)
		r.stopper.RunWorker(r.processConflictCheckLoop)
		r.stopper.RunWorker(r.processGetLogLoop)
		r.stopper.RunWorker(r.processStoreAppendLoop)
		r.stopper.RunWorker(r.processApplyLogLoop)
		r.stopper.RunWorker(r.processLearnerToFollowerLoop)
		r.stopper.RunWorker(r.processLearnerToLeaderLoop)
	}

	// for i := 0; i < r.opts.AppendLogWorkerNum; i++ {
	// 	r.stopper.RunWorker(r.appendLogLoop)
	// }
	for _, sub := range r.subReactors {
		err := sub.Start()
		if err != nil {
			return err
		}

	}
	return nil
}

func (r *Reactor) Stop() {

	for _, sub := range r.subReactors {
		sub.Stop()
	}
	r.stopper.Stop()

}
func (r *Reactor) ProposeAndWait(ctx context.Context, handleKey string, logs []replica.Log) ([]ProposeResult, error) {
	sub := r.reactorSub(handleKey)
	return sub.proposeAndWait(ctx, handleKey, logs)
}

func (r *Reactor) AddHandler(key string, handler IHandler) {
	h := getHandlerFromPool()
	h.init(key, handler, r)
	sub := r.reactorSub(key)
	sub.addHandler(h)
}

func (r *Reactor) RemoveHandler(key string) {

	sub := r.reactorSub(key)
	h := sub.removeHandler(key)
	if h != nil {
		putHandlerToPool(h)
	}
}

func (r *Reactor) Handler(key string) IHandler {
	h := r.handler(key)
	if h == nil {
		return nil
	}
	return h.handler
}

func (r *Reactor) handler(key string) *handler {
	sub := r.reactorSub(key)
	h := sub.handler(key)
	if h == nil {
		return nil
	}
	return h
}

func (r *Reactor) Step(key string, msg replica.Message) {
	sub := r.reactorSub(key)
	sub.step(key, msg)
}

func (r *Reactor) ExistHandler(key string) bool {
	sub := r.reactorSub(key)
	return sub.existHandler(key)
}

func (r *Reactor) AddMessage(m Message) {
	sub := r.reactorSub(m.HandlerKey)
	sub.addMessage(m)
}

func (r *Reactor) HandlerLen() int {
	len := 0
	for _, sub := range r.subReactors {
		len += sub.handlerLen()
	}
	return len
}

func (r *Reactor) IteratorHandler(f func(h IHandler) bool) {
	for _, sub := range r.subReactors {
		sub.iterator(func(h *handler) bool {
			return f(h.handler)
		})
	}
}

func (r *Reactor) newReactorSub(i int) *ReactorSub {
	return NewReactorSub(i, r)
}

func (r *Reactor) reactorSub(key string) *ReactorSub {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx := hashWthString(key)
	return r.subReactors[idx%r.opts.SubReactorNum]
}

func (r *Reactor) newMessageQueue() *MessageQueue {
	return NewMessageQueue(r.opts.ReceiveQueueLength, r.opts.MaxReceiveQueueSize)
}

func (r *Reactor) submitTask(f func()) error {
	running := r.taskPool.Running()
	offsetSize := 10
	if r.opts.TaskPoolSize <= 100 {
		offsetSize = 1
	}
	if running > r.opts.TaskPoolSize-offsetSize {
		r.Warn("task pool is full", zap.Int("running", running), zap.Int("size", r.opts.TaskPoolSize))
	}
	return r.taskPool.Submit(f)
}

// func (r *Reactor) appendLogLoop() {

// 	done := false
// 	var err error
// 	reqs := make([]AppendLogReq, 0, 100)
// 	for {
// 		select {
// 		case req := <-r.appendLogC:
// 			reqs = append(reqs, req)
// 			// 取出所有的请求
// 			for !done {
// 				select {
// 				case req := <-r.appendLogC:
// 					reqs = append(reqs, req)
// 				default:
// 					done = true
// 				}
// 			}
// 			err = r.opts.Event.OnAppendLogs(reqs)
// 			if err != nil {
// 				r.Error("on append logs failed", zap.Error(err))
// 			} else {
// 				// 回执
// 				for _, req := range reqs {
// 					handler := r.handler(req.HandleKey)
// 					if handler == nil {
// 						continue
// 					}
// 					lastLog := req.Logs[len(req.Logs)-1]
// 					if handler.lastIndex.Load() > lastLog.Index {
// 						continue
// 					}
// 					handler.lastIndex.Store(lastLog.Index)
// 					sub := r.reactorSub(req.HandleKey)

// 					select {
// 					case sub.storeAppendRespC <- handler:
// 					case <-r.stopper.ShouldStop():
// 						return
// 					}
// 				}

// 			}
// 			// 清空
// 			done = false
// 			reqs = reqs[:0]

// 		case <-r.stopper.ShouldStop():
// 			return
// 		}
// 	}
// }
