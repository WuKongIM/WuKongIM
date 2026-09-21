//go:build integration

package rpc

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestServiceBorrowedResponseRemainsValidUntilCallbackReturns(t *testing.T) {
	const message = "handler aliases request"
	payload := []byte(message)
	var released atomic.Bool
	owner := core.NewOwnedBuffer(payload, func(p []byte) { clear(p); released.Store(true) })
	defer owner.Release()
	svc := NewService(1, func(_ context.Context, p []byte) ([]byte, error) { return p, nil }, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, nil)
	defer svc.Stop()
	var retained []byte
	calls := 0
	finished := make(chan struct{})
	reply := make(chan Response, 1)
	req := Request{Payload: owner, Reply: reply, Finish: func() { close(finished) }, RespondBorrowed: func(resp Response) {
		calls++
		if released.Load() || resp.Err != nil || string(resp.Payload) != message {
			t.Error("borrowed response invalid during callback")
		}
		retained = append([]byte(nil), resp.Payload...)
	}}
	if err := svc.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, finished)
	if calls != 1 || !released.Load() || string(retained) != message {
		t.Fatalf("calls=%d released=%v retained=%q", calls, released.Load(), retained)
	}
	select {
	case <-reply:
		t.Fatal("borrowed callback must take precedence over Reply")
	default:
	}
}
