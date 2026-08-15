package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
)

func TestUpsertUserChannelMembershipsSubmitsIndependentSlotsConcurrently(t *testing.T) {
	proposer := &blockingMembershipProposer{
		entered: make(chan uint16, 2),
		release: make(chan struct{}),
	}
	node := newStartedSlotProxyPortNode(t, proposer)
	u0 := keyForNodeHashSlot(t, 4, 0)
	u3 := keyForNodeHashSlot(t, 4, 3)

	done := make(chan error, 1)
	go func() {
		done <- node.UpsertUserChannelMemberships(context.Background(), "person-channel", 1, []string{u0, u3}, 0, 1, 1)
	}()
	released := false
	defer func() {
		if !released {
			close(proposer.release)
		}
	}()

	first := awaitMembershipProposal(t, proposer.entered)
	second := awaitMembershipProposal(t, proposer.entered)
	if first == second {
		t.Fatalf("membership proposals used one hash slot %d, want two independent slots", first)
	}

	close(proposer.release)
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpsertUserChannelMemberships() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UpsertUserChannelMemberships() did not join concurrent slot proposals")
	}
}

func TestUpsertUserChannelMembershipsBoundsIndependentSlotConcurrency(t *testing.T) {
	proposer := &blockingMembershipProposer{
		entered: make(chan uint16, 3),
		release: make(chan struct{}),
	}
	node := newStartedSlotProxyPortNode(t, proposer)
	u0 := keyForNodeHashSlot(t, 4, 0)
	u1 := keyForNodeHashSlot(t, 4, 1)
	u3 := keyForNodeHashSlot(t, 4, 3)

	done := make(chan error, 1)
	go func() {
		done <- node.UpsertUserChannelMemberships(context.Background(), "person-channel", 1, []string{u0, u1, u3}, 0, 1, 1)
	}()
	released := false
	defer func() {
		if !released {
			close(proposer.release)
		}
	}()

	_ = awaitMembershipProposal(t, proposer.entered)
	_ = awaitMembershipProposal(t, proposer.entered)
	select {
	case hashSlot := <-proposer.entered:
		t.Fatalf("third hash-slot proposal %d started before one of two bounded workers completed", hashSlot)
	case <-time.After(100 * time.Millisecond):
	}

	close(proposer.release)
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpsertUserChannelMemberships() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UpsertUserChannelMemberships() did not finish bounded slot proposals")
	}
}

type blockingMembershipProposer struct {
	entered chan uint16
	release chan struct{}
}

func (p *blockingMembershipProposer) Propose(_ context.Context, req propose.Request) error {
	p.entered <- req.Target.HashSlot
	<-p.release
	return nil
}

func awaitMembershipProposal(t *testing.T, entered <-chan uint16) uint16 {
	t.Helper()
	select {
	case hashSlot := <-entered:
		return hashSlot
	case <-time.After(250 * time.Millisecond):
		t.Fatal("independent UID hash-slot proposal was serialized behind another slot")
		return 0
	}
}
