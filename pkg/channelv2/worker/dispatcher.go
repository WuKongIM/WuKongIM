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
			if !p.submitGroups(p.taskGroups(queued)) {
				return
			}
		case <-p.stop:
			return
		}
	}
}

func (p *Pool) submitGroups(groups [][]queuedTask) bool {
	taskCount := queuedGroupCount(groups)
	if taskCount == 0 {
		return true
	}
	for {
		select {
		case <-p.stop:
			p.releaseQueuedSlotsAndObserve(taskCount)
			p.completeGroupsWithErr(groups, ch.ErrClosed)
			return false
		default:
		}

		p.taskWG.Add(1)
		err := p.exec.submit(func() {
			defer p.taskWG.Done()
			for _, group := range groups {
				p.runTaskGroup(group)
			}
		})
		if err == nil {
			p.releaseQueuedSlotsAndObserve(taskCount)
			return true
		}
		p.taskWG.Done()

		if errors.Is(err, errExecutorOverloaded) {
			if p.waitExecutorRetry() {
				continue
			}
			p.releaseQueuedSlotsAndObserve(taskCount)
			p.completeGroupsWithErr(groups, ch.ErrClosed)
			return false
		}
		if errors.Is(err, ch.ErrClosed) {
			p.releaseQueuedSlotsAndObserve(taskCount)
			p.completeGroupsWithErr(groups, ch.ErrClosed)
			return false
		}
		p.releaseQueuedSlotsAndObserve(taskCount)
		p.completeGroupsWithErr(groups, fmt.Errorf("channelv2 worker executor submit: %w", err))
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

func (p *Pool) completeQueuedClosed() {
	for {
		select {
		case queued := <-p.queue:
			p.releaseQueuedSlotsAndObserve(1)
			p.completeGroupWithErr([]queuedTask{queued}, ch.ErrClosed)
		default:
			p.observeQueueDepth()
			return
		}
	}
}

func queuedGroupCount(groups [][]queuedTask) int {
	count := 0
	for _, group := range groups {
		count += len(group)
	}
	return count
}

func (p *Pool) completeGroupsWithErr(groups [][]queuedTask, err error) {
	for _, group := range groups {
		p.completeGroupWithErr(group, err)
	}
}

func (p *Pool) completeGroupWithErr(group []queuedTask, err error) {
	for _, queued := range group {
		p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
		p.observeTask(queued.task.Kind, err, 0)
		p.sink.Complete(Result{Kind: queued.task.Kind, Fence: queued.task.Fence, Err: err})
	}
}
