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
	// idle holds, per stream, the turn until which an empty stream is skipped.
	// It has at most one entry per enabled stream of a led Slot.
	idle map[uint32]uint64
	// turn counts sweeps; it only orders cooldown expiry.
	turn uint64
}

// consumerIdleCooldownTurns skips a stream for this many turns after it
// returned an empty terminal page (3.2s at the default 200ms interval).
const consumerIdleCooldownTurns = 16

// sweep advances only past visited/skipped or accepted keys. A stream keeps
// reading within the turn budget only while every row of its full page was
// admitted; failures, cohort pressure and terminal pages yield to the next.
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
	for h := range state.idle {
		if !led[slotOf(h)] {
			delete(state.idle, h)
		}
	}
	if len(slots) == 0 {
		state.next = 0
		return out
	}
	if state.cursors == nil {
		state.cursors = make(map[uint32]meta.MQTTReadCursor)
	}
	// Sorted streams rotate across all enabled kinds, including failed builds
	// and cohort pressure. Each stream position is examined at most once per
	// turn; cooling streams cost no page.
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
	if state.idle == nil {
		state.idle = make(map[uint32]uint64)
	}
	state.turn++
	budget := w.opts.PagesPerTurn
	// Streams with an unfinished cursor have known backlog. Up to half of the
	// turn continues them first, so idle streams outnumbering the budget cannot
	// delay a backlog by a whole rotation. Rotation skips streams that yielded here.
	served := make(map[uint32]bool, len(state.cursors))
	reserved := budget / 2
	for _, stream := range streams {
		if reserved <= 0 || ctx.Err() != nil {
			break
		}
		if _, ok := state.cursors[stream]; !ok || state.idle[stream] > state.turn {
			continue
		}
		// A stream that yielded (pressure, failure, terminal page) is done for
		// this turn; one still admitting may continue in rotation.
		for reserved > 0 && ctx.Err() == nil {
			reserved--
			budget--
			if !w.sweepPage(ctx, state, stream, slotOf(stream), stream >= reclamationBase, admit, &out) {
				served[stream] = true
				break
			}
		}
	}
	for examined := 0; examined < len(streams) && budget > 0 && ctx.Err() == nil; examined++ {
		stream := streams[index]
		index = (index + 1) % len(streams)
		if served[stream] || state.idle[stream] > state.turn {
			continue
		}
		state.next = stream + 1
		for budget > 0 && ctx.Err() == nil {
			budget--
			if !w.sweepPage(ctx, state, stream, slotOf(stream), stream >= reclamationBase, admit, &out) {
				break
			}
		}
	}
	return out
}

// sweepPage reads, validates and admits one page of stream. It reports true
// only when every row of a non-terminal page was admitted, so the stream may
// continue within the same turn. An empty terminal page starts a cooldown.
func (w *ConsumerWorker) sweepPage(ctx context.Context, state *consumerScanState, stream uint32, slot uint16, reclamation bool, admit func(consumerWorkKey) bool, out *ConsumerObservation) bool {
	kind := meta.MQTTReadSourceRecovery
	if reclamation {
		kind = meta.MQTTReadSessionReclamation
	} else if stream%2 == 1 {
		kind = meta.MQTTReadSubscriptionRecovery
	}
	q := meta.MQTTRead{Kind: kind, Limit: w.opts.PageSize, After: state.cursors[stream]}
	out.Pages++
	if kind == meta.MQTTReadSessionReclamation && !state.indexReady[slot] {
		call, done := context.WithTimeout(ctx, w.opts.CallTimeout)
		built, buildErr := w.buildReclamationIndex(call, slot)
		done()
		if buildErr != nil {
			out.Failures++
			return false
		}
		out.ReclamationIndexRows += built.Scanned
		if !built.Done {
			return false
		}
		state.indexReady[slot] = true
	}
	call, done := context.WithTimeout(ctx, w.opts.CallTimeout)
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
		return false
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
		return false
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
		if len(candidates) == 0 {
			state.idle[stream] = state.turn + consumerIdleCooldownTurns
		}
		return false
	}
	return complete
}
