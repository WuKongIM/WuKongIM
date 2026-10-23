package multiraft

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// storageWork is one accepted Ready's local messages. The append FIFO also
// orders apply-only work behind prior HardState.Commit persistence, without
// inventing a Raft response or performing an empty Save.
type storageWork struct {
	slot                *slot
	append, apply       raftpb.Message
	hasAppend, hasApply bool
	lease               *storageLease
	transport           Transport
	commitBefore        uint64
}

type storageCompletion struct {
	work    *storageWork
	part    uint8
	persist PersistentState
	err     error
}

// storagePipeline serializes append work per generation and caps physical
// worker concurrency. Completion memory was prepaid by each Ready's lease.
type storagePipeline struct {
	mu     sync.Mutex
	queues map[*slot][]*storageWork
	ready  chan *slot
	stop   chan struct{}
	wg     sync.WaitGroup
	budget *storageBudget
}

func newStoragePipeline(workers int, budget *storageBudget, registry *goroutine.Registry) *storagePipeline {
	p := &storagePipeline{queues: make(map[*slot][]*storageWork), ready: make(chan *slot, budget.nodeCount), stop: make(chan struct{}), budget: budget}
	for range workers {
		p.wg.Add(1)
		goroutine.SafeGo(registry, goroutine.TaskSlotRaftWorker, func() { defer p.wg.Done(); p.run() })
	}
	return p
}

// enqueue transfers prepaid Ready ownership to its generation's append FIFO.
// Admission and the running-task count are published before workers can see it.
func (p *storagePipeline) enqueue(w *storageWork) error {
	g := w.slot
	g.mu.Lock()
	if err := g.admissionErrLocked(); err != nil {
		g.mu.Unlock()
		return err
	}
	g.storageTasks++
	g.mu.Unlock()
	p.mu.Lock()
	q := p.queues[g]
	p.queues[g] = append(q, w)
	if len(q) == 0 {
		p.ready <- g
	}
	p.mu.Unlock()
	return nil
}

// run executes one append per generation at a time, then rotates to other Slots.
// It fences failures and drops queue references before publishing completion.
func (p *storagePipeline) run() {
	for {
		select {
		case <-p.stop:
			return
		case g := <-p.ready:
			p.mu.Lock()
			w := p.queues[g][0]
			p.mu.Unlock()
			persist, err := g.runStorageAppend(context.Background(), w, nil)
			// Stop this generation before the FIFO can begin any successor Save.
			// Waiting for the owner to consume the error could persist a later
			// HardState.Commit over an undurable predecessor.
			if err != nil {
				g.fail(err)
			}
			// Drop the queue's payload reference before completion can refund it.
			// Keep a nil running-head sentinel until publication, preserving FIFO
			// against a concurrent enqueue for this same generation.
			p.mu.Lock()
			p.queues[g][0] = nil
			p.mu.Unlock()
			g.publishStorageCompletion(storageCompletion{work: w, part: storageAppendPart, persist: persist, err: err})
			p.mu.Lock()
			q := p.queues[g]
			q[0] = nil
			q = q[1:]
			if len(q) == 0 {
				delete(p.queues, g)
			} else {
				p.queues[g] = q
				p.ready <- g
			}
			p.mu.Unlock()
		}
	}
}

func (p *storagePipeline) close() {
	if p != nil {
		close(p.stop)
		p.wg.Wait()
	}
}

func storageAppendReady(m raftpb.Message) raft.Ready {
	rd := raft.Ready{Entries: m.Entries, HardState: raftpb.HardState{Term: m.Term, Vote: m.Vote, Commit: m.Commit}}
	if m.Snapshot != nil {
		rd.Snapshot = *m.Snapshot
	}
	return rd
}

