//go:build integration

package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Independent source Slots must enter durable completion without waiting for a
// neighboring source. Every submitted proposal call must still join before return.
func TestCompletePersonDirectoryTasksIndependentSourcesProgressTogether(t *testing.T) {
	proposer := newControlledMembershipProposer(2)
	defer proposer.releaseAll()
	node := newStartedSlotProxyPortNode(t, proposer)
	tasks := []metadb.PersonDirectoryTaskLocation{
		{HashSlot: 0, ChannelID: personChannelForHashSlot(t, 4, 0), ChannelType: 1, Generation: 1},
		{HashSlot: 3, ChannelID: personChannelForHashSlot(t, 4, 3), ChannelType: 1, Generation: 1},
	}
	done := make(chan []error, 1)
	go func() { done <- node.CompletePersonDirectoryTasks(context.Background(), tasks) }()
	first := awaitControlledMembershipProposal(t, proposer.entered)
	second := awaitControlledMembershipProposal(t, proposer.entered)
	if first.hashSlot == second.hashSlot {
		t.Fatal("independent source completions used the same Hash Slot")
	}
	second.releaseProposal()
	awaitMembershipProposalCompletion(t, second.completed)
	select {
	case <-done:
		t.Fatal("returned before the first submitted completion call joined")
	default:
	}
	first.releaseProposal()
	select {
	case results := <-done:
		if len(results) != len(tasks) || results[0] != nil || results[1] != nil {
			t.Fatalf("completion results = %v, want aligned success", results)
		}
	case <-time.After(time.Second):
		t.Fatal("source completions did not join after release")
	}
}

// Failure cases: completion must remain bounded across many independent Slots,
// cancellation must not abandon submitted proposal calls, and unsubmitted tasks must
// retain their own cancellation result rather than inherit a sibling's success.
func TestCompletePersonDirectoryTasksBoundsAndJoinsCanceledWave(t *testing.T) {
	proposer := newControlledMembershipProposer(16)
	defer proposer.releaseAll()
	node, _ := membershipSchedulingNode(t, proposer)
	tasks := make([]metadb.PersonDirectoryTaskLocation, 16)
	for i := range tasks {
		tasks[i] = metadb.PersonDirectoryTaskLocation{
			HashSlot: metadb.HashSlot(i), ChannelID: personChannelForHashSlot(t, 16, uint16(i)), ChannelType: 1, Generation: 1,
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan []error, 1)
	go func() { done <- node.CompletePersonDirectoryTasks(ctx, tasks) }()
	for range 10 {
		awaitControlledMembershipProposal(t, proposer.entered)
	}
	select {
	case <-proposer.entered:
		t.Fatal("completion exceeded ten concurrent source proposals")
	default:
	}
	cancel()
	select {
	case <-done:
		t.Fatal("cancellation abandoned submitted completion calls")
	default:
	}
	proposer.releaseAll()
	select {
	case results := <-done:
		if len(results) != len(tasks) {
			t.Fatalf("completion result count = %d, want %d", len(results), len(tasks))
		}
		for i, err := range results {
			if i < 10 && err != nil || i >= 10 && !errors.Is(err, context.Canceled) {
				t.Fatalf("completion result %d = %v, want admitted success or queued cancellation", i, err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("canceled completion wave did not join")
	}
	if got := proposer.maxActiveCount(); got != 10 {
		t.Fatalf("maximum concurrent completions = %d, want ten", got)
	}
}
