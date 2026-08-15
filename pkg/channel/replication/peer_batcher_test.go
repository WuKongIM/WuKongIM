package replication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestDurableRoundUsesDataBearingPeerBatcherCompletionAsDurableVote(t *testing.T) {
	executor := &manualPeerExecutor{submitted: make(chan struct{}, 1)}
	link := &recordingPeerLink{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 4, MaxBatchBytes: 4096,
		MaxQueuedItems: 8, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	request := testReplicateRequest(t, "1:round", "round", 1, []byte("proposal-body"))
	dispatcher := &batchingDurabilityDispatcher{
		ownerContext: context.Background(), local: immediateLocalDurability{}, peers: batcher, repairs: discardFollowerRepairSink{},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2}, 2, durableProposal{
			first: request.Manifest.BaseOffset + 1, last: request.Manifest.LastOffset,
			channelKey: request.ChannelKey, channelID: request.ChannelID, leader: request.Leader,
			manifest: request.Manifest, records: request.Records,
		}, dispatcher)
		done <- err
	}()

	<-executor.submitted
	if len(link.batches) != 0 {
		t.Fatal("peer exchange ran before the bounded executor")
	}
	executor.RunNext()
	if err := <-done; err != nil {
		t.Fatalf("runDurableRound() error = %v", err)
	}
	if len(link.batches) != 1 || string(link.batches[0].Items[0].Replicate.Records[0].Payload) != "proposal-body" {
		t.Fatalf("peer batches = %+v, want one data-bearing proposal", link.batches)
	}
}

func TestBatchingDurabilityDispatcherUsesOwnerContextForAcceptedLocalWrite(t *testing.T) {
	owner, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	local := &contextRecordingLocalDurability{}
	dispatcher := &batchingDurabilityDispatcher{ownerContext: owner, local: local}
	caller, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	var completion durabilityCompletion
	if err := dispatcher.submitLocal(caller, durableProposal{}, func(got durabilityCompletion) { completion = got }); err != nil {
		t.Fatalf("submitLocal() error = %v", err)
	}
	if local.ctx != owner {
		t.Fatal("local durability inherited caller cancellation instead of owner lifecycle")
	}
	if completion.outcome != ch.AppendOutcomeDurable {
		t.Fatalf("completion outcome = %v, want durable", completion.outcome)
	}
}

func TestPeerBatcherCarriesRecordsAndCoalescesReadyWorkForOneTarget(t *testing.T) {
	executor := &manualPeerExecutor{}
	link := &recordingPeerLink{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 4, MaxBatchBytes: 4096,
		MaxQueuedItems: 8, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}

	first := testReplicateRequest(t, "1:first", "first", 1, []byte("payload-a"))
	second := testReplicateRequest(t, "1:second", "second", 2, []byte("payload-b"))
	completions := make([]ReplicateResult, 0, 2)
	for _, request := range []ReplicateRequest{first, second} {
		request := request
		if err := batcher.submit(context.Background(), 2, request, func(result ReplicateResult, err error) {
			if err != nil {
				t.Errorf("replicate completion error = %v", err)
			}
			completions = append(completions, result)
		}); err != nil {
			t.Fatalf("submit(%s) error = %v", request.ChannelKey, err)
		}
	}
	if len(link.batches) != 0 {
		t.Fatalf("Exchange calls before executor drain = %d, want 0", len(link.batches))
	}
	if executor.Len() != 1 {
		t.Fatalf("scheduled drains = %d, want one target owner", executor.Len())
	}

	executor.RunNext()

	if len(link.batches) != 1 {
		t.Fatalf("Exchange calls = %d, want 1", len(link.batches))
	}
	batch := link.batches[0]
	if batch.Version != ExchangeVersion || len(batch.Items) != 2 {
		t.Fatalf("Exchange batch = %+v, want version %d with 2 items", batch, ExchangeVersion)
	}
	if got := string(batch.Items[0].Replicate.Records[0].Payload); got != "payload-a" {
		t.Fatalf("first replicated payload = %q", got)
	}
	if got := string(batch.Items[1].Replicate.Records[0].Payload); got != "payload-b" {
		t.Fatalf("second replicated payload = %q", got)
	}
	if len(completions) != 2 || completions[0].Status != ReplicateDurable || completions[1].Status != ReplicateDurable {
		t.Fatalf("completions = %+v, want two durable results", completions)
	}
}

