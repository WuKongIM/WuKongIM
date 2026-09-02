package cmdsync

import (
	"testing"
	"time"
)

func TestSyncRecordCacheEvictsOldestUIDAndUsesStableTieBreak(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewSyncRecordCache(SyncRecordCacheOptions{
		TTL: time.Hour, MaxUIDs: 2, MaxRecordsPerUID: 2,
		Now: func() time.Time { return now },
	})
	record := []SyncRecord{{CommandChannelID: "channel____cmd", ChannelType: 1, LastReturnedMsgSeq: 7}}
	cache.Replace("oldest", record)
	now = now.Add(time.Second)
	cache.Replace("middle", record)
	now = now.Add(time.Second)
	cache.Replace("newest", record)
	if got := cache.Peek("oldest"); got != nil {
		t.Fatalf("oldest UID survived overflow: %+v", got)
	}
	if cache.Peek("middle") == nil || cache.Peek("newest") == nil {
		t.Fatal("newer UID generation was evicted")
	}

	tied := NewSyncRecordCache(SyncRecordCacheOptions{
		TTL: time.Hour, MaxUIDs: 2, MaxRecordsPerUID: 2,
		Now: func() time.Time { return now },
	})
	for _, uid := range []string{"b", "c", "a"} {
		tied.Replace(uid, record)
	}
	if got := tied.Peek("a"); got != nil {
		t.Fatalf("lexicographically oldest tied UID survived: %+v", got)
	}
	if tied.Peek("b") == nil || tied.Peek("c") == nil {
		t.Fatal("tie-break evicted a non-minimum UID")
	}
}

func TestSyncRecordCachePrunesExpiredEntriesAndProtectsLatestGeneration(t *testing.T) {
	now := time.Unix(200, 0)
	cache := NewSyncRecordCache(SyncRecordCacheOptions{
		TTL: time.Second, MaxUIDs: 2, MaxRecordsPerUID: 1,
		Now: func() time.Time { return now },
	})
	first := []SyncRecord{
		{CommandChannelID: "first____cmd", ChannelType: 1, LastReturnedMsgSeq: 1},
		{CommandChannelID: "truncated____cmd", ChannelType: 1, LastReturnedMsgSeq: 2},
	}
	cache.Replace("user", first)
	first[0].LastReturnedMsgSeq = 99
	if got := cache.Peek("user"); len(got) != 1 || got[0].LastReturnedMsgSeq != 1 {
		t.Fatalf("stored generation was not detached/truncated: %+v", got)
	}

	latest := []SyncRecord{{CommandChannelID: "latest____cmd", ChannelType: 1, LastReturnedMsgSeq: 3}}
	cache.Replace("user", latest)
	cache.DeleteIfUnchanged("user", []SyncRecord{{CommandChannelID: "stale____cmd", ChannelType: 1, LastReturnedMsgSeq: 2}})
	if got := cache.Peek("user"); len(got) != 1 || got[0] != latest[0] {
		t.Fatalf("stale acknowledgement deleted latest generation: %+v", got)
	}
	cache.DeleteIfUnchanged("user", latest)
	if got := cache.Peek("user"); got != nil {
		t.Fatalf("matching acknowledgement did not delete generation: %+v", got)
	}

	cache.Replace("expired", latest)
	now = now.Add(2 * time.Second)
	cache.Replace("current", latest)
	if got := cache.Peek("expired"); got != nil {
		t.Fatalf("expired generation survived prune: %+v", got)
	}
}

func TestSyncMessageOrderingUsesEveryStableTieBreaker(t *testing.T) {
	base := syncMessageCandidate{
		commandChannelID: "b____cmd", channelType: 2,
		message: SyncedMessage{ServerTimestampMS: 20, MessageSeq: 4, MessageID: 8},
	}
	tests := []struct {
		name  string
		left  syncMessageCandidate
		right syncMessageCandidate
	}{
		{name: "timestamp", left: withSyncTimestamp(base, 19), right: base},
		{name: "channel", left: withSyncChannel(base, "a____cmd"), right: base},
		{name: "channel type", left: withSyncChannelType(base, 1), right: base},
		{name: "sequence", left: withSyncSequence(base, 3), right: base},
		{name: "message id", left: withSyncMessageID(base, 7), right: base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !syncMessageLess(tt.left, tt.right) || syncMessageLess(tt.right, tt.left) {
				t.Fatalf("unstable ordering: left=%+v right=%+v", tt.left, tt.right)
			}
		})
	}
	if syncMessageLess(base, base) {
		t.Fatal("equal messages compare less")
	}
}

func withSyncTimestamp(value syncMessageCandidate, timestamp int64) syncMessageCandidate {
	value.message.ServerTimestampMS = timestamp
	return value
}

func withSyncChannel(value syncMessageCandidate, channelID string) syncMessageCandidate {
	value.commandChannelID = channelID
	return value
}

func withSyncChannelType(value syncMessageCandidate, channelType uint8) syncMessageCandidate {
	value.channelType = channelType
	return value
}

func withSyncSequence(value syncMessageCandidate, sequence uint64) syncMessageCandidate {
	value.message.MessageSeq = sequence
	return value
}

func withSyncMessageID(value syncMessageCandidate, messageID uint64) syncMessageCandidate {
	value.message.MessageID = messageID
	return value
}
