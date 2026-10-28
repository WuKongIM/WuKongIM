package diagnostics

import (
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"time"
)

// Metrics records low-cardinality diagnostics sink outcomes.
type Metrics interface {
	RecordEvent(stage, result string)
	RecordDropped(reason string)
	SetBufferUsageRatio(v float64)
}

// SendTraceSink adapts sendtrace events into the node-local diagnostics store.
type SendTraceSink struct {
	store    *Store
	sampler  *Sampler
	metrics  Metrics
	attempts *AttemptStore
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
	if s != nil && s.attempts != nil {
		switch event.Stage {
		case sendtrace.StageChannelAppendForward:
			s.attempts.RecordTraceRPC(event.TraceID, event.RPCReference)
		case sendtrace.StageMessageSendDurable:
			s.attempts.RecordTraceDurable(event.TraceID, event.Duration, event.Result)
		}
	}
	if s == nil || s.store == nil {
		return
	}
	diagEvent := Event{
		RPCReference: event.RPCReference,
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

// WithAttemptStore attaches the independently enabled fixed summary capability.
func (s *SendTraceSink) WithAttemptStore(store *AttemptStore) *SendTraceSink {
	if s != nil {
		s.attempts = store
	}
	return s
}

// AttemptLinkEnabled reports only the independent fixed-summary capability.
func (s *SendTraceSink) AttemptLinkEnabled() bool { return s != nil && s.attempts != nil }

// BeginSendAttempt forwards the existing ingress clock without another read.
func (s *SendTraceSink) BeginSendAttempt(key sendtrace.AttemptKey, at time.Time) sendtrace.AttemptToken {
	if s == nil {
		return sendtrace.AttemptToken{}
	}
	return s.attempts.BeginSendAttempt(key, at)
}

// RecordAttemptPhase forwards only observation data; it never changes a SEND result.
func (s *SendTraceSink) RecordAttemptPhase(token sendtrace.AttemptToken, p sendtrace.AttemptPhase) {
	if s != nil {
		s.attempts.RecordAttemptPhase(token, p)
	}
}

// BindAttemptTrace binds one exact invocation without choosing a shared caller.
func (s *SendTraceSink) BindAttemptTrace(token sendtrace.AttemptToken, trace string) {
	if s != nil {
		s.attempts.BindAttemptTrace(token, trace)
	}
}

// RecordAttemptRPC copies a bounded reference under the original invocation fence.
func (s *SendTraceSink) RecordAttemptRPC(token sendtrace.AttemptToken, ref sendtrace.RPCReference) {
	if s != nil {
		s.attempts.RecordAttemptRPC(token, ref)
	}
}

// DiscardSendAttempt removes only a reservation rejected before mailbox admission.
func (s *SendTraceSink) DiscardSendAttempt(token sendtrace.AttemptToken) {
	if s != nil {
		s.attempts.DiscardSendAttempt(token)
	}
}