func TestDurableRoundOwnsOneImmutablePayloadCopyAcrossFollowers(t *testing.T) {
	executor := &manualPeerExecutor{submitted: make(chan struct{}, 2)}
	link := &recordingPeerLink{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 2, MaxBatchBytes: 4096,
		MaxQueuedItems: 4, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 2, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	request := testReplicateRequest(t, "1:owned", "owned", 1, []byte("immutable"))
	dispatcher := &batchingDurabilityDispatcher{
		ownerContext: context.Background(), local: immediateLocalDurability{}, peers: batcher, repairs: discardFollowerRepairSink{},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{
			first: 1, last: 1, channelKey: request.ChannelKey, channelID: request.ChannelID,
			leader: request.Leader, manifest: request.Manifest, records: request.Records,
		}, dispatcher)
		done <- err
	}()
	<-executor.submitted
	<-executor.submitted
	executor.RunNext()
	executor.RunNext()
	if err := <-done; err != nil {
		t.Fatalf("runDurableRound() error = %v", err)
	}
	if len(link.batches) != 2 {
		t.Fatalf("peer batches = %d, want two follower targets", len(link.batches))
	}
	firstPayload := link.batches[0].Items[0].Replicate.Records[0].Payload
	secondPayload := link.batches[1].Items[0].Replicate.Records[0].Payload
	if &firstPayload[0] != &secondPayload[0] {
		t.Fatal("followers did not share the round-owned immutable payload")
	}
	request.Records[0].Payload[0] = 'X'
	if string(firstPayload) != "immutable" {
		t.Fatalf("round-owned payload = %q after caller mutation, want immutable", firstPayload)
	}
}

