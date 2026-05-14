package diagnostics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrackingRulesKeepSenderUIDUntilTTL(t *testing.T) {
	now := time.Unix(100, 0)
	rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return now }})

	rule, err := rules.Add(TrackingRuleInput{
		ID:         "rule-1",
		Target:     TrackingTargetSenderUID,
		UID:        "u1",
		TTL:        time.Minute,
		SampleRate: 1,
	})
	require.NoError(t, err)
	require.Equal(t, "rule-1", rule.ID)
	require.Equal(t, now.Add(time.Minute), rule.ExpiresAt)

	require.True(t, rules.Keep(Event{FromUID: "u1"}))
	require.False(t, rules.Keep(Event{FromUID: "u2"}))

	now = now.Add(time.Minute + time.Nanosecond)
	require.False(t, rules.Keep(Event{FromUID: "u1"}))
	require.Empty(t, rules.List())
}

func TestTrackingRulesKeepChannel(t *testing.T) {
	rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return time.Unix(100, 0) }})
	_, err := rules.Add(TrackingRuleInput{
		ID:         "rule-channel",
		Target:     TrackingTargetChannel,
		ChannelKey: "channel/2/ZzE",
		TTL:        time.Hour,
		SampleRate: 1,
	})
	require.NoError(t, err)

	require.True(t, rules.Keep(Event{ChannelKey: "channel/2/ZzE"}))
	require.False(t, rules.Keep(Event{ChannelKey: "channel/2/ZzI"}))
}

func TestTrackingRulesRejectInvalidInputAndLimit(t *testing.T) {
	rules := NewTrackingRules(TrackingRulesOptions{MaxRules: 1, MaxTTL: time.Hour})

	_, err := rules.Add(TrackingRuleInput{ID: "", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
	require.Error(t, err)
	_, err = rules.Add(TrackingRuleInput{ID: "bad-target", Target: TrackingTarget("bad"), UID: "u1", TTL: time.Minute, SampleRate: 1})
	require.Error(t, err)
	_, err = rules.Add(TrackingRuleInput{ID: "bad-ttl", Target: TrackingTargetSenderUID, UID: "u1", TTL: 0, SampleRate: 1})
	require.Error(t, err)
	_, err = rules.Add(TrackingRuleInput{ID: "bad-rate", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1.1})
	require.Error(t, err)

	_, err = rules.Add(TrackingRuleInput{ID: "one", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
	require.NoError(t, err)
	_, err = rules.Add(TrackingRuleInput{ID: "two", Target: TrackingTargetSenderUID, UID: "u2", TTL: time.Minute, SampleRate: 1})
	require.Error(t, err)
}

func TestTrackingRulesAddIsIdempotentByID(t *testing.T) {
	now := time.Unix(100, 0)
	rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return now }})

	_, err := rules.Add(TrackingRuleInput{ID: "same", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
	require.NoError(t, err)
	updated, err := rules.Add(TrackingRuleInput{ID: "same", Target: TrackingTargetChannel, ChannelKey: "channel/2/ZzE", TTL: 2 * time.Minute, SampleRate: 1})
	require.NoError(t, err)

	require.Equal(t, TrackingTargetChannel, updated.Target)
	require.False(t, rules.Keep(Event{FromUID: "u1"}))
	require.True(t, rules.Keep(Event{ChannelKey: "channel/2/ZzE"}))
	require.Len(t, rules.List(), 1)
}
