package mqttsession

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type consumerScanState struct {
	next    uint32
	cursors map[uint16]meta.MQTTReadCursor
}

// sweep advances only past visited/skipped or accepted keys. Each attempted
// source yields to the next Slot, including failures and cohort pressure.
func (w *ConsumerWorker) sweep(parent context.Context, state *consumerScanState, admit func(meta.MQTTSourceBindingKey) bool) (out ConsumerObservation) {
	started := time.Now()
	defer func() { out.Duration = time.Since(started) }()
	if parent.Err() != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(parent, w.opts.ScanTimeout)
	defer cancel()
	call, done := context.WithTimeout(ctx, w.opts.CallTimeout)
	slots, err := w.opts.Source.LocalLeaderHashSlots(call)
	if err == nil {
		err = call.Err()
	}
	done()
	if err != nil || len(slots) > int(w.opts.HashSlotCount) {
		out.Failures++
		return out
	}
	slots = slices.Clone(slots)
	slices.Sort(slots)
	led := make(map[uint16]bool, len(slots))
	for _, h := range slots {
		if uint16(h) >= w.opts.HashSlotCount || led[uint16(h)] {
			out.Failures++
			return out
		}
		led[uint16(h)] = true
	}
	for h := range state.cursors {
		if !led[h] {
			delete(state.cursors, h)
		}
	}
	if len(slots) == 0 {
		state.next = 0
		return out
	}
	if state.cursors == nil {
		state.cursors = make(map[uint16]meta.MQTTReadCursor)
	}
	index := sort.Search(len(slots), func(i int) bool { return uint32(slots[i]) >= state.next })
	if index == len(slots) {
		index = 0
	}
	for page := 0; page < min(w.opts.PagesPerTurn, len(slots)) && ctx.Err() == nil; page++ {
		slot := uint16(slots[index])
		state.next = uint32(slot) + 1
		index = (index + 1) % len(slots)
		q := meta.MQTTRead{Kind: meta.MQTTReadSourceRecovery, Limit: w.opts.PageSize, After: state.cursors[slot]}
		out.Pages++
		call, done = context.WithTimeout(ctx, w.opts.CallTimeout)
		r, e := w.opts.Source.ReadMQTTRecovery(call, slot, q)
		if e == nil {
			e = call.Err()
		}
		done()
		if e != nil {
			out.Failures++
			continue
		}
		// Validate every witness before admitting any key from this page.
		candidates, e := consumerCandidates(q, r)
		if e != nil {
			out.Failures++
			continue
		}
		complete := true
		for _, candidate := range candidates {
			if ctx.Err() != nil {
				complete = false
				break
			}
			now := w.opts.Now().UnixMilli()
			if now <= 0 {
				out.Failures++
				complete = false
				break
			}
			out.Visited++
			if candidate.SourceRecovery.RecoveryAtMS > now {
				delete(state.cursors, slot)
				complete = false
				break
			}
			if candidate.SourceRecovery.Key.Owner.Kind == meta.MQTTBindingChannel {
				if !admit(candidate.SourceRecovery.Key) {
					complete = false
					break
				}
				out.Scheduled++
			}
			state.cursors[slot] = candidate
		}
		if complete && r.Done {
			delete(state.cursors, slot)
		}
	}
	return out
}
