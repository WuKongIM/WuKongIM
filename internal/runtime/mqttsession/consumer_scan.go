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
	// indexReady is a bounded process hint; reads still require durable coverage.
	indexReady map[uint16]bool
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
	// Preserve the two existing stream IDs; reclamation occupies one disjoint
	// range after them. There are at most three cursors per led Slot.
	reclamationBase := uint32(w.opts.HashSlotCount) * 2
	slotOf := func(stream uint32) uint16 {
		if stream >= reclamationBase {
			return uint16(stream - reclamationBase)
		}
		return uint16(stream / 2)
	}
	for h := range state.indexReady {
		if !led[h] {
			delete(state.indexReady, h)
		}
	}
	for h := range state.cursors {
		if !led[slotOf(h)] {
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
	// Sorted streams rotate fairly across all enabled kinds, including failed
	// builds and cohort pressure. A turn attempts at most one page per stream.
	streams := make([]uint32, 0, len(slots)*3)
	for _, h := range slots {
		streams = append(streams, uint32(h)*2)
		if w.opts.Subscriptions != nil {
			streams = append(streams, uint32(h)*2+1)
		}
	}
	if w.opts.Reclamation != nil {
		if state.indexReady == nil {
			state.indexReady = make(map[uint16]bool)
		}
		for _, h := range slots {
			streams = append(streams, reclamationBase+uint32(h))
		}
	}
	index := sort.Search(len(streams), func(i int) bool { return streams[i] >= state.next })
	if index == len(streams) {
		index = 0
	}
	for page := 0; page < min(w.opts.PagesPerTurn, len(streams)) && ctx.Err() == nil; page++ {
		stream := streams[index]
		slot := slotOf(stream)
		state.next = stream + 1
		index = (index + 1) % len(streams)
		kind := meta.MQTTReadSourceRecovery
		if stream >= reclamationBase {
			kind = meta.MQTTReadSessionReclamation
		} else if stream%2 == 1 {
			kind = meta.MQTTReadSubscriptionRecovery
		}
		q := meta.MQTTRead{Kind: kind, Limit: w.opts.PageSize, After: state.cursors[stream]}
		out.Pages++
		if kind == meta.MQTTReadSessionReclamation && !state.indexReady[slot] {
			call, done = context.WithTimeout(ctx, w.opts.CallTimeout)
			built, buildErr := w.buildReclamationIndex(call, slot)
			done()
			if buildErr != nil {
				out.Failures++
				continue
			}
			out.ReclamationIndexRows += built.Scanned
			if !built.Done {
				continue
			}
			state.indexReady[slot] = true
		}
		call, done = context.WithTimeout(ctx, w.opts.CallTimeout)
		r, e := w.opts.Source.ReadMQTTRecovery(call, slot, q)
		if e == nil {
			e = call.Err()
		}
		done()
		if e != nil {
			if kind == meta.MQTTReadSessionReclamation {
				delete(state.indexReady, slot)
			}
			out.Failures++
			continue
		}
		// Validate every witness before admitting any key from this page.
		var candidates []meta.MQTTReadCursor
		if kind == meta.MQTTReadSessionReclamation {
			candidates, e = consumerReclamationCandidates(q, r)
		} else if kind == meta.MQTTReadSubscriptionRecovery {
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
			if kind == meta.MQTTReadSessionReclamation {
				due = 0
				key = consumerWorkKey{session: candidate.Session}
			} else if kind == meta.MQTTReadSubscriptionRecovery {
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
