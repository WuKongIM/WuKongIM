package replica

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

type blockingCheckpointStore struct {
	mu sync.Mutex

	checkpoint channel.Checkpoint
	stored     []channel.Checkpoint

	startedFirst chan struct{}
	releaseFirst chan struct{}
}

func newBlockingCheckpointStore() *blockingCheckpointStore {
	return &blockingCheckpointStore{
		startedFirst: make(chan struct{}, 1),
		releaseFirst: make(chan struct{}),
	}
}

func (s *blockingCheckpointStore) Load() (channel.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoint, nil
}

func (s *blockingCheckpointStore) Store(checkpoint channel.Checkpoint) error {
	s.mu.Lock()
	index := len(s.stored)
	s.checkpoint = checkpoint
	s.stored = append(s.stored, checkpoint)
	startedFirst := s.startedFirst
	releaseFirst := s.releaseFirst
	s.mu.Unlock()

	if index == 0 {
		select {
		case startedFirst <- struct{}{}:
		default:
		}
		<-releaseFirst
	}
	return nil
}

func (s *blockingCheckpointStore) storedHWs() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]uint64, 0, len(s.stored))
	for _, checkpoint := range s.stored {
		values = append(values, checkpoint.HW)
	}
	return values
}

type flakyCheckpointStore struct {
	mu sync.Mutex

	checkpoint channel.Checkpoint
	stored     []channel.Checkpoint
	attempts   int

	firstFailed  chan struct{}
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

func newFlakyCheckpointStore() *flakyCheckpointStore {
	return &flakyCheckpointStore{
		firstFailed:   make(chan struct{}, 1),
		secondStarted: make(chan struct{}, 1),
		releaseSecond: make(chan struct{}),
	}
}

func (s *flakyCheckpointStore) Load() (channel.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoint, nil
}

func (s *flakyCheckpointStore) Store(checkpoint channel.Checkpoint) error {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	secondStarted := s.secondStarted
	releaseSecond := s.releaseSecond
	s.mu.Unlock()

	switch attempt {
	case 1:
		select {
		case s.firstFailed <- struct{}{}:
		default:
		}
		return errors.New("checkpoint write failed")
	case 2:
		select {
		case secondStarted <- struct{}{}:
		default:
		}
		<-releaseSecond
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpoint = checkpoint
	s.stored = append(s.stored, checkpoint)
	return nil
}

func (s *flakyCheckpointStore) storedHWs() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]uint64, 0, len(s.stored))
	for _, checkpoint := range s.stored {
		values = append(values, checkpoint.HW)
	}
	return values
}

func TestApplyProgressAckReturnsBeforeCheckpointStoreCompletes(t *testing.T) {
	runAdvanceCommitHWCompletesWaitersBeforeCheckpointStoreReturns(t)
}

func TestAdvanceCommitHWCompletesWaitersBeforeCheckpointStoreReturns(t *testing.T) {
	runAdvanceCommitHWCompletesWaitersBeforeCheckpointStoreReturns(t)
}

