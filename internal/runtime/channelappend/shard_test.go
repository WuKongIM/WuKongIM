package channelappend

import (
	"sync"
	"testing"
	"time"
)

func TestShardGetOrCreateStable(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1}, 1024, time.Minute)
	target := benchmarkAuthorityTarget("s1")
	w1 := s.getOrCreate(target)
	w2 := s.getOrCreate(target)
	if w1 != w2 {
		t.Fatal("getOrCreate must return the same writer for the same channel")
	}
}

func TestShardGetOrCreateConcurrent(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1}, 1024, time.Minute)
	target := benchmarkAuthorityTarget("s2")
	var wg sync.WaitGroup
	writers := make([]*channelWriter, 32)
	for i := range writers {
		wg.Add(1)
		go func(i int) { defer wg.Done(); writers[i] = s.getOrCreate(target) }(i)
	}
	wg.Wait()
	for i := 1; i < len(writers); i++ {
		if writers[i] != writers[0] {
			t.Fatal("concurrent getOrCreate must converge to one writer")
		}
	}
}

func TestShardReclaimsOnlyExpiredIdleWriters(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1}, 1024, time.Minute)
	idleTarget := benchmarkAuthorityTarget("idle")
	activeTarget := benchmarkAuthorityTarget("active")
	pendingTarget := benchmarkAuthorityTarget("pending")

	idle := s.getOrCreate(idleTarget)
	idle.scheduled.Store(true)
	if idle.deactivate() {
		t.Fatalf("idle writer reported runnable work")
	}
	active := s.getOrCreate(activeTarget)
	active.scheduled.Store(true)
	pending := s.getOrCreate(pendingTarget)
	pending.inbox = append(pending.inbox, submittedBatch{future: newFuture(1)})

	time.Sleep(2 * time.Millisecond)
	removed := s.reclaimIdleWriters(time.Now(), time.Nanosecond)
	if removed != 1 {
		t.Fatalf("reclaimed writers = %d, want 1", removed)
	}
	if s.lookup(targetKey(idleTarget)) != nil {
		t.Fatalf("expired idle writer was not reclaimed")
	}
	if s.lookup(targetKey(activeTarget)) == nil {
		t.Fatalf("scheduled writer was reclaimed")
	}
	if s.lookup(targetKey(pendingTarget)) == nil {
		t.Fatalf("pending writer was reclaimed")
	}
}
