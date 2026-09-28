package mqttsession

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// busyStreamSource serves one Slot with an endless backlog of full pages and
// every other Slot empty.
func busyStreamSource(busy uint16) *deadlineSource {
	s := &deadlineSource{}
	for i := 0; i < 256; i++ {
		s.slots = append(s.slots, meta.HashSlot(i))
	}
	s.read = func(h uint16, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		if h != busy {
			return meta.MQTTReadResult{After: q.After, Done: true}, nil
		}
		start := 0
		if id := q.After.SourceRecovery.Key.ClientID; id != "" {
			_, _ = fmt.Sscanf(id, "c%06d", &start)
			start++
		}
		rows := make([]meta.MQTTSourceBinding, 0, q.Limit)
		for i := start; i < start+q.Limit; i++ {
			rows = append(rows, consumerBinding(fmt.Sprintf("c%06d", i), 5000))
		}
		return meta.MQTTReadResult{Bindings: rows, After: consumerCursor(rows[len(rows)-1])}, nil
	}
	return s
}

func countVisits(v []uint16, h uint16) int {
	n := 0
	for _, x := range v {
		if x == h {
			n++
		}
	}
	return n
}

// Empty streams must not starve a backlogged stream: after one full pass the
// busy stream gets the whole page budget, and an idle stream is still reread
// no later than today's full-rotation bound.
func TestConsumerWorkerFocusesBudgetOnBackloggedStream(t *testing.T) {
	s := busyStreamSource(7)
	w := consumerWorkerFixture(t, s)
	var state consumerScanState
	admitAll := func(consumerWorkKey) bool { return true }
	// Nine turns read every empty stream once, so all of them are cooling.
	for range 9 {
		require.Equal(t, 32, w.sweep(context.Background(), &state, admitAll).Pages)
	}
	s.visited = nil
	o := w.sweep(context.Background(), &state, admitAll)
	require.Equal(t, 32, o.Pages)
	require.Equal(t, 32, countVisits(s.visited, 7), "budget follows the backlog")
	require.Equal(t, 32*16, o.Scheduled)

	// Cohort pressure stops continuation; the rest of the budget is not
	// spent re-reading the same refused page.
	s.visited = nil
	refused := 0
	o = w.sweep(context.Background(), &state, func(consumerWorkKey) bool { refused++; return refused <= 8 })
	require.Equal(t, 1, countVisits(s.visited, 7))
	require.Equal(t, 8, o.Scheduled)

	// Idle streams come back within the existing 24-turn rotation bound.
	s.visited = nil
	for range 24 {
		w.sweep(context.Background(), &state, admitAll)
	}
	require.Positive(t, countVisits(s.visited, 3), "idle stream must be reread")
}
