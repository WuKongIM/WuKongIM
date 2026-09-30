package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
)

// errQuorumSubmitPanic reports an ambiguous commit whose submission panicked.
var errQuorumSubmitPanic = errors.New("channel worker: quorum submit panic; outcome unknown")

// quorumSubmitter is the optional callback-driven commit seam. A nil return
// transfers exactly one completion callback; an error transfers none.
type quorumSubmitter interface {
	SubmitCommit(context.Context, replication.Proposal, func(replication.Receipt, error)) error
}

// runDeferredQuorumCommit starts one quorum commit and returns the worker
// immediately. The task keeps its budget, context, and a join slot until the
// log publishes the single terminal result.
func (p *Pool) runDeferredQuorumCommit(ctx context.Context, queued queuedTask, submitter quorumSubmitter) {
	task := queued.task
	p.observeWait(task.Kind, time.Since(queued.enqueuedAt))
	taskCtx, cancel := taskContext(ctx, task.Context)
	started := time.Now()
	p.deferredWG.Add(1)
	var once sync.Once
	finish := func(receipt replication.Receipt, err error) {
		once.Do(func() {
			defer p.deferredWG.Done()
			defer p.outstanding.Add(-1)
			res := normalizeContextErr(taskCtx, Result{Kind: task.Kind, Fence: task.Fence, Err: err, QuorumCommit: &QuorumCommitResult{Receipt: receipt}})
			cancel()
			res.Duration = nonNegativeDuration(time.Since(started))
			p.observeTask(res.Kind, res.Err, res.Duration)
			p.sink.Complete(res)
		})
	}
	err := func() (err error) {
		defer func() {
			if value := recover(); value != nil {
				// Ownership is ambiguous after a panic; the log may still call back.
				finish(replication.Receipt{}, errQuorumSubmitPanic)
				err = nil
			}
		}()
		return submitter.SubmitCommit(taskCtx, task.QuorumCommit.Proposal, finish)
	}()
	if err != nil {
		finish(replication.Receipt{}, err)
	}
}
