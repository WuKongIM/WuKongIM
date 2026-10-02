package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestPersonDirectoryTaskAdmissionAndMembershipProjectionUseBoundedSlotCommands(t *testing.T) {
	proposer := &collectingMembershipProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	node.cfg.Channel.ReplicaCount = 1
	node.channelDataNodes.UpdateAtRevision(node.router.Table().Revision, []uint64{1, 2})
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")

	admissionResults := node.AdmitPersonDirectoryTasks(context.Background(), []metadb.PersonDirectoryTask{{
		ChannelID: channelID, ChannelType: 1, CommittedTail: 9, CreatedAt: 123,
	}})
	if len(admissionResults) != 1 || admissionResults[0] != nil {
		t.Fatalf("AdmitPersonDirectoryTasks(): %#v", admissionResults)
	}
	requests := proposer.take()
	if len(requests) != 1 || !metafsm.IsCreateChannelRuntimeMetaCommand(requests[0].Command) {
		t.Fatalf("admission requests = %#v, want one create-bearing Slot command", requests)
	}
	if slots, err := metafsm.DecodeCommandHashSlots(requests[0].Command, requests[0].Target.HashSlot); err != nil || len(slots) != 1 {
		t.Fatalf("admission hash slots = %#v err=%v, want exactly source Slot", slots, err)
	}

	memberships := []metadb.UserChannelMembership{
		{UID: "u1", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 9, SourceVersion: 1, UpdatedAt: 123},
		{UID: "u2", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 9, SourceVersion: 1, UpdatedAt: 123},
	}
	results := node.EnsureUserChannelMembershipBatch(context.Background(), memberships)
	if len(results) != len(memberships) || results[0] != nil || results[1] != nil {
		t.Fatalf("membership results = %#v, want aligned success", results)
	}
	requests = proposer.take()
	if len(requests) == 0 || len(requests) > 2 {
		t.Fatalf("membership proposal count = %d, want one per touched logical Slot", len(requests))
	}
	for _, request := range requests {
		if slots, err := metafsm.DecodeCommandHashSlots(request.Command, request.Target.HashSlot); err != nil || len(slots) == 0 {
			t.Fatalf("membership proposal slots = %#v err=%v", slots, err)
		}
	}
}

func TestCompletePersonDirectoryTasksPreservesAlignedCrossSlotResults(t *testing.T) {
	proposer := &collectingMembershipProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	left := personChannelForHashSlot(t, 4, 0)
	right := personChannelForHashSlot(t, 4, 3)

	results := node.CompletePersonDirectoryTasks(context.Background(), []metadb.PersonDirectoryTaskLocation{
		{HashSlot: 0, ChannelID: left, ChannelType: 1, Generation: 1},
		{HashSlot: 3, ChannelID: right, ChannelType: 1, Generation: 1},
	})
	if len(results) != 2 || results[0] != nil || results[1] != nil {
		t.Fatalf("completion results = %#v, want aligned success", results)
	}
	requests := proposer.take()
	if len(requests) != 2 {
		t.Fatalf("completion proposals = %d, want one per logical Slot", len(requests))
	}
	for _, request := range requests {
		hashSlots, err := metafsm.DecodeCommandHashSlots(request.Command, request.Target.HashSlot)
		if err != nil || len(hashSlots) != 1 || hashSlots[0] != request.Target.HashSlot {
			t.Fatalf("completion proposal hash slots = %#v err=%v target=%+v", hashSlots, err, request.Target)
		}
	}

	results = node.CompletePersonDirectoryTasks(context.Background(), []metadb.PersonDirectoryTaskLocation{
		{HashSlot: 1, ChannelID: left, ChannelType: 1, Generation: 1},
		{HashSlot: 3, ChannelID: right, ChannelType: 1, Generation: 1},
	})
	if len(results) != 2 || !errors.Is(results[0], metadb.ErrStaleMeta) || results[1] != nil {
		t.Fatalf("partial completion results = %#v, want stale first and successful second", results)
	}
	if requests := proposer.take(); len(requests) != 1 || requests[0].Target.HashSlot != 3 {
		t.Fatalf("partial completion proposals = %#v, want only the valid hash-slot 3 task", requests)
	}
}

