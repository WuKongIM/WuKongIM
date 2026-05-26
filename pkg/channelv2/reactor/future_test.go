package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestFutureCompletesOnce(t *testing.T) {
	future := NewFuture()
	future.Complete(Result{Append: ch.AppendResult{MessageSeq: 1}})
	future.Complete(Result{Append: ch.AppendResult{MessageSeq: 2}})
	res, err := future.Await(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Append.MessageSeq)
}

func TestFutureAwaitContextCancellation(t *testing.T) {
	future := NewFuture()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := future.Await(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
func TestFutureDoneAndResultRemainAvailableAfterCompletion(t *testing.T) {
	future := NewFuture()
	future.Complete(Result{Append: ch.AppendResult{MessageSeq: 7}})

	select {
	case <-future.Done():
	default:
		t.Fatal("future done channel was not closed")
	}

	first := future.Result()
	second := future.Result()
	require.Equal(t, uint64(7), first.Append.MessageSeq)
	require.Equal(t, uint64(7), second.Append.MessageSeq)
}
