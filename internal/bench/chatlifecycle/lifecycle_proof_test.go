package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestLifecycleCandidateSelectionIsExactlyBalancedAcrossUnevenHashSlots(t *testing.T) {
	now := time.Unix(1_000, 0)
	table := mustInitialLifecycleSlotAssignment(t)
	candidates := make([]LifecycleCandidate, 0, 1_212)
	for slotID := uint32(1); slotID <= 12; slotID++ {
		added := 0
		for ordinal := 0; added < 101; ordinal++ {
			id := channelid.EncodePersonChannel(fmt.Sprintf("uid-%02d-%05d-a", slotID, ordinal), fmt.Sprintf("uid-%02d-%05d-b", slotID, ordinal))
			hash := hashslot.HashSlotForKey(id, 256)
			assigned, ok := table.Lookup(hash)
			if !ok || assigned != slotID {
				continue
			}
			candidates = append(candidates, LifecycleCandidate{
				ChannelID: id, ChannelType: 1, HashSlot: hash, SlotID: slotID,
				TimerToken: uint64(len(candidates) + 1), ActivityVersion: 1,
				InitialSequence: uint64(added + 1), QuietNotBefore: now.Add(6 * time.Minute),
				QuietDeadline: now.Add(9 * time.Minute), ReheatAt: now.Add(10 * time.Minute),
				ObservedLoaded: added != 100,
			})
			added++
		}
	}
	selected, err := SelectLifecycleCohort(candidates, now, table, 12)
	if err != nil {
		t.Fatalf("SelectLifecycleCohort: %v", err)
	}
	if len(selected) != 1_200 {
		t.Fatalf("selected = %d, want 1200", len(selected))
	}
	counts := [12]int{}
	for _, candidate := range selected {
		counts[candidate.SlotID-1]++
		if !candidate.ObservedLoaded {
			t.Fatalf("selected unobserved candidate for slot %d", candidate.SlotID)
		}
	}
	for index, count := range counts {
		if count != 100 {
			t.Fatalf("slot %d count = %d, want 100", index+1, count)
		}
	}
}

func TestLifecycleHashAndInitialAssignmentMatchServerContract(t *testing.T) {
	serverTable := hashslot.NewHashSlotTable(formalHashSlots, formalLogicalSlotGroups)
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	for hash := uint16(0); ; hash++ {
		slotID, ok := assignment.Lookup(hash)
		if !ok || slotID != uint32(serverTable.Lookup(hash)) {
			t.Fatalf("hash slot %d = (%d,%v), want %d", hash, slotID, ok, serverTable.Lookup(hash))
		}
		if hash == formalHashSlots-1 {
			break
		}
	}
	for _, identity := range []string{
		channelid.EncodePersonChannel("a", "b"),
		channelid.EncodePersonChannel("uid-000000", "uid-999999"),
		channelid.EncodePersonChannel("mixed-ASCII-123", "unicode-用户"),
		channelid.EncodePersonChannel("0", "255"),
	} {
		if got, want := lifecycleHashSlotForKey(identity, formalHashSlots), hashslot.HashSlotForKey(identity, formalHashSlots); got != want {
			t.Fatalf("hash(%q) = %d, want %d", identity, got, want)
		}
	}
	if lifecycleHashSlotForKey("anything", 0) != hashslot.HashSlotForKey("anything", 0) {
		t.Fatal("zero-count hash contract differs")
	}
}

func TestLifecycleSlotAssignmentStrictlyValidatesLiveMapping(t *testing.T) {
	valid := make([]uint32, formalHashSlots)
	for hash := range valid {
		valid[hash] = uint32(hash%formalLogicalSlotGroups + 1)
	}
	assignment, err := NewLifecycleSlotAssignment(valid)
	if err != nil {
		t.Fatal(err)
	}
	valid[0] = 12
	if slotID, ok := assignment.Lookup(0); !ok || slotID != 1 {
		t.Fatalf("constructor did not copy mapping: (%d,%v)", slotID, ok)
	}
	if _, ok := assignment.Lookup(formalHashSlots); ok {
		t.Fatal("out-of-range lookup succeeded")
	}

	for _, test := range []struct {
		name    string
		mapping []uint32
	}{
		{"short", append([]uint32(nil), valid[:formalHashSlots-1]...)},
		{"long", append(append([]uint32(nil), valid...), 1)},
		{"zero", func() []uint32 { out := append([]uint32(nil), valid...); out[0] = 0; return out }()},
		{"above twelve", func() []uint32 { out := append([]uint32(nil), valid...); out[0] = 13; return out }()},
		{"slot gap", func() []uint32 {
			out := append([]uint32(nil), valid...)
			for index := range out {
				if out[index] == 7 {
					out[index] = 6
				}
			}
			return out
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewLifecycleSlotAssignment(test.mapping); !errors.Is(err, ErrLifecycleHarnessInvalid) {
				t.Fatalf("error = %v, want harness invalid", err)
			}
		})
	}
}

func TestLifecycleCandidateSelectionUsesInjectedLiveAssignment(t *testing.T) {
	now := time.Unix(1_000, 0)
	initial := mustInitialLifecycleSlotAssignment(t)
	liveMapping := make([]uint32, formalHashSlots)
	for hash := uint16(0); hash < formalHashSlots; hash++ {
		slotID, ok := initial.Lookup(hash)
		if !ok {
			t.Fatalf("initial lookup %d failed", hash)
		}
		liveMapping[hash] = slotID%formalLogicalSlotGroups + 1
	}
	live, err := NewLifecycleSlotAssignment(liveMapping)
	if err != nil {
		t.Fatal(err)
	}
	candidates := lifecycleTestCandidates(t, now)
	for index := range candidates {
		candidates[index].SlotID = candidates[index].SlotID%formalLogicalSlotGroups + 1
	}
	if selected, err := SelectLifecycleCohort(candidates, now, live, formalLogicalSlotGroups); err != nil || len(selected) != lifecycleCohortSize {
		t.Fatalf("live selection = %d,%v", len(selected), err)
	}
	if _, err := SelectLifecycleCohort(candidates, now, initial, formalLogicalSlotGroups); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("stale assignment error = %v, want harness invalid", err)
	}
}