func TestAdmitPersonDirectoryTasksPreservesAlignedCrossSlotResults(t *testing.T) {
	t.Parallel()

	admissionErr := errors.New("source slot unavailable")
	proposer := &selectiveMembershipProposer{failHashSlot: 0, err: admissionErr}
	node := newStartedSlotProxyPortNode(t, proposer)
	node.cfg.Channel.ReplicaCount = 1
	node.channelDataNodes.UpdateAtRevision(node.router.Table().Revision, []uint64{1, 2})
	left := personChannelForHashSlot(t, 4, 0)
	right := personChannelForHashSlot(t, 4, 3)

	results := node.AdmitPersonDirectoryTasks(context.Background(), []metadb.PersonDirectoryTask{
		{ChannelID: left, ChannelType: 1, CreatedAt: 1},
		{ChannelID: right, ChannelType: 1, CreatedAt: 2},
	})
	if len(results) != 2 || !errors.Is(results[0], admissionErr) || results[1] != nil {
		t.Fatalf("admission results = %#v, want failed slot 0 and successful slot 3", results)
	}
}

func TestAdmitPersonDirectoryTaskWavesEmitsFastSlotBeforeSlowSibling(t *testing.T) {
	t.Parallel()

	releaseSlow := make(chan struct{})
	proposer := &blockingSelectiveMembershipProposer{slowHashSlot: 0, entered: make(chan uint16, 2), release: releaseSlow}
	node := newStartedSlotProxyPortNode(t, proposer)
	node.cfg.Channel.ReplicaCount = 1
	node.channelDataNodes.UpdateAtRevision(node.router.Table().Revision, []uint64{1, 2})
	left := personChannelForHashSlot(t, 4, 0)
	right := personChannelForHashSlot(t, 4, 3)

	type admissionResult struct {
		index int
		err   error
	}
	results := make(chan admissionResult, 2)
	done := make(chan struct{})
	go func() {
		node.AdmitPersonDirectoryTaskWaves(context.Background(), []metadb.PersonDirectoryTask{
			{ChannelID: left, ChannelType: 1, CreatedAt: 1},
			{ChannelID: right, ChannelType: 1, CreatedAt: 2},
		}, func(index int, err error) {
			results <- admissionResult{index: index, err: err}
		})
		close(done)
	}()

	select {
	case got := <-results:
		if got.index != 1 || got.err != nil {
			close(releaseSlow)
			t.Fatalf("first admission result = %+v, want fast slot index 1 success", got)
		}
	case <-time.After(time.Second):
		close(releaseSlow)
		t.Fatal("fast source Slot result was held behind slow sibling")
	}
	close(releaseSlow)
	select {
	case got := <-results:
		if got.index != 0 || got.err != nil {
			t.Fatalf("second admission result = %+v, want slow slot index 0 success", got)
		}
	case <-time.After(time.Second):
		t.Fatal("slow source Slot result did not complete after release")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source Slot admission did not join all proposal workers")
	}
}

func personChannelForHashSlot(t *testing.T, count, want uint16) string {
	t.Helper()
	for i := 0; i < 100_000; i++ {
		channelID := runtimechannelid.EncodePersonChannel(fmt.Sprintf("person-%d-a", i), fmt.Sprintf("person-%d-b", i))
		if routing.HashSlotForKey(channelID, count) == want {
			return channelID
		}
	}
	t.Fatalf("no canonical person channel found for hash slot %d", want)
	return ""
}

type collectingMembershipProposer struct {
	mu       sync.Mutex
	requests []propose.Request
}

type selectiveMembershipProposer struct {
	failHashSlot uint16
	err          error
}

