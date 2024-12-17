package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type worker struct {
	id       int
	sub      *reactorSub
	inbound  *ready
	outbound *ready
	actions  []reactor.PushAction
	wklog.Log
}

func newWorker(id int, sub *reactorSub) *worker {
	prefix := fmt.Sprintf("worker[%d]", id)
	return &worker{
		id:       id,
		sub:      sub,
		inbound:  newReady(prefix),
		outbound: newReady(prefix),
		Log:      wklog.NewWKLog(prefix),
	}
}

func (w *worker) hasReady() bool {
	if len(w.actions) > 0 {
		return true
	}
	if w.inbound.has() {
		return true
	}

	if w.outbound.has() {
		return true
	}
	return false
}

func (w *worker) ready() []reactor.PushAction {
	// ---------- inbound ----------
	if w.inbound.has() {
		msgs := w.inbound.sliceAndTruncate()
		w.actions = append(w.actions, reactor.PushAction{
			WorkerId: w.id,
			Type:     reactor.PushActionInbound,
			Messages: msgs,
		})
	}
	// ---------- outbound ----------
	if w.outbound.has() {
		msgs := w.outbound.sliceAndTruncate()
		w.actions = append(w.actions, reactor.PushAction{
			WorkerId: w.id,
			Type:     reactor.PushActionOutboundForward,
			Messages: msgs,
		})
	}

	actions := w.actions
	w.actions = w.actions[:0]
	return actions
}

func (w *worker) step(a reactor.PushAction) {
	switch a.Type {
	case reactor.PushActionInboundAdd:
		for _, msg := range a.Messages {
			w.inbound.append(msg)
		}
	case reactor.PushActionOutboundAdd:
		for _, msg := range a.Messages {
			w.outbound.append(msg)
		}
	}
}

func (w *worker) tick() {
}