func TestPeerBatcherSplitsOneTargetWithoutASecondCollectionTimer(t *testing.T) {
	executor := &manualPeerExecutor{}
	link := &recordingPeerLink{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 2, MaxBatchBytes: 4096,
		MaxQueuedItems: 8, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	for index := 0; index < 3; index++ {
		request := testReplicateRequest(t, ch.ChannelKey("1:batch"+string(rune('a'+index))), "batch", byte(index+1), []byte{byte(index + 1)})
		if err := batcher.submit(context.Background(), 2, request, func(ReplicateResult, error) {}); err != nil {
			t.Fatalf("submit(%d) error = %v", index, err)
		}
	}

	executor.RunNext()

	if executor.Len() != 0 {
		t.Fatalf("scheduled drains after owner emptied target = %d, want 0", executor.Len())
	}
	if len(link.batches) != 2 || len(link.batches[0].Items) != 2 || len(link.batches[1].Items) != 1 {
		t.Fatalf("batch item counts = %v, want [2 1]", batchItemCounts(link.batches))
	}
}

func TestPeerBatcherBoundsAcceptedOwnershipUntilCompletion(t *testing.T) {
	executor := &manualPeerExecutor{}
	link := &recordingPeerLink{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 2, MaxBatchBytes: 4096,
		MaxQueuedItems: 4, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 2, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	for index := 0; index < 2; index++ {
		request := testReplicateRequest(t, ch.ChannelKey("1:bounded"+string(rune('a'+index))), "bounded", byte(index+1), []byte{byte(index + 1)})
		if err := batcher.submit(context.Background(), 2, request, func(ReplicateResult, error) {}); err != nil {
			t.Fatalf("submit(%d) error = %v", index, err)
		}
	}
	third := testReplicateRequest(t, "1:bounded-c", "bounded", 3, []byte("c"))
	if err := batcher.submit(context.Background(), 2, third, func(ReplicateResult, error) {}); !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("third submit error = %v, want backpressured", err)
	}

	executor.RunNext()

	if err := batcher.submit(context.Background(), 2, third, func(ReplicateResult, error) {}); err != nil {
		t.Fatalf("submit after completion error = %v", err)
	}
}

func TestPeerBatcherCorrelatesOutOfOrderResultsAndRejectsDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		duplicate bool
	}{
		{name: "out-of-order"},
		{name: "duplicate", duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &manualPeerExecutor{}
			link := &reorderingPeerLink{duplicate: tc.duplicate}
			batcher, err := newPeerBatcher(peerBatcherConfig{
				Link: link, Executor: executor,
				OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
				MaxBatchItems: 2, MaxBatchBytes: 4096,
				MaxQueuedItems: 4, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 2, MaxTargetQueuedBytes: 4096,
			})
			if err != nil {
				t.Fatalf("newPeerBatcher() error = %v", err)
			}
			results := make([]ReplicateResult, 0, 2)
			errs := make([]error, 0, 2)
			for index := 0; index < 2; index++ {
				request := testReplicateRequest(t, ch.ChannelKey("1:correlate"+string(rune('a'+index))), "correlate", byte(index+1), []byte{byte(index + 1)})
				if err := batcher.submit(context.Background(), 2, request, func(result ReplicateResult, err error) {
					results = append(results, result)
					errs = append(errs, err)
				}); err != nil {
					t.Fatalf("submit(%d) error = %v", index, err)
				}
			}
			executor.RunNext()
			if tc.duplicate {
				for index := range results {
					if results[index].Status != ReplicateOutcomeUnknown || !errors.Is(errs[index], errInvalidExchangeResult) {
						t.Fatalf("duplicate response completion[%d] = %+v, %v", index, results[index], errs[index])
					}
				}
				return
			}
			if len(results) != 2 || results[0].LastOffset != 1 || results[1].LastOffset != 1 || errs[0] != nil || errs[1] != nil {
				t.Fatalf("out-of-order correlated completions = %+v errors=%v", results, errs)
			}
		})
	}
}

