package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLaneDispatchQueueDedupesQueuedWork(t *testing.T) {
	q := newLaneDispatchQueue()
	work := laneDispatchWorkKey{peer: 2, lane: 7}

	q.schedule(work)
	q.schedule(work)

	first, ok := q.pop()
	require.True(t, ok)
	require.Equal(t, work, first)

	_, ok = q.pop()
	require.False(t, ok)
}

func TestLaneDispatchQueueMarksDirtyWhileProcessing(t *testing.T) {
	q := newLaneDispatchQueue()
	work := laneDispatchWorkKey{peer: 2, lane: 7}

	q.schedule(work)

	first, ok := q.pop()
	require.True(t, ok)
	require.Equal(t, work, first)

	q.schedule(work)

	_, ok = q.pop()
	require.False(t, ok)
	require.True(t, q.finish(work))

	next, ok := q.pop()
	require.True(t, ok)
	require.Equal(t, work, next)
}

func TestLaneDispatchQueueFinishRequeuesDirtyWorkOnce(t *testing.T) {
	q := newLaneDispatchQueue()
	work := laneDispatchWorkKey{peer: 2, lane: 7}

	q.schedule(work)

	first, ok := q.pop()
	require.True(t, ok)
	require.Equal(t, work, first)

	q.schedule(work)
	q.schedule(work)

	require.True(t, q.finish(work))

	next, ok := q.pop()
	require.True(t, ok)
	require.Equal(t, work, next)

	_, ok = q.pop()
	require.False(t, ok)
}

func TestLaneDispatchQueuePreservesFIFOAcrossLanes(t *testing.T) {
	q := newLaneDispatchQueue()
	first := laneDispatchWorkKey{peer: 2, lane: 1}
	second := laneDispatchWorkKey{peer: 3, lane: 2}

	q.schedule(first)
	q.schedule(second)

	got, ok := q.pop()
	require.True(t, ok)
	require.Equal(t, first, got)

	got, ok = q.pop()
	require.True(t, ok)
	require.Equal(t, second, got)
}
