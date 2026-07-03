//go:build e2e

package suite

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TopDeliverySnapshot is the public delivery section returned by /top/v1/snapshot.
type TopDeliverySnapshot struct {
	// AckBindings is the current number of owner-local pending recvack bindings.
	AckBindings int64 `json:"ack_bindings"`
	// RetryQueueDepth is the current delivery retry queue depth.
	RetryQueueDepth int64 `json:"retry_queue_depth"`
}

type topSnapshotResponse struct {
	Delivery *TopDeliverySnapshot `json:"delivery"`
}

// FetchTopDelivery fetches the public delivery top snapshot from one node.
func FetchTopDelivery(ctx context.Context, apiAddr string) (TopDeliverySnapshot, error) {
	var out topSnapshotResponse
	_, err := GetJSON(ctx, "http://"+apiAddr+"/top/v1/snapshot?view=delivery&window=2s", &out)
	if err != nil {
		return TopDeliverySnapshot{}, err
	}
	if out.Delivery == nil {
		return TopDeliverySnapshot{}, fmt.Errorf("delivery top section missing")
	}
	return *out.Delivery, nil
}

// RequireTopDeliveryAckBindingsAtLeastEventually waits until ack_bindings reaches at least want.
func RequireTopDeliveryAckBindingsAtLeastEventually(t *testing.T, node StartedNode, want int64) {
	t.Helper()
	requireTopDeliveryAckBindingsEventually(t, node, want, func(got int64) bool { return got >= want }, ">=")
}

// RequireTopDeliveryAckBindingsEventually waits until ack_bindings equals want.
func RequireTopDeliveryAckBindingsEventually(t *testing.T, node StartedNode, want int64) {
	t.Helper()
	requireTopDeliveryAckBindingsEventually(t, node, want, func(got int64) bool { return got == want }, "==")
}

func requireTopDeliveryAckBindingsEventually(t *testing.T, node StartedNode, want int64, match func(int64) bool, op string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last TopDeliverySnapshot
	var lastErr error
	for {
		last, lastErr = FetchTopDelivery(ctx, node.APIAddr())
		if lastErr == nil && match(last.AckBindings) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("top delivery ack_bindings = %d err=%v, want %s %d\n%s", last.AckBindings, lastErr, op, want, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