func TestLifecycleProofCycleIsExactlyEveryTenMinutesWithoutRetainedHistory(t *testing.T) {
	start := time.Unix(1_000, 0)
	for cycle := uint64(0); cycle < 4; cycle++ {
		got, err := LifecycleProofCycleTime(start, cycle)
		if err != nil || !got.Equal(start.Add(time.Duration(cycle+1)*10*time.Minute)) {
			t.Fatalf("cycle %d = %v,%v", cycle, got, err)
		}
	}
	if _, err := LifecycleProofCycleTime(start, ^uint64(0)); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestLifecycleCandidateSelectionRejectsDuplicateUndersupplyAndBadPhysicalAssignment(t *testing.T) {
	now := time.Unix(1_000, 0)
	valid := lifecycleTestCandidates(t, now)
	for _, test := range []struct {
		name string
		edit func([]LifecycleCandidate) []LifecycleCandidate
	}{
		{"duplicate", func(items []LifecycleCandidate) []LifecycleCandidate {
			items[1].ChannelID = items[0].ChannelID
			return items
		}},
		{"undersupply", func(items []LifecycleCandidate) []LifecycleCandidate { return items[:len(items)-1] }},
		{"bad physical assignment", func(items []LifecycleCandidate) []LifecycleCandidate {
			items[0].SlotID = items[0].SlotID%12 + 1
			return items
		}},
		{"quiet lower bound elapsed", func(items []LifecycleCandidate) []LifecycleCandidate {
			items[0].QuietNotBefore = now.Add(-time.Nanosecond)
			return items
		}},
		{"zero timer token", func(items []LifecycleCandidate) []LifecycleCandidate {
			items[0].TimerToken = 0
			return items
		}},
		{"zero activity version", func(items []LifecycleCandidate) []LifecycleCandidate {
			items[0].ActivityVersion = 0
			return items
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := append([]LifecycleCandidate(nil), valid...)
			if _, err := SelectLifecycleCohort(test.edit(items), now, mustInitialLifecycleSlotAssignment(t), 12); !errors.Is(err, ErrLifecycleHarnessInvalid) {
				t.Fatalf("error = %v, want harness invalid", err)
			}
		})
	}
}

func TestLifecycleProofLoadedAbsentReheatSequenceContinuity(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatalf("loaded: %v", err)
	}
	if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
		t.Fatalf("absent: %v", err)
	}
	if !proof.ColdEligible(candidate.ChannelID) {
		t.Fatal("candidate not cold eligible after all three nodes absent")
	}
	sender := &fakeLifecycleSender{}
	if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, sender); err != nil {
		t.Fatalf("reheat: %v", err)
	}
	if err := proof.Observe(candidate.ReheatAt.Add(2*time.Second), lifecycleRows(candidate, "active", 11, 11)); err != nil {
		t.Fatalf("reloaded: %v", err)
	}
	snapshot := proof.Snapshot()
	if snapshot.Completed != 1 || snapshot.ColdEligible != 1 || snapshot.ReheatLatency.Count != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	if bytes.Contains(encoded, []byte(candidate.ChannelID)) || bytes.Contains(encoded, []byte("channel_id")) ||
		bytes.Contains(encoded, []byte("timer_token")) || bytes.Contains(encoded, []byte("activity_version")) {
		t.Fatal("transient candidate lease leaked into lifecycle snapshot")
	}
}

