package diagnostics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSamplerKeepsErrorsAndSlowEvents(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SampleRate: 0, SlowThreshold: 500 * time.Millisecond})
	keep, reason := sampler.Keep(Event{Result: ResultError})
	require.True(t, keep)
	require.Equal(t, "error", reason)

	keep, reason = sampler.Keep(Event{Result: ResultOK, Duration: time.Second})
	require.True(t, keep)
	require.Equal(t, "slow", reason)
}

func TestSamplerDropsUnsampledSuccess(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SampleRate: 0, SlowThreshold: time.Second})
	keep, _ := sampler.Keep(Event{Result: ResultOK, Duration: time.Millisecond})
	require.False(t, keep)
}

func TestSamplerKeepsDebugMatchesUntilTTL(t *testing.T) {
	now := time.Unix(10, 0)
	sampler := NewSampler(SamplerOptions{
		SampleRate: 0,
		Now:        func() time.Time { return now },
		DebugMatches: []DebugMatch{{
			ClientMsgNo: "c1",
			TTL:         time.Minute,
			SampleRate:  1,
		}},
	})
	keep, reason := sampler.Keep(Event{ClientMsgNo: "c1"})
	require.True(t, keep)
	require.Equal(t, "debug", reason)
}

func TestSamplerExpiresDebugMatches(t *testing.T) {
	now := time.Unix(10, 0)
	sampler := NewSampler(SamplerOptions{
		SampleRate: 0,
		Now:        func() time.Time { return now },
		DebugMatches: []DebugMatch{{
			ClientMsgNo: "c1",
			TTL:         time.Second,
			SampleRate:  1,
		}},
	})
	now = now.Add(2 * time.Second)
	keep, _ := sampler.Keep(Event{ClientMsgNo: "c1"})
	require.False(t, keep)
}

func TestSamplerHonorsErrorSampleRate(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SampleRate: 0, ErrorSampleRate: 0, ErrorSampleRateSet: true})
	keep, _ := sampler.Keep(Event{Result: ResultError})
	require.False(t, keep)
}

func TestSamplerRateNinetyPercentDoesNotKeepEveryEvent(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SampleRate: 0.9})
	kept := 0
	for i := 0; i < 1000; i++ {
		keep, _ := sampler.Keep(Event{Result: ResultOK})
		if keep {
			kept++
		}
	}

	require.GreaterOrEqual(t, kept, 890)
	require.LessOrEqual(t, kept, 910)
}

func TestSamplerKeepsSlowErrorWhenErrorSampleRateDropsErrors(t *testing.T) {
	sampler := NewSampler(SamplerOptions{SampleRate: 0, SlowThreshold: time.Second, ErrorSampleRate: 0, ErrorSampleRateSet: true})

	keep, reason := sampler.Keep(Event{Result: ResultError, Duration: 2 * time.Second})

	require.True(t, keep)
	require.Equal(t, "slow", reason)
}

func TestSamplerKeepsDebugErrorWhenErrorSampleRateDropsErrors(t *testing.T) {
	now := time.Unix(10, 0)
	sampler := NewSampler(SamplerOptions{
		SampleRate:         0,
		ErrorSampleRate:    0,
		ErrorSampleRateSet: true,
		Now:                func() time.Time { return now },
		DebugMatches:       []DebugMatch{{TraceID: "trace-1", TTL: time.Minute, SampleRate: 1}},
	})

	keep, reason := sampler.Keep(Event{TraceID: "trace-1", Result: ResultError})

	require.True(t, keep)
	require.Equal(t, "debug", reason)
}

func TestSamplerTreatsZeroTTLDebugMatchAsExpired(t *testing.T) {
	now := time.Unix(10, 0)
	sampler := NewSampler(SamplerOptions{
		SampleRate:   0,
		Now:          func() time.Time { return now },
		DebugMatches: []DebugMatch{{ClientMsgNo: "c1", TTL: 0, SampleRate: 1}},
	})

	keep, _ := sampler.Keep(Event{ClientMsgNo: "c1"})

	require.False(t, keep)
}