type blockingSelectiveMembershipProposer struct {
	slowHashSlot uint16
	entered      chan uint16
	release      <-chan struct{}
}

func (p *blockingSelectiveMembershipProposer) Propose(ctx context.Context, req propose.Request) error {
	p.entered <- req.Target.HashSlot
	if req.Target.HashSlot != p.slowHashSlot {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.release:
		return nil
	}
}

func (p *selectiveMembershipProposer) Propose(_ context.Context, req propose.Request) error {
	if req.Target.HashSlot == p.failHashSlot {
		return p.err
	}
	return nil
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

func TestEnsureUserChannelMembershipBatchSubmitsIndependentSlotsConcurrently(t *testing.T) {
	proposer := &blockingMembershipProposer{
		entered: make(chan uint16, 2),
		release: make(chan struct{}),
	}
	node := newStartedSlotProxyPortNode(t, proposer)
	u0 := keyForNodeHashSlot(t, 4, 0)
	u3 := keyForNodeHashSlot(t, 4, 3)
	channelID := runtimechannelid.EncodePersonChannel(u0, u3)

	done := make(chan []error, 1)
	go func() {
		done <- node.EnsureUserChannelMembershipBatch(context.Background(), []metadb.UserChannelMembership{
			{UID: u0, ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1},
			{UID: u3, ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1},
		})
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
		t.Fatalf("membership projections used one hash slot %d, want two independent slots", first)
	}

	close(proposer.release)
	released = true
	select {
	case results := <-done:
		if len(results) != 2 || results[0] != nil || results[1] != nil {
			t.Fatalf("EnsureUserChannelMembershipBatch() = %#v, want aligned success", results)
		}
	case <-time.After(time.Second):
		t.Fatal("EnsureUserChannelMembershipBatch() did not join concurrent slot proposals")
	}
}

// The production preparation path needs bounded overlap for many logical Slots.
// Keep this fixed resource contract independent of the implementation constant.
func TestUpsertUserChannelMembershipsBoundsIndependentSlotConcurrency(t *testing.T) {
	const width = 8
	proposer := newControlledMembershipProposer(16)
	defer proposer.releaseAll()
	node, uids := membershipSchedulingNode(t, proposer)
	done := make(chan error, 1)
	go func() {
		done <- node.UpsertUserChannelMemberships(context.Background(), "large-group", 2, uids, 9, 17, 123)
	}()
	var active []*controlledMembershipProposal
	for range width {
		active = append(active, awaitControlledMembershipProposal(t, proposer.entered))
	}
	select {
	case <-proposer.entered:
		t.Fatal("membership proposal exceeded the eight-worker bound")
	default:
	}
	if got := proposer.maxActiveCount(); got != width {
		t.Fatalf("active proposals = %d, want %d", got, width)
	}
	// One completed proposal admits exactly one successor, without growing the cohort.
	active[0].releaseProposal()
	awaitMembershipProposalCompletion(t, active[0].completed)
	active = append(active[1:], awaitControlledMembershipProposal(t, proposer.entered))
	if got := proposer.maxActiveCount(); got != width {
		t.Fatalf("refilled proposals = %d, want %d", got, width)
	}
	proposer.releaseAll()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("membership proposals were not joined")
	}
}

func TestUpsertUserChannelMembershipsCancellationJoinsAdmittedCommands(t *testing.T) {
	proposer := newControlledMembershipProposer(16)
	defer proposer.releaseAll()
	node, uids := membershipSchedulingNode(t, proposer)
	observer := &membershipSchedulingObserver{}
	node.cfg.MembershipObserver = observer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- node.UpsertUserChannelMemberships(ctx, "large-group", 2, uids, 9, 17, 123) }()
	for range 8 {
		_ = awaitControlledMembershipProposal(t, proposer.entered)
	}
	cancel()
	select {
	case <-done:
		t.Fatal("returned before admitted durable commands finished")
	default:
	}
	proposer.releaseAll()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled membership proposal workers did not join")
	}
	if got := observer.rows; got != 8 {
		t.Fatalf("confirmed rows = %d, want eight admitted completions", got)
	}
	select {
	case <-proposer.entered:
		t.Fatal("cancellation admitted queued membership commands")
	default:
	}
}