func TestLifecycleCandidateEngineLeaseReconstructsCurrentTimerAndAdmitsRealScheduledReheat(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	edge := fixture.graph.Incoming(18).Items[0]
	now := fixture.clock.Now()
	installed := make(chan struct{}, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := &engineWork{due: now.Add(10 * time.Minute), eligibilityDeadline: now.Add(11 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true, NaturalCooling: true}, lifecycleTimerToken: 41,
			activityVersion: 1, initialSequence: 42, lastActivityAt: now, observedLoaded: true}
		fixture.engine.lifecycleByChannel[edge.PersonChannelID] = work
		installed <- struct{}{}
	}}); err != nil {
		t.Fatal(err)
	}
	<-installed
	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("lease = %+v, %v", candidates, err)
	}
	candidate := candidates[0]
	if candidate.ChannelID != edge.PersonChannelID || candidate.TimerToken != 41 || candidate.ActivityVersion != 1 || candidate.InitialSequence != 42 || !candidate.ObservedLoaded ||
		!candidate.QuietNotBefore.Equal(now.Add(5*time.Minute+time.Nanosecond)) || !candidate.QuietDeadline.Equal(now.Add(10*time.Minute-time.Nanosecond)) || !candidate.ReheatAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("candidate = %+v", candidate)
	}
	fixture.clock.Set(now.Add(time.Minute))
	intent := fixture.intent(t, edge.OwnerUID, edge.PeerUID, 0, TrafficPerson)
	intent.ChannelID = edge.PersonChannelID
	if err := fixture.verifier.RegisterSend(intent.Logical, now); err != nil {
		t.Fatal(err)
	}
	if err := fixture.verifier.ObserveAttempt(intent.Logical, RetryAttempt{ClientMsgNo: intent.Logical.ClientMsgNo}, 1); err != nil {
		t.Fatal(err)
	}
	installedAck := make(chan struct{}, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		inflight := &engineInflight{intent: intent, currentClientSeq: 1}
		inflight.registerClientSeq(1)
		fixture.engine.inflight[intent.Logical.ClientMsgNo] = inflight
		installedAck <- struct{}{}
	}}); err != nil {
		t.Fatal(err)
	}
	<-installedAck
	ack := &frame.SendackPacket{ClientSeq: 1, ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 101, MessageSeq: 43, ReasonCode: frame.ReasonSuccess}
	verificationErr := fixture.verifier.HandleSendack(ack)
	if err := fixture.engine.ObserveSendack(edge.OwnerUID, ack, verificationErr); err != nil {
		t.Fatal(err)
	}
	if stale, staleErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion); staleErr != nil || stale {
		t.Fatalf("stale activity lease approval = %v,%v", stale, staleErr)
	}
	refreshed, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t))
	if err != nil || len(refreshed) != 1 || refreshed[0].TimerToken != candidate.TimerToken || refreshed[0].ActivityVersion != 2 || refreshed[0].InitialSequence != 43 || !refreshed[0].QuietNotBefore.Equal(now.Add(6*time.Minute+time.Nanosecond)) {
		t.Fatalf("refreshed lease = %+v,%v", refreshed, err)
	}
	candidate = refreshed[0]
	approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion)
	if err != nil || !approved {
		t.Fatalf("approve = %v, %v", approved, err)
	}
	confirmed := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() { confirmed <- fixture.engine.lifecycleByChannel[candidate.ChannelID].coldConfirmed }}); err != nil {
		t.Fatal(err)
	}
	if !<-confirmed {
		t.Fatal("real scheduled revisit was not admitted")
	}
	if replay, replayErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion); replayErr != nil || !replay {
		t.Fatalf("idempotent replay = %v, %v", replay, replayErr)
	}
	lateIntent := fixture.intent(t, edge.OwnerUID, edge.PeerUID, 1, TrafficPerson)
	lateIntent.ChannelID = edge.PersonChannelID
	if err := fixture.verifier.RegisterSend(lateIntent.Logical, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.verifier.ObserveAttempt(lateIntent.Logical, RetryAttempt{ClientMsgNo: lateIntent.Logical.ClientMsgNo}, 2); err != nil {
		t.Fatal(err)
	}
	lateInstalled := make(chan struct{}, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		inflight := &engineInflight{intent: lateIntent, currentClientSeq: 2}
		inflight.registerClientSeq(2)
		fixture.engine.inflight[lateIntent.Logical.ClientMsgNo] = inflight
		lateInstalled <- struct{}{}
	}}); err != nil {
		t.Fatal(err)
	}
	<-lateInstalled
	lateAck := &frame.SendackPacket{ClientSeq: 2, ClientMsgNo: lateIntent.Logical.ClientMsgNo, MessageID: 102, MessageSeq: 44, ReasonCode: frame.ReasonSuccess}
	lateVerificationErr := fixture.verifier.HandleSendack(lateAck)
	lateErr := fixture.engine.ObserveSendack(edge.OwnerUID, lateAck, lateVerificationErr)
	var runtimeErr *RuntimeError
	if !errors.As(lateErr, &runtimeErr) || runtimeErr.Code() != RuntimeFailureLifecycleLeaseInvalidated {
		t.Fatalf("approved lease invalidation error = %v", lateErr)
	}
	if evidence := fixture.evidence.Snapshot(); evidence.Classification != SyncClassificationHarnessInvalid || !workerEvidenceHasCode(evidence, FailureCodeLifecycleLeaseInvalidated) {
		t.Fatalf("approved lease invalidation evidence = %+v", evidence)
	}
	invalidated := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := fixture.engine.lifecycleByChannel[candidate.ChannelID]
		invalidated <- work != nil && work.lifecycleLeaseInvalidated && !work.coldConfirmed
	}}); err != nil {
		t.Fatal(err)
	}
	if !<-invalidated {
		t.Fatal("approved timer activity did not retain harness-invalidated state")
	}
	if missing, missingErr := fixture.engine.ApproveColdRevisitContext(context.Background(), channelid.EncodePersonChannel("missing-a", "missing-b"), candidate.TimerToken, candidate.ActivityVersion); missingErr != nil || missing {
		t.Fatalf("missing approval = %v, %v", missing, missingErr)
	}
	replacementChecked := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		replacement := &engineWork{due: now.Add(20 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 42,
			activityVersion: 1, initialSequence: 50, lastActivityAt: now.Add(2 * time.Minute), observedLoaded: true}
		fixture.engine.lifecycleByChannel[candidate.ChannelID] = replacement
		replacementChecked <- replacement.coldConfirmed
	}}); err != nil {
		t.Fatal(err)
	}
	if <-replacementChecked {
		t.Fatal("replacement unexpectedly confirmed")
	}
	if stale, staleErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion); staleErr != nil || stale {
		t.Fatalf("ABA stale approval = %v,%v", stale, staleErr)
	}
	if tampered, tamperedErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, 42, 2); tamperedErr != nil || tampered {
		t.Fatalf("tampered approval = %v,%v", tampered, tamperedErr)
	}
	if zero, zeroErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, 0, 1); !errors.Is(zeroErr, errEngineConfig) || zero {
		t.Fatalf("zero token approval = %v,%v", zero, zeroErr)
	}
	if exact, exactErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, 42, 1); exactErr != nil || !exact {
		t.Fatalf("replacement exact approval = %v,%v", exact, exactErr)
	}
}

func TestEngineLifecycleTimerTokenMonotonicOverflowIsHarnessInvalid(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	type allocation struct {
		first, second, overflow uint64
		err                     error
	}
	result := make(chan allocation, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		first, firstErr := fixture.engine.allocateLifecycleTimerToken()
		second, secondErr := fixture.engine.allocateLifecycleTimerToken()
		fixture.engine.nextLifecycleTimerToken = math.MaxUint64
		overflow, overflowErr := fixture.engine.allocateLifecycleTimerToken()
		result <- allocation{first: first, second: second, overflow: overflow, err: errors.Join(firstErr, secondErr, overflowErr)}
	}}); err != nil {
		t.Fatal(err)
	}
	got := <-result
	var runtimeErr *RuntimeError
	if got.first != 1 || got.second != 2 || got.overflow != 0 || !errors.As(got.err, &runtimeErr) || runtimeErr.Code() != RuntimeFailureLifecycleFenceExhausted {
		t.Fatalf("allocation = %+v, runtime=%v", got, runtimeErr)
	}
}

