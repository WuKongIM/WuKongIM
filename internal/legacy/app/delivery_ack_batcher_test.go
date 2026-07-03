package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/deliveryevents"
	"github.com/stretchr/testify/require"
)

func TestDeliveryAckBatchNotifierCoalescesConcurrentAcks(t *testing.T) {
	sender := &recordingDeliveryAckBatchSender{}
	notifier := newDeliveryAckBatchNotifier(sender, deliveryAckBatchNotifierOptions{
		FlushDelay: 20 * time.Millisecond,
		MaxBatch:   8,
	})
	defer notifier.Close()

	cmds := []deliveryevents.RouteAck{
		{UID: "u1", SessionID: 10, MessageID: 88, MessageSeq: 9},
		{UID: "u2", SessionID: 11, MessageID: 89, MessageSeq: 10},
	}
	var wg sync.WaitGroup
	errs := make([]error, len(cmds))
	for i, cmd := range cmds {
		wg.Add(1)
		go func(i int, cmd deliveryevents.RouteAck) {
			defer wg.Done()
			errs[i] = notifier.NotifyAck(context.Background(), 2, cmd)
		}(i, cmd)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Empty(t, sender.singleCallsSnapshot())
	require.Eventually(t, func() bool {
		return len(sender.batchCallsSnapshot()) == 1
	}, time.Second, 10*time.Millisecond)
	batchCalls := sender.batchCallsSnapshot()
	require.Equal(t, uint64(2), batchCalls[0].nodeID)
	require.ElementsMatch(t, cmds, batchCalls[0].commands)
}

func TestDeliveryAckBatchNotifierReturnsAfterEnqueueBeforeFlush(t *testing.T) {
	sender := &recordingDeliveryAckBatchSender{}
	notifier := newDeliveryAckBatchNotifier(sender, deliveryAckBatchNotifierOptions{
		FlushDelay: time.Hour,
		MaxBatch:   8,
	})

	returned := make(chan error, 1)
	go func() {
		returned <- notifier.NotifyAck(context.Background(), 2, deliveryevents.RouteAck{
			UID:        "u1",
			SessionID:  10,
			MessageID:  88,
			MessageSeq: 9,
		})
	}()

	select {
	case err := <-returned:
		require.NoError(t, err)
	case <-time.After(50 * time.Millisecond):
		notifier.Close()
		t.Fatal("NotifyAck blocked while waiting for a delayed batch flush")
	}

	require.Empty(t, sender.batchCallsSnapshot())
	notifier.Close()
	require.Len(t, sender.batchCallsSnapshot(), 1)
}

func TestDeliveryAckBatchNotifierFlushesImmediatelyAtMaxBatch(t *testing.T) {
	sender := &recordingDeliveryAckBatchSender{}
	notifier := newDeliveryAckBatchNotifier(sender, deliveryAckBatchNotifierOptions{
		FlushDelay: time.Hour,
		MaxBatch:   2,
	})
	defer notifier.Close()

	cmds := []deliveryevents.RouteAck{
		{UID: "u1", SessionID: 10, MessageID: 88, MessageSeq: 9},
		{UID: "u2", SessionID: 11, MessageID: 89, MessageSeq: 10},
	}
	var wg sync.WaitGroup
	for _, cmd := range cmds {
		wg.Add(1)
		go func(cmd deliveryevents.RouteAck) {
			defer wg.Done()
			require.NoError(t, notifier.NotifyAck(context.Background(), 2, cmd))
		}(cmd)
	}
	wg.Wait()

	require.Eventually(t, func() bool {
		return len(sender.batchCallsSnapshot()) == 1
	}, time.Second, 10*time.Millisecond)
	require.ElementsMatch(t, cmds, sender.batchCallsSnapshot()[0].commands)
}

type recordingDeliveryAckBatchCall struct {
	nodeID   uint64
	commands []deliveryevents.RouteAck
}

type recordingDeliveryAckBatchSender struct {
	mu          sync.Mutex
	singleCalls []deliveryevents.RouteAck
	batchCalls  []recordingDeliveryAckBatchCall
}

func (s *recordingDeliveryAckBatchSender) NotifyAck(_ context.Context, _ uint64, cmd deliveryevents.RouteAck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.singleCalls = append(s.singleCalls, cmd)
	return nil
}

func (s *recordingDeliveryAckBatchSender) NotifyAckBatch(_ context.Context, nodeID uint64, commands []deliveryevents.RouteAck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchCalls = append(s.batchCalls, recordingDeliveryAckBatchCall{
		nodeID:   nodeID,
		commands: append([]deliveryevents.RouteAck(nil), commands...),
	})
	return nil
}

func (s *recordingDeliveryAckBatchSender) NotifyOffline(context.Context, uint64, deliveryevents.SessionClosed) error {
	return nil
}

func (s *recordingDeliveryAckBatchSender) singleCallsSnapshot() []deliveryevents.RouteAck {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deliveryevents.RouteAck(nil), s.singleCalls...)
}

func (s *recordingDeliveryAckBatchSender) batchCallsSnapshot() []recordingDeliveryAckBatchCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordingDeliveryAckBatchCall, len(s.batchCalls))
	for i, call := range s.batchCalls {
		out[i] = recordingDeliveryAckBatchCall{
			nodeID:   call.nodeID,
			commands: append([]deliveryevents.RouteAck(nil), call.commands...),
		}
	}
	return out
}