func (g *slot) acquireSave(ctx context.Context, owner *ownerStorageToken) (func(), error) {
	if g.storagePipeline == nil {
		return func() {}, nil
	}
	prior := owner.setStage(ownerStagePermit)
	defer owner.setStage(prior)
	select {
	case g.storagePipeline.budget.saves <- struct{}{}:
		return func() { <-g.storagePipeline.budget.saves }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// runStorageAppend executes only physical persistence. Memory and RawNode
// mutation remain with the Raft owner after the completion is consumed.
func (g *slot) runStorageAppend(ctx context.Context, w *storageWork, owner *ownerStorageToken) (PersistentState, error) {
	if err := g.currentErr(); err != nil {
		return PersistentState{}, err
	}
	if !w.hasAppend {
		return PersistentState{}, nil
	}
	rd := storageAppendReady(w.append)
	var release func()
	var err error
	if len(rd.Entries) > 0 || !raft.IsEmptyHardState(rd.HardState) || !raft.IsEmptySnap(rd.Snapshot) {
		release, err = g.acquireSave(ctx, owner)
		if err != nil {
			return PersistentState{}, err
		}
		defer release()
		// Close can begin while this accepted work waits for a node IO permit.
		// Skip work that has not entered Storage; already entered calls still join.
		if err := g.currentErr(); err != nil {
			return PersistentState{}, err
		}
	}
	var stage rpctrace.Stage
	if g.readBarrierTracing {
		stage = readySaveStage(rd)
		g.beginReadReadyOverlay(stage, false)
	}
	persist, err := g.storageView.persistReadyDurable(ctx, rd)
	if g.readBarrierTracing {
		g.endReadReadyOverlay(stage, err, false)
	}
	if err != nil {
		return PersistentState{}, err
	}
	// This fault marker observes persisted but not yet quorum-committed bytes.
	// The owner captures the commit boundary before handing work to this lane.
	persistMatch := ""
	// gofail: var wkSlotPersistedCommandMatch string
	// persistMatch = wkSlotPersistedCommandMatch
	if persistMatch != "" {
		for _, entry := range rd.Entries {
			if matchesSlotFaultCommand(entry.Data, persistMatch) && entry.Index > w.commitBefore {
				// gofail: var wkSlotPersistedUncommitted bool
				// _ = wkSlotPersistedUncommitted
			}
		}
	}
	return persist, nil
}

// publishStorageCompletion never enters the bounded, lossy network queue.
// Closed/fatal generations retire work after its physical call has returned,
// so shutdown does not need a Raft owner that has already stopped.
func (g *slot) publishStorageCompletion(c storageCompletion) {
	g.mu.Lock()
	retired := g.closed || g.fatalErr != nil
	if !retired {
		g.storageCompletions = append(g.storageCompletions, c)
	} else {
		c.work.lease.retire()
	}
	if c.part == storageAppendPart {
		g.storageTasks--
	} else if g.applying > 0 {
		g.applying--
	}
	if g.cond != nil {
		g.cond.Broadcast()
	}
	wake := g.storageWake
	notify := g.storageNotify
	g.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	if notify != nil {
		notify()
	}
}

// drainStorageCompletions runs only on the Raft owner. It publishes durable
// memory, steps original responses and retires credits in completion order.
func (g *slot) drainStorageCompletions() bool {
	worked := false
	for {
		g.mu.Lock()
		if len(g.storageCompletions) == 0 {
			g.mu.Unlock()
			return worked
		}
		c := g.storageCompletions[0]
		copy(g.storageCompletions, g.storageCompletions[1:])
		g.storageCompletions[len(g.storageCompletions)-1] = storageCompletion{}
		g.storageCompletions = g.storageCompletions[:len(g.storageCompletions)-1]
		if len(g.storageCompletions) == 0 {
			g.storageCompletions = nil
		}
		retired := g.closed || g.fatalErr != nil
		g.mu.Unlock()
		worked = true
		if retired {
			c.work.lease.retire()
			continue
		}
		if c.err != nil {
			g.fail(c.err)
			c.work.lease.retire()
			continue
		}
		if c.part == storageAppendPart {
			if err := g.storageView.applyReadyToMemory(c.persist); err != nil {
				g.fail(err)
				c.work.lease.retire()
				continue
			}
			if c.work.hasAppend {
				g.deliverStorageResponses(context.Background(), c.work.append.Responses, c.work.transport)
			}
			if c.work.hasApply {
				task := applyTask{slot: g, entries: c.work.apply.Entries, appliedBefore: g.appliedIndex(), storageWork: c.work}
				if err := g.apply.enqueue(task); err != nil {
					g.fail(err)
					c.work.lease.retire()
					continue
				}
			}
		} else {
			g.deliverStorageResponses(context.Background(), c.work.apply.Responses, c.work.transport)
		}
		c.work.lease.complete(c.part)
	}
}

func (g *slot) deliverStorageResponses(ctx context.Context, messages []raftpb.Message, transport Transport) {
	start := 0
	for i, m := range messages {
		if m.To == uint64(g.nodeID()) {
			g.sendReadyMessages(ctx, messages[start:i], transport)
			if err := g.rawNode.Step(m); err != nil {
				g.fail(err)
				return
			}
			start = i + 1
		}
	}
	g.sendReadyMessages(ctx, messages[start:], transport)
}

func (g *slot) sendReadyMessages(ctx context.Context, messages []raftpb.Message, transport Transport) {
	if len(messages) == 0 {
		return
	}
	g.transportBuf = wrapMessagesIntoForTransport(g.transportBuf[:0], g.id, messages, transport)
	if g.readBarrierTracing {
		g.beginReadReadyOverlay(rpctrace.StageSlotRaftSend, true)
	}
	err := transport.Send(ctx, g.transportBuf)
	if g.readBarrierTracing {
		g.endReadReadyOverlay(rpctrace.StageSlotRaftSend, err, true)
	}
	clear(g.transportBuf)
	g.transportBuf = g.transportBuf[:0]
}

// waitStorageIdle is owner-only and drains private completions while joining
// both lanes. It never waits under either pipeline lock or for its own callback.
func (g *slot) waitStorageIdle(ctx context.Context, owner *ownerStorageToken) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		g.drainStorageCompletions()
		g.mu.Lock()
		idle := g.storageTasks == 0 && g.applying == 0 && len(g.storageCompletions) == 0
		wake := g.storageWake
		g.mu.Unlock()
		if idle {
			return ctx.Err()
		}
		prior := owner.setStage(ownerStageDrain)
		if wake == nil {
			err := g.waitApplyIdle(ctx)
			owner.setStage(prior)
			return err
		}
		select {
		case <-wake:
			owner.setStage(prior)
		case <-ctx.Done():
			owner.setStage(prior)
			return ctx.Err()
		}
	}
}

// retireStorageCompletions runs after the generation's physical lanes join.
func (g *slot) retireStorageCompletions() {
	g.mu.Lock()
	g.retireStorageCompletionsLocked()
	g.mu.Unlock()
}

// retireStorageCompletionsLocked drops completed payloads before refunding them.
// In-flight append/apply work is absent from this mailbox and remains charged
// until its physical call returns and publishes its own completion.
func (g *slot) retireStorageCompletionsLocked() {
	for i := range g.storageCompletions {
		lease := g.storageCompletions[i].work.lease
		g.storageCompletions[i] = storageCompletion{}
		lease.retire()
	}
	g.storageCompletions = nil
}