func TestEngineLifecycleActivityVersionOverflowIsHarnessInvalid(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	edge := fixture.graph.Incoming(18).Items[0]
	now := fixture.clock.Now()
	intent := fixture.intent(t, edge.OwnerUID, edge.PeerUID, 0, TrafficPerson)
	intent.ChannelID = edge.PersonChannelID
	if err := fixture.verifier.RegisterSend(intent.Logical, now); err != nil {
		t.Fatal(err)
	}
	if err := fixture.verifier.ObserveAttempt(intent.Logical, RetryAttempt{ClientMsgNo: intent.Logical.ClientMsgNo}, 1); err != nil {
		t.Fatal(err)
	}
	installed := make(chan struct{}, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := &engineWork{due: now.Add(10 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 1,
			activityVersion: math.MaxUint64, initialSequence: 42, lastActivityAt: now, observedLoaded: true}
		fixture.engine.lifecycleByChannel[edge.PersonChannelID] = work
		inflight := &engineInflight{intent: intent, currentClientSeq: 1}
		inflight.registerClientSeq(1)
		fixture.engine.inflight[intent.Logical.ClientMsgNo] = inflight
		installed <- struct{}{}
	}}); err != nil {
		t.Fatal(err)
	}
	<-installed
	ack := &frame.SendackPacket{ClientSeq: 1, ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 201, MessageSeq: 43, ReasonCode: frame.ReasonSuccess}
	verificationErr := fixture.verifier.HandleSendack(ack)
	err := fixture.engine.ObserveSendack(edge.OwnerUID, ack, verificationErr)
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code() != RuntimeFailureLifecycleFenceExhausted {
		t.Fatalf("activity overflow error = %v", err)
	}
	if evidence := fixture.evidence.Snapshot(); evidence.Classification != SyncClassificationHarnessInvalid || !workerEvidenceHasCode(evidence, FailureCodeLifecycleFenceExhausted) {
		t.Fatalf("activity overflow evidence = %+v", evidence)
	}
	unchanged := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := fixture.engine.lifecycleByChannel[edge.PersonChannelID]
		unchanged <- work != nil && work.activityVersion == math.MaxUint64 && work.initialSequence == 42 && work.lastActivityAt.Equal(now)
	}}); err != nil {
		t.Fatal(err)
	}
	if !<-unchanged {
		t.Fatal("activity overflow mutated the fenced quiet window")
	}
}

func TestLifecycleProofWorkerSenderUsesFencedApprovalWithoutForgingSequence(t *testing.T) {
	fence := WorkerFence{RunID: "run", AssignmentID: "assignment", Generation: 1}
	control := &fakeLifecycleReheatControl{response: WorkerLifecycleReheatResponse{WorkerFence: fence, WorkerID: 0, WorkerCount: 3, Approved: true}}
	sender, err := NewWorkerLifecycleReheatSender(control, fence)
	if err != nil {
		t.Fatal(err)
	}
	candidate := lifecycleTestCandidates(t, time.Unix(1_000, 0))[0]
	if err := sender.ApproveLifecycleReheat(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if control.request.ChannelID != candidate.ChannelID || control.request.TimerToken != candidate.TimerToken || control.request.ActivityVersion != candidate.ActivityVersion || control.request.WorkerFence != fence {
		t.Fatalf("request = %+v", control.request)
	}
	invalid := candidate
	invalid.TimerToken = 0
	if err := sender.ApproveLifecycleReheat(context.Background(), invalid); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("zero-token approval error = %v", err)
	}
	invalid = candidate
	invalid.ActivityVersion = 0
	if err := sender.ApproveLifecycleReheat(context.Background(), invalid); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("zero-version approval error = %v", err)
	}
}

func TestLifecycleProofRejectsProductTransitionFailures(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	tests := []struct {
		name string
		rows []model.ChannelRuntimeProbeResult
	}{
		{"closing", lifecycleRows(candidate, "closing", 10, 10)},
		{"error", lifecycleRows(candidate, "error", 10, 10)},
		{"two leaders", lifecycleRowsWithRoles(candidate, [3]string{"leader", "leader", "follower"}, 10, 10)},
		{"non monotonic watermark", lifecycleRowsWithOffsets(candidate, [3][2]uint64{{10, 9}, {8, 8}, {10, 10}})},
		{"partial missing", lifecycleRowsWithRoles(candidate, [3]string{"missing", "leader", "follower"}, 10, 10)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
			if err := proof.Observe(now, test.rows); !errors.Is(err, ErrLifecycleProductFailure) {
				t.Fatalf("error = %v, want product failure", err)
			}
		})
	}
}

func TestLifecycleProofAllowsBoundedPartialCoolingButNotReappearance(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	partial := lifecycleRowsWithRoles(candidate, [3]string{"missing", "follower", "follower"}, 10, 10)

	proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatal(err)
	}
	if err := proof.Observe(candidate.QuietNotBefore, partial); err != nil {
		t.Fatalf("partial cooling: %v", err)
	}
	if proof.ColdEligible(candidate.ChannelID) || proof.Snapshot().ColdEligible != 0 {
		t.Fatal("partial cooling became cold eligible")
	}
	if err := proof.Observe(candidate.QuietNotBefore.Add(time.Second), lifecycleRows(candidate, "missing", 0, 0)); err != nil {
		t.Fatalf("all missing: %v", err)
	}
	if !proof.ColdEligible(candidate.ChannelID) {
		t.Fatal("all-node absence did not become cold eligible")
	}

	deadline, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	_ = deadline.Observe(now, lifecycleRows(candidate, "active", 10, 10))
	if err := deadline.Observe(candidate.QuietDeadline, partial); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("deadline partial error = %v, want product failure", err)
	}

	reappeared, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	_ = reappeared.Observe(now, lifecycleRows(candidate, "active", 10, 10))
	_ = reappeared.Observe(candidate.QuietNotBefore, partial)
	if err := reappeared.Observe(candidate.QuietNotBefore.Add(time.Second), lifecycleRows(candidate, "active", 11, 11)); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("reappearance error = %v, want product failure", err)
	}
}

