package channelwrite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGroupConcurrentChannelsPreserveOrder(t *testing.T) {
	const channels = 16
	const perChannel = 25

	appenders := make(map[string]*orderedAppender, channels)
	for i := 0; i < channels; i++ {
		appenders[fmt.Sprintf("ch-%d", i)] = &orderedAppender{}
	}
	routing := &routingAppender{byChannel: appenders}

	group := New(Options{
		LocalNodeID:       1,
		ShardCount:        8,
		AppendWorkers:     4,
		PostCommitWorkers: 2,
		MessageID:         newBenchmarkMessageIDs(1),
		Appender:          routing,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop error = %v", err)
		}
	})

	var wg sync.WaitGroup
	for i := 0; i < channels; i++ {
		ch := fmt.Sprintf("ch-%d", i)
		target := benchmarkAuthorityTarget(ch)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perChannel; j++ {
				future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem(ch)})
				if err != nil {
					t.Errorf("SubmitLocal error = %v", err)
					return
				}
				results, err := future.Wait(context.Background())
				if err != nil {
					t.Errorf("Wait error = %v", err)
					return
				}
				if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
					t.Errorf("result = %#v, want success", results)
					return
				}
			}
		}()
	}
	wg.Wait()

	for ch, appender := range appenders {
		if !appender.seqsMonotonic() {
			t.Fatalf("channel %s appends not monotonic: %v", ch, appender.seqs())
		}
	}
}
