package channelwrite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
)

const (
	effectStagePrepare    = "prepare"
	effectStageAppend     = "append"
	effectStagePostCommit = "post_commit"
)

const defaultSchedulerReleaseTimeout = time.Second

type effectSchedulerOptions struct {
	ReactorCount      int
	PrepareWorkers    int
	AppendWorkers     int
	PostCommitWorkers int
}

type effectScheduler struct {
	prepare            *effectStagePool[prepareEffect]
	append             *effectStagePool[appendEffect]
	postCommit         *effectStagePool[commitEffect]
	prepareWorkers     int
	appendWorkers      int
	postCommitWorkers  int
	prepareCapacity    int
	appendCapacity     int
	postCommitCapacity int

	wake *effectWake
}

type effectWake struct {
	mu sync.Mutex
	ch chan struct{}
}

type effectStagePool[T any] struct {
	stage  string
	pool   *ants.PoolWithFuncGeneric[effectTask[T]]
	tokens chan struct{}
	notify func()
}

type effectTask[T any] struct {
	reactor *reactor
	effect  T
}

func newEffectScheduler(opts effectSchedulerOptions) *effectScheduler {
	wake := newEffectWake()
	scheduler := &effectScheduler{
		prepareWorkers:    boundedPositive(opts.PrepareWorkers, defaultPrepareWorkerCount),
		appendWorkers:     boundedPositive(opts.AppendWorkers, defaultAppendWorkerCount),
		postCommitWorkers: boundedPositive(opts.PostCommitWorkers, defaultPostCommitWorkerCount),
		wake:              wake,
	}
	scheduler.prepareCapacity = stageWorkerCapacity(opts.ReactorCount, scheduler.prepareWorkers)
	scheduler.appendCapacity = stageWorkerCapacity(opts.ReactorCount, scheduler.appendWorkers)
	scheduler.postCommitCapacity = stageWorkerCapacity(opts.ReactorCount, scheduler.postCommitWorkers)
	scheduler.prepare = mustNewEffectStagePool(
		effectStagePrepare,
		scheduler.prepareCapacity,
		wake.notify,
		runPrepareEffectTask,
	)
	scheduler.append = mustNewEffectStagePool(
		effectStageAppend,
		scheduler.appendCapacity,
		wake.notify,
		runAppendEffectTask,
	)
	scheduler.postCommit = mustNewEffectStagePool(
		effectStagePostCommit,
		scheduler.postCommitCapacity,
		wake.notify,
		runPostCommitEffectTask,
	)
	return scheduler
}

func newEffectWake() *effectWake {
	return &effectWake{ch: make(chan struct{})}
}

func (w *effectWake) watch() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ch
}

func (w *effectWake) notify() {
	w.mu.Lock()
	close(w.ch)
	w.ch = make(chan struct{})
	w.mu.Unlock()
}

func mustNewEffectStagePool[T any](stage string, capacity int, notify func(), runner func(*reactor, T) reactorEvent) *effectStagePool[T] {
	if capacity <= 0 {
		capacity = 1
	}
	stagePool := &effectStagePool[T]{
		stage:  stage,
		tokens: make(chan struct{}, capacity),
		notify: notify,
	}
	pool, err := ants.NewPoolWithFuncGeneric[effectTask[T]](
		capacity,
		func(task effectTask[T]) {
			stagePool.run(stage, runner, task)
		},
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
	)
	if err != nil {
		panic(fmt.Sprintf("channelwrite: create %s effect pool: %v", stage, err))
	}
	stagePool.pool = pool
	return stagePool
}

func (p *effectStagePool[T]) run(stage string, runner func(*reactor, T) reactorEvent, task effectTask[T]) {
	defer p.release(task.reactor, "released")
	task.reactor.addEffectWorkerBusy(stage, 1)
	defer task.reactor.addEffectWorkerBusy(stage, -1)
	task.reactor.complete(runner(task.reactor, task.effect))
}

