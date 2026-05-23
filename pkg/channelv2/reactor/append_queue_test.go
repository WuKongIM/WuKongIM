package reactor

import (
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
