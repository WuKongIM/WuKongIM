//go:build integration

package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/stretchr/testify/require"
)

// Failure cases: concurrent callers race between the ownership check and the
// runtime enqueue; a terminal callback blocks while publishing its result.
// Both queued and unpublished work must remain in the same fixed budget.
type enqueueGateContext struct {
	context.Context
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (c *enqueueGateContext) Err() error {
	if c.calls.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

func TestQuorumBudgetConcurrentAdmission(t *testing.T) {
	log := &asyncQuorumLog{submitted: make(chan replication.Proposal, 8)}
	sink := &captureSink{ch: make(chan Result, 8)}
	pool, err := NewPool(PoolConfig{Name: "q", Workers: 1, QueueSize: 1}, Deps{QuorumLog: log}, sink)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, pool.Submit(context.Background(), quorumCommitTask(1)))
	<-log.submitted

	gate := &enqueueGateContext{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
	second := make(chan error, 1)
	go func() { second <- pool.Submit(gate, quorumCommitTask(2)) }()
	<-gate.entered
	thirdErr := pool.Submit(context.Background(), quorumCommitTask(3))
	if thirdErr == nil {
		<-log.submitted
	}
	close(gate.release)
	secondErr := <-second
	if secondErr == nil {
		<-log.submitted
	}
	accepted := 1
	for _, admissionErr := range []error{secondErr, thirdErr} {
		if admissionErr == nil {
			accepted++
		} else {
			require.True(t, errors.Is(admissionErr, ch.ErrBackpressured), "unexpected rejection: %v", admissionErr)
		}
	}
	require.Equal(t, 2, accepted, "concurrent enqueue must not exceed Workers+QueueSize unresolved proposals")
}

type blockedQuorumSink struct {
	entered chan struct{}
	release chan struct{}
	results chan Result
}

func (s *blockedQuorumSink) Complete(result Result) {
	if result.Fence.OpID == 1 {
		close(s.entered)
		<-s.release
	}
	s.results <- result
}

func TestQuorumBudgetIncludesBlockedPublication(t *testing.T) {
	log := &asyncQuorumLog{submitted: make(chan replication.Proposal, 8)}
	sink := &blockedQuorumSink{entered: make(chan struct{}), release: make(chan struct{}), results: make(chan Result, 8)}
	pool, err := NewPool(PoolConfig{Name: "q", Workers: 1, QueueSize: 1}, Deps{QuorumLog: log}, sink)
	require.NoError(t, err)
	defer pool.Close()
	defer close(sink.release)
	for _, n := range []byte{1, 2} {
		require.NoError(t, pool.Submit(context.Background(), quorumCommitTask(n)))
		<-log.submitted
	}
	go log.complete(0, replication.Receipt{HW: 7})
	<-sink.entered
	require.ErrorIs(t, pool.Submit(context.Background(), quorumCommitTask(3)), ch.ErrBackpressured,
		"callback-owned result remains charged until its publication returns")
}
