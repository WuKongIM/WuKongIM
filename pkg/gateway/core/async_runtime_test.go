package core

import (
	"os"
	"strings"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

func TestNewAsyncRuntimeUsesNormalizedOptions(t *testing.T) {
	tests := []struct {
		name string
		opts gatewaytypes.RuntimeOptions
		want gatewaytypes.RuntimeOptions
	}{
		{
			name: "configured values",
			opts: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        2,
				AsyncSendQueueCapacity:  8,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  4,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
			want: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        2,
				AsyncSendQueueCapacity:  8,
				AsyncAuthWorkers:        1,
				AsyncAuthQueueCapacity:  4,
				AsyncPoolReleaseTimeout: time.Millisecond,
			},
		},
		{
			name: "zero values use defaults",
			opts: gatewaytypes.RuntimeOptions{},
			want: gatewaytypes.DefaultRuntimeOptions(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &Server{
				options: gatewaytypes.Options{
					Runtime: tt.opts,
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
			if got := runtime.auth.workers; got != tt.want.AsyncAuthWorkers {
				t.Fatalf("auth workers = %d, want %d", got, tt.want.AsyncAuthWorkers)
			}
			if got := runtime.auth.totalCapacity(); got != tt.want.AsyncAuthQueueCapacity {
				t.Fatalf("auth capacity = %d, want %d", got, tt.want.AsyncAuthQueueCapacity)
			}
			if got := runtime.send.workers; got != tt.want.AsyncSendWorkers {
				t.Fatalf("send workers = %d, want %d", got, tt.want.AsyncSendWorkers)
			}
			if got := runtime.send.totalCapacity(); got != tt.want.AsyncSendQueueCapacity {
				t.Fatalf("send capacity = %d, want %d", got, tt.want.AsyncSendQueueCapacity)
			}
			if got := runtime.releaseTimeout; got != tt.want.AsyncPoolReleaseTimeout {
				t.Fatalf("release timeout = %s, want %s", got, tt.want.AsyncPoolReleaseTimeout)
			}
		})
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

func TestAsyncExecutorsUseWorkqueuePrimitives(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{file: "async_auth.go", want: "workqueue.NewBoundedPool"},
		{file: "async_send.go", want: "workqueue.NewShardedMailbox"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatalf("read %s: %v", tt.file, err)
			}
			source := string(data)
			if strings.Contains(source, "github.com/panjf2000/ants/v2") {
				t.Fatalf("%s still imports ants directly", tt.file)
			}
			if !strings.Contains(source, tt.want) {
				t.Fatalf("%s does not use %s", tt.file, tt.want)
			}
		})
	}
}