func TestPeerBatcherRejectsDurableResultForAnotherManifest(t *testing.T) {
	executor := &manualPeerExecutor{}
	link := &wrongProofPeerLink{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 2, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 1, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	request := testReplicateRequest(t, "1:proof", "proof", 1, []byte("payload"))
	var result ReplicateResult
	var completionErr error
	if err := batcher.submit(context.Background(), 2, request, func(got ReplicateResult, err error) {
		result, completionErr = got, err
	}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}

	executor.RunNext()

	if result.Status != ReplicateOutcomeUnknown || !errors.Is(completionErr, errInvalidExchangeResult) {
		t.Fatalf("completion = %+v, %v, want invalid proof mapped to outcome unknown", result, completionErr)
	}
}

func TestPeerBatcherRejectsCanceledCallerBeforeAdmission(t *testing.T) {
	executor := &manualPeerExecutor{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: &recordingPeerLink{}, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 2, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 1, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := testReplicateRequest(t, "1:canceled", "canceled", 1, []byte("payload"))
	if err := batcher.submit(ctx, 2, request, func(ReplicateResult, error) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("submit() error = %v, want caller cancellation before admission", err)
	}
	if executor.Len() != 0 || batcher.ownedItems != 0 || batcher.ownedBytes != 0 {
		t.Fatalf("canceled admission retained work: tasks=%d items=%d bytes=%d", executor.Len(), batcher.ownedItems, batcher.ownedBytes)
	}
}

func TestPeerBatcherOwnerCancellationReleasesHungExchange(t *testing.T) {
	owner, cancelOwner := context.WithCancel(context.Background())
	link := &blockingPeerLink{started: make(chan struct{})}
	executor := &manualPeerExecutor{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: owner, ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 2, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 1, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	completed := make(chan error, 1)
	request := testReplicateRequest(t, "1:hung", "hung", 1, []byte("payload"))
	if err := batcher.submit(context.Background(), 2, request, func(result ReplicateResult, err error) {
		if result.Status != ReplicateOutcomeUnknown {
			t.Errorf("completion status = %v, want outcome unknown", result.Status)
		}
		completed <- err
	}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}
	drained := make(chan struct{})
	go func() {
		executor.RunNext()
		close(drained)
	}()
	<-link.started
	cancelOwner()
	if err := <-completed; !errors.Is(err, errPeerOutcomeUnknown) {
		t.Fatalf("completion error = %v, want outcome unknown", err)
	}
	<-drained
	if batcher.ownedItems != 0 || batcher.ownedBytes != 0 {
		t.Fatalf("owner cancellation retained items=%d bytes=%d", batcher.ownedItems, batcher.ownedBytes)
	}
	if err := batcher.submit(context.Background(), 2, request, func(ReplicateResult, error) {}); !errors.Is(err, ch.ErrClosed) {
		t.Fatalf("submit after owner cancellation error = %v, want closed", err)
	}
}

func TestPeerBatcherBoundsOneTargetWithoutBlockingAnother(t *testing.T) {
	executor := &manualPeerExecutor{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: &recordingPeerLink{}, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 4, MaxQueuedBytes: 16384, MaxTargetQueuedItems: 2, MaxTargetQueuedBytes: 8192,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	for index := 0; index < 2; index++ {
		request := testReplicateRequest(t, ch.ChannelKey("1:target"+string(rune('a'+index))), "target", byte(index+1), []byte("payload"))
		if err := batcher.submit(context.Background(), 2, request, func(ReplicateResult, error) {}); err != nil {
			t.Fatalf("target 2 submit(%d) error = %v", index, err)
		}
	}
	blocked := testReplicateRequest(t, "1:blocked", "blocked", 3, []byte("payload"))
	if err := batcher.submit(context.Background(), 2, blocked, func(ReplicateResult, error) {}); !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("third target 2 submit error = %v, want backpressured", err)
	}
	healthy := testReplicateRequest(t, "1:healthy", "healthy", 4, []byte("payload"))
	healthy.Follower = 3
	if err := batcher.submit(context.Background(), 3, healthy, func(ReplicateResult, error) {}); err != nil {
		t.Fatalf("healthy target submit error = %v", err)
	}
}

func TestPeerBatcherRequiresGlobalHeadroomForAnotherTarget(t *testing.T) {
	_, err := newPeerBatcher(peerBatcherConfig{
		Link: &recordingPeerLink{}, Executor: &manualPeerExecutor{},
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 2, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 2, MaxTargetQueuedBytes: 8192,
	})
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("newPeerBatcher() error = %v, want missing cross-target headroom rejected", err)
	}
}

func TestDurableRoundRetainsFollowerNeedFromWithoutClassifyingGapAsConflict(t *testing.T) {
	executor := &manualPeerExecutor{submitted: make(chan struct{}, 1)}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: needFromPeerLink{}, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 2, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 1, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	request := testReplicateRequest(t, "1:gap", "gap", 1, []byte("payload"))
	repairs := &recordingFollowerRepairSink{}
	dispatcher := &batchingDurabilityDispatcher{
		ownerContext: context.Background(), local: definitelyNotLocalDurability{}, peers: batcher, repairs: repairs,
	}
	done := make(chan struct {
		result durableRoundResult
		err    error
	}, 1)
	go func() {
		result, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2}, 2, durableProposal{
			first: 1, last: 1, channelKey: request.ChannelKey, channelID: request.ChannelID,
			leader: request.Leader, manifest: request.Manifest, records: request.Records,
		}, dispatcher)
		done <- struct {
			result durableRoundResult
			err    error
		}{result: result, err: err}
	}()
	<-executor.submitted
	executor.RunNext()
	got := <-done
	if !errors.Is(got.err, errDurableQuorumUnavailable) {
		t.Fatalf("runDurableRound() error = %v, want quorum unavailable", got.err)
	}
	if got.result.outcome != ch.AppendOutcomeDefinitelyNotWritten {
		t.Fatalf("gap outcome = %v, want definitely not written", got.result.outcome)
	}
	if len(got.result.repairs) != 1 || got.result.repairs[0].follower != 2 || got.result.repairs[0].needFrom != 1 {
		t.Fatalf("gap repairs = %+v, want follower 2 from offset 1", got.result.repairs)
	}
}

