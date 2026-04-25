package delivery

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestRetryWheelPopsDueEntriesInTimeOrder(t *testing.T) {
	wheel := NewRetryWheel()
	base := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)

	wheel.Schedule(RetryEntry{
		When:        base.Add(2 * time.Second),
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   102,
		Route:       testRoute("u2", 1, 11, 2),
		Attempt:     2,
	})
	wheel.Schedule(RetryEntry{
		When:        base.Add(time.Second),
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   101,
		Route:       testRoute("u2", 1, 11, 2),
		Attempt:     1,
	})

	due := wheel.PopDue(base.Add(1500 * time.Millisecond))
	require.Len(t, due, 1)
	require.Equal(t, uint64(101), due[0].MessageID)

	due = wheel.PopDue(base.Add(3 * time.Second))
	require.Len(t, due, 1)
	require.Equal(t, uint64(102), due[0].MessageID)
}

func TestRetryWheelAppliesCappedBackoffWithJitter(t *testing.T) {
	delay := cappedBackoffWithJitter([]time.Duration{time.Second, 2 * time.Second}, 6)

	require.Greater(t, delay, 2*time.Second)
	require.LessOrEqual(t, delay, 2200*time.Millisecond)
}
