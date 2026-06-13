package core

import (
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

func TestNewAsyncRuntimeUsesNormalizedOptions(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        2,
				AsyncSendQueueCapacity:  8,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  4,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
		},
	}
	runtime, err := newAsyncRuntime(srv)
	if err != nil {
		t.Fatalf("newAsyncRuntime failed: %v", err)
	}
	defer runtime.stop()

	if runtime.auth == nil {
		t.Fatal("auth executor is nil")
	}
	if runtime.send == nil {
		t.Fatal("send executor is nil")
	}
	if got := runtime.auth.workers; got != 1 {
		t.Fatalf("auth workers = %d, want 1", got)
	}
	if got := runtime.auth.totalCapacity(); got != 4 {
		t.Fatalf("auth capacity = %d, want 4", got)
	}
	if got := runtime.send.workers; got != 2 {
		t.Fatalf("send workers = %d, want 2", got)
	}
	if got := runtime.send.totalCapacity(); got != 8 {
		t.Fatalf("send capacity = %d, want 8", got)
	}
}

func TestAsyncRuntimeStopIsIdempotent(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  1,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
		},
	}
	runtime, err := newAsyncRuntime(srv)
	if err != nil {
		t.Fatalf("newAsyncRuntime failed: %v", err)
	}
	runtime.stop()
	runtime.stop()
}
