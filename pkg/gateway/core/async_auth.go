package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const asyncAuthPanicValueMaxLen = 256

// authExecutor admits and executes bounded async CONNECT authentication work.
type authExecutor struct {
	// server owns session and gateway state needed by auth tasks.
	server *Server
	// workers is the normalized auth worker count.
	workers int
	// capacity is the normalized maximum admitted auth backlog.
	capacity int
	// queue admits and runs CONNECT auth tasks on the shared workqueue primitive.
	queue *workqueue.BoundedPool[asyncAuthTask]
	// releaseTimeout bounds graceful queue shutdown.
	releaseTimeout time.Duration
	// panicC records worker panics for package tests and diagnostics.
	panicC chan any
}

func newAuthExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*authExecutor, error) {
	opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	e := &authExecutor{
		server:         s,
		workers:        opts.AsyncAuthWorkers,
		capacity:       opts.AsyncAuthQueueCapacity,
		releaseTimeout: opts.AsyncPoolReleaseTimeout,
		panicC:         make(chan any, 1),
	}

	queue, err := workqueue.NewBoundedPool[asyncAuthTask](workqueue.BoundedPoolConfig{
		Name:           "gateway-auth",
		Workers:        opts.AsyncAuthWorkers,
		QueueSize:      opts.AsyncAuthQueueCapacity,
		ReleaseTimeout: opts.AsyncPoolReleaseTimeout,
	}, e.handle)
	if err != nil {
		return nil, err
	}
	e.queue = queue
	return e, nil
}

func (e *authExecutor) submit(task asyncAuthTask) bool {
	if e == nil || e.queue == nil || task.state == nil || task.connect == nil {
		return false
	}

	task.connect = cloneAuthConnectPacket(task.connect)
	task.enqueuedAt = time.Now()

	err := e.queue.Submit(context.Background(), task)
	switch {
	case err == nil:
		return true
	case errors.Is(err, workqueue.ErrFull), errors.Is(err, workqueue.ErrClosed):
		return false
	default:
		return false
	}
}

func (e *authExecutor) stop() {
	if e == nil || e.queue == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.releaseTimeout)
	defer cancel()
	_ = e.queue.Close(ctx)
}

func (e *authExecutor) depth() int {
	if e == nil || e.queue == nil {
		return 0
	}
	return e.queue.QueueDepth()
}

func (e *authExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}

func (e *authExecutor) workerCount() int {
	if e == nil {
		return 0
	}
	return e.workers
}

func (e *authExecutor) handle(_ context.Context, task asyncAuthTask) error {
	defer func() {
		if v := recover(); v != nil {
			if task.state != nil {
				task.state.setAuthPending(false)
			}
			e.recordPanic(v, task)
		}
	}()

	if e.server == nil {
		return nil
	}
	e.server.observeAsyncAuthQueue(e)
	e.server.observeAsyncAuthWait(task)
	e.server.runAuthTask(task)
	return nil
}

func (e *authExecutor) recordPanic(v any, task asyncAuthTask) {
	if e == nil {
		return
	}
	select {
	case e.panicC <- v:
	default:
	}
	defer func() {
		_ = recover()
	}()
	e.logPanic(v, task)
}

func (e *authExecutor) logPanic(v any, task asyncAuthTask) {
	if e == nil || e.server == nil || e.server.options.Logger == nil {
		return
	}
	fields := []wklog.Field{
		wklog.String("panic", boundedAsyncAuthPanicValue(v)),
	}
	if task.state != nil && task.state.listener != nil {
		fields = append(fields, wklog.String("listener", task.state.listener.options.Name))
	}
	if task.connect != nil {
		fields = append(fields, wklog.String("uid", task.connect.UID), wklog.String("device_id", task.connect.DeviceID))
	}
	e.server.options.Logger.Warn("gateway async auth task panic", fields...)
}

func boundedAsyncAuthPanicValue(v any) string {
	text := fmt.Sprint(v)
	if len(text) <= asyncAuthPanicValueMaxLen {
		return text
	}
	return text[:asyncAuthPanicValueMaxLen]
}
