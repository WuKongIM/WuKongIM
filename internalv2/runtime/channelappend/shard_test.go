package channelappend

import (
	"sync"
	"testing"
)

func TestShardGetOrCreateStable(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	target := benchmarkAuthorityTarget("s1")
	w1 := s.getOrCreate(target)
	w2 := s.getOrCreate(target)
	if w1 != w2 {
		t.Fatal("getOrCreate must return the same writer for the same channel")
	}
}

func TestShardGetOrCreateConcurrent(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	target := benchmarkAuthorityTarget("s2")
	var wg sync.WaitGroup
	writers := make([]*channelAppendr, 32)
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
