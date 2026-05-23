package reactor

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestAppendQueueFlushesByMaxRecords(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 3, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(1, 0)
	require.NoError(t, q.push(appendRequest{opID: 1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 1}}}))
	require.False(t, q.shouldFlush(now))
	require.NoError(t, q.push(appendRequest{opID: 2, enqueuedAt: now, records: []ch.Record{{SizeBytes: 1}, {SizeBytes: 1}}}))
	require.True(t, q.shouldFlush(now))
}

func TestAppendQueueFlushesByMaxWait(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: 5 * time.Millisecond, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(1, 0)
	require.NoError(t, q.push(appendRequest{opID: 1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 1}}}))
	require.False(t, q.shouldFlush(now.Add(4*time.Millisecond)))
	require.True(t, q.shouldFlush(now.Add(5*time.Millisecond)))
}

func TestAppendQueueRejectsPendingLimits(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 1, MaxPendingBytes: 1})
	require.NoError(t, q.push(appendRequest{opID: 1, records: []ch.Record{{SizeBytes: 1}}}))
	err := q.push(appendRequest{opID: 2, records: []ch.Record{{SizeBytes: 1}}})
	require.ErrorIs(t, err, ch.ErrBackpressured)

	q = newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 10, MaxPendingBytes: 1})
	require.NoError(t, q.push(appendRequest{opID: 1, records: []ch.Record{{SizeBytes: 1}}}))
	err = q.push(appendRequest{opID: 2, records: []ch.Record{{SizeBytes: 1}}})
	require.ErrorIs(t, err, ch.ErrBackpressured)
}

func TestAppendQueuePopBatchHonorsMaxRecordsAndBytes(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 2, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 10, MaxPendingBytes: 1024})
	require.NoError(t, q.push(appendRequest{opID: 1, records: []ch.Record{{SizeBytes: 1}}}))
	require.NoError(t, q.push(appendRequest{opID: 2, records: []ch.Record{{SizeBytes: 1}}}))
	require.NoError(t, q.push(appendRequest{opID: 3, records: []ch.Record{{SizeBytes: 1}}}))

	batch := q.popBatch(10, nil)
	require.Len(t, batch.requests, 2)
	require.Len(t, batch.records, 2)
	require.Len(t, q.pending, 1)

	q = newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 3, MaxWait: time.Second, MaxPending: 10, MaxPendingBytes: 1024})
	require.NoError(t, q.push(appendRequest{opID: 1, records: []ch.Record{{SizeBytes: 2}}}))
	require.NoError(t, q.push(appendRequest{opID: 2, records: []ch.Record{{SizeBytes: 2}}}))

	batch = q.popBatch(11, nil)
	require.Len(t, batch.requests, 1)
	require.Len(t, batch.records, 1)
	require.Len(t, q.pending, 1)
}

func TestAppendQueueRestoreFrontPrependsPoppedBatchAndRecounts(t *testing.T) {
	maxWait := 10 * time.Millisecond
	q := newAppendQueue(appendQueueConfig{MaxRecords: 3, MaxBytes: 1024, MaxWait: maxWait, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(1, 0)
	req1 := appendRequest{opID: 1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 2}, {SizeBytes: 3}}}
	req2 := appendRequest{opID: 2, enqueuedAt: now.Add(time.Millisecond), records: []ch.Record{{SizeBytes: 4}}}
	req3 := appendRequest{opID: 3, enqueuedAt: now.Add(2 * time.Millisecond), records: []ch.Record{{SizeBytes: 6}}}
	require.NoError(t, q.push(req1))
	require.NoError(t, q.push(req2))
	require.NoError(t, q.push(req3))

	batch := q.popBatch(10, nil)
	require.Len(t, batch.requests, 2)
	require.True(t, q.storeBlocked)
	require.False(t, q.shouldFlush(now.Add(maxWait)))

	q.restoreFront(batch)
	require.False(t, q.storeBlocked)
	require.Equal(t, []ch.OpID{1, 2, 3}, appendQueueOpIDs(q.pending))
	require.Equal(t, 4, q.records)
	require.Equal(t, 15, q.bytes)
	require.Equal(t, now.Add(maxWait), q.flushDue)
	require.True(t, q.shouldFlush(now.Add(maxWait)))
}

func TestAppendQueueRemoveDeletesQueuedRequestAndRecounts(t *testing.T) {
	maxWait := 20 * time.Millisecond
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: maxWait, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(2, 0)
	req1 := appendRequest{opID: 1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 2}}}
	req2 := appendRequest{opID: 2, enqueuedAt: now.Add(time.Millisecond), records: []ch.Record{{SizeBytes: 3}, {SizeBytes: 4}}}
	req3 := appendRequest{opID: 3, enqueuedAt: now.Add(2 * time.Millisecond), records: []ch.Record{{SizeBytes: 5}}}
	require.NoError(t, q.push(req1))
	require.NoError(t, q.push(req2))
	require.NoError(t, q.push(req3))

	removed, ok := q.remove(1)
	require.True(t, ok)
	require.Equal(t, ch.OpID(1), removed.opID)
	require.Equal(t, []ch.OpID{2, 3}, appendQueueOpIDs(q.pending))
	require.Equal(t, 3, q.records)
	require.Equal(t, 12, q.bytes)
	require.Equal(t, req2.enqueuedAt.Add(maxWait), q.flushDue)
}

func TestAppendQueueFailAllCompletesQueuedFuturesAndClearsState(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(3, 0)
	future1 := NewFuture()
	future2 := NewFuture()
	require.NoError(t, q.push(appendRequest{opID: 1, future: future1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 2}}}))
	require.NoError(t, q.push(appendRequest{opID: 2, future: future2, enqueuedAt: now.Add(time.Millisecond), records: []ch.Record{{SizeBytes: 3}}}))
	q.storeBlocked = true
	failErr := errors.New("append queue failed")

	q.failAll(failErr)

	for _, future := range []*Future{future1, future2} {
		_, err := future.Await(context.Background())
		require.ErrorIs(t, err, failErr)
	}
	require.Empty(t, q.pending)
	require.Zero(t, q.records)
	require.Zero(t, q.bytes)
	require.True(t, q.flushDue.IsZero())
	require.False(t, q.storeBlocked)
}

func appendQueueOpIDs(requests []appendRequest) []ch.OpID {
	opIDs := make([]ch.OpID, 0, len(requests))
	for _, req := range requests {
		opIDs = append(opIDs, req.opID)
	}
	return opIDs
}
