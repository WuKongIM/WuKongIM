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
	cursors map[uint32]meta.MQTTReadCursor
}

// sweep advances only past visited/skipped or accepted keys. Each attempted
// source yields to the next Slot, including failures and cohort pressure.
func (w *ConsumerWorker) sweep(parent context.Context, state *consumerScanState, admit func(consumerWorkKey) bool) (out ConsumerObservation) {
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
		if !led[uint16(h/2)] {
			delete(state.cursors, h)
		}
	}
	if len(slots) == 0 {
		state.next = 0
		return out
	}
	if state.cursors == nil {
		state.cursors = make(map[uint32]meta.MQTTReadCursor)
	}
	// Each led Slot owns at most two independent continuations. Rotation uses
	// (Slot, stream) order, so a pressured source cannot starve pending intent.
	streams := make([]uint32, 0, len(slots)*2)
	for _, h := range slots {
		streams = append(streams, uint32(h)*2)
		if w.opts.Subscriptions != nil {
			streams = append(streams, uint32(h)*2+1)
		}
	}
	index := sort.Search(len(streams), func(i int) bool { return streams[i] >= state.next })
	if index == len(streams) {
		index = 0
	}
	for page := 0; page < min(w.opts.PagesPerTurn, len(streams)) && ctx.Err() == nil; page++ {
		stream := streams[index]
		slot := uint16(stream / 2)
		state.next = stream + 1
		index = (index + 1) % len(streams)
		kind := meta.MQTTReadSourceRecovery
		if stream%2 == 1 {
			kind = meta.MQTTReadSubscriptionRecovery
		}
		q := meta.MQTTRead{Kind: kind, Limit: w.opts.PageSize, After: state.cursors[stream]}
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
		var candidates []meta.MQTTReadCursor
		if kind == meta.MQTTReadSubscriptionRecovery {
			candidates, e = consumerSubscriptionCandidates(q, r)
		} else {
			candidates, e = consumerCandidates(q, r)
		}
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
			due := candidate.SourceRecovery.RecoveryAtMS
			key := consumerWorkKey{binding: candidate.SourceRecovery.Key}
			if kind == meta.MQTTReadSubscriptionRecovery {
				due = candidate.Subscription.RecoveryAtMS
				key = consumerWorkKey{subscription: candidate.Subscription}
				key.subscription.RecoveryAtMS = 0
			}
			if due > now {
				delete(state.cursors, stream)
				complete = false
				break
			}
			if !admit(key) {
				complete = false
				break
			}
			out.Scheduled++
			state.cursors[stream] = candidate
		}
		if complete && r.Done {
			delete(state.cursors, stream)
		}
	}
	return out
}
