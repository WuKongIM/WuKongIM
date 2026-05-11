package sendtrace

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *recordingSink) RecordSendTrace(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func TestSetSinkRestoreHandlesNilPrevious(t *testing.T) {
	sink := &recordingSink{}
	restore := SetSink(sink)
	Record(Event{Stage: StageGatewayMessagesSend, TraceID: "trace-1", Result: ResultOK})
	restore()
	Record(Event{Stage: StageGatewayMessagesSend, TraceID: "trace-2", Result: ResultOK})

	require.Len(t, sink.events, 1)
	require.Equal(t, "trace-1", sink.events[0].TraceID)
}
