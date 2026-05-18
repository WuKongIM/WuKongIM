package userlimit

import (
	"sync"
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenRejectsWithRetryAfter(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := New(Config{Enabled: true, RatePerSecond: 2, Burst: 2, BucketShards: 4, IdleTTL: time.Minute, MaxBuckets: 10})

	if decision := limiter.AllowSend(now, Request{UID: "u1"}); !decision.Allowed {
		t.Fatalf("first send rejected: %+v", decision)
	}
	if decision := limiter.AllowSend(now, Request{UID: "u1"}); !decision.Allowed {
		t.Fatalf("second send rejected: %+v", decision)
	}
	decision := limiter.AllowSend(now, Request{UID: "u1"})
	if decision.Allowed {
		t.Fatalf("third send allowed after burst exhausted: %+v", decision)
	}
	if decision.RetryAfter != 500*time.Millisecond {
		t.Fatalf("expected retry-after 500ms, got %s", decision.RetryAfter)
	}
}

func TestLimiterRefillsFromElapsedTime(t *testing.T) {
	start := time.Unix(200, 0)
	limiter := New(Config{Enabled: true, RatePerSecond: 4, Burst: 1, BucketShards: 2, IdleTTL: time.Minute, MaxBuckets: 10})

	if !limiter.AllowSend(start, Request{UID: "u1"}).Allowed {
		t.Fatal("initial send rejected")
	}
	if limiter.AllowSend(start.Add(200*time.Millisecond), Request{UID: "u1"}).Allowed {
		t.Fatal("send allowed before one full token refilled")
	}
	if !limiter.AllowSend(start.Add(250*time.Millisecond), Request{UID: "u1"}).Allowed {
		t.Fatal("send rejected after one token refilled")
	}
}

func TestLimiterEvictsIdleBuckets(t *testing.T) {
	start := time.Unix(300, 0)
	limiter := New(Config{Enabled: true, RatePerSecond: 1, Burst: 1, BucketShards: 2, IdleTTL: time.Second, MaxBuckets: 10})

	limiter.AllowSend(start, Request{UID: "u1"})
	limiter.AllowSend(start, Request{UID: "u2"})
	if got := limiter.ActiveBuckets(); got != 2 {
		t.Fatalf("expected 2 active buckets, got %d", got)
	}

	evicted := limiter.EvictIdle(start.Add(time.Second + time.Nanosecond))
	if evicted != 2 {
		t.Fatalf("expected 2 evictions, got %d", evicted)
	}
	if got := limiter.ActiveBuckets(); got != 0 {
		t.Fatalf("expected no active buckets, got %d", got)
	}
}

func TestLimiterRejectsNewBucketsAtMaxBuckets(t *testing.T) {
	start := time.Unix(400, 0)
	limiter := New(Config{Enabled: true, RatePerSecond: 1, Burst: 1, BucketShards: 1, IdleTTL: time.Minute, MaxBuckets: 1})

	if !limiter.AllowSend(start, Request{UID: "u1"}).Allowed {
		t.Fatal("first uid rejected")
	}
	decision := limiter.AllowSend(start, Request{UID: "u2"})
	if decision.Allowed {
		t.Fatalf("second uid allowed despite max bucket cap: %+v", decision)
	}
	if decision.Reason != ReasonBucketLimit {
		t.Fatalf("expected bucket-limit reason, got %q", decision.Reason)
	}
}

func TestLimiterDisabledAllowsWithoutBuckets(t *testing.T) {
	limiter := New(Config{Enabled: false, RatePerSecond: 1, Burst: 1})

	if decision := limiter.AllowSend(time.Now(), Request{UID: "u1"}); !decision.Allowed {
		t.Fatalf("disabled limiter rejected send: %+v", decision)
	}
	if got := limiter.ActiveBuckets(); got != 0 {
		t.Fatalf("disabled limiter created buckets: %d", got)
	}
}

func TestLimiterIsConcurrentSafe(t *testing.T) {
	limiter := New(Config{Enabled: true, RatePerSecond: 1000, Burst: 1000, BucketShards: 16, IdleTTL: time.Minute, MaxBuckets: 100})
	start := time.Unix(500, 0)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				limiter.AllowSend(start, Request{UID: "u1"})
			}
		}()
	}
	wg.Wait()
	if got := limiter.ActiveBuckets(); got != 1 {
		t.Fatalf("expected one bucket for one uid, got %d", got)
	}
}

func TestLimiterBypassesSystemUIDAndPluginOriginWhenConfigured(t *testing.T) {
	now := time.Unix(600, 0)
	limiter := New(Config{Enabled: true, RatePerSecond: 1, Burst: 1, BucketShards: 2, IdleTTL: time.Minute, MaxBuckets: 10, SystemUIDBypass: true, PluginBypass: true})

	if !limiter.AllowSend(now, Request{UID: "sys", IsSystemUID: true}).Allowed {
		t.Fatal("system uid was limited despite bypass")
	}
	if !limiter.AllowSend(now, Request{UID: "plugin", Origin: "plugin"}).Allowed {
		t.Fatal("plugin origin was limited despite bypass")
	}
	if got := limiter.ActiveBuckets(); got != 0 {
		t.Fatalf("bypassed sends allocated buckets: %d", got)
	}
}