func runAdvanceCommitHWCompletesWaitersBeforeCheckpointStoreReturns(t *testing.T) {
	cluster := newThreeReplicaClusterWithMinISR(t, 2)
	checkpoints := newBlockingCheckpointStore()
	cluster.leader.checkpoints = checkpoints

	appendDone := make(chan channel.CommitResult, 1)
	appendErr := make(chan error, 1)
	go func() {
		result, err := cluster.leader.Append(context.Background(), []channel.Record{
			{Payload: []byte("a"), SizeBytes: 1},
			{Payload: []byte("b"), SizeBytes: 1},
			{Payload: []byte("c"), SizeBytes: 1},
		})
		if err != nil {
			appendErr <- err
			return
		}
		appendDone <- result
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 3)

	ack := func(matchOffset uint64) <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- cluster.leader.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{
				ChannelKey:  cluster.leader.state.ChannelKey,
				Epoch:       cluster.leader.state.Epoch,
				ReplicaID:   cluster.follower2.localNode,
				MatchOffset: matchOffset,
			})
		}()
		return done
	}

	ackOne := ack(1)
	select {
	case <-checkpoints.startedFirst:
	case <-time.After(time.Second):
		t.Fatal("first checkpoint store did not start")
	}

	select {
	case err := <-ackOne:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first progress ack did not return while checkpoint store was blocked")
	}

	ackTwo := ack(2)
	select {
	case err := <-ackTwo:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second progress ack did not return while checkpoint store was blocked")
	}

	ackThree := ack(3)
	select {
	case err := <-ackThree:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("third progress ack did not return while checkpoint store was blocked")
	}

	select {
	case result := <-appendDone:
		require.Equal(t, uint64(3), result.NextCommitHW)
	case err := <-appendErr:
		t.Fatalf("append failed while checkpoint store was blocked: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("append still blocked on checkpoint persistence")
	}

	close(checkpoints.releaseFirst)

	select {
	case result := <-appendDone:
		t.Fatalf("append completed twice after checkpoint release: %+v", result)
	case err := <-appendErr:
		t.Fatalf("append returned error after checkpoint release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestApplyProgressAckPublishesLatestPendingCheckpointAfterBlockedStore(t *testing.T) {
	runCheckpointWriterCoalescesToLatestCommitHW(t)
}

func TestCheckpointWriterCoalescesToLatestCommitHW(t *testing.T) {
	runCheckpointWriterCoalescesToLatestCommitHW(t)
}

func runCheckpointWriterCoalescesToLatestCommitHW(t *testing.T) {
	cluster := newThreeReplicaClusterWithMinISR(t, 2)
	checkpoints := newBlockingCheckpointStore()
	cluster.leader.checkpoints = checkpoints

	appendDone := make(chan channel.CommitResult, 1)
	appendErr := make(chan error, 1)
	go func() {
		result, err := cluster.leader.Append(context.Background(), []channel.Record{
			{Payload: []byte("a"), SizeBytes: 1},
			{Payload: []byte("b"), SizeBytes: 1},
			{Payload: []byte("c"), SizeBytes: 1},
		})
		if err != nil {
			appendErr <- err
			return
		}
		appendDone <- result
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 3)

	for _, matchOffset := range []uint64{1, 2, 3} {
		done := make(chan error, 1)
		go func(offset uint64) {
			done <- cluster.leader.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{
				ChannelKey:  cluster.leader.state.ChannelKey,
				Epoch:       cluster.leader.state.Epoch,
				ReplicaID:   cluster.follower2.localNode,
				MatchOffset: offset,
			})
		}(matchOffset)
		if matchOffset == 1 {
			select {
			case <-checkpoints.startedFirst:
			case <-time.After(time.Second):
				t.Fatal("first checkpoint store did not start")
			}
		}
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("progress ack for offset %d did not return while checkpoint store was blocked", matchOffset)
		}
	}

	select {
	case result := <-appendDone:
		require.Equal(t, uint64(3), result.NextCommitHW)
	case err := <-appendErr:
		t.Fatalf("append failed while checkpoint store was blocked: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("append still blocked on checkpoint persistence")
	}

	close(checkpoints.releaseFirst)

	select {
	case result := <-appendDone:
		t.Fatalf("append completed twice after checkpoint release: %+v", result)
	case err := <-appendErr:
		t.Fatalf("append returned error after checkpoint release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	require.Equal(t, []uint64{1, 3}, checkpoints.storedHWs())
	require.Equal(t, uint64(3), cluster.leader.Status().HW)
}

func TestCheckpointWriterRetriesFailedStoreAndRestoresCommitReady(t *testing.T) {
	cluster := newThreeReplicaClusterWithMinISR(t, 2)
	checkpoints := newFlakyCheckpointStore()
	cluster.leader.checkpoints = checkpoints

	appendDone := make(chan channel.CommitResult, 1)
	appendErr := make(chan error, 1)
	go func() {
		result, err := cluster.leader.Append(context.Background(), []channel.Record{
			{Payload: []byte("a"), SizeBytes: 1},
		})
		if err != nil {
			appendErr <- err
			return
		}
		appendDone <- result
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	require.NoError(t, cluster.leader.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{
		ChannelKey:  cluster.leader.state.ChannelKey,
		Epoch:       cluster.leader.state.Epoch,
		ReplicaID:   cluster.follower2.localNode,
		MatchOffset: 1,
	}))

	select {
	case result := <-appendDone:
		require.Equal(t, uint64(1), result.NextCommitHW)
	case err := <-appendErr:
		t.Fatalf("append failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("append did not complete after quorum commit")
	}

	select {
	case <-checkpoints.firstFailed:
	case <-time.After(time.Second):
		t.Fatal("checkpoint writer did not observe the injected failure")
	}
	select {
	case <-checkpoints.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("checkpoint writer did not retry after store failure")
	}

	require.Eventually(t, func() bool {
		return !cluster.leader.Status().CommitReady
	}, time.Second, 10*time.Millisecond, "checkpoint failure must gate serving until persistence catches up")

	_, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("blocked"), SizeBytes: 1}})
	require.ErrorIs(t, err, channel.ErrNotReady)

	close(checkpoints.releaseSecond)

	require.Eventually(t, func() bool {
		st := cluster.leader.Status()
		return st.CommitReady && st.CheckpointHW == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []uint64{1}, checkpoints.storedHWs())
}
