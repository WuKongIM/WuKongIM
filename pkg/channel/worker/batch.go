package worker

import (
	"context"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
)

func (p *Pool) taskGroups(items []queuedTask) [][]queuedTask {
	if len(items) == 0 {
		return nil
	}
	first := items[0]
	switch {
	case p.canCollectRPCBatch(first.task):
		return groupRPCBatchItems(items)
	case p.canCollectStoreAppendBatch(first.task):
		return groupStoreBatchItems(items, TaskStoreAppend)
	case p.canCollectStoreApplyBatch(first.task):
		return groupStoreBatchItems(items, TaskStoreApply)
	default:
		return singleTaskGroups(items)
	}
}

func (p *Pool) batchMaxWait(defaultWait time.Duration) time.Duration {
	if p != nil && p.cfg.BatchMaxWait > 0 {
		return p.cfg.BatchMaxWait
	}
	return defaultWait
}

func (p *Pool) canCollectRPCBatch(task Task) bool {
	if _, ok := p.deps.Transport.(transport.BatchClient); !ok {
		return false
	}
	_, ok := rpcBatchKeyFor(task)
	return ok
}

func (p *Pool) canCollectStoreAppendBatch(task Task) bool {
	if _, ok := p.deps.Stores.(store.LeaderAppendBatcher); !ok {
		return false
	}
	return task.Kind == TaskStoreAppend
}

func (p *Pool) canCollectStoreApplyBatch(task Task) bool {
	if _, ok := p.deps.Stores.(store.FollowerApplyBatcher); !ok {
		return false
	}
	return task.Kind == TaskStoreApply
}

func groupRPCBatchItems(items []queuedTask) [][]queuedTask {
	groups := make([][]queuedTask, 0, len(items))
	used := make([]bool, len(items))
	for i, item := range items {
		if used[i] {
			continue
		}
		key, ok := rpcBatchKeyFor(item.task)
		if !ok {
			used[i] = true
			groups = append(groups, []queuedTask{item})
			continue
		}
		group := []queuedTask{item}
		indexes := []int{i}
		for j := i + 1; j < len(items); j++ {
			if used[j] {
				continue
			}
			other, ok := rpcBatchKeyFor(items[j].task)
			if !ok || other != key {
				continue
			}
			group = append(group, items[j])
			indexes = append(indexes, j)
		}
		for _, index := range indexes {
			used[index] = true
		}
		groups = append(groups, group)
	}
	return groups
}

func groupStoreBatchItems(items []queuedTask, kind TaskKind) [][]queuedTask {
	groups := make([][]queuedTask, 0, len(items))
	used := make([]bool, len(items))
	for i, item := range items {
		if used[i] {
			continue
		}
		if item.task.Kind != kind {
			used[i] = true
			groups = append(groups, []queuedTask{item})
			continue
		}
		group := []queuedTask{item}
		indexes := []int{i}
		keys := map[ch.ChannelKey]struct{}{item.task.Fence.ChannelKey: {}}
		for j := i + 1; j < len(items); j++ {
			if used[j] || items[j].task.Kind != kind {
				continue
			}
			key := items[j].task.Fence.ChannelKey
			if _, ok := keys[key]; ok {
				continue
			}
			group = append(group, items[j])
			indexes = append(indexes, j)
			keys[key] = struct{}{}
		}
		for _, index := range indexes {
			used[index] = true
		}
		groups = append(groups, group)
	}
	return groups
}

func singleTaskGroups(items []queuedTask) [][]queuedTask {
	groups := make([][]queuedTask, 0, len(items))
	for _, item := range items {
		groups = append(groups, []queuedTask{item})
	}
	return groups
}

func rpcBatchKeyFor(task Task) (rpcBatchKey, bool) {
	switch task.Kind {
	case TaskRPCPull:
		if task.RPCPull == nil {
			return rpcBatchKey{}, false
		}
		return rpcBatchKey{kind: task.Kind, node: task.RPCPull.Node}, true
	case TaskRPCPullHint:
		if task.RPCPullHint == nil {
			return rpcBatchKey{}, false
		}
		return rpcBatchKey{kind: task.Kind, node: task.RPCPullHint.Node}, true
	default:
		return rpcBatchKey{}, false
	}
}

func (p *Pool) runQueuedBatch(ctx context.Context, items []queuedTask) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(items) == 1 {
		p.runTaskGroup(ctx, items)
		return nil
	}
	for _, group := range p.taskGroups(items) {
		p.runTaskGroup(ctx, group)
	}
	return nil
}

