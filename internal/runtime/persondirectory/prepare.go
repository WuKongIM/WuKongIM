package persondirectory

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrPreparationBusy bounds direct work and its retained waiters independently
// of callers. Durable tasks remain available to the repair scanner.
var ErrPreparationBusy = errors.New("persondirectory: preparation capacity exhausted")

type preparationRequest struct {
	ctx     context.Context
	items   []metadb.PersonDirectoryTaskEntry
	results chan preparationResult
}
type preparationResult struct {
	index int
	err   error
}
type preparationWaiter struct {
	index   int
	results chan preparationResult
}

// Prepare submits exact durable tasks to the existing worker pool and waits
// for source completion. Canceling a waiter never cancels shared durable work.
// The scheduler coalesces the same source/generation, with at most 512 waiters.
func (p *Projector) Prepare(ctx context.Context, items []metadb.PersonDirectoryTaskEntry) []error {
	results := make([]error, len(items))
	fail := func(err error) []error {
		for i := range results {
			results[i] = err
		}
		return results
	}
	if len(items) == 0 || len(items) > projectorPageSize {
		return fail(metadb.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	p.mu.Lock()
	run := p.run
	running := p.state == projectorRunning
	p.mu.Unlock()
	if !running || run == nil {
		return fail(context.Canceled)
	}
	request := preparationRequest{ctx: ctx, items: append([]metadb.PersonDirectoryTaskEntry(nil), items...), results: make(chan preparationResult, len(items))}
	select {
	case run.direct <- request:
	case <-run.ctx.Done():
		return fail(context.Canceled)
	default:
		return fail(ErrPreparationBusy)
	}
	for range items {
		select {
		case result := <-request.results:
			results[result.index] = result.err
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-run.ctx.Done():
			return fail(context.Canceled)
		}
	}
	return results
}

func (p *Projector) admitPreparation(run *projectorRun, request preparationRequest, inflight, known map[ownedTaskKey]struct{}, waiters map[ownedTaskKey][]preparationWaiter, count *int) {
	reject := func(err error) {
		for i := range request.items {
			request.results <- preparationResult{index: i, err: err}
		}
	}
	if err := request.ctx.Err(); err != nil {
		reject(err)
		return
	}
	if *count+len(request.items) > projectorQueueSize || len(inflight)+len(request.items) > projectorQueueSize {
		reject(ErrPreparationBusy)
		return
	}
	batch := ownedTaskBatch{tasks: make([]ownedTask, 0, len(request.items))}
	for i, item := range request.items {
		key := ownedTaskKey{hashSlot: item.HashSlot, channelID: item.Task.ChannelID, channelType: item.Task.ChannelType, generation: item.Task.Generation}
		waiters[key] = append(waiters[key], preparationWaiter{index: i, results: request.results})
		*count++
		if _, ok := inflight[key]; ok {
			continue
		}
		inflight[key] = struct{}{}
		rememberKnownTask(known, inflight, key)
		batch.tasks = append(batch.tasks, ownedTask{hashSlot: item.HashSlot, task: item.Task})
	}
	if len(batch.tasks) == 0 {
		return
	}
	select {
	case run.tasks <- batch:
	case <-run.ctx.Done():
	}
}
