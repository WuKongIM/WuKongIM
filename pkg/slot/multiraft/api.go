package multiraft

import (
	"context"
	"errors"
	"sort"

	raft "go.etcd.io/raft/v3"
)

func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	slots := make([]*slot, 0, len(r.slots))
	for _, g := range r.slots {
		slots = append(slots, g)
		g.mu.Lock()
		g.closed = true
		g.failPendingLocked(ErrRuntimeClosed)
		g.mu.Unlock()
	}
	close(r.stopCh)
	r.mu.Unlock()

	r.wg.Wait()

	for _, g := range slots {
		if r.apply != nil {
			r.apply.closeSlot(g.id)
		}
		g.mu.Lock()
		g.waitIdleLocked()
		g.mu.Unlock()
	}
	if r.apply != nil {
		r.apply.close()
	}

	r.mu.Lock()
	r.slots = make(map[SlotID]*slot)
	r.mu.Unlock()
	return nil
}

func (r *Runtime) OpenSlot(ctx context.Context, opts SlotOptions) error {
	if err := validateSlotOptions(opts); err != nil {
		return err
	}

	g, err := newSlot(ctx, r.opts.NodeID, r.opts.Logger, r.opts.Raft, opts, r.opts.Observer, r.apply)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrRuntimeClosed
	}
	if _, exists := r.slots[opts.ID]; exists {
		return ErrSlotExists
	}
	r.slots[opts.ID] = g
	return nil
}

func (r *Runtime) BootstrapSlot(ctx context.Context, req BootstrapSlotRequest) error {
	if err := validateSlotOptions(req.Slot); err != nil {
		return err
	}

	g, err := newSlot(ctx, r.opts.NodeID, r.opts.Logger, r.opts.Raft, req.Slot, r.opts.Observer, r.apply)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRuntimeClosed
	}
	if _, exists := r.slots[req.Slot.ID]; exists {
		r.mu.Unlock()
		return ErrSlotExists
	}
	r.slots[req.Slot.ID] = g
	if len(req.Voters) > 0 {
		peers := make([]raft.Peer, 0, len(req.Voters))
		for _, id := range req.Voters {
			peers = append(peers, raft.Peer{ID: uint64(id)})
		}
		if err := g.rawNode.Bootstrap(peers); err != nil {
			delete(r.slots, req.Slot.ID)
			r.mu.Unlock()
			return err
		}
		if len(req.Voters) == 1 && req.Voters[0] == r.opts.NodeID {
			g.requestCampaignAfterReady()
		}
	}
	r.mu.Unlock()

	r.scheduler.enqueue(req.Slot.ID)
	return nil
}

func (r *Runtime) CloseSlot(ctx context.Context, slotID SlotID) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	if !ok {
		r.mu.Unlock()
		return ErrSlotNotFound
	}
	g.mu.Lock()
	g.closed = true
	g.failPendingLocked(ErrSlotClosed)
	delete(r.slots, slotID)
	r.mu.Unlock()
	g.mu.Unlock()
	if r.apply != nil {
		r.apply.closeSlot(slotID)
	}
	g.mu.Lock()
	g.waitIdleLocked()
	g.mu.Unlock()
	return nil
}

func (r *Runtime) Step(ctx context.Context, msg Envelope) error {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return ErrRuntimeClosed
	}
	g, ok := r.slots[msg.SlotID]
	r.mu.RUnlock()
	if !ok {
		return ErrSlotNotFound
	}

	if err := g.enqueueRequest(msg.Message); err != nil {
		return err
	}
	r.scheduler.enqueue(msg.SlotID)
	return nil
}

func (r *Runtime) Propose(ctx context.Context, slotID SlotID, data []byte) (Future, error) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return nil, ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrSlotNotFound
	}

	fut := newFuture(proposalStageObserversFromContext(ctx))
	if err := g.enqueueControl(controlAction{
		kind:   controlPropose,
		data:   append([]byte(nil), data...),
		future: fut,
	}); err != nil {
		return nil, err
	}
	r.scheduler.enqueue(slotID)
	return fut, nil
}

func (r *Runtime) ChangeConfig(ctx context.Context, slotID SlotID, change ConfigChange) (Future, error) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return nil, ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrSlotNotFound
	}

	fut := newFuture(nil)
	if err := g.enqueueControl(controlAction{
		kind:   controlConfigChange,
		change: change,
		future: fut,
	}); err != nil {
		return nil, err
	}
	r.scheduler.enqueue(slotID)
	return fut, nil
}

func (r *Runtime) TransferLeadership(ctx context.Context, slotID SlotID, target NodeID) error {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	r.mu.RUnlock()
	if !ok {
		return ErrSlotNotFound
	}

	if err := g.enqueueControl(controlAction{
		kind:   controlTransferLeader,
		target: target,
	}); err != nil {
		return err
	}
	r.scheduler.enqueue(slotID)
	return nil
}

// CompactLog manually snapshots one local Slot and compacts its applied Raft entries.
func (r *Runtime) CompactLog(ctx context.Context, slotID SlotID) (LogCompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return LogCompactionResult{}, ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	r.mu.RUnlock()
	if !ok {
		return LogCompactionResult{}, ErrSlotNotFound
	}

	req := logCompactionRequest{
		ctx:  ctx,
		resp: make(chan logCompactionResponse, 1),
	}
	if err := g.enqueueControl(controlAction{kind: controlCompactLog, compact: &req}); err != nil {
		return LogCompactionResult{}, err
	}
	r.scheduler.enqueue(slotID)

	select {
	case resp := <-req.resp:
		return resp.result, resp.err
	case <-ctx.Done():
		return LogCompactionResult{}, ctx.Err()
	}
}

func (r *Runtime) Status(slotID SlotID) (Status, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.closed {
		return Status{}, ErrRuntimeClosed
	}
	g, ok := r.slots[slotID]
	if !ok {
		return Status{}, ErrSlotNotFound
	}
	st, err := g.statusSnapshot()
	if errors.Is(err, ErrSlotClosed) {
		return Status{}, ErrSlotNotFound
	}
	return st, err
}

func (r *Runtime) Slots() []SlotID {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]SlotID, 0, len(r.slots))
	for id := range r.slots {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func validateSlotOptions(opts SlotOptions) error {
	if opts.ID == 0 || opts.Storage == nil || opts.StateMachine == nil {
		return ErrInvalidOptions
	}
	return nil
}