func TestUpsertUserChannelMembershipsFailurePreservesCauseAndJoinsPeers(t *testing.T) {
	cause := errors.New("membership Slot unavailable")
	proposer := newControlledMembershipProposer(16)
	proposer.failHashSlot, proposer.failErr = 0, cause
	defer proposer.releaseAll()
	node, uids := membershipSchedulingNode(t, proposer)
	observer := &membershipSchedulingObserver{}
	node.cfg.MembershipObserver = observer
	done := make(chan error, 1)
	go func() {
		done <- node.UpsertUserChannelMemberships(context.Background(), "large-group", 2, uids, 9, 17, 123)
	}()
	var active []*controlledMembershipProposal
	for range 8 {
		active = append(active, awaitControlledMembershipProposal(t, proposer.entered))
	}
	for _, proposal := range active {
		if proposal.hashSlot == 0 {
			proposal.releaseProposal()
		}
	}
	select {
	case <-active[0].ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("failed proposal did not cancel queued work")
	}
	select {
	case <-done:
		t.Fatal("failure returned before admitted peers finished")
	default:
	}
	proposer.releaseAll()
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatalf("error = %v, want original Slot cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("failed membership proposal workers did not join")
	}
	if got := observer.rows; got != 7 {
		t.Fatalf("confirmed rows = %d, want seven admitted successes", got)
	}
	select {
	case <-proposer.entered:
		t.Fatal("failure admitted queued membership commands")
	default:
	}
}

// membershipSchedulingNode supplies independent physical owners, so coalescing
// logical shards does not turn the eight-worker resource contract into two jobs.
func membershipSchedulingNode(t *testing.T, proposer *controlledMembershipProposer) (*Node, []string) {
	t.Helper()
	node := newStartedSlotProxyPortNode(t, proposer)
	snapshot := node.controlSnapshot
	snapshot.Revision++
	snapshot.Slots = nil
	snapshot.HashSlots = control.HashSlotTable{Revision: 10, Count: 16}
	leaders := make([]routing.SlotStatus, 0, 16)
	for i := uint16(0); i < 16; i++ {
		slotID := uint32(i) + 1
		snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{SlotID: slotID, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1, PreferredLeader: 1})
		snapshot.HashSlots.Ranges = append(snapshot.HashSlots.Ranges, control.HashSlotRange{From: i, To: i, SlotID: slotID})
		leaders = append(leaders, routing.SlotStatus{SlotID: slotID, Leader: 1})
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	node.router.UpdateSlotLeaders(leaders)
	uids := make([]string, 16)
	for i := range uids {
		uids[i] = keyForNodeHashSlot(t, 16, uint16(i))
	}
	return node, uids
}

type membershipSchedulingObserver struct{ rows int }

func (o *membershipSchedulingObserver) ObserveMembershipMutation(obs MembershipMutationObservation) {
	o.rows += obs.Rows
}

type controlledMembershipProposal struct {
	hashSlot    uint16
	ctx         context.Context
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

	mu           sync.Mutex
	active       int
	maxActive    int
	failHashSlot uint16
	failErr      error
}

func newControlledMembershipProposer(capacity int) *controlledMembershipProposer {
	return &controlledMembershipProposer{
		entered:  make(chan *controlledMembershipProposal, capacity),
		shutdown: make(chan struct{}),
	}
}

func (p *controlledMembershipProposer) Propose(ctx context.Context, req propose.Request) error {
	proposal := &controlledMembershipProposal{
		hashSlot:  req.Target.HashSlot,
		ctx:       ctx,
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
	if req.Target.HashSlot == p.failHashSlot {
		return p.failErr
	}
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