func TestTrailingFollowerNeedFromReachesOwnedRepairSinkAfterQuorumReturns(t *testing.T) {
	executor := &manualPeerExecutor{submitted: make(chan struct{}, 2)}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: splitFollowerPeerLink{}, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 4, MaxQueuedBytes: 16384, MaxTargetQueuedItems: 1, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	repairs := &recordingFollowerRepairSink{}
	request := testReplicateRequest(t, "1:trailing-gap", "trailing-gap", 1, []byte("payload"))
	dispatcher := &batchingDurabilityDispatcher{
		ownerContext: context.Background(), local: immediateLocalDurability{}, peers: batcher, repairs: repairs,
	}
	done := make(chan error, 1)
	go func() {
		_, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{
			first: 1, last: 1, channelKey: request.ChannelKey, channelID: request.ChannelID,
			leader: request.Leader, manifest: request.Manifest, records: request.Records,
		}, dispatcher)
		done <- err
	}()
	<-executor.submitted
	<-executor.submitted
	executor.RunNext()
	if err := <-done; err != nil {
		t.Fatalf("runDurableRound() error = %v", err)
	}
	if got := repairs.snapshot(); len(got) != 0 {
		t.Fatalf("repairs before trailing follower completion = %+v, want none", got)
	}

	executor.RunNext()
	got := repairs.snapshot()
	if len(got) != 1 || got[0].follower != 3 || got[0].needFrom != 1 || got[0].channelKey != request.ChannelKey || got[0].manifest != request.Manifest {
		t.Fatalf("trailing repairs = %+v, want exact follower 3 gap", got)
	}
}

func TestPeerBatcherMapsPeerPanicToOutcomeUnknownAndReleasesOwnership(t *testing.T) {
	executor := &manualPeerExecutor{}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: panicPeerLink{}, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 2, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 1, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	request := testReplicateRequest(t, "1:panic", "panic", 1, []byte("payload"))
	var result ReplicateResult
	var completionErr error
	if err := batcher.submit(context.Background(), 2, request, func(got ReplicateResult, err error) {
		result, completionErr = got, err
	}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}
	executor.RunNext()
	if result.Status != ReplicateOutcomeUnknown || !errors.Is(completionErr, errPeerExchangePanic) || batcher.ownedItems != 0 {
		t.Fatalf("panic completion = %+v, %v owned=%d", result, completionErr, batcher.ownedItems)
	}
}

func testReplicateRequest(t *testing.T, key ch.ChannelKey, channelID string, commandByte byte, payload []byte) ReplicateRequest {
	t.Helper()
	records := []ch.Record{{ID: uint64(commandByte), Epoch: 3, FromUID: "sender", ClientMsgNo: "msg", ServerTimestampMS: 1, Payload: append([]byte(nil), payload...), SizeBytes: len(payload)}}
	manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{commandByte}, BaseOffset: 0, LastOffset: 1,
	}, records)
	if !ok {
		t.Fatal("SealProposalManifest() failed")
	}
	return ReplicateRequest{
		ChannelKey: key, ChannelID: ch.ChannelID{ID: channelID, Type: 1},
		Leader: 1, Follower: 2, Manifest: manifest, Records: records,
	}
}

type manualPeerExecutor struct {
	tasks     []func()
	submitted chan struct{}
}

func (e *manualPeerExecutor) Submit(task func()) error {
	e.tasks = append(e.tasks, task)
	if e.submitted != nil {
		e.submitted <- struct{}{}
	}
	return nil
}

func (e *manualPeerExecutor) Len() int { return len(e.tasks) }

