package cluster

import (
	"context"
	"errors"
	"sync"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	personDirectoryBatchMaxItems    = 128
	personDirectoryQueueMaxItems    = 512
	personDirectoryBatchTargetItems = 128
	personDirectoryBatchMaxActive   = 4
	personDirectoryBatchCollectWait = 250 * time.Millisecond
	personDirectoryBatchTimeout     = 4 * time.Second
)

var errPersonDirectoryBackpressured = errors.New("person directory batch backpressured")

type personDirectoryBatchNode interface {
	UpsertUserChannelMembershipBatch(context.Context, []metadb.UserChannelMembership) error
	EnsureChannelDirectoriesReady(context.Context, []metadb.ChannelKey) error
}

type personDirectoryMutation struct {
	key         metadb.ChannelKey
	memberships []metadb.UserChannelMembership
}

type personDirectoryBatcher struct {
	node personDirectoryBatchNode

	mu          sync.Mutex
	current     *personDirectoryBatch
	queuedItems int
	collectWait time.Duration
	targetItems int
	timeout     time.Duration
	active      chan struct{}
}

type personDirectoryBatch struct {
	entries map[metadb.ChannelKey]*personDirectoryEntry
	order   []*personDirectoryEntry
	trigger chan struct{}
	once    sync.Once
	sealed  bool
}

type personDirectoryEntry struct {
	mutation personDirectoryMutation
	waiters  map[*personDirectoryWaiter]struct{}
	canceled bool
}

type personDirectoryWaiter struct {
	entry  *personDirectoryEntry
	result chan error
}

func newPersonDirectoryBatcher(node personDirectoryBatchNode) *personDirectoryBatcher {
	return &personDirectoryBatcher{
		node: node, collectWait: personDirectoryBatchCollectWait,
		targetItems: personDirectoryBatchTargetItems, timeout: personDirectoryBatchTimeout,
		active: make(chan struct{}, personDirectoryBatchMaxActive),
	}
}

func (b *personDirectoryBatcher) ensure(ctx context.Context, mutation personDirectoryMutation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || b.node == nil || mutation.key.ChannelID == "" || mutation.key.ChannelType <= 0 || len(mutation.memberships) == 0 {
		return metadb.ErrInvalidArgument
	}
	waiter, err := b.admit(mutation)
	if err != nil {
		return err
	}
	select {
	case err := <-waiter.result:
		return err
	case <-ctx.Done():
		b.detach(waiter)
		return ctx.Err()
	}
}

func (b *personDirectoryBatcher) admit(mutation personDirectoryMutation) (*personDirectoryWaiter, error) {
	waiter := &personDirectoryWaiter{result: make(chan error, 1)}
	b.mu.Lock()
	batch := b.current
	if batch == nil || batch.sealed || len(batch.order) >= personDirectoryBatchMaxItems {
		if b.queuedItems >= personDirectoryQueueMaxItems {
			b.mu.Unlock()
			return nil, errPersonDirectoryBackpressured
		}
		batch = &personDirectoryBatch{entries: make(map[metadb.ChannelKey]*personDirectoryEntry), trigger: make(chan struct{})}
		b.current = batch
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskMessageDirectoryBatch, func() { b.run(batch) })
	}
	if entry := batch.entries[mutation.key]; entry != nil {
		waiter.entry = entry
		entry.waiters[waiter] = struct{}{}
		b.mu.Unlock()
		return waiter, nil
	}
	if b.queuedItems >= personDirectoryQueueMaxItems {
		b.mu.Unlock()
		return nil, errPersonDirectoryBackpressured
	}
	entry := &personDirectoryEntry{mutation: mutation, waiters: map[*personDirectoryWaiter]struct{}{waiter: {}}}
	waiter.entry = entry
	batch.entries[mutation.key] = entry
	batch.order = append(batch.order, entry)
	b.queuedItems++
	if len(batch.order) >= b.targetItems {
		batch.once.Do(func() { close(batch.trigger) })
	}
	b.mu.Unlock()
	return waiter, nil
}

func (b *personDirectoryBatcher) detach(waiter *personDirectoryWaiter) {
	if waiter == nil || waiter.entry == nil {
		return
	}
	b.mu.Lock()
	entry := waiter.entry
	delete(entry.waiters, waiter)
	if len(entry.waiters) == 0 && !entry.canceled {
		for _, batch := range []*personDirectoryBatch{b.current} {
			if batch != nil && !batch.sealed && batch.entries[entry.mutation.key] == entry {
				entry.canceled = true
				delete(batch.entries, entry.mutation.key)
				b.queuedItems--
			}
		}
	}
	b.mu.Unlock()
}

func (b *personDirectoryBatcher) run(batch *personDirectoryBatch) {
	timer := time.NewTimer(b.collectWait)
	select {
	case <-batch.trigger:
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}

	b.mu.Lock()
	batch.sealed = true
	if b.current == batch {
		b.current = nil
	}
	entries := make([]*personDirectoryEntry, 0, len(batch.order))
	for _, entry := range batch.order {
		if !entry.canceled {
			entries = append(entries, entry)
		}
	}
	b.mu.Unlock()
	if len(entries) == 0 {
		return
	}

	b.active <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	err := b.submit(ctx, entries)
	cancel()
	<-b.active

	b.mu.Lock()
	for _, entry := range entries {
		if !entry.canceled {
			b.queuedItems--
		}
		for waiter := range entry.waiters {
			waiter.result <- err
		}
		entry.waiters = nil
	}
	b.mu.Unlock()
}

func (b *personDirectoryBatcher) submit(ctx context.Context, entries []*personDirectoryEntry) error {
	memberships := make([]metadb.UserChannelMembership, 0, len(entries)*2)
	ready := make([]metadb.ChannelKey, 0, len(entries))
	for _, entry := range entries {
		memberships = append(memberships, entry.mutation.memberships...)
		ready = append(ready, entry.mutation.key)
	}
	if err := b.node.UpsertUserChannelMembershipBatch(ctx, memberships); err != nil {
		return err
	}
	return b.node.EnsureChannelDirectoriesReady(ctx, ready)
}