func TestLifecycleProofRejectsInvalidBatchAtomically(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidates := lifecycleTestCandidates(t, now)[:2]
	proof, _ := NewLifecycleProof(candidates)
	results := make([]model.ChannelRuntimeProbeResult, 3)
	for node := range results {
		results[node] = model.ChannelRuntimeProbeResult{NodeID: uint64(node + 1), Checked: 2, Channels: []model.ChannelRuntimeProbeChannel{
			{ChannelID: candidates[0].ChannelID, ChannelType: 1, Role: map[bool]string{true: "leader", false: "follower"}[node == 0], Status: "active", LEO: 10, HW: 10, CheckpointHW: 10},
			{ChannelID: candidates[1].ChannelID, ChannelType: 1, Role: "follower", Status: "active", LEO: 10, HW: 10, CheckpointHW: 10},
		}}
	}
	if err := proof.Observe(now, results); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("error = %v", err)
	}
	if snapshot := proof.Snapshot(); snapshot.Loaded != 0 || snapshot.ProductFailures != 1 {
		t.Fatalf("non-atomic snapshot = %+v", snapshot)
	}
}

func TestLifecycleProofRejectsStuckPartialReheatAndSequenceReset(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	for _, test := range []struct {
		name string
		run  func(*LifecycleProof) error
	}{
		{"stuck loaded", func(p *LifecycleProof) error {
			_ = p.Observe(now, lifecycleRows(candidate, "active", 10, 10))
			return p.Observe(candidate.QuietDeadline, lifecycleRows(candidate, "active", 10, 10))
		}},
		{"unproven reheat", func(p *LifecycleProof) error {
			return p.Reheat(context.Background(), now, candidate.ChannelID, &fakeLifecycleSender{})
		}},
		{"sequence reset", func(p *LifecycleProof) error {
			_ = p.Observe(now, lifecycleRows(candidate, "active", 10, 10))
			_ = p.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
			_ = p.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{})
			return p.Observe(candidate.ReheatAt, lifecycleRows(candidate, "active", 10, 10))
		}},
		{"partial reheat", func(p *LifecycleProof) error {
			_ = p.Observe(now, lifecycleRows(candidate, "active", 10, 10))
			_ = p.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
			_ = p.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{})
			return p.Observe(candidate.ReheatAt, lifecycleRowsWithRoles(candidate, [3]string{"leader", "follower", "missing"}, 11, 11))
		}},
		{"absence after quiet deadline", func(p *LifecycleProof) error {
			_ = p.Observe(now, lifecycleRows(candidate, "active", 10, 10))
			return p.Observe(candidate.QuietDeadline.Add(time.Nanosecond), lifecycleRows(candidate, "missing", 0, 0))
		}},
		{"reload after reheat deadline", func(p *LifecycleProof) error {
			_ = p.Observe(now, lifecycleRows(candidate, "active", 10, 10))
			_ = p.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
			_ = p.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{})
			return p.Observe(candidate.ReheatAt.Add(lifecycleReheatDeadline+time.Nanosecond), lifecycleRows(candidate, "active", 11, 11))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
			if err := test.run(proof); !errors.Is(err, ErrLifecycleProductFailure) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLifecycleProofRejectsCheckpointAndPostReheatWatermarkRegression(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]

	t.Run("checkpoint", func(t *testing.T) {
		proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
		if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
			t.Fatal(err)
		}
		rows := lifecycleRows(candidate, "active", 11, 11)
		rows[1].Channels[0].CheckpointHW = 9
		if err := proof.Observe(now.Add(time.Second), rows); !errors.Is(err, ErrLifecycleProductFailure) {
			t.Fatalf("error = %v, want product failure", err)
		}
	})

	t.Run("post reheat", func(t *testing.T) {
		proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
		_ = proof.Observe(now, lifecycleRows(candidate, "active", 10, 10))
		_ = proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
		_ = proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{})
		if err := proof.Observe(candidate.ReheatAt, lifecycleRows(candidate, "active", 20, 20)); err != nil {
			t.Fatal(err)
		}
		if err := proof.Observe(candidate.ReheatAt.Add(time.Second), lifecycleRows(candidate, "active", 15, 15)); !errors.Is(err, ErrLifecycleProductFailure) {
			t.Fatalf("error = %v, want product failure", err)
		}
	})
}

func TestLifecycleProofRejectsNilContexts(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	_ = proof.Observe(now, lifecycleRows(candidate, "active", 10, 10))
	_ = proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
	if err := proof.Reheat(nil, candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("reheat error = %v, want harness invalid", err)
	}
	if _, err := proof.Poll(nil, &fakeLifecycleProber{nodes: 3}, candidate.QuietNotBefore, LifecycleProbeOptions{BatchSize: 1, MaxConcurrency: 1}); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("probe error = %v, want harness invalid", err)
	}
}

func TestLifecycleProofReheatAdmissionWindow(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	ready := func(t *testing.T) *LifecycleProof {
		t.Helper()
		proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
		_ = proof.Observe(now, lifecycleRows(candidate, "active", 10, 10))
		_ = proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
		return proof
	}
	if err := ready(t).Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
		t.Fatalf("early approval after cold proof: %v", err)
	}
	if err := ready(t).Reheat(context.Background(), candidate.ReheatAt.Add(-time.Nanosecond), candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
		t.Fatalf("latest pre-due approval: %v", err)
	}
	for _, observedAt := range []time.Time{candidate.ReheatAt, candidate.ReheatAt.Add(time.Nanosecond)} {
		if err := ready(t).Reheat(context.Background(), observedAt, candidate.ChannelID, &fakeLifecycleSender{}); !errors.Is(err, ErrLifecycleHarnessInvalid) {
			t.Fatalf("late approval at %v error = %v, want harness invalid", observedAt, err)
		}
	}
	if err := ready(t).Observe(candidate.ReheatAt, lifecycleRows(candidate, "missing", 0, 0)); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("missing approval deadline error = %v, want harness invalid", err)
	}
}

