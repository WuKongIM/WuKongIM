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
	for _, g := range r.slots {
		g.mu.Lock()
		g.closed = true
		g.failPendingLocked(ErrRuntimeClosed)
		g.mu.Unlock()
	}
	close(r.stopCh)
	r.mu.Unlock()

	r.wg.Wait()

	r.mu.Lock()
	r.slots = make(map[SlotID]*slot)
	r.mu.Unlock()
	return nil
}

func (r *Runtime) OpenSlot(ctx context.Context, opts SlotOptions) error {
	if err := validateSlotOptions(opts); err != nil {
		return err
	}

	g, err := newSlot(ctx, r.opts.NodeID, r.opts.Logger, r.opts.Raft, opts)
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

	g, err := newSlot(ctx, r.opts.NodeID, r.opts.Logger, r.opts.Raft, req.Slot)
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
			if err := g.enqueueControl(controlAction{kind: controlCampaign}); err != nil {
				delete(r.slots, req.Slot.ID)
				r.mu.Unlock()
				return err
			}
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
	for g.processing {
		g.cond.Wait()
	}
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

	fut := newFuture()
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

	fut := newFuture()
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