func (p *effectStagePool[T]) trySubmit(reactor *reactor, effect T) (bool, error) {
	select {
	case p.tokens <- struct{}{}:
	default:
		p.observe(reactor, "full")
		return false, nil
	}
	if err := p.pool.Invoke(effectTask[T]{reactor: reactor, effect: effect}); err != nil {
		p.release(reactor, "error")
		return false, err
	}
	p.observe(reactor, "submitted")
	return true, nil
}

func (p *effectStagePool[T]) release(reactor *reactor, result string) {
	select {
	case <-p.tokens:
	default:
	}
	p.observe(reactor, result)
	if p.notify != nil {
		p.notify()
	}
}

func (p *effectStagePool[T]) observe(reactor *reactor, result string) {
	if reactor == nil {
		return
	}
	inflight := len(p.tokens)
	capacity := cap(p.tokens)
	observeEffectPool(reactor.appendPorts.observer, EffectPoolObservation{
		Stage:     p.stage,
		Result:    result,
		Inflight:  inflight,
		Capacity:  capacity,
		Saturated: capacity > 0 && inflight >= capacity,
	})
}

func (p *effectStagePool[T]) stop(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultSchedulerReleaseTimeout
	}
	err := p.pool.ReleaseTimeout(timeout)
	if errors.Is(err, ants.ErrPoolClosed) {
		return nil
	}
	return err
}

func (s *effectScheduler) trySubmitPrepare(reactor *reactor, effect prepareEffect) (bool, error) {
	return s.prepare.trySubmit(reactor, effect)
}

func (s *effectScheduler) trySubmitAppend(reactor *reactor, effect appendEffect) (bool, error) {
	return s.append.trySubmit(reactor, effect)
}

func (s *effectScheduler) trySubmitPostCommit(reactor *reactor, effect commitEffect) (bool, error) {
	return s.postCommit.trySubmit(reactor, effect)
}

func (s *effectScheduler) prepareWorkerCapacity() int {
	return s.prepareCapacity
}

func (s *effectScheduler) appendWorkerCapacity() int {
	return s.appendCapacity
}

func (s *effectScheduler) postCommitWorkerCapacity() int {
	return s.postCommitCapacity
}

func (s *effectScheduler) wakeC() <-chan struct{} {
	if s == nil || s.wake == nil {
		return nil
	}
	return s.wake.watch()
}

func (s *effectScheduler) stop(ctx context.Context) error {
	timeout := defaultSchedulerReleaseTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return ctx.Err()
		}
	}
	if err := s.prepare.stop(timeout); err != nil {
		return err
	}
	if err := s.append.stop(timeout); err != nil {
		return err
	}
	if err := s.postCommit.stop(timeout); err != nil {
		return err
	}
	return nil
}

func stageWorkerCapacity(reactors int, workersPerReactor int) int {
	if reactors <= 0 {
		reactors = 1
	}
	if workersPerReactor <= 0 {
		workersPerReactor = 1
	}
	return reactors * workersPerReactor
}

func runPrepareEffectTask(r *reactor, effect prepareEffect) (event reactorEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			event = preparePanicCompletion(effect, recovered)
		}
	}()
	return effect.run(r.stopCtx, r.ports)
}

func runAppendEffectTask(r *reactor, effect appendEffect) (event reactorEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			event = appendPanicCompletion(effect, recovered)
		}
	}()
	return effect.run(r.stopCtx, r.appendPorts)
}

func runPostCommitEffectTask(r *reactor, effect commitEffect) (event reactorEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			event = commitPanicCompletion(effect, recovered)
		}
	}()
	return effect.run(r.stopCtx, r.commitPorts)
}

func effectPanicError(stage string, recovered any) error {
	return fmt.Errorf("%w: %s: %v", ErrEffectPanic, stage, recovered)
}

func effectScheduleError(stage string, err error) error {
	if err == nil || errors.Is(err, ants.ErrPoolOverload) || errors.Is(err, ants.ErrPoolClosed) {
		return ErrBackpressured
	}
	return fmt.Errorf("%w: %s scheduler submit: %w", ErrBackpressured, stage, err)
}