func TestLifecycleProofAsyncProbeBatchesBoundsConcurrencyAndCancellation(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidates := lifecycleTestCandidates(t, now)[:5]
	prober := &fakeLifecycleProber{nodes: 3, block: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		proof, _ := NewLifecycleProof(candidates)
		_, err := proof.Poll(ctx, prober, now, LifecycleProbeOptions{BatchSize: 2, MaxConcurrency: 2})
		done <- err
	}()
	prober.awaitCalls(t, 2)
	if prober.peak != 2 {
		t.Fatalf("peak = %d, want 2", prober.peak)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	for _, size := range prober.sizes {
		if size > 2 {
			t.Fatalf("batch = %d", size)
		}
	}
}

func TestLifecycleProofProbeRequiresAllThreeNodesAndSeparatesTransportEvidence(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidates := lifecycleTestCandidates(t, now)[:1]
	prober := &fakeLifecycleProber{nodes: 2}
	proof, _ := NewLifecycleProof(candidates)
	result, err := proof.Poll(context.Background(), prober, now, LifecycleProbeOptions{BatchSize: 1200, MaxConcurrency: 1})
	if !errors.Is(err, ErrLifecycleHarnessInvalid) || result.TransportErrors != 0 || result.Latency.Count != 1 {
		t.Fatalf("result/error = %+v / %v", result, err)
	}
	prober = &fakeLifecycleProber{err: errors.New("private transport detail")}
	result, err = proof.Poll(context.Background(), prober, now, LifecycleProbeOptions{BatchSize: 1200, MaxConcurrency: 1})
	if !errors.Is(err, ErrLifecycleHarnessInvalid) || result.TransportErrors != 1 || result.Latency.Count != 1 || containsRawLifecycleIdentity(result) {
		t.Fatalf("transport result/error = %+v / %v", result, err)
	}
	prober = &fakeLifecycleProber{block: make(chan struct{})}
	result, err = proof.Poll(context.Background(), prober, now, LifecycleProbeOptions{BatchSize: 1200, MaxConcurrency: 1, RequestTimeout: time.Nanosecond})
	if !errors.Is(err, ErrLifecycleHarnessInvalid) || result.TransportErrors != 1 {
		t.Fatalf("deadline result/error = %+v/%v", result, err)
	}
}

func TestLifecycleProofPollMergesBatchesAndAdvancesAtomically(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidates := lifecycleTestCandidates(t, now)[:5]
	proof, _ := NewLifecycleProof(candidates)
	prober := &fakeLifecycleProber{nodes: 3, sequence: 10}
	options := LifecycleProbeOptions{BatchSize: 2, MaxConcurrency: 2}
	if result, err := proof.Poll(context.Background(), prober, now, options); err != nil || result.Requests != 3 || proof.Snapshot().Loaded != 5 {
		t.Fatalf("loaded poll = %+v,%v snapshot=%+v", result, err, proof.Snapshot())
	}
	prober.status = "missing"
	if _, err := proof.Poll(context.Background(), prober, candidates[0].QuietNotBefore, options); err != nil || proof.Snapshot().ColdEligible != 5 {
		t.Fatalf("absent poll = %v snapshot=%+v", err, proof.Snapshot())
	}
	for _, candidate := range candidates {
		if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
			t.Fatal(err)
		}
	}
	prober.status, prober.sequence = "active", 11
	if _, err := proof.Poll(context.Background(), prober, candidates[0].ReheatAt.Add(time.Second), options); err != nil || proof.Snapshot().Completed != 5 {
		t.Fatalf("reheat poll = %v snapshot=%+v", err, proof.Snapshot())
	}

	failed, _ := NewLifecycleProof(candidates)
	badBatch := &fakeLifecycleProber{nodes: 3, sequence: 10, failCall: 2}
	if _, err := failed.Poll(context.Background(), badBatch, now, LifecycleProbeOptions{BatchSize: 2, MaxConcurrency: 1}); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("batch error = %v", err)
	}
	if snapshot := failed.Snapshot(); snapshot.Loaded != 0 || snapshot.ProductFailures != 0 || snapshot.HarnessFailures != 0 {
		t.Fatalf("partial proof mutation = %+v", snapshot)
	}
}

func TestMetaCreateAccountingInitialExpectedAndReheatZeroDelta(t *testing.T) {
	accounting := NewMetaCreateAccounting()
	if err := lifecycleMetaCheckpoint(accounting, 1_000_000, 2_000, lifecycleMetaMetrics(1_002_000, 3, 0), false); err != nil {
		t.Fatalf("initial: %v", err)
	}
	if err := lifecycleMetaCheckpoint(accounting, 1_000_000, 2_000, lifecycleMetaMetrics(1_002_000, 9, 0), true); err != nil {
		t.Fatalf("reheat: %v", err)
	}
	if snapshot := accounting.Snapshot(); snapshot.ExpectedUnique != 1_002_000 || snapshot.Created != 1_002_000 || snapshot.ReheatCreated != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestMetaCreateAccountingRejectsWrongLogicalSlotDistributionWithMatchingTotal(t *testing.T) {
	assignment := mustInitialLifecycleSlotAssignment(t)
	var personEdges, preparedGroups MetaCreateHashSlotCounts
	personEdges[0] = 5
	preparedGroups[22] = 1
	var created [formalLogicalSlotGroups]uint64
	created[1] = 6
	metrics := lifecycleMetaMetricsBySlot(created, [formalLogicalSlotGroups]uint64{}, [formalLogicalSlotGroups]uint64{})
	if err := NewMetaCreateAccounting().Checkpoint(personEdges, preparedGroups, assignment, metrics, false); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("wrong-slot accounting error = %v, want product failure", err)
	}
}

