package worker

import (
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

const executorSubmitRetryDelay = 10 * time.Microsecond

func (p *Pool) dispatch() {
	defer p.dispatchWG.Done()
	for {
		select {
		case queued := <-p.queue:
			p.observeQueueDepth()
			for _, group := range p.taskGroups(queued) {
				if !p.submitGroup(group) {
					return
				}
			}
		case <-p.stop:
			return
		}
	}
}

func (p *Pool) submitGroup(group []queuedTask) bool {
	if len(group) == 0 {
		return true
	}
	for {
		select {
		case <-p.stop:
			p.releaseQueuedSlots(len(group))
			p.completeGroupWithErr(group, ch.ErrClosed)
			return false
		default:
		}

		p.taskWG.Add(1)
		err := p.exec.submit(func() {
			defer p.taskWG.Done()
			p.runTaskGroupSafely(group)
		})
		if err == nil {
			p.releaseQueuedSlots(len(group))
			return true
		}
		p.taskWG.Done()

		if errors.Is(err, errExecutorOverloaded) {
			if p.waitExecutorRetry() {
				continue
			}
			p.releaseQueuedSlots(len(group))
			p.completeGroupWithErr(group, ch.ErrClosed)
			return false
		}
		if errors.Is(err, ch.ErrClosed) {
			p.releaseQueuedSlots(len(group))
			p.completeGroupWithErr(group, ch.ErrClosed)
			return false
		}
		p.releaseQueuedSlots(len(group))
		p.completeGroupWithErr(group, fmt.Errorf("channelv2 worker executor submit: %w", err))
		return true
	}
}

func (p *Pool) waitExecutorRetry() bool {
	timer := time.NewTimer(executorSubmitRetryDelay)
	defer timer.Stop()
	select {
	case <-p.stop:
		return false
	case <-timer.C:
		return true
	}
}

func (p *Pool) runTaskGroupSafely(group []queuedTask) {
	defer func() {
		if recovered := recover(); recovered != nil {
			p.completeGroupWithErr(group, fmt.Errorf("channelv2 worker panic: %v", recovered))
		}
	}()
	p.runTaskGroup(group)
}

func (p *Pool) completeQueuedClosed() {
	for {
		select {
		case queued := <-p.queue:
			p.releaseQueuedSlots(1)
			p.completeGroupWithErr([]queuedTask{queued}, ch.ErrClosed)
		default:
			p.observeQueueDepth()
			return
		}
	}
}

func (p *Pool) completeGroupWithErr(group []queuedTask, err error) {
	for _, queued := range group {
		p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
		p.observeTask(queued.task.Kind, err, 0)
		p.sink.Complete(Result{Kind: queued.task.Kind, Fence: queued.task.Fence, Err: err})
	}
}