func (p *Pool) runTaskGroup(ctx context.Context, group []queuedTask) {
	if len(group) == 0 {
		return
	}
	for _, queued := range group {
		p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
	}
	running := int(p.inflight.Add(1))
	p.observeInflight(running)
	started := time.Now()
	results, _ := p.runQueuedGroupSafely(ctx, group)
	duration := nonNegativeDuration(time.Since(started))
	for i := range results {
		results[i].Duration = duration
		p.observeTask(results[i].Kind, results[i].Err, results[i].Duration)
	}
	running = int(p.inflight.Add(-1))
	p.observeInflight(running)
	for _, result := range results {
		p.sink.Complete(result)
	}
}

func (p *Pool) runQueuedGroupSafely(ctx context.Context, group []queuedTask) (results []Result, recovered bool) {
	defer func() {
		if value := recover(); value != nil {
			recovered = true
			err := fmt.Errorf("channel worker panic: %v", value)
			results = make([]Result, 0, len(group))
			for _, queued := range group {
				results = append(results, Result{Kind: queued.task.Kind, Fence: queued.task.Fence, Err: err})
			}
		}
	}()
	return p.runQueuedGroup(ctx, group), false
}

func (p *Pool) runQueuedGroup(ctx context.Context, group []queuedTask) []Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(group) > 1 {
		key, ok := rpcBatchKeyFor(group[0].task)
		batchClient, batchOK := p.deps.Transport.(transport.BatchClient)
		if ok && batchOK {
			switch key.kind {
			case TaskRPCPull:
				return p.runRPCPullBatch(ctx, group, batchClient, key.node)
			case TaskRPCPullHint:
				return p.runRPCPullHintBatch(ctx, group, batchClient, key.node)
			}
		}
		if group[0].task.Kind == TaskStoreAppend {
			if batcher, ok := p.deps.Stores.(store.LeaderAppendBatcher); ok {
				return p.runStoreAppendBatch(ctx, group, batcher)
			}
		}
		if group[0].task.Kind == TaskStoreApply {
			if batcher, ok := p.deps.Stores.(store.FollowerApplyBatcher); ok {
				return p.runStoreApplyBatch(ctx, group, batcher)
			}
		}
	}
	results := make([]Result, 0, len(group))
	for _, queued := range group {
		results = append(results, queued.task.Run(ctx, p.deps))
	}
	return results
}