func TestMetaCreateAccountingRejectsAndCountsReheatSlotRedistribution(t *testing.T) {
	assignment := mustInitialLifecycleSlotAssignment(t)
	var initialPerson, nextPerson, preparedGroups MetaCreateHashSlotCounts
	initialPerson[0], nextPerson[0], preparedGroups[22] = 5, 6, 1
	var initialCreated, redistributedCreated [formalLogicalSlotGroups]uint64
	initialCreated[0], initialCreated[1] = 5, 1
	redistributedCreated[0], redistributedCreated[1] = 5, 2
	accounting := NewMetaCreateAccounting()
	if err := accounting.Checkpoint(
		initialPerson, preparedGroups, assignment,
		lifecycleMetaMetricsBySlot(initialCreated, [formalLogicalSlotGroups]uint64{}, [formalLogicalSlotGroups]uint64{}), false,
	); err != nil {
		t.Fatal(err)
	}
	err := accounting.Checkpoint(
		nextPerson, preparedGroups, assignment,
		lifecycleMetaMetricsBySlot(redistributedCreated, [formalLogicalSlotGroups]uint64{}, [formalLogicalSlotGroups]uint64{}), true,
	)
	if !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("redistributed reheat error = %v, want product failure", err)
	}
	if snapshot := accounting.Snapshot(); snapshot.ReheatCreated != 1 {
		t.Fatalf("redistributed reheat snapshot = %+v, want one excess create", snapshot)
	}
}

func TestMetaCreateAccountingReheatAllowsOnlyExpectedConcurrentGrowth(t *testing.T) {
	accounting := NewMetaCreateAccounting()
	if err := lifecycleMetaCheckpoint(accounting, 10, 2, lifecycleMetaMetrics(12, 3, 0), false); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleMetaCheckpoint(accounting, 13, 2, lifecycleMetaMetrics(15, 9, 0), true); err != nil {
		t.Fatalf("expected concurrent growth: %v", err)
	}
	if snapshot := accounting.Snapshot(); snapshot.ExpectedUnique != 15 || snapshot.Created != 15 || snapshot.AlreadyExisting != 9 || snapshot.ReheatCreated != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	excess := NewMetaCreateAccounting()
	_ = lifecycleMetaCheckpoint(excess, 10, 2, lifecycleMetaMetrics(12, 0, 0), false)
	if err := lifecycleMetaCheckpoint(excess, 13, 2, lifecycleMetaMetrics(16, 0, 0), true); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("excess error = %v, want product failure", err)
	}
	if snapshot := excess.Snapshot(); snapshot.ReheatCreated != 1 {
		t.Fatalf("excess snapshot = %+v", snapshot)
	}
}

func TestMetaCreateAccountingRejectsCreatedOnReheatErrorsRegressionAndOverflow(t *testing.T) {
	base := lifecycleMetaMetrics(12, 0, 0)
	for _, test := range []struct {
		name string
		run  func(*MetaCreateAccounting) error
		want error
	}{
		{"created on reheat", func(a *MetaCreateAccounting) error {
			_ = lifecycleMetaCheckpoint(a, 10, 2, base, false)
			return lifecycleMetaCheckpoint(a, 10, 2, lifecycleMetaMetrics(13, 0, 0), true)
		}, ErrLifecycleProductFailure},
		{"error result", func(a *MetaCreateAccounting) error {
			return lifecycleMetaCheckpoint(a, 10, 2, lifecycleMetaMetrics(12, 0, 1), false)
		}, ErrLifecycleProductFailure},
		{"undercreated", func(a *MetaCreateAccounting) error {
			return lifecycleMetaCheckpoint(a, 10, 2, lifecycleMetaMetrics(11, 0, 0), false)
		}, ErrLifecycleProductFailure},
		{"counter regression", func(a *MetaCreateAccounting) error {
			_ = lifecycleMetaCheckpoint(a, 10, 2, base, false)
			return lifecycleMetaCheckpoint(a, 9, 2, lifecycleMetaMetrics(11, 0, 0), false)
		}, ErrLifecycleHarnessInvalid},
		{"expected overflow", func(a *MetaCreateAccounting) error {
			return lifecycleMetaCheckpoint(a, ^uint64(0), 1, base, false)
		}, ErrLifecycleHarnessInvalid},
		{"fractional", func(a *MetaCreateAccounting) error {
			metrics := lifecycleMetaMetrics(12, 0, 0)
			metrics[0].MetaCreatedTotal["created"] = 12.5
			return lifecycleMetaCheckpoint(a, 10, 2, metrics, false)
		}, ErrLifecycleHarnessInvalid},
		{"missing result series", func(a *MetaCreateAccounting) error {
			metrics := lifecycleMetaMetrics(12, 0, 0)
			delete(metrics[1].MetaCreatedTotal, "already_existing")
			return lifecycleMetaCheckpoint(a, 10, 2, metrics, false)
		}, ErrLifecycleHarnessInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(NewMetaCreateAccounting()); !errors.Is(err, test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func lifecycleMetaMetrics(created, already, errorCount float64) [3]target.MetricsSnapshot {
	var createdBySlot, alreadyBySlot, errorsBySlot [formalLogicalSlotGroups]uint64
	createdBySlot[0], _ = exactMetricCounter(created)
	alreadyBySlot[0], _ = exactMetricCounter(already)
	errorsBySlot[0], _ = exactMetricCounter(errorCount)
	metrics := lifecycleMetaMetricsBySlot(createdBySlot, alreadyBySlot, errorsBySlot)
	metrics[0].MetaCreatedTotal = map[string]float64{"created": created, "already_existing": already, "error": errorCount}
	return metrics
}

func lifecycleMetaMetricsBySlot(
	created, already, errorsCount [formalLogicalSlotGroups]uint64,
) [3]target.MetricsSnapshot {
	metrics := [3]target.MetricsSnapshot{}
	for node := range metrics {
		metrics[node].MetaCreatedTotal = map[string]float64{"created": 0, "already_existing": 0, "error": 0}
	}
	for slot := range formalLogicalSlotGroups {
		metrics[0].MetaCreatedBySlot[slot] = target.MetaCreateSlotCounters{
			Created: created[slot], AlreadyExisting: already[slot], Errors: errorsCount[slot],
		}
		metrics[0].MetaCreatedTotal["created"] += float64(created[slot])
		metrics[0].MetaCreatedTotal["already_existing"] += float64(already[slot])
		metrics[0].MetaCreatedTotal["error"] += float64(errorsCount[slot])
	}
	return metrics
}

func lifecycleMetaCheckpoint(
	accounting *MetaCreateAccounting,
	personEdges, preparedGroups uint64,
	metrics [3]target.MetricsSnapshot,
	reheat bool,
) error {
	var personByHash, groupsByHash MetaCreateHashSlotCounts
	personByHash[0], groupsByHash[0] = personEdges, preparedGroups
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		return err
	}
	return accounting.Checkpoint(personByHash, groupsByHash, assignment, metrics, reheat)
}

type fakeLifecycleSender struct{ err error }

func (s *fakeLifecycleSender) ApproveLifecycleReheat(context.Context, LifecycleCandidate) error {
	return s.err
}

type fakeLifecycleReheatControl struct {
	request  WorkerLifecycleReheatRequest
	response WorkerLifecycleReheatResponse
	err      error
}

func (c *fakeLifecycleReheatControl) ApproveLifecycleReheat(_ context.Context, request WorkerLifecycleReheatRequest) (WorkerLifecycleReheatResponse, error) {
	c.request = request
	return c.response, c.err
}

type fakeLifecycleProber struct {
	mu                         sync.Mutex
	calls, active, peak, nodes int
	sizes                      []int
	block                      chan struct{}
	err                        error
	status                     string
	sequence                   uint64
	failCall                   int
}

func (p *fakeLifecycleProber) ProbeChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.active++
	if p.active > p.peak {
		p.peak = p.active
	}
	p.sizes = append(p.sizes, len(req.Channels))
	status, sequence, configuredErr, failCall := p.status, p.sequence, p.err, p.failCall
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if configuredErr != nil {
		return nil, configuredErr
	}
	if failCall > 0 && call == failCall {
		return nil, errors.New("private batch transport detail")
	}
	if status == "" {
		status = "active"
	}
	if sequence == 0 && status != "missing" {
		sequence = 10
	}
	rows := make([]model.ChannelRuntimeProbeResult, p.nodes)
	for node := range rows {
		channels := make([]model.ChannelRuntimeProbeChannel, len(req.Channels))
		for index, identity := range req.Channels {
			role := "follower"
			if node == 0 {
				role = "leader"
			}
			rowStatus, leo, hw, checkpoint := status, sequence, sequence, sequence
			if status == "missing" {
				role, rowStatus, leo, hw, checkpoint = "missing", "missing", 0, 0, 0
			}
			channels[index] = model.ChannelRuntimeProbeChannel{ChannelID: identity.ChannelID, ChannelType: identity.ChannelType, Role: role, Status: rowStatus, LEO: leo, HW: hw, CheckpointHW: checkpoint}
		}
		rows[node] = model.ChannelRuntimeProbeResult{NodeID: uint64(node + 1), Checked: len(req.Channels), Channels: channels}
	}
	return rows, nil
}
func (p *fakeLifecycleProber) awaitCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		p.mu.Lock()
		got := p.calls
		p.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("calls=%d", got)
		default:
		}
	}
}

