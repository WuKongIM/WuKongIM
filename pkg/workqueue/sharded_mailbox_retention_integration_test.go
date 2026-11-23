//go:build integration

package workqueue

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShardedMailboxReleasesBorrowedBatchReferences(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			seen := make(chan int, 1)
			mailbox, err := NewShardedMailbox[[]byte](ShardedMailboxConfig{
				Name: "batch-retention", Shards: 1, Workers: 1, QueueSizePerShard: 4, BatchMaxItems: 4,
			}, func(_ context.Context, batch MailboxBatch[[]byte]) error {
				seen <- len(batch.Items[0])
				switch outcome {
				case "error":
					return errors.New("test handler error")
				case "panic":
					panic("test handler panic")
				default:
					return nil
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = mailbox.Close(ctx) })
			if err := mailbox.SubmitHash(ctx, 0, make([]byte, 64<<10)); err != nil {
				t.Fatal(err)
			}
			select {
			case n := <-seen:
				if n != 64<<10 {
					t.Fatalf("handler received %d bytes", n)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := mailbox.Close(ctx); err != nil {
				t.Fatal(err)
			}
			batch := mailbox.shards[0].batch
			for i, body := range batch[:cap(batch)] {
				if body != nil {
					t.Fatalf("idle shard retained %d bytes in completed slot %d", len(body), i)
				}
			}
		})
	}
}
