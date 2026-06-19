package diagnostics

import "github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"

// Metrics records low-cardinality diagnostics sink outcomes.
type Metrics interface {
	RecordEvent(stage, result string)
	RecordDropped(reason string)
	SetBufferUsageRatio(v float64)
}

// SendTraceSink adapts sendtrace events into the node-local diagnostics store.
type SendTraceSink struct {
	store   *Store
	sampler *Sampler
	metrics Metrics
}

// NewSendTraceSink creates a diagnostics sink for sendtrace hot-path events.
func NewSendTraceSink(store *Store, sampler *Sampler) *SendTraceSink {
	return &SendTraceSink{store: store, sampler: sampler}
}

// WithMetrics attaches optional diagnostics metrics to the sink.
func (s *SendTraceSink) WithMetrics(metrics Metrics) *SendTraceSink {
	if s == nil {
		return nil
	}
	s.metrics = metrics
	return s
}

// KeepSendTraceDetail implements sendtrace.DetailSampler for expensive reactor/store detail.
func (s *SendTraceSink) KeepSendTraceDetail(key sendtrace.DetailKey) sendtrace.DetailDecision {
	if s == nil || s.sampler == nil {
		return sendtrace.DetailDecision{}
	}
	keep, reason := s.sampler.KeepDetail(Event{
		TraceID:     key.TraceID,
		ChannelKey:  key.ChannelKey,
		ClientMsgNo: key.ClientMsgNo,
		FromUID:     key.FromUID,
		Result:      ResultOK,
	})
	return sendtrace.DetailDecision{Keep: keep, Reason: reason}
}

// SendTraceDetailLimits implements sendtrace.DetailSampler.
func (s *SendTraceSink) SendTraceDetailLimits() sendtrace.DetailLimits {
	if s == nil || s.sampler == nil {
		return sendtrace.DetailLimits{}
	}
	slow, maxItems := s.sampler.DetailLimits()
	return sendtrace.DetailLimits{SlowThreshold: slow, MaxItemsPerBatch: maxItems}
}

// RecordSendTrace maps a sendtrace event into diagnostics and records it when sampled.
func (s *SendTraceSink) RecordSendTrace(event sendtrace.Event) {
	if s == nil || s.store == nil {
		return
	}
	diagEvent := Event{
		TraceID:      event.TraceID,
		Stage:        Stage(event.Stage),
		At:           event.At,
		Duration:     event.Duration,
		NodeID:       event.NodeID,
		PeerNodeID:   event.PeerNodeID,
		ChannelKey:   event.ChannelKey,
		ClientMsgNo:  event.ClientMsgNo,
		MessageSeq:   event.MessageSeq,
		RangeStart:   event.RangeStart,
		RangeEnd:     event.RangeEnd,
		FromUID:      event.FromUID,
		Result:       Result(event.Result),
		ErrorCode:    ErrorCode(event.ErrorCode),
		Error:        event.Error,
		Attempt:      event.Attempt,
		RequestCount: event.RequestCount,
		RecordCount:  event.RecordCount,
		ByteCount:    event.ByteCount,
		QueueDepth:   event.QueueDepth,
		Service:      event.Service,
	}
	if diagEvent.Result == "" {
		diagEvent.Result = ResultOK
	}
	if s.sampler != nil {
		keep, reason := s.sampler.Keep(diagEvent)
		if !keep {
			if s.metrics != nil {
				s.metrics.RecordDropped("sampled_out")
			}
			return
		}
		diagEvent.SampleReason = reason
	}
	s.store.Record(diagEvent)
	if s.metrics != nil {
		s.metrics.RecordEvent(string(diagEvent.Stage), string(diagEvent.Result))
		s.metrics.SetBufferUsageRatio(s.store.UsageRatio())
	}
}