func lifecycleTestCandidates(t *testing.T, now time.Time) []LifecycleCandidate {
	t.Helper()
	table := mustInitialLifecycleSlotAssignment(t)
	out := make([]LifecycleCandidate, 0, 1200)
	for slotID := uint32(1); slotID <= 12; slotID++ {
		added := 0
		for ordinal := 0; added < 100; ordinal++ {
			id := channelid.EncodePersonChannel(fmt.Sprintf("slot-%02d-%04d-a", slotID, ordinal), fmt.Sprintf("slot-%02d-%04d-b", slotID, ordinal))
			hash := lifecycleHashSlotForKey(id, 256)
			assigned, ok := table.Lookup(hash)
			if !ok || assigned != slotID {
				continue
			}
			out = append(out, LifecycleCandidate{ChannelID: id, ChannelType: 1, HashSlot: hash, SlotID: slotID, TimerToken: uint64(len(out) + 1), ActivityVersion: 1, InitialSequence: 10, QuietNotBefore: now.Add(6 * time.Minute), QuietDeadline: now.Add(9 * time.Minute), ReheatAt: now.Add(10 * time.Minute), ObservedLoaded: true})
			added++
		}
	}
	return out
}

func mustInitialLifecycleSlotAssignment(t *testing.T) LifecycleSlotAssignment {
	t.Helper()
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	return assignment
}

func lifecycleRows(candidate LifecycleCandidate, status string, leo, hw uint64) []model.ChannelRuntimeProbeResult {
	roles := [3]string{"leader", "follower", "follower"}
	if status == "missing" {
		roles = [3]string{"missing", "missing", "missing"}
	}
	rows := lifecycleRowsWithRoles(candidate, roles, leo, hw)
	if status != "active" && status != "missing" {
		for index := range rows {
			rows[index].Channels[0].Status = status
		}
	}
	return rows
}
func lifecycleRowsWithRoles(candidate LifecycleCandidate, roles [3]string, leo, hw uint64) []model.ChannelRuntimeProbeResult {
	offsets := [3][2]uint64{{leo, hw}, {leo, hw}, {leo, hw}}
	return lifecycleRowsFull(candidate, roles, offsets)
}
func lifecycleRowsWithOffsets(candidate LifecycleCandidate, offsets [3][2]uint64) []model.ChannelRuntimeProbeResult {
	return lifecycleRowsFull(candidate, [3]string{"leader", "follower", "follower"}, offsets)
}
func lifecycleRowsFull(candidate LifecycleCandidate, roles [3]string, offsets [3][2]uint64) []model.ChannelRuntimeProbeResult {
	out := make([]model.ChannelRuntimeProbeResult, 3)
	for i := range out {
		status := "active"
		if roles[i] == "missing" {
			status = "missing"
			offsets[i] = [2]uint64{}
		}
		out[i] = model.ChannelRuntimeProbeResult{NodeID: uint64(i + 1), Checked: 1, Channels: []model.ChannelRuntimeProbeChannel{{ChannelID: candidate.ChannelID, ChannelType: 1, Role: roles[i], Status: status, LEO: offsets[i][0], HW: offsets[i][1], CheckpointHW: offsets[i][1], LeaderEpoch: 1, ChannelEpoch: 1}}}
	}
	return out
}

func containsRawLifecycleIdentity(value any) bool {
	encoded, _ := json.Marshal(value)
	return bytes.Contains(encoded, []byte("channel_id"))
}