func (e *manualPeerExecutor) RunNext() {
	task := e.tasks[0]
	e.tasks = e.tasks[1:]
	task()
}

type recordingPeerLink struct {
	batches []ExchangeBatch
}

type reorderingPeerLink struct {
	duplicate bool
}

type wrongProofPeerLink struct{}

type blockingPeerLink struct {
	started chan struct{}
	once    sync.Once
}

type panicPeerLink struct{}

type needFromPeerLink struct{}

type splitFollowerPeerLink struct{}

func (needFromPeerLink) Exchange(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	request := batch.Items[0].Replicate
	return ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{{
		RequestID: batch.Items[0].RequestID,
		Replicate: ReplicateResult{Status: ReplicateNeedFrom, NeedFrom: request.Manifest.BaseOffset + 1},
	}}}, nil
}

func (splitFollowerPeerLink) Exchange(_ context.Context, node ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	if node == 2 {
		return ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{durableExchangeResult(batch.Items[0])}}, nil
	}
	return needFromPeerLink{}.Exchange(context.Background(), node, batch)
}

func (panicPeerLink) Exchange(context.Context, ch.NodeID, ExchangeBatch) (ExchangeBatchResult, error) {
	panic("test peer panic")
}

func (l *blockingPeerLink) Exchange(ctx context.Context, _ ch.NodeID, _ ExchangeBatch) (ExchangeBatchResult, error) {
	l.once.Do(func() { close(l.started) })
	<-ctx.Done()
	return ExchangeBatchResult{}, ctx.Err()
}

func (*wrongProofPeerLink) Exchange(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	request := *batch.Items[0].Replicate
	proof := replicateProofFor(request)
	proof.Manifest.Digest[0] ^= 0xff
	return ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{{
		RequestID: batch.Items[0].RequestID,
		Replicate: ReplicateResult{Status: ReplicateDurable, LastOffset: request.Manifest.LastOffset, Proof: proof},
	}}}, nil
}

func (l *reorderingPeerLink) Exchange(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	first := batch.Items[1]
	if l.duplicate {
		first.RequestID = batch.Items[0].RequestID
	}
	return ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{
		durableExchangeResult(first),
		durableExchangeResult(batch.Items[0]),
	}}, nil
}

func (l *recordingPeerLink) Exchange(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	l.batches = append(l.batches, batch)
	results := make([]ExchangeItemResult, len(batch.Items))
	for index, item := range batch.Items {
		results[index] = durableExchangeResult(item)
	}
	return ExchangeBatchResult{Version: ExchangeVersion, Items: results}, nil
}

func durableExchangeResult(item ExchangeItem) ExchangeItemResult {
	return ExchangeItemResult{RequestID: item.RequestID, Replicate: ReplicateResult{
		Status: ReplicateDurable, LastOffset: item.Replicate.Manifest.LastOffset, Proof: replicateProofFor(*item.Replicate),
	}}
}

func batchItemCounts(batches []ExchangeBatch) []int {
	counts := make([]int, len(batches))
	for index := range batches {
		counts[index] = len(batches[index].Items)
	}
	return counts
}

type immediateLocalDurability struct{}

func (immediateLocalDurability) submitLocal(_ context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	return nil
}

type definitelyNotLocalDurability struct{}

func (definitelyNotLocalDurability) submitLocal(_ context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: ch.ErrBackpressured})
	return nil
}

type contextRecordingLocalDurability struct {
	ctx context.Context
}

type recordingFollowerRepairSink struct {
	mu      sync.Mutex
	repairs []followerRepair
}

type discardFollowerRepairSink struct{}

func (discardFollowerRepairSink) RecordFollowerRepair(followerRepair) {}

func (s *recordingFollowerRepairSink) RecordFollowerRepair(repair followerRepair) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repairs = append(s.repairs, repair)
}

func (s *recordingFollowerRepairSink) snapshot() []followerRepair {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]followerRepair(nil), s.repairs...)
}

func (d *contextRecordingLocalDurability) submitLocal(ctx context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	d.ctx = ctx
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	return nil
}
