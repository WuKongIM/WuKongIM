package app

import (
	"context"
	"sync"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultPluginReceiveObserverConcurrency = 64
	defaultPluginReceiveObserverQueueDepth  = 1024
	defaultPluginReceiveObserverTimeout     = 5 * time.Second
)

type pluginOfflineReceiveUsecase interface {
	ReceiveOffline(context.Context, pluginusecase.OfflineReceiveEvent) error
}

// pluginReceiveObserver adapts delivery offline recipient notifications into plugin Receive hooks.
type pluginReceiveObserver struct {
	// pluginUsecase handles Receive hook eligibility, binding selection, and invocation.
	pluginUsecase pluginOfflineReceiveUsecase
	// logger records hook invocation failures.
	logger wklog.Logger
	// workQueue bounds pending offline Receive work; producers block when the queue is full.
	workQueue chan pluginReceiveWork
	// timeout bounds each plugin Receive invocation.
	timeout time.Duration
	// concurrency is the number of worker goroutines processing Receive work.
	concurrency int

	// mu protects lifecycle state below.
	mu sync.Mutex
	// accepting reports whether new offline Receive work can be enqueued.
	accepting bool
	// cancel stops worker contexts during observer shutdown.
	cancel context.CancelFunc
	// done closes after all workers have exited.
	done chan struct{}
	// wg waits for worker goroutines to exit.
	wg sync.WaitGroup
}

type pluginReceiveWork struct {
	ctx   context.Context
	event deliveryruntime.OfflineResolvedEvent
}

func newPluginReceiveObserver(pluginUsecase pluginOfflineReceiveUsecase, timeout time.Duration, logger wklog.Logger) *pluginReceiveObserver {
	return newPluginReceiveObserverWithLimits(pluginUsecase, timeout, logger, defaultPluginReceiveObserverConcurrency, defaultPluginReceiveObserverQueueDepth)
}

func newPluginReceiveObserverWithLimits(pluginUsecase pluginOfflineReceiveUsecase, timeout time.Duration, logger wklog.Logger, concurrency int, queueDepth int) *pluginReceiveObserver {
	if timeout <= 0 {
		timeout = defaultPluginReceiveObserverTimeout
	}
	if pluginUsecase == nil {
		return &pluginReceiveObserver{logger: logger, timeout: timeout}
	}
	if concurrency <= 0 {
		concurrency = defaultPluginReceiveObserverConcurrency
	}
	if queueDepth <= 0 {
		queueDepth = defaultPluginReceiveObserverQueueDepth
	}
	return &pluginReceiveObserver{
		pluginUsecase: pluginUsecase,
		logger:        logger,
		workQueue:     make(chan pluginReceiveWork, queueDepth),
		timeout:       timeout,
		concurrency:   concurrency,
	}
}

func (o *pluginReceiveObserver) Start(ctx context.Context) error {
	if o == nil || o.pluginUsecase == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != nil {
		return nil
	}
	if o.workQueue == nil {
		o.workQueue = make(chan pluginReceiveWork, defaultPluginReceiveObserverQueueDepth)
	}
	concurrency := o.concurrency
	if concurrency <= 0 {
		concurrency = defaultPluginReceiveObserverConcurrency
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	o.cancel = cancel
	o.done = done
	o.accepting = true
	o.wg.Add(concurrency)
	for range concurrency {
		go o.runWorker(runCtx)
	}
	go func() {
		o.wg.Wait()
		close(done)
	}()
	return nil
}

func (o *pluginReceiveObserver) Stop(ctx context.Context) error {
	if o == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	o.mu.Lock()
	cancel := o.cancel
	done := o.done
	if cancel == nil {
		o.accepting = false
		o.mu.Unlock()
		return nil
	}
	o.cancel = nil
	o.accepting = false
	o.mu.Unlock()

	cancel()
	select {
	case <-done:
		o.drainQueuedWork()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *pluginReceiveObserver) OfflineResolved(ctx context.Context, event deliveryruntime.OfflineResolvedEvent) {
	if o == nil || o.pluginUsecase == nil {
		return
	}
	o.mu.Lock()
	accepting := o.accepting
	queue := o.workQueue
	done := o.done
	o.mu.Unlock()
	if !accepting || queue == nil || done == nil {
		return
	}
	select {
	case queue <- pluginReceiveWork{ctx: ctx, event: event}:
	case <-done:
	}
}

func (o *pluginReceiveObserver) runWorker(ctx context.Context) {
	defer o.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case work := <-o.workQueue:
			o.invoke(ctx, work.ctx, work.event)
		}
	}
}

func (o *pluginReceiveObserver) invoke(parent context.Context, ctx context.Context, event deliveryruntime.OfflineResolvedEvent) {
	if o.pluginUsecase == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithoutCancel(ctx)
	timeout := o.timeout
	if timeout <= 0 {
		timeout = defaultPluginReceiveObserverTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if parent != nil {
		stop := context.AfterFunc(parent, cancel)
		defer stop()
		select {
		case <-parent.Done():
			return
		default:
		}
	}
	err := o.pluginUsecase.ReceiveOffline(ctx, pluginusecase.OfflineReceiveEvent{
		Message:       event.Envelope.Message,
		UID:           event.UID,
		RequestScoped: len(event.Envelope.MessageScopedUIDs) > 0,
	})
	if err != nil {
		o.logReceiveObserverFailure(event, err)
	}
}

func (o *pluginReceiveObserver) drainQueuedWork() {
	if o == nil || o.workQueue == nil {
		return
	}
	for {
		select {
		case <-o.workQueue:
		default:
			return
		}
	}
}

func (o *pluginReceiveObserver) logReceiveObserverFailure(event deliveryruntime.OfflineResolvedEvent, err error) {
	if o.logger == nil || err == nil {
		return
	}
	o.logger.Warn("plugin Receive observer failed",
		wklog.Event("plugin.receive_observer.failed"),
		wklog.String("uid", event.UID),
		wklog.String("channelID", event.Envelope.ChannelID),
		wklog.Int("channelType", int(event.Envelope.ChannelType)),
		wklog.Uint64("messageID", event.Envelope.MessageID),
		wklog.Uint64("messageSeq", event.Envelope.MessageSeq),
		wklog.Error(err),
	)
}
