package cmdsync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSyncRecordCacheLatestGenerationWins(t *testing.T) {
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	cache := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10, Now: func() time.Time { return now }})
	cache.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})
	cache.Replace("u1", []SyncRecord{{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})

	records := cache.Pop("u1")
	require.Equal(t, []SyncRecord{{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}}, records)
	require.Empty(t, cache.Pop("u1"))
}

func TestSyncRecordCacheExpiresRecords(t *testing.T) {
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	cache := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10, Now: func() time.Time { return now }})
	cache.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})

	now = now.Add(time.Minute + time.Nanosecond)
	require.Empty(t, cache.Pop("u1"))
}

func TestSyncRecordCacheEmptyReplaceClearsPreviousGeneration(t *testing.T) {
	cache := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10})
	cache.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})
	cache.Replace("u1", nil)

	require.Empty(t, cache.Pop("u1"))
}

func TestSyncRecordCacheTruncatesRecordsPerUID(t *testing.T) {
	cache := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 2})
	cache.Replace("u1", []SyncRecord{
		{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 1},
		{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 2},
		{CommandChannelID: "g3____cmd", ChannelType: 2, LastReturnedMsgSeq: 3},
	})

	require.Equal(t, []SyncRecord{
		{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 1},
		{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 2},
	}, cache.Pop("u1"))
}

func TestSyncRecordCacheEvictsOldestUIDWhenCapacityExceeded(t *testing.T) {
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	cache := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 2, MaxRecordsPerUID: 10, Now: func() time.Time { return now }})
	cache.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 1}})
	now = now.Add(time.Second)
	cache.Replace("u2", []SyncRecord{{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 2}})
	now = now.Add(time.Second)
	cache.Replace("u3", []SyncRecord{{CommandChannelID: "g3____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})

	require.Empty(t, cache.Pop("u1"))
	require.Equal(t, []SyncRecord{{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 2}}, cache.Pop("u2"))
	require.Equal(t, []SyncRecord{{CommandChannelID: "g3____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}}, cache.Pop("u3"))
}
