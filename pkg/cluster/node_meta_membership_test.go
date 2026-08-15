package cluster

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestPreparePersonChannelDirectoryBatchCombinesSameSlotMembershipAndRuntimeMeta(t *testing.T) {
	proposer := &collectingMembershipProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	node.cfg.Channel.ReplicaCount = 1
	node.channelDataNodes.UpdateAtRevision(node.router.Table().Revision, []uint64{1, 2})
	uid := keyForNodeHashSlot(t, 4, 1)
	channelID := keyForNodeHashSlot(t, 4, 0)
	memberships := []metadb.UserChannelMembership{{
		UID: uid, ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1,
	}}
	channels := []metadb.ChannelKey{{ChannelID: channelID, ChannelType: 1}}

	if err := node.PreparePersonChannelDirectoryBatch(context.Background(), memberships, channels); err != nil {
		t.Fatalf("PreparePersonChannelDirectoryBatch() error = %v", err)
	}
	requests := proposer.take()
	if len(requests) != 1 {
		t.Fatalf("prepare proposal count = %d, want one combined logical-Slot proposal", len(requests))
	}
	request := requests[0]
	if !metafsm.IsCreateChannelRuntimeMetaCommand(request.Command) {
		t.Fatalf("prepare command does not contain runtime meta: %x", request.Command)
	}
	if count, err := metafsm.CreateChannelRuntimeMetaBatchCommandSize(request.Command); err != nil || count != 1 {
		t.Fatalf("runtime meta count = %d, err=%v, want 1", count, err)
	}
	if slots, err := metafsm.DecodeCommandHashSlots(request.Command, request.Target.HashSlot); err != nil || len(slots) != 2 || slots[0] != 0 || slots[1] != 1 {
		t.Fatalf("prepare hash slots = %#v, err=%v, want [0 1]", slots, err)
	}
}

func TestPersonDirectoryBatchMutationsSubmitOncePerLogicalSlot(t *testing.T) {
	proposer := &collectingMembershipProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	keys := []string{
		keyForNodeHashSlot(t, 4, 0),
		keyForNodeHashSlot(t, 4, 1),
		keyForNodeHashSlot(t, 4, 2),
		keyForNodeHashSlot(t, 4, 3),
	}
	memberships := make([]metadb.UserChannelMembership, len(keys))
	ready := make([]metadb.ChannelKey, len(keys))
	for i, key := range keys {
		memberships[i] = metadb.UserChannelMembership{UID: key, ChannelID: "person-" + key, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1}
		ready[i] = metadb.ChannelKey{ChannelID: key, ChannelType: 1}
	}

	if err := node.UpsertUserChannelMembershipBatch(context.Background(), memberships); err != nil {
		t.Fatalf("UpsertUserChannelMembershipBatch() error = %v", err)
	}
	requests := proposer.take()
	assertTwoLogicalSlotBatchRequests(t, requests)

	if err := node.EnsureChannelDirectoriesReady(context.Background(), ready); err != nil {
		t.Fatalf("EnsureChannelDirectoriesReady() error = %v", err)
	}
	requests = proposer.take()
	assertTwoLogicalSlotBatchRequests(t, requests)
}

type collectingMembershipProposer struct {
	mu       sync.Mutex
	requests []propose.Request
}

func (p *collectingMembershipProposer) Propose(_ context.Context, req propose.Request) error {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return nil
}

func (p *collectingMembershipProposer) take() []propose.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := append([]propose.Request(nil), p.requests...)
	p.requests = nil
	return requests
}

