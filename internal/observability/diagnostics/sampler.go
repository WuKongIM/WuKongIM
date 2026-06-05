package diagnostics

import (
	"math"
	"sync/atomic"
	"time"
)

const DefaultDeepMaxItemsPerBatch = 16

// SamplerOptions configures low-cost diagnostics event retention decisions.
type SamplerOptions struct {
	// SampleRate is the baseline keep probability for ordinary successful events.
	SampleRate float64
	// SlowThreshold keeps otherwise successful events whose duration meets this threshold.
	SlowThreshold time.Duration
	// ErrorSampleRate overrides error-event sampling when ErrorSampleRateSet is true.
	ErrorSampleRate float64
	// ErrorSampleRateSet distinguishes an explicit zero error sample rate from the default keep-all behavior.
	ErrorSampleRateSet bool
	// DebugMatches contains immutable temporary match rules evaluated before ordinary sampling.
	DebugMatches []DebugMatch
	// TrackingRules contains mutable runtime rules evaluated before static debug matches.
	TrackingRules *TrackingRules
	// Now supplies time for debug-match TTL checks.
	Now func() time.Time
	// DeepSampleRate is the keep probability for expensive reactor/store detail sidecars.
	DeepSampleRate float64
	// DeepSlowThreshold enables lazy deep trace selection for slow reactor/store stages.
	DeepSlowThreshold time.Duration
	// DeepMaxItemsPerBatch bounds how many trace items one deep batch expands.
	DeepMaxItemsPerBatch int
}

// DebugMatch defines one temporary high-priority diagnostics sampling rule.
type DebugMatch struct {
	UID         string
	ChannelKey  string
	ClientMsgNo string
	TraceID     string
	TTL         time.Duration
	SampleRate  float64
}

type debugRule struct {
	match     DebugMatch
	expiresAt time.Time
	counter   atomic.Uint64
}

// Sampler makes bounded, lock-free keep/drop decisions after construction.
type Sampler struct {
	sampleRate           float64
	slowThreshold        time.Duration
	errorSampleRate      float64
	errorSampleRateSet   bool
	deepSampleRate       float64
	deepSlowThreshold    time.Duration
	deepMaxItemsPerBatch int
	debugRules           []*debugRule
	trackingRules        *TrackingRules
	now                  func() time.Time
	counter              atomic.Uint64
	errorCounter         atomic.Uint64
	deepCounter          atomic.Uint64
}

// NewSampler builds an immutable sampler suitable for hot-path diagnostics calls.
func NewSampler(opts SamplerOptions) *Sampler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	now := opts.Now()
	rules := make([]*debugRule, 0, len(opts.DebugMatches))
	for _, match := range opts.DebugMatches {
		if match.TTL <= 0 {
			continue
		}
		rules = append(rules, &debugRule{match: match, expiresAt: now.Add(match.TTL)})
	}
	deepSlowThreshold := opts.DeepSlowThreshold
	if deepSlowThreshold <= 0 {
		deepSlowThreshold = opts.SlowThreshold
	}
	deepMaxItems := opts.DeepMaxItemsPerBatch
	if deepMaxItems <= 0 {
		deepMaxItems = DefaultDeepMaxItemsPerBatch
	}
	return &Sampler{
		sampleRate:           opts.SampleRate,
		slowThreshold:        opts.SlowThreshold,
		errorSampleRate:      opts.ErrorSampleRate,
		errorSampleRateSet:   opts.ErrorSampleRateSet,
		deepSampleRate:       opts.DeepSampleRate,
		deepSlowThreshold:    deepSlowThreshold,
		deepMaxItemsPerBatch: deepMaxItems,
		debugRules:           rules,
		trackingRules:        opts.TrackingRules,
		now:                  opts.Now,
	}
}

// Keep returns whether event should be retained and a stable reason token.
func (s *Sampler) Keep(event Event) (bool, string) {
	if s == nil {
		return false, ""
	}
	if s.trackingRules != nil && s.trackingRules.Keep(event) {
		return true, "debug"
	}
	if s.keepDebug(event) {
		return true, "debug"
	}
	if s.slowThreshold > 0 && event.Duration >= s.slowThreshold {
		return true, "slow"
	}
	if isErrorResult(event.Result) {
		if !s.errorSampleRateSet {
			return true, "error"
		}
		if keepByRate(s.errorSampleRate, &s.errorCounter) {
			return true, "error"
		}
		return false, ""
	}
	if keepByRate(s.sampleRate, &s.counter) {
		return true, "sample"
	}
	return false, ""
}

// KeepDetail returns whether expensive deep SEND trace detail should be collected.
func (s *Sampler) KeepDetail(event Event) (bool, string) {
	if s == nil {
		return false, ""
	}
	if s.trackingRules != nil && s.trackingRules.Keep(event) {
		return true, "debug"
	}
	if s.keepDebug(event) {
		return true, "debug"
	}
	if keepByRate(s.deepSampleRate, &s.deepCounter) {
		return true, "sample"
	}
	return false, ""
}

// DetailLimits returns normalized slow-stage and item-count limits for deep tracing.
func (s *Sampler) DetailLimits() (time.Duration, int) {
	if s == nil {
		return 0, 0
	}
	return s.deepSlowThreshold, s.deepMaxItemsPerBatch
}

func (s *Sampler) keepDebug(event Event) bool {
	if len(s.debugRules) == 0 {
		return false
	}
	now := s.now()
	for _, rule := range s.debugRules {
		if now.After(rule.expiresAt) || !debugMatches(rule.match, event) {
			continue
		}
		if keepByRate(rule.match.SampleRate, &rule.counter) {
			return true
		}
	}
	return false
}

func debugMatches(match DebugMatch, event Event) bool {
	if match.UID != "" && event.FromUID != match.UID {
		return false
	}
	if match.ChannelKey != "" && event.ChannelKey != match.ChannelKey {
		return false
	}
	if match.ClientMsgNo != "" && event.ClientMsgNo != match.ClientMsgNo {
		return false
	}
	if match.TraceID != "" && event.TraceID != match.TraceID {
		return false
	}
	return match.UID != "" || match.ChannelKey != "" || match.ClientMsgNo != "" || match.TraceID != ""
}

const sampleRateScale = uint64(1_000_000)

func keepByRate(rate float64, counter *atomic.Uint64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	threshold := uint64(math.Round(rate * float64(sampleRateScale)))
	if threshold == 0 {
		return false
	}
	n := counter.Add(1)
	slot := (n * 2_654_435_761) % sampleRateScale
	return slot < threshold
}
