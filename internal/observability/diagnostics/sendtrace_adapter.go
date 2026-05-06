package diagnostics

import "github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"

// SendTraceSink adapts sendtrace events into the node-local diagnostics store.
type SendTraceSink struct {
	store   *Store
	sampler *Sampler
}

// NewSendTraceSink creates a diagnostics sink for sendtrace hot-path events.
func NewSendTraceSink(store *Store, sampler *Sampler) *SendTraceSink {
	return &SendTraceSink{store: store, sampler: sampler}
}

// RecordSendTrace maps a sendtrace event into diagnostics and records it when sampled.
func (s *SendTraceSink) RecordSendTrace(event sendtrace.Event) {
	if s == nil || s.store == nil {
		return
	}
	diagEvent := Event{
		TraceID:     event.TraceID,
		Stage:       Stage(event.Stage),
		At:          event.At,
		Duration:    event.Duration,
		NodeID:      event.NodeID,
		PeerNodeID:  event.PeerNodeID,
		ChannelKey:  event.ChannelKey,
		ClientMsgNo: event.ClientMsgNo,
		MessageSeq:  event.MessageSeq,
		RangeStart:  event.RangeStart,
		RangeEnd:    event.RangeEnd,
		Result:      Result(event.Result),
		ErrorCode:   ErrorCode(event.ErrorCode),
		Error:       event.Error,
		Attempt:     event.Attempt,
		Service:     event.Service,
	}
	if diagEvent.Result == "" {
		diagEvent.Result = ResultOK
	}
	if s.sampler != nil {
		keep, reason := s.sampler.Keep(diagEvent)
		if !keep {
			return
		}
		diagEvent.SampleReason = reason
	}
	s.store.Record(diagEvent)
}
