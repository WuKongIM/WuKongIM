//go:build integration

package multiraft

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestReadBarrierQuorumWithoutLogWrites(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{Seed: 951})
	slotID := SlotID(205)
	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	leader := cluster.waitForLeader(t, slotID)
	rt := cluster.runtime(leader)
	future, err := rt.Propose(context.Background(), slotID, proposalString("visible"))
	if err != nil {
		t.Fatal(err)
	}
	written := waitForFutureResult(t, future)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 10; i++ {
		if err := rt.ReadBarrier(ctx, slotID); err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent callers exercise the same public admission and worker path.
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- rt.ReadBarrier(ctx, slotID) }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	status, err := rt.Status(slotID)
	if err != nil || status.CommitIndex != written.Index || status.AppliedIndex < written.Index {
		t.Fatalf("read changed log or missed apply: %+v %v", status, err)
	}
	follower := cluster.pickFollower(leader)
	if err := cluster.runtime(follower).ReadBarrier(ctx, slotID); err == nil {
		t.Fatal("follower served a read barrier")
	}
	cluster.partitionNode(leader)
	isolated, cancelIsolated := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelIsolated()
	if err := rt.ReadBarrier(isolated, slotID); err == nil {
		t.Fatal("isolated leader reused earlier quorum evidence")
	}
}