func assertTwoLogicalSlotBatchRequests(t *testing.T, requests []propose.Request) {
	t.Helper()
	if len(requests) != 2 {
		t.Fatalf("proposal count = %d, want one per logical Slot (2); requests=%#v", len(requests), requests)
	}
	seen := map[uint32]bool{}
	for _, request := range requests {
		if !request.Target.HasSlotID || !request.Target.HasHashSlot || len(request.Command) < 2 {
			t.Fatalf("proposal target/command = %#v, want explicit logical Slot and hash slot", request)
		}
		seen[request.Target.SlotID] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("proposal logical Slots = %#v, want 1 and 2", seen)
	}
}

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
	proposer := newControlledMembershipProposer(3)
	defer proposer.releaseAll()
	node := newStartedSlotProxyPortNode(t, proposer)
	u0 := keyForNodeHashSlot(t, 4, 0)
	u1 := keyForNodeHashSlot(t, 4, 1)
	u3 := keyForNodeHashSlot(t, 4, 3)

	done := make(chan error, 1)
	go func() {
		done <- node.UpsertUserChannelMemberships(context.Background(), "person-channel", 1, []string{u0, u1, u3}, 0, 1, 1)
	}()

	first := awaitControlledMembershipProposal(t, proposer.entered)
	second := awaitControlledMembershipProposal(t, proposer.entered)
	select {
	case third := <-proposer.entered:
		t.Fatalf("third hash-slot proposal %d started before one of two bounded workers completed", third.hashSlot)
	default:
	}
	if got := proposer.maxActiveCount(); got != maxMembershipProposalConcurrency {
		t.Fatalf("maximum active membership proposals = %d, want %d", got, maxMembershipProposalConcurrency)
	}

	first.releaseProposal()
	awaitMembershipProposalCompletion(t, first.completed)
	third := awaitControlledMembershipProposal(t, proposer.entered)
	if third.hashSlot == first.hashSlot || third.hashSlot == second.hashSlot {
		t.Fatalf("third proposal reused an entered hash slot: first=%d second=%d third=%d", first.hashSlot, second.hashSlot, third.hashSlot)
	}
	if got := proposer.maxActiveCount(); got != maxMembershipProposalConcurrency {
		t.Fatalf("maximum active membership proposals after refill = %d, want %d", got, maxMembershipProposalConcurrency)
	}

	second.releaseProposal()
	third.releaseProposal()
	awaitMembershipProposalCompletion(t, second.completed)
	awaitMembershipProposalCompletion(t, third.completed)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpsertUserChannelMemberships() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UpsertUserChannelMemberships() did not finish bounded slot proposals")
	}
}

type controlledMembershipProposal struct {
	hashSlot    uint16
	release     chan struct{}
	completed   chan struct{}
	releaseOnce sync.Once
}

func (p *controlledMembershipProposal) releaseProposal() {
	p.releaseOnce.Do(func() { close(p.release) })
}

type controlledMembershipProposer struct {
	entered  chan *controlledMembershipProposal
	shutdown chan struct{}
	stopOnce sync.Once

	mu        sync.Mutex
	active    int
	maxActive int
}

func newControlledMembershipProposer(capacity int) *controlledMembershipProposer {
	return &controlledMembershipProposer{
		entered:  make(chan *controlledMembershipProposal, capacity),
		shutdown: make(chan struct{}),
	}
}

func (p *controlledMembershipProposer) Propose(_ context.Context, req propose.Request) error {
	proposal := &controlledMembershipProposal{
		hashSlot:  req.Target.HashSlot,
		release:   make(chan struct{}),
		completed: make(chan struct{}),
	}
	p.mu.Lock()
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.mu.Unlock()
	p.entered <- proposal
	select {
	case <-proposal.release:
	case <-p.shutdown:
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	close(proposal.completed)
	return nil
}

func (p *controlledMembershipProposer) maxActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

func (p *controlledMembershipProposer) releaseAll() {
	p.stopOnce.Do(func() { close(p.shutdown) })
}

func awaitControlledMembershipProposal(t *testing.T, entered <-chan *controlledMembershipProposal) *controlledMembershipProposal {
	t.Helper()
	select {
	case proposal := <-entered:
		return proposal
	case <-time.After(time.Second):
		t.Fatal("membership proposal did not enter after a bounded worker became available")
		return nil
	}
}

func awaitMembershipProposalCompletion(t *testing.T, completed <-chan struct{}) {
	t.Helper()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("membership proposal did not complete after release")
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