func (p *Pool) runStoreAppendBatch(ctx context.Context, group []queuedTask, batcher store.LeaderAppendBatcher) []Result {
	results := make([]Result, len(group))
	items := make([]store.AppendLeaderBatchItem, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, StoreAppend: &StoreAppendResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.StoreAppend == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		payload := queued.task.StoreAppend
		items = append(items, store.AppendLeaderBatchItem{
			ChannelKey: queued.task.Fence.ChannelKey,
			ChannelID:  payload.ChannelID,
			Request:    store.AppendLeaderRequest{Records: payload.Records, Sync: payload.Sync},
		})
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(ctx, group, active)
	defer cancel()
	batchResults := batcher.AppendLeaderBatch(ctx, items)
	if len(batchResults) != len(active) {
		p.observeBatch(TaskStoreAppend, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	batchErr := firstStoreAppendBatchErr(batchResults)
	p.observeBatch(TaskStoreAppend, len(active), batchErr)
	for i, index := range active {
		item := batchResults[i]
		results[index].Err = batchContextErr(group[index].task, ctx, item.Err)
		results[index].StoreAppend = &StoreAppendResult{BaseOffset: item.BaseOffset, LastOffset: item.LastOffset}
	}
	return results
}

func (p *Pool) runStoreApplyBatch(ctx context.Context, group []queuedTask, batcher store.FollowerApplyBatcher) []Result {
	results := make([]Result, len(group))
	items := make([]store.ApplyFollowerBatchItem, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, StoreApply: &StoreApplyResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.StoreApply == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		payload := queued.task.StoreApply
		items = append(items, store.ApplyFollowerBatchItem{
			ChannelKey: queued.task.Fence.ChannelKey,
			ChannelID:  payload.ChannelID,
			Request:    store.ApplyFollowerRequest{Records: payload.Records, LeaderHW: payload.LeaderHW},
		})
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(ctx, group, active)
	defer cancel()
	batchResults := batcher.ApplyFollowerBatch(ctx, items)
	if len(batchResults) != len(active) {
		p.observeBatch(TaskStoreApply, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	batchErr := firstStoreApplyBatchErr(batchResults)
	p.observeBatch(TaskStoreApply, len(active), batchErr)
	for i, index := range active {
		item := batchResults[i]
		results[index].Err = batchContextErr(group[index].task, ctx, item.Err)
		results[index].StoreApply = &StoreApplyResult{LEO: item.LEO}
	}
	return results
}

func (p *Pool) runRPCPullBatch(ctx context.Context, group []queuedTask, batchClient transport.BatchClient, node ch.NodeID) []Result {
	results := make([]Result, len(group))
	requests := make([]transport.PullRequest, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, RPCPull: &RPCPullResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.RPCPull == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		requests = append(requests, queued.task.RPCPull.Request)
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(ctx, group, active)
	defer cancel()
	resp, err := batchClient.PullBatch(ctx, node, transport.PullBatchRequest{Items: requests})
	if err != nil {
		p.observeBatch(TaskRPCPull, len(active), err)
		for _, index := range active {
			results[index].Err = batchContextErr(group[index].task, ctx, err)
		}
		return results
	}
	if len(resp.Items) != len(active) {
		p.observeBatch(TaskRPCPull, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	p.observeBatch(TaskRPCPull, len(active), nil)
	for i, index := range active {
		item := resp.Items[i]
		results[index].Err = batchContextErr(group[index].task, ctx, item.Err)
		results[index].RPCPull = &RPCPullResult{Response: item.Response}
	}
	return results
}

func (p *Pool) runRPCPullHintBatch(ctx context.Context, group []queuedTask, batchClient transport.BatchClient, node ch.NodeID) []Result {
	results := make([]Result, len(group))
	requests := make([]transport.PullHintRequest, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, RPCPullHint: &RPCPullHintResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.RPCPullHint == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		requests = append(requests, queued.task.RPCPullHint.Request)
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(ctx, group, active)
	defer cancel()
	resp, err := batchClient.PullHintBatch(ctx, node, transport.PullHintBatchRequest{Items: requests})
	if err != nil {
		p.observeBatch(TaskRPCPullHint, len(active), err)
		for _, index := range active {
			results[index].Err = batchContextErr(group[index].task, ctx, err)
		}
		return results
	}
	if len(resp.Items) != len(active) {
		p.observeBatch(TaskRPCPullHint, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	p.observeBatch(TaskRPCPullHint, len(active), nil)
	for i, index := range active {
		results[index].Err = batchContextErr(group[index].task, ctx, resp.Items[i].Err)
		results[index].RPCPullHint = &RPCPullHintResult{}
	}
	return results
}

func firstStoreAppendBatchErr(results []store.AppendLeaderBatchResult) error {
	for _, result := range results {
		if result.Err != nil {
			return result.Err
		}
	}
	return nil
}

func firstStoreApplyBatchErr(results []store.ApplyFollowerBatchResult) error {
	for _, result := range results {
		if result.Err != nil {
			return result.Err
		}
	}
	return nil
}

func batchTaskContext(parent context.Context, group []queuedTask, active []int) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	var deadline time.Time
	hasDeadline := false
	now := time.Now()
	for _, index := range active {
		taskCtx := group[index].task.Context
		if taskCtx != nil {
			next, ok := taskCtx.Deadline()
			if ok && (!hasDeadline || next.Before(deadline)) {
				deadline = next
				hasDeadline = true
			}
		}
		if timeout := taskRunTimeout(group[index].task); timeout > 0 {
			next := now.Add(timeout)
			if !hasDeadline || next.Before(deadline) {
				deadline = next
				hasDeadline = true
			}
		}
	}
	if !hasDeadline {
		return parent, func() {}
	}
	return context.WithDeadline(parent, deadline)
}

// taskRunTimeout returns an execution-only timeout that starts after queue wait.
func taskRunTimeout(task Task) time.Duration {
	switch task.Kind {
	case TaskRPCPull:
		if task.RPCPull != nil {
			return task.RPCPull.Timeout
		}
	}
	return 0
}

func batchContextErr(task Task, ctx context.Context, err error) error {
	if taskErr := taskContextDoneErr(task); taskErr != nil {
		return taskErr
	}
	if err == context.Canceled {
		if cause := context.Cause(ctx); cause != nil && cause != context.Canceled {
			return cause
		}
	}
	return err
}

func taskContextDoneErr(task Task) error {
	if task.Context == nil || task.Context.Err() == nil {
		return nil
	}
	return contextFromTaskCause(task.Context)
}
