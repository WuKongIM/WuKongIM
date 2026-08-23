package chatlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
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

func TestInitialLifecycleSlotAssignmentIsAvailableToProductionComposition(t *testing.T) {
	assignment, err := NewInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	if assignment.HashSlotCount() != formalHashSlots {
		t.Fatalf("hash slot count = %d, want %d", assignment.HashSlotCount(), formalHashSlots)
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

func TestLifecycleProofAcceptsLeaderOnlyRuntimeAcrossNaturalColdReheat(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	leaderOnly := lifecycleRowsWithRoles(candidate, [3]string{"leader", "missing", "missing"}, 10, 10)
	if err := proof.Observe(now, leaderOnly); err != nil {
		t.Fatalf("initial leader runtime: %v", err)
	}
	if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
		t.Fatalf("all-node absence: %v", err)
	}
	if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
		t.Fatalf("reheat: %v", err)
	}
	reheatedLeaderOnly := lifecycleRowsWithRoles(candidate, [3]string{"missing", "leader", "missing"}, 11, 11)
	if err := proof.Observe(candidate.ReheatAt.Add(time.Second), reheatedLeaderOnly); err != nil {
		t.Fatalf("reheated leader runtime: %v", err)
	}
	snapshot := proof.Snapshot()
	if snapshot.Loaded != 1 || snapshot.ColdEligible != 1 || snapshot.Completed != 1 || snapshot.ProductFailures != 0 {
		t.Fatalf("snapshot = %+v, want one leader-only lifecycle proof", snapshot)
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
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{
		UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: edge.OwnerIndex,
	}); err != nil {
		t.Fatalf("login inflight sender: %v", err)
	}
	installed := make(chan struct{}, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := &engineWork{due: now.Add(10 * time.Minute), eligibilityDeadline: now.Add(11 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true, NaturalCooling: true}, lifecycleTimerToken: 41,
			activityVersion: 1, initialSequence: 42, lastActivityAt: now, observedLoaded: true}
		fixture.engine.installLifecycleTimer(work)
		fixture.engine.offerLifecycleCandidate(work)
		installed <- struct{}{}
	}}); err != nil {
		t.Fatal(err)
	}
	<-installed
	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now())
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
	if err := fixture.verifier.RegisterSend(intent.Logical, now, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	if err := fixture.verifier.ObserveAttempt(intent.Logical, RetryAttempt{ClientMsgNo: intent.Logical.ClientMsgNo}, 1); err != nil {
		t.Fatal(err)
	}
	installedAck := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		if !fixture.engine.sessions.acquireSendLease(intent.Logical.Sender) {
			installedAck <- false
			return
		}
		inflight := &engineInflight{intent: intent, senderLeaseUID: intent.Logical.Sender, currentClientSeq: 1}
		inflight.registerClientSeq(1)
		fixture.engine.inflight[intent.Logical.ClientMsgNo] = inflight
		installedAck <- true
	}}); err != nil {
		t.Fatal(err)
	}
	if !<-installedAck {
		t.Fatal("install inflight send lease")
	}
	ack := &frame.SendackPacket{ClientSeq: 1, ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 101, MessageSeq: 43, ReasonCode: frame.ReasonSuccess}
	verificationErr := fixture.verifier.HandleSendack(ack)
	if err := fixture.engine.ObserveSendack(edge.OwnerUID, ack, verificationErr); err != nil {
		t.Fatal(err)
	}
	if stale, staleErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion); staleErr != nil || stale {
		t.Fatalf("stale activity lease approval = %v,%v", stale, staleErr)
	}
	refreshed, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now())
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
	if err := fixture.verifier.RegisterSend(lateIntent.Logical, now.Add(time.Minute), SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	if err := fixture.verifier.ObserveAttempt(lateIntent.Logical, RetryAttempt{ClientMsgNo: lateIntent.Logical.ClientMsgNo}, 2); err != nil {
		t.Fatal(err)
	}
	lateInstalled := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		if !fixture.engine.sessions.acquireSendLease(lateIntent.Logical.Sender) {
			lateInstalled <- false
			return
		}
		inflight := &engineInflight{intent: lateIntent, senderLeaseUID: lateIntent.Logical.Sender, currentClientSeq: 2}
		inflight.registerClientSeq(2)
		fixture.engine.inflight[lateIntent.Logical.ClientMsgNo] = inflight
		lateInstalled <- true
	}}); err != nil {
		t.Fatal(err)
	}
	if !<-lateInstalled {
		t.Fatal("install late inflight send lease")
	}
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
	if replay, replayErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion); replayErr != nil || replay {
		t.Fatalf("externally invalidated approval replay = %v,%v", replay, replayErr)
	}
	if leased, leaseErr := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now()); leaseErr != nil || len(leased) != 0 {
		t.Fatalf("invalidated timer lease = %+v,%v", leased, leaseErr)
	}
	if washed, washErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion+1); washErr != nil || washed {
		t.Fatalf("invalidated timer reapproval = %v,%v", washed, washErr)
	}
	dueResult := make(chan error, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		dueResult <- fixture.engine.processWork(context.Background(), fixture.engine.lifecycleByChannel[candidate.ChannelID], candidate.ReheatAt)
	}}); err != nil {
		t.Fatal(err)
	}
	var dueRuntime *RuntimeError
	if dueErr := <-dueResult; !errors.As(dueErr, &dueRuntime) || dueRuntime.Code() != RuntimeFailureLifecycleLeaseInvalidated {
		t.Fatalf("invalidated timer due error = %v", dueErr)
	}
	if missing, missingErr := fixture.engine.ApproveColdRevisitContext(context.Background(), channelid.EncodePersonChannel("missing-a", "missing-b"), candidate.TimerToken, candidate.ActivityVersion); missingErr != nil || missing {
		t.Fatalf("missing approval = %v, %v", missing, missingErr)
	}
	replacementChecked := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		replacement := &engineWork{due: now.Add(20 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 42,
			activityVersion: 1, initialSequence: 50, lastActivityAt: now.Add(2 * time.Minute), observedLoaded: true}
		fixture.engine.installLifecycleTimer(replacement)
		fixture.engine.offerLifecycleCandidate(replacement)
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

func TestEngineApprovedRevisitReplaySurvivesRealDueCompletion(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256, MaxWorkPerAdvance: 256})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()

	edge := fixture.graph.Incoming(18).Items[0]
	relationshipOrdinal, schedule := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRevisit)
	loginOrdinal := findLoginLongerThan(t, fixture.schedule, schedule.RevisitAfter+time.Minute)
	for _, endpoint := range []struct {
		uid   string
		index uint64
	}{{edge.OwnerUID, edge.OwnerIndex}, {edge.PeerUID, edge.PeerIndex}} {
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{
			UID: endpoint.uid, UserIndex: endpoint.index, LoginOrdinal: loginOrdinal,
		}); err != nil {
			t.Fatalf("Login(%q): %v", endpoint.uid, err)
		}
	}
	if activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal); err != nil || !activated {
		t.Fatalf("ActivateRelationship = %v, %v", activated, err)
	}

	type lifecycleLease struct {
		token   uint64
		version uint64
		due     time.Time
	}
	leaseResult := make(chan lifecycleLease, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := fixture.engine.lifecycleByChannel[edge.PersonChannelID]
		// This scheduler-focused fixture does not run the initial SENDACK path.
		if work.activityVersion == 0 {
			work.activityVersion = 1
		}
		leaseResult <- lifecycleLease{token: work.lifecycleTimerToken, version: work.activityVersion, due: work.due}
	}}); err != nil {
		t.Fatal(err)
	}
	lease := <-leaseResult
	if approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, lease.token, lease.version); err != nil || !approved {
		t.Fatalf("ApproveColdRevisitContext = %v, %v", approved, err)
	}

	fixture.clock.Set(lease.due)
	if _, err := fixture.engine.Advance(lease.due); err != nil {
		t.Fatalf("Advance due: %v", err)
	}
	deleted := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		_, live := fixture.engine.lifecycleByChannel[edge.PersonChannelID]
		deleted <- !live
	}}); err != nil {
		t.Fatal(err)
	}
	if !<-deleted {
		t.Fatal("real due processing retained lifecycleByChannel entry")
	}
	grant := fixture.intent(t, edge.OwnerUID, edge.PeerUID, 99, TrafficPerson)
	type routedResult struct {
		intent TrafficIntent
		err    error
	}
	routed := make(chan routedResult, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for _, activity := range fixture.engine.activity {
			if activity.intent.Domain != LogicalDomainRevisit || activity.intent.ChannelID != edge.PersonChannelID {
				continue
			}
			intent, err := fixture.engine.retargetPersonGrant(grant, activity.intent)
			routed <- routedResult{intent: intent, err: err}
			return
		}
		routed <- routedResult{err: errors.New("scheduled reheat activity missing")}
	}}); err != nil {
		t.Fatal(err)
	}
	routedReheat := <-routed
	if routedReheat.err != nil || routedReheat.intent.Domain != LogicalDomainRevisit {
		t.Fatalf("route scheduled reheat = %+v, %v", routedReheat.intent, routedReheat.err)
	}
	if err := fixture.engine.SubmitGranted(routedReheat.intent, lease.due); err != nil {
		t.Fatalf("SubmitGranted scheduled reheat: %v", err)
	}
	if _, err := fixture.engine.Advance(lease.due); err != nil {
		t.Fatalf("Advance scheduled reheat SEND: %v", err)
	}
	clientSeq := make(chan uint64, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		inflight := fixture.engine.inflight[routedReheat.intent.Logical.ClientMsgNo]
		if inflight == nil {
			clientSeq <- 0
			return
		}
		clientSeq <- inflight.currentClientSeq
	}}); err != nil {
		t.Fatal(err)
	}
	ack := &frame.SendackPacket{
		ClientSeq: <-clientSeq, ClientMsgNo: routedReheat.intent.Logical.ClientMsgNo,
		MessageID: 501, MessageSeq: 501, ReasonCode: frame.ReasonSuccess,
	}
	if ack.ClientSeq == 0 {
		t.Fatal("scheduled reheat SEND was not inflight")
	}
	if err := fixture.engine.ObserveSendack(routedReheat.intent.Logical.Sender, ack, fixture.verifier.HandleSendack(ack)); err != nil {
		t.Fatalf("scheduled reheat SENDACK: %v", err)
	}

	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, lease.token, lease.version); err != nil || !replay {
		t.Fatalf("exact replay after real due completion = %v, %v; want true", replay, err)
	}
	wrongChannel := channelid.EncodePersonChannel("wrong-replay-a", "wrong-replay-b")
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), wrongChannel, lease.token, lease.version); err != nil || replay {
		t.Fatalf("wrong-channel replay = %v, %v; want false", replay, err)
	}
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, lease.token+1, lease.version); err != nil || replay {
		t.Fatalf("wrong-token replay = %v, %v; want false", replay, err)
	}
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, lease.token, lease.version+1); err != nil || replay {
		t.Fatalf("wrong-version replay = %v, %v; want false", replay, err)
	}
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		fixture.engine.installLifecycleTimer(&engineWork{
			due: lease.due.Add(10 * time.Minute), eligibilityDeadline: lease.due.Add(11 * time.Minute), kind: engineWorkLifecycle,
			edge: edge, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
			lifecycleTimerToken: lease.token + 1, activityVersion: 1,
		})
	}}); err != nil {
		t.Fatal(err)
	}
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, lease.token, lease.version); err != nil || replay {
		t.Fatalf("same-channel ABA replay = %v, %v; want false", replay, err)
	}
	if approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, lease.token+1, 1); err != nil || !approved {
		t.Fatalf("replacement approval = %v, %v; want true", approved, err)
	}
}

func TestEngineLifecycleApprovalReplayCapacityFailsClosedAndRedactsIdentity(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{
		WorkCapacity: lifecycleApprovalReplayCapacity + 1, MaxWorkPerAdvance: lifecycleApprovalReplayCapacity + 1,
	})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	senderIndex := uint64(500)
	senderUID := fixture.identity.UID(senderIndex)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: senderUID, UserIndex: senderIndex, LoginOrdinal: senderIndex}); err != nil {
		t.Fatalf("Login completion sender: %v", err)
	}
	now := fixture.clock.Now()
	due := now.Add(10 * time.Minute)
	works := make([]*engineWork, lifecycleApprovalReplayCapacity+1)
	installed := make(chan struct{}, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for index := range works {
			identity := channelid.EncodePersonChannel(fmt.Sprintf("replay-capacity-%04d-a", index), fmt.Sprintf("replay-capacity-%04d-b", index))
			work := &engineWork{
				due: due, eligibilityDeadline: due.Add(time.Minute), kind: engineWorkLifecycle,
				edge:                RelationshipEdge{PersonChannelID: identity},
				schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(index + 1), activityVersion: 1,
			}
			works[index] = work
			fixture.engine.installLifecycleTimer(work)
		}
		installed <- struct{}{}
	}}); err != nil {
		t.Fatal(err)
	}
	<-installed
	for index, work := range works {
		approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), work.edge.PersonChannelID, work.lifecycleTimerToken, work.activityVersion)
		if err != nil || !approved {
			t.Fatalf("approval %d = %v, %v", index, approved, err)
		}
	}
	approvalState := make(chan [2]int, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		approvalState <- [2]int{len(fixture.engine.lifecycleApprovalReplays), len(fixture.engine.lifecycleApprovalReplayByChannel)}
	}}); err != nil {
		t.Fatal(err)
	}
	if sizes := <-approvalState; sizes != [2]int{} {
		t.Fatalf("live approvals retained completed replay state: %v", sizes)
	}

	fixture.clock.Set(due)
	completionResult := make(chan error, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for _, work := range works[:lifecycleApprovalReplayCapacity] {
			if err := fixture.engine.completeLifecycleTimer(work, due); err != nil {
				completionResult <- err
				return
			}
		}
		completionResult <- nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := <-completionResult; err != nil {
		t.Fatalf("fill completed replay capacity: %v", err)
	}
	overflowWork := works[lifecycleApprovalReplayCapacity]
	overflowIdentity := overflowWork.edge.PersonChannelID
	overflowWork.requiredSender = senderUID
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		completionResult <- fixture.engine.processWork(context.Background(), overflowWork, due)
	}}); err != nil {
		t.Fatal(err)
	}
	overflowErr := <-completionResult
	var runtimeErr *RuntimeError
	if !errors.As(overflowErr, &runtimeErr) || runtimeErr.Code() != RuntimeFailureLifecycleReplaySaturated {
		t.Fatalf("overflow completion = %v; want replay saturation", overflowErr)
	}

	type replayState struct {
		replays      int
		channels     int
		overflowCold bool
		overflowSeen bool
		overflowLive bool
		activity     int
	}
	stateResult := make(chan replayState, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		_, overflowSeen := fixture.engine.lifecycleApprovalReplays[overflowWork.lifecycleTimerToken]
		stateResult <- replayState{
			replays: len(fixture.engine.lifecycleApprovalReplays), channels: len(fixture.engine.lifecycleApprovalReplayByChannel),
			overflowCold: overflowWork.coldConfirmed, overflowSeen: overflowSeen,
			overflowLive: fixture.engine.lifecycleByChannel[overflowIdentity] == overflowWork,
			activity:     len(fixture.engine.activity),
		}
	}}); err != nil {
		t.Fatal(err)
	}
	state := <-stateResult
	if state.replays != lifecycleApprovalReplayCapacity || state.channels != lifecycleApprovalReplayCapacity ||
		!state.overflowCold || state.overflowSeen || !state.overflowLive || state.activity != 0 {
		t.Fatalf("capacity state = %+v, want %d completed and overflow live", state, lifecycleApprovalReplayCapacity)
	}
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(),
		channelid.EncodePersonChannel("replay-capacity-0000-a", "replay-capacity-0000-b"), 1, 1,
	); err != nil || !replay {
		t.Fatalf("retained replay after saturation = %v, %v", replay, err)
	}
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), overflowIdentity, overflowWork.lifecycleTimerToken, overflowWork.activityVersion); err != nil || !replay {
		t.Fatalf("live overflow approval after failed completion = %v, %v", replay, err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	evidenceJSON, _ := json.Marshal(fixture.evidence.Snapshot())
	observable := string(snapshotJSON) + string(evidenceJSON) + (&RuntimeError{code: RuntimeFailureLifecycleReplaySaturated}).Error()
	if strings.Contains(observable, overflowIdentity) || strings.Contains(observable, fmt.Sprintf("replay-capacity-%04d", lifecycleApprovalReplayCapacity)) {
		t.Fatalf("overflow channel identity leaked into observable evidence: %s", observable)
	}
	if evidence := fixture.evidence.Snapshot(); evidence.Classification != SyncClassificationHarnessInvalid || !workerEvidenceHasCode(evidence, FailureCodeLifecycleReplaySaturated) {
		t.Fatalf("replay saturation evidence = %+v", evidence)
	}
}

func TestEngineLifecycleApprovalReplayBoundsCoverWorstCaseCadenceOverlap(t *testing.T) {
	if lifecycleApprovalReplayRetention != time.Minute || lifecycleApprovalReplayRetention >= LifecycleProofCadence {
		t.Fatalf("replay retention = %v, want 1m and less than %v", lifecycleApprovalReplayRetention, LifecycleProofCadence)
	}
	wantOverlapping := int((maximumRevisitDelay + LifecycleProofCadence - 1) / LifecycleProofCadence)
	if lifecycleApprovalReplayOverlappingCohorts != wantOverlapping || lifecycleApprovalReplayCapacity != lifecycleCohortSize*wantOverlapping {
		t.Fatalf("replay bounds = cohorts %d capacity %d, want %d and %d", lifecycleApprovalReplayOverlappingCohorts, lifecycleApprovalReplayCapacity, wantOverlapping, lifecycleCohortSize*wantOverlapping)
	}
	if lifecycleApprovalReplayOverlappingCohorts != 6 || lifecycleApprovalReplayCapacity != 7_200 {
		t.Fatalf("reviewed replay bounds = cohorts %d capacity %d, want 6 and 7200", lifecycleApprovalReplayOverlappingCohorts, lifecycleApprovalReplayCapacity)
	}
}

func TestEngineLifecycleApprovalReplayRotatesWorstCaseOverlappingCohortsAcrossGeneration(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 8_000, MaxWorkPerAdvance: 8_000})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	base := fixture.clock.Now()
	var nextToken uint64
	retainedCompleted := 0
	for wave, waveOffset := range []time.Duration{0, 72 * time.Hour} {
		due := base.Add(waveOffset + maximumRevisitDelay)
		works := make([]*engineWork, 0, lifecycleApprovalReplayCapacity)
		for cohort := 0; cohort < lifecycleApprovalReplayOverlappingCohorts; cohort++ {
			approvalAt := base.Add(waveOffset + time.Duration(cohort)*LifecycleProofCadence)
			fixture.clock.Set(approvalAt)
			cohortWorks := make([]*engineWork, lifecycleCohortSize)
			installed := make(chan struct{}, 1)
			if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
				for index := range cohortWorks {
					nextToken++
					identity := channelid.EncodePersonChannel(
						fmt.Sprintf("replay-overlap-%d-%d-%04d-a", wave, cohort, index),
						fmt.Sprintf("replay-overlap-%d-%d-%04d-b", wave, cohort, index),
					)
					work := &engineWork{
						due: due, eligibilityDeadline: due.Add(time.Minute), kind: engineWorkLifecycle,
						edge:                RelationshipEdge{PersonChannelID: identity},
						schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
						lifecycleTimerToken: nextToken, activityVersion: 1,
					}
					cohortWorks[index] = work
					fixture.engine.installLifecycleTimer(work)
				}
				installed <- struct{}{}
			}}); err != nil {
				t.Fatal(err)
			}
			<-installed
			for index, work := range cohortWorks {
				approved, err := fixture.engine.ApproveColdRevisitContext(
					context.Background(), work.edge.PersonChannelID, work.lifecycleTimerToken, work.activityVersion,
				)
				if err != nil || !approved {
					t.Fatalf("wave %d cohort %d approval %d = %v, %v", wave, cohort, index, approved, err)
				}
			}
			works = append(works, cohortWorks...)
		}
		mapSize := make(chan [2]int, 1)
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			mapSize <- [2]int{len(fixture.engine.lifecycleApprovalReplays), len(fixture.engine.lifecycleApprovalReplayByChannel)}
		}}); err != nil {
			t.Fatal(err)
		}
		if sizes := <-mapSize; sizes != [2]int{retainedCompleted, retainedCompleted} {
			t.Fatalf("wave %d live approval replay sizes = %v", wave, sizes)
		}

		fixture.clock.Set(due)
		completed := make(chan error, 1)
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			for _, work := range works {
				if err := fixture.engine.completeLifecycleTimer(work, due); err != nil {
					completed <- err
					return
				}
			}
			completed <- nil
		}}); err != nil {
			t.Fatal(err)
		}
		if err := <-completed; err != nil {
			t.Fatalf("wave %d complete overlapping cohorts: %v", wave, err)
		}
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			mapSize <- [2]int{len(fixture.engine.lifecycleApprovalReplays), len(fixture.engine.lifecycleApprovalReplayByChannel)}
		}}); err != nil {
			t.Fatal(err)
		}
		wantCompleted := len(works)
		if sizes := <-mapSize; sizes != [2]int{wantCompleted, wantCompleted} {
			t.Fatalf("wave %d completed replay sizes = %v, want %d", wave, sizes, wantCompleted)
		}

		fixture.clock.Set(due.Add(30 * time.Second))
		for cohort := 0; cohort < lifecycleApprovalReplayOverlappingCohorts; cohort++ {
			sample := works[cohort*lifecycleCohortSize+cohort]
			if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), sample.edge.PersonChannelID, sample.lifecycleTimerToken, sample.activityVersion); err != nil || !replay {
				t.Fatalf("wave %d cohort %d in-window replay = %v, %v", wave, cohort, replay, err)
			}
		}
		sample := works[0]
		wrongChannel := channelid.EncodePersonChannel(fmt.Sprintf("replay-overlap-wrong-%d-a", wave), fmt.Sprintf("replay-overlap-wrong-%d-b", wave))
		if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), wrongChannel, sample.lifecycleTimerToken, sample.activityVersion); err != nil || replay {
			t.Fatalf("wave %d wrong-channel replay = %v, %v", wave, replay, err)
		}
		if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), sample.edge.PersonChannelID, sample.lifecycleTimerToken, sample.activityVersion+1); err != nil || replay {
			t.Fatalf("wave %d wrong-version replay = %v, %v", wave, replay, err)
		}
		fixture.clock.Set(due.Add(time.Minute))
		for cohort := 0; cohort < lifecycleApprovalReplayOverlappingCohorts; cohort++ {
			sample := works[cohort*lifecycleCohortSize+cohort]
			if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), sample.edge.PersonChannelID, sample.lifecycleTimerToken, sample.activityVersion); err != nil || replay {
				t.Fatalf("wave %d cohort %d expired replay = %v, %v; want false", wave, cohort, replay, err)
			}
		}
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			mapSize <- [2]int{len(fixture.engine.lifecycleApprovalReplays), len(fixture.engine.lifecycleApprovalReplayByChannel)}
		}}); err != nil {
			t.Fatal(err)
		}
		expiredSizes := <-mapSize
		if expiredSizes[0] != expiredSizes[1] || expiredSizes[0] > lifecycleApprovalReplayCapacity {
			t.Fatalf("wave %d expired replay sizes = %v", wave, expiredSizes)
		}
		retainedCompleted = expiredSizes[0]
	}
	if snapshot, err := fixture.engine.Snapshot(); err != nil {
		t.Fatal(err)
	} else if snapshot.HarnessInvalid != 0 || snapshot.Classification == SyncClassificationHarnessInvalid {
		t.Fatalf("overlapping cohorts saturated lifecycle replay state: %+v", snapshot)
	}
}

func TestEngineLifecycleApprovalReplayResetsAcrossStopAndGeneration(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	identity := channelid.EncodePersonChannel("replay-reset-a", "replay-reset-b")
	now := fixture.clock.Now()
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		fixture.engine.installLifecycleTimer(&engineWork{
			due: now.Add(10 * time.Minute), kind: engineWorkLifecycle, edge: RelationshipEdge{PersonChannelID: identity},
			schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
			lifecycleTimerToken: 1, activityVersion: 1,
		})
	}}); err != nil {
		t.Fatal(err)
	}
	if approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), identity, 1, 1); err != nil || !approved {
		t.Fatalf("initial approval = %v, %v", approved, err)
	}
	if err := fixture.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	if fixture.engine.lifecycleApprovalReplays != nil || fixture.engine.lifecycleApprovalReplayByChannel != nil {
		t.Fatal("Stop retained lifecycle approval replay state")
	}
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), identity, 1, 1); err != nil || replay {
		t.Fatalf("previous-generation replay = %v, %v; want false", replay, err)
	}
	snapshot, _ := fixture.engine.Snapshot()
	encoded, _ := json.Marshal(struct {
		Engine   EngineSnapshot
		Evidence EvidenceSnapshot
	}{Engine: snapshot, Evidence: fixture.evidence.Snapshot()})
	if bytes.Contains(encoded, []byte(identity)) || bytes.Contains(encoded, []byte("channel_digest")) {
		t.Fatalf("approval replay identity leaked after generation reset: %s", encoded)
	}
}

func TestEngineLifecycleApprovalUsesOwnerExecutionDeadlineAndKeepsExactReplay(t *testing.T) {
	for _, test := range []struct {
		name     string
		ownerAt  func(time.Time) time.Time
		approved bool
	}{
		{name: "before due", ownerAt: func(due time.Time) time.Time { return due.Add(-time.Nanosecond) }, approved: true},
		{name: "equal due", ownerAt: func(due time.Time) time.Time { return due }, approved: false},
		{name: "after due", ownerAt: func(due time.Time) time.Time { return due.Add(time.Nanosecond) }, approved: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer fixture.engine.Stop()
			now := fixture.clock.Now()
			due := now.Add(10 * time.Minute)
			edge := fixture.graph.Incoming(18).Items[0]
			if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
				work := &engineWork{due: due, eligibilityDeadline: due.Add(time.Minute), kind: engineWorkLifecycle, edge: edge,
					schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 1,
					activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
				fixture.engine.installLifecycleTimer(work)
				fixture.engine.offerLifecycleCandidate(work)
			}}); err != nil {
				t.Fatal(err)
			}

			entered, release := blockEngineOwner(t, fixture.engine)
			<-entered
			type approvalResult struct {
				approved bool
				err      error
			}
			result := make(chan approvalResult, 1)
			go func() {
				approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, 1, 1)
				result <- approvalResult{approved: approved, err: err}
			}()
			waitForEngineQueuedCommand(t, fixture.engine)
			fixture.clock.Set(test.ownerAt(due))
			close(release)
			got := <-result
			if got.err != nil || got.approved != test.approved {
				t.Fatalf("approval at owner time %v = %v,%v, want %v,nil", fixture.clock.Now(), got.approved, got.err, test.approved)
			}
			if !test.approved {
				type timerState struct {
					present  bool
					token    uint64
					version  uint64
					sequence uint64
				}
				state := make(chan timerState, 1)
				if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
					work := fixture.engine.lifecycleByChannel[edge.PersonChannelID]
					if work == nil {
						state <- timerState{}
						return
					}
					state <- timerState{present: true, token: work.lifecycleTimerToken, version: work.activityVersion, sequence: work.initialSequence}
				}}); err != nil {
					t.Fatal(err)
				}
				if gotState := <-state; !gotState.present || gotState.token != 1 || gotState.version != 1 || gotState.sequence != 10 {
					t.Fatalf("late rejection washed timer state: %+v", gotState)
				}
			}
			leased, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(leased) != 0 {
				t.Fatalf("approved or expired timer remained publicly leasable: %+v", leased)
			}
		})
	}

	t.Run("canceled queued approval has no late effect", func(t *testing.T) {
		fixture := newEngineTestFixture(t, engineTestLimits{})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer fixture.engine.Stop()
		now := fixture.clock.Now()
		due := now.Add(10 * time.Minute)
		edge := fixture.graph.Incoming(18).Items[0]
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			work := &engineWork{due: due, eligibilityDeadline: due.Add(time.Minute), kind: engineWorkLifecycle, edge: edge,
				schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 1,
				activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
		}}); err != nil {
			t.Fatal(err)
		}
		entered, release := blockEngineOwner(t, fixture.engine)
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := fixture.engine.ApproveColdRevisitContext(ctx, edge.PersonChannelID, 1, 1)
			result <- err
		}()
		waitForEngineQueuedCommand(t, fixture.engine)
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			close(release)
			t.Fatalf("canceled approval error = %v, want context canceled", err)
		}
		close(release)
		leased, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now())
		if err != nil || len(leased) != 1 {
			t.Fatalf("canceled approval mutated timer/index: %+v,%v", leased, err)
		}
	})

	t.Run("successful exact replay remains idempotent after due", func(t *testing.T) {
		fixture := newEngineTestFixture(t, engineTestLimits{})
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer fixture.engine.Stop()
		now := fixture.clock.Now()
		due := now.Add(10 * time.Minute)
		edge := fixture.graph.Incoming(18).Items[0]
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			work := &engineWork{due: due, eligibilityDeadline: due.Add(time.Minute), kind: engineWorkLifecycle, edge: edge,
				schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 1,
				activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
		}}); err != nil {
			t.Fatal(err)
		}
		fixture.clock.Set(due.Add(-time.Nanosecond))
		if approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, 1, 1); err != nil || !approved {
			t.Fatalf("first pre-due approval = %v,%v", approved, err)
		}
		fixture.clock.Set(due.Add(time.Minute))
		if replay, err := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, 1, 1); err != nil || !replay {
			t.Fatalf("post-due exact replay = %v,%v, want idempotent true", replay, err)
		}
	})
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

func TestEngineLifecycleCandidateLeaseUsesFixedBalancedIndex(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 8_192})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now()
	assignment := mustInitialLifecycleSlotAssignment(t)
	type replacementFence struct {
		identity string
		token    uint64
		counts   [formalLogicalSlotGroups]int
	}
	replaced := make(chan replacementFence, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		var nextToken uint64
		for slotID := uint32(1); slotID <= formalLogicalSlotGroups; slotID++ {
			works := make([]*engineWork, 0, 150)
			for ordinal := 0; len(works) < 150; ordinal++ {
				identity := channelid.EncodePersonChannel(
					fmt.Sprintf("indexed-%02d-%06d-a", slotID, ordinal),
					fmt.Sprintf("indexed-%02d-%06d-b", slotID, ordinal),
				)
				hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
				assigned, _ := assignment.Lookup(hashSlot)
				if assigned != slotID {
					continue
				}
				nextToken++
				works = append(works, &engineWork{
					due: now.Add(10*time.Minute + time.Duration(len(works))*time.Second), kind: engineWorkLifecycle,
					edge:                RelationshipEdge{PersonChannelID: identity},
					schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
					lifecycleTimerToken: nextToken, activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true,
				})
			}
			for index := 50; index < len(works); index++ {
				fixture.engine.installLifecycleTimer(works[index])
				fixture.engine.offerLifecycleCandidate(works[index])
			}
			for index := 0; index < 50; index++ {
				fixture.engine.installLifecycleTimer(works[index])
				fixture.engine.offerLifecycleCandidate(works[index])
			}
		}
		for index := 0; index < 5_000; index++ {
			identity := fmt.Sprintf("poison-unindexed-%05d", index)
			fixture.engine.lifecycleByChannel[identity] = &engineWork{
				due: now.Add(6 * time.Minute), kind: engineWorkLifecycle,
				edge:                RelationshipEdge{PersonChannelID: identity},
				schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(index + 100_000), activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true,
			}
		}
		var counts [formalLogicalSlotGroups]int
		for slot := range formalLogicalSlotGroups {
			counts[slot] = int(fixture.engine.lifecycleCandidates[slot].count)
		}
		old := fixture.engine.lifecycleCandidates[0].items[0].work
		replacement := &engineWork{
			due: now.Add(10 * time.Minute), kind: engineWorkLifecycle,
			edge:                RelationshipEdge{PersonChannelID: old.edge.PersonChannelID},
			schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
			lifecycleTimerToken: nextToken + 1, activityVersion: 1, initialSequence: 11, lastActivityAt: now, observedLoaded: true,
		}
		fixture.engine.installLifecycleTimer(replacement)
		fixture.engine.offerLifecycleCandidate(replacement)
		fixture.engine.completeLifecycleTimer(replacement, now)
		replacement.lifecycleTimerToken++
		fixture.engine.installLifecycleTimer(replacement)
		fixture.engine.offerLifecycleCandidate(replacement)
		replaced <- replacementFence{identity: replacement.edge.PersonChannelID, token: replacement.lifecycleTimerToken, counts: counts}
	}}); err != nil {
		t.Fatal(err)
	}
	replacement := <-replaced
	for slot, count := range replacement.counts {
		if count != lifecyclePerSlot {
			t.Fatalf("slot %d indexed count = %d, want %d", slot+1, count, lifecyclePerSlot)
		}
	}
	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), lifecycleCohortSize, assignment, fixture.clock.Now())
	if err != nil || len(candidates) != lifecycleCohortSize {
		t.Fatalf("bounded lease = %d,%v", len(candidates), err)
	}
	var perSlot [formalLogicalSlotGroups]int
	foundReplacement := false
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.ChannelID, "poison-") {
			t.Fatalf("unindexed poison leaked into lease: %+v", candidate)
		}
		perSlot[candidate.SlotID-1]++
		if candidate.ChannelID == replacement.identity {
			if candidate.TimerToken != replacement.token {
				t.Fatalf("ABA replacement token = %d, want %d", candidate.TimerToken, replacement.token)
			}
			foundReplacement = true
		}
	}
	for slot, count := range perSlot {
		if count != lifecyclePerSlot {
			t.Fatalf("slot %d lease count = %d, want %d", slot+1, count, lifecyclePerSlot)
		}
	}
	if !foundReplacement {
		t.Fatal("current ABA replacement missing from bounded lease")
	}
	scanned := make(chan int, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() { scanned <- fixture.engine.lifecycleCandidateLeaseScanned }}); err != nil {
		t.Fatal(err)
	}
	if got := <-scanned; got != lifecycleCohortSize {
		t.Fatalf("lease scanned = %d, want fixed %d", got, lifecycleCohortSize)
	}
	mapping := make([]uint32, formalHashSlots)
	for hashSlot := range formalHashSlots {
		mapping[hashSlot], _ = assignment.Lookup(uint16(hashSlot))
	}
	mapping[0], mapping[22] = mapping[22], mapping[0]
	mismatch, err := NewLifecycleSlotAssignment(mapping)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mismatch, fixture.clock.Now()); !errors.Is(err, ErrLifecycleHarnessInvalid) {
		t.Fatalf("mismatched mapping error = %v, want harness invalid", err)
	}
}

func TestEngineLifecycleCandidateStandbyPromotesBestWithoutFenceReentry(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now()
	assignment := mustInitialLifecycleSlotAssignment(t)
	worksReady := make(chan []*engineWork, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		works := make([]*engineWork, 0, 150)
		for ordinal := 0; len(works) < 150; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("standby-%06d-a", ordinal), fmt.Sprintf("standby-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: now.Add(10*time.Minute + time.Duration(len(works))*time.Second), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(len(works) + 1), activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true,
			}
			works = append(works, work)
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
		}
		works[100].lifecycleLeaseInvalidated = true
		fixture.engine.offerLifecycleCandidate(works[100])
		works[101].lifecycleFenceExhausted = true
		fixture.engine.offerLifecycleCandidate(works[101])
		replacement := &engineWork{
			due: works[102].due, kind: engineWorkLifecycle, edge: works[102].edge,
			schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
			lifecycleTimerToken: 10_000, activityVersion: 1, initialSequence: 11, lastActivityAt: now, observedLoaded: true,
		}
		fixture.engine.installLifecycleTimer(replacement)
		fixture.engine.offerLifecycleCandidate(replacement)
		works = append(works, replacement)
		worksReady <- works
	}}); err != nil {
		t.Fatal(err)
	}
	works := <-worksReady
	replacement := works[len(works)-1]
	assertEngineLifecycleCandidateIndexInvariant(t, fixture.engine)
	if works[100].lifecycleCandidateTier != engineLifecycleCandidateNone || works[101].lifecycleCandidateTier != engineLifecycleCandidateNone ||
		works[102].lifecycleCandidateTier != engineLifecycleCandidateNone || replacement.lifecycleCandidateTier != engineLifecycleCandidateStandby {
		t.Fatalf("fenced/ABA standby tiers = %d,%d,%d replacement=%d",
			works[100].lifecycleCandidateTier, works[101].lifecycleCandidateTier, works[102].lifecycleCandidateTier, replacement.lifecycleCandidateTier)
	}

	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 100, assignment, fixture.clock.Now())
	if err != nil || len(candidates) != 100 {
		t.Fatalf("initial primary lease = %d,%v", len(candidates), err)
	}
	if approved, approveErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidates[0].ChannelID, candidates[0].TimerToken, candidates[0].ActivityVersion); approveErr != nil || !approved {
		t.Fatalf("primary approval = %v,%v", approved, approveErr)
	}
	candidates, err = fixture.engine.LeaseLifecycleCandidates(context.Background(), 100, assignment, fixture.clock.Now())
	if err != nil || len(candidates) != 100 {
		t.Fatalf("promoted primary lease = %d,%v", len(candidates), err)
	}
	foundReplacement := false
	for _, candidate := range candidates {
		if candidate.ChannelID == replacement.edge.PersonChannelID {
			foundReplacement = candidate.TimerToken == replacement.lifecycleTimerToken
		}
		if candidate.ChannelID == works[100].edge.PersonChannelID || candidate.ChannelID == works[101].edge.PersonChannelID {
			t.Fatalf("fenced standby returned through promotion: %+v", candidate)
		}
	}
	if !foundReplacement {
		t.Fatal("best current ABA replacement was not promoted")
	}
	assertEngineLifecycleCandidateIndexInvariant(t, fixture.engine)
	for removed := 0; removed < 10; removed++ {
		candidate := candidates[0]
		if removed == 0 {
			if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
				fixture.engine.removeLifecycleCandidate(fixture.engine.lifecycleByChannel[candidate.ChannelID])
			}}); err != nil {
				t.Fatal(err)
			}
		} else if removed == 1 {
			if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
				fixture.engine.completeLifecycleTimer(fixture.engine.lifecycleByChannel[candidate.ChannelID], now)
			}}); err != nil {
				t.Fatal(err)
			}
		} else if approved, approveErr := fixture.engine.ApproveColdRevisitContext(context.Background(), candidate.ChannelID, candidate.TimerToken, candidate.ActivityVersion); approveErr != nil || !approved {
			t.Fatalf("repeated primary approval %d = %v,%v", removed, approved, approveErr)
		}
		candidates, err = fixture.engine.LeaseLifecycleCandidates(context.Background(), 100, assignment, fixture.clock.Now())
		if err != nil || len(candidates) != 100 {
			t.Fatalf("lease after removal %d = %d,%v, want standby refill", removed, len(candidates), err)
		}
		assertEngineLifecycleCandidateIndexInvariant(t, fixture.engine)
	}
}

func TestEngineLifecycleCandidateLeaseEvictsExpiredPrimariesAndPromotesValidStandby(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: lifecyclePerSlot + 1})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	initialNow := fixture.clock.Now()
	assignment := mustInitialLifecycleSlotAssignment(t)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for ordinal, added := 0, 0; added < lifecyclePerSlot; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("expired-primary-%06d-a", ordinal), fmt.Sprintf("expired-primary-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: initialNow.Add(20*time.Minute + time.Duration(added)*time.Second), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(added + 1), activityVersion: 1, initialSequence: 10, lastActivityAt: initialNow, observedLoaded: true,
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			added++
		}
	}}); err != nil {
		t.Fatal(err)
	}

	leaseNow := initialNow.Add(lifecycleNaturalQuiet + time.Minute)
	fixture.clock.Set(leaseNow)
	var validStandby string
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for ordinal := 0; ; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("valid-standby-%06d-a", ordinal), fmt.Sprintf("valid-standby-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: leaseNow.Add(20 * time.Minute), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: lifecyclePerSlot + 1, activityVersion: 1, initialSequence: 11, lastActivityAt: leaseNow, observedLoaded: true,
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			validStandby = identity
			return
		}
	}}); err != nil {
		t.Fatal(err)
	}

	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), lifecyclePerSlot, assignment, fixture.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ChannelID != validStandby {
		t.Fatalf("lease after primary quiet-start expiry = %+v, want only valid standby %q", candidates, validStandby)
	}
	if !candidates[0].QuietNotBefore.After(leaseNow) {
		t.Fatalf("leased candidate quiet-not-before = %v, want after lease time %v", candidates[0].QuietNotBefore, leaseNow)
	}
}

func TestEngineLifecycleCandidateLeasePromotesStandbyWithCompleteColdObservationWindow(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: lifecyclePerSlot + 1})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	leaseNow := fixture.clock.Now()
	assignment := mustInitialLifecycleSlotAssignment(t)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for ordinal, added := 0, 0; added < lifecyclePerSlot; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("narrow-window-primary-%06d-a", ordinal), fmt.Sprintf("narrow-window-primary-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: leaseNow.Add(lifecycleNaturalQuiet + 30*time.Second), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(added + 1), activityVersion: 1, initialSequence: 10,
				lastActivityAt: leaseNow, observedLoaded: true,
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			added++
		}
	}}); err != nil {
		t.Fatal(err)
	}

	var validStandby string
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for ordinal := 0; ; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("complete-window-standby-%06d-a", ordinal), fmt.Sprintf("complete-window-standby-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: leaseNow.Add(lifecycleNaturalQuiet + 2*time.Minute), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: lifecyclePerSlot + 1, activityVersion: 1, initialSequence: 11,
				lastActivityAt: leaseNow, observedLoaded: true,
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			validStandby = identity
			return
		}
	}}); err != nil {
		t.Fatal(err)
	}

	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), lifecyclePerSlot, assignment, leaseNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ChannelID != validStandby {
		t.Fatalf("lease with narrow primary windows = %+v, want complete-window standby %q", candidates, validStandby)
	}
}

func TestEngineLifecycleCandidateLeasePromotesStandbyThatRemainsLoadedThroughInitialProbe(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: lifecyclePerSlot + 1})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	leaseNow := fixture.clock.Now()
	loadedThrough := leaseNow.Add(5 * time.Second)
	assignment := mustInitialLifecycleSlotAssignment(t)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for ordinal, added := 0, 0; added < lifecyclePerSlot; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("boundary-primary-%06d-a", ordinal), fmt.Sprintf("boundary-primary-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: leaseNow.Add(20*time.Minute + time.Duration(added)*time.Second), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(added + 1), activityVersion: 1, initialSequence: 10,
				lastActivityAt: leaseNow.Add(-lifecycleNaturalQuiet + time.Second), observedLoaded: true,
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			added++
		}
	}}); err != nil {
		t.Fatal(err)
	}

	var validStandby string
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		for ordinal := 0; ; ordinal++ {
			identity := channelid.EncodePersonChannel(
				fmt.Sprintf("probe-safe-standby-%06d-a", ordinal), fmt.Sprintf("probe-safe-standby-%06d-b", ordinal),
			)
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{
				due: leaseNow.Add(30 * time.Minute), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: lifecyclePerSlot + 1, activityVersion: 1, initialSequence: 11,
				lastActivityAt: leaseNow, observedLoaded: true,
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			validStandby = identity
			return
		}
	}}); err != nil {
		t.Fatal(err)
	}

	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), lifecyclePerSlot, assignment, loadedThrough)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ChannelID != validStandby || !candidates[0].QuietNotBefore.After(loadedThrough) {
		t.Fatalf("lease through initial probe = %+v, want only safe standby %q after %v", candidates, validStandby, loadedThrough)
	}
}

func TestEngineLifecycleCandidateIndexIsBoundedByWorkCapacity(t *testing.T) {
	const capacity = 128
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: capacity})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now()
	assignment := mustInitialLifecycleSlotAssignment(t)
	type boundedIndex struct {
		indexed, standby int
		overflowTier     engineLifecycleCandidateTier
	}
	result := make(chan boundedIndex, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		added := 0
		for ordinal := 0; added < capacity; ordinal++ {
			identity := channelid.EncodePersonChannel(fmt.Sprintf("bounded-%06d-a", ordinal), fmt.Sprintf("bounded-%06d-b", ordinal))
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{due: now.Add(10*time.Minute + time.Duration(added)*time.Second), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(added + 1), activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
			if err := fixture.engine.addWork(work); err != nil {
				t.Errorf("add production candidate %d: %v", added, err)
				break
			}
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
			added++
		}
		overflow := &engineWork{due: now.Add(20 * time.Minute), kind: engineWorkLifecycle,
			edge:                RelationshipEdge{PersonChannelID: channelid.EncodePersonChannel("bounded-overflow-a", "bounded-overflow-b")},
			schedule:            ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
			lifecycleTimerToken: 10_000, activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
		fixture.engine.installLifecycleTimer(overflow)
		fixture.engine.offerLifecycleCandidate(overflow)
		result <- boundedIndex{indexed: fixture.engine.lifecycleCandidateIndexed, standby: len(fixture.engine.lifecycleCandidateStandbys[0]), overflowTier: overflow.lifecycleCandidateTier}
	}}); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.indexed != capacity || got.standby != capacity-lifecyclePerSlot || got.overflowTier != engineLifecycleCandidateNone {
		t.Fatalf("bounded index = %+v, want indexed=%d standby=%d and overflow unindexed", got, capacity, capacity-lifecyclePerSlot)
	}
	assertEngineLifecycleCandidateIndexInvariant(t, fixture.engine)
}

func TestEngineLifecycleCandidateFullIndexRefreshKeepsLiveTimerAndInvalidatesOldLease(t *testing.T) {
	const capacity = lifecyclePerSlot + 1
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: capacity})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now()
	assignment := mustInitialLifecycleSlotAssignment(t)
	type refreshState struct {
		identity            string
		token               uint64
		tier                engineLifecycleCandidateTier
		indexed, standbys   int
		primary, oldVersion uint64
	}
	refreshed := make(chan refreshState, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		works := make([]*engineWork, 0, capacity)
		for ordinal := 0; len(works) < capacity; ordinal++ {
			identity := channelid.EncodePersonChannel(fmt.Sprintf("refresh-%06d-a", ordinal), fmt.Sprintf("refresh-%06d-b", ordinal))
			hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
			slotID, _ := assignment.Lookup(hashSlot)
			if slotID != 1 {
				continue
			}
			work := &engineWork{due: now.Add(10*time.Minute + time.Duration(len(works))*time.Second), kind: engineWorkLifecycle,
				edge: RelationshipEdge{PersonChannelID: identity}, schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true},
				lifecycleTimerToken: uint64(len(works) + 1), activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
			works = append(works, work)
			fixture.engine.installLifecycleTimer(work)
			fixture.engine.offerLifecycleCandidate(work)
		}
		work := works[0]
		work.activityVersion = 2
		work.initialSequence = 11
		work.lastActivityAt = now.Add(time.Second)
		fixture.engine.offerLifecycleCandidate(work)
		refreshed <- refreshState{identity: work.edge.PersonChannelID, token: work.lifecycleTimerToken, tier: work.lifecycleCandidateTier,
			indexed: fixture.engine.lifecycleCandidateIndexed, standbys: len(fixture.engine.lifecycleCandidateStandbys[0]), primary: uint64(fixture.engine.lifecycleCandidates[0].count), oldVersion: 1}
	}}); err != nil {
		t.Fatal(err)
	}
	state := <-refreshed
	if state.tier == engineLifecycleCandidateNone || state.indexed != capacity || state.primary != lifecyclePerSlot || state.standbys != 1 {
		t.Fatalf("full refresh state = %+v, want live indexed timer and unchanged full shape", state)
	}
	assertEngineLifecycleCandidateIndexInvariant(t, fixture.engine)
	if approved, err := fixture.engine.ApproveColdRevisitContext(context.Background(), state.identity, state.token, state.oldVersion); err != nil || approved {
		t.Fatalf("old refreshed lease approval = %v,%v, want false,nil", approved, err)
	}
	candidates, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), lifecyclePerSlot, assignment, fixture.clock.Now())
	if err != nil || len(candidates) != lifecyclePerSlot {
		t.Fatalf("lease after full refresh = %d,%v", len(candidates), err)
	}
	found := false
	for _, candidate := range candidates {
		if candidate.ChannelID == state.identity {
			found = candidate.ActivityVersion == 2 && candidate.InitialSequence == 11
		}
	}
	if !found {
		t.Fatal("refreshed live timer missing or retained stale fence")
	}
}

func TestEngineLifecycleCandidateDetachMismatchFailsClosedWithoutDuplicate(t *testing.T) {
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer fixture.engine.Stop()
	now := fixture.clock.Now()
	edge := fixture.graph.Incoming(18).Items[0]
	type corruptState struct {
		tier               engineLifecycleCandidateTier
		position           int
		indexed, primaries int
	}
	result := make(chan corruptState, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := &engineWork{due: now.Add(10 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 1,
			activityVersion: 1, initialSequence: 10, lastActivityAt: now, observedLoaded: true}
		fixture.engine.installLifecycleTimer(work)
		fixture.engine.offerLifecycleCandidate(work)
		slot := int(work.lifecycleCandidateSlot) - 1
		work.lifecycleCandidatePosition = lifecyclePerSlot - 1
		fixture.engine.offerLifecycleCandidate(work)
		result <- corruptState{tier: work.lifecycleCandidateTier, position: work.lifecycleCandidatePosition,
			indexed: fixture.engine.lifecycleCandidateIndexed, primaries: int(fixture.engine.lifecycleCandidates[slot].count)}
	}}); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.tier != engineLifecycleCandidatePrimary || got.position != lifecyclePerSlot-1 || got.indexed != 1 || got.primaries != 1 {
		t.Fatalf("detach mismatch state = %+v, want one fail-closed retained entry without duplicate", got)
	}
	leased, err := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now())
	if err != nil || len(leased) != 0 {
		t.Fatalf("corrupt fail-closed entry became leasable: %+v,%v", leased, err)
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
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{
		UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: edge.OwnerIndex,
	}); err != nil {
		t.Fatalf("login inflight sender: %v", err)
	}
	intent := fixture.intent(t, edge.OwnerUID, edge.PeerUID, 0, TrafficPerson)
	intent.ChannelID = edge.PersonChannelID
	if err := fixture.verifier.RegisterSend(intent.Logical, now, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	if err := fixture.verifier.ObserveAttempt(intent.Logical, RetryAttempt{ClientMsgNo: intent.Logical.ClientMsgNo}, 1); err != nil {
		t.Fatal(err)
	}
	installed := make(chan bool, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		work := &engineWork{due: now.Add(10 * time.Minute), kind: engineWorkLifecycle, edge: edge,
			schedule: ChannelSchedule{Class: LifecycleRevisit, RequiresColdRuntimeEvidence: true}, lifecycleTimerToken: 1,
			activityVersion: math.MaxUint64, initialSequence: 42, lastActivityAt: now, observedLoaded: true}
		fixture.engine.installLifecycleTimer(work)
		fixture.engine.offerLifecycleCandidate(work)
		if !fixture.engine.sessions.acquireSendLease(intent.Logical.Sender) {
			installed <- false
			return
		}
		inflight := &engineInflight{intent: intent, senderLeaseUID: intent.Logical.Sender, currentClientSeq: 1}
		inflight.registerClientSeq(1)
		fixture.engine.inflight[intent.Logical.ClientMsgNo] = inflight
		installed <- true
	}}); err != nil {
		t.Fatal(err)
	}
	if !<-installed {
		t.Fatal("install inflight send lease")
	}
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
	if leased, leaseErr := fixture.engine.LeaseLifecycleCandidates(context.Background(), 1, mustInitialLifecycleSlotAssignment(t), fixture.clock.Now()); leaseErr != nil || len(leased) != 0 {
		t.Fatalf("exhausted timer lease = %+v,%v", leased, leaseErr)
	}
	if washed, washErr := fixture.engine.ApproveColdRevisitContext(context.Background(), edge.PersonChannelID, 1, math.MaxUint64); washErr != nil || washed {
		t.Fatalf("exhausted timer reapproval = %v,%v", washed, washErr)
	}
	dueResult := make(chan error, 1)
	if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
		dueResult <- fixture.engine.processWork(context.Background(), fixture.engine.lifecycleByChannel[edge.PersonChannelID], now.Add(10*time.Minute))
	}}); err != nil {
		t.Fatal(err)
	}
	var dueRuntime *RuntimeError
	if dueErr := <-dueResult; !errors.As(dueErr, &dueRuntime) || dueRuntime.Code() != RuntimeFailureLifecycleFenceExhausted {
		t.Fatalf("exhausted timer due error = %v", dueErr)
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
		{"no leader", lifecycleRowsWithRoles(candidate, [3]string{"missing", "follower", "follower"}, 10, 10)},
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

func TestLifecycleProofReportsClosedProductFailureReasons(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	load := func(proof *LifecycleProof) {
		t.Helper()
		if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
			t.Fatal(err)
		}
	}
	cool := func(proof *LifecycleProof) {
		t.Helper()
		load(proof)
		if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	reheat := func(proof *LifecycleProof) {
		t.Helper()
		cool(proof)
		if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		reason LifecycleProductFailureReason
		run    func(*LifecycleProof) error
	}{
		{name: "incomplete initial load", reason: LifecycleFailureInitialLoad, run: func(proof *LifecycleProof) error {
			return proof.Observe(now, lifecycleRows(candidate, "missing", 0, 0))
		}},
		{name: "runtime state", reason: LifecycleFailureRuntimeState, run: func(proof *LifecycleProof) error {
			return proof.Observe(now, lifecycleRows(candidate, "closing", 10, 10))
		}},
		{name: "runtime error", reason: LifecycleFailureRuntimeState, run: func(proof *LifecycleProof) error {
			return proof.Observe(now, lifecycleRows(candidate, "error", 10, 10))
		}},
		{name: "role disagreement", reason: LifecycleFailureRoleDisagreement, run: func(proof *LifecycleProof) error {
			return proof.Observe(now, lifecycleRowsWithRoles(candidate, [3]string{"leader", "leader", "follower"}, 10, 10))
		}},
		{name: "watermark regression", reason: LifecycleFailureWatermarkRegression, run: func(proof *LifecycleProof) error {
			load(proof)
			return proof.Observe(now.Add(time.Second), lifecycleRowsWithOffsets(candidate, [3][2]uint64{{9, 9}, {10, 10}, {10, 10}}))
		}},
		{name: "continued loading", reason: LifecycleFailureContinuedLoading, run: func(proof *LifecycleProof) error {
			load(proof)
			return proof.Observe(candidate.QuietDeadline, lifecycleRows(candidate, "active", 10, 10))
		}},
		{name: "premature absence", reason: LifecycleFailurePrematureAbsence, run: func(proof *LifecycleProof) error {
			load(proof)
			return proof.Observe(candidate.QuietNotBefore.Add(-time.Nanosecond), lifecycleRows(candidate, "missing", 0, 0))
		}},
		{name: "reheat timeout", reason: LifecycleFailureReheatTimeout, run: func(proof *LifecycleProof) error {
			reheat(proof)
			return proof.Observe(candidate.ReheatAt.Add(lifecycleReheatDeadline+time.Nanosecond), lifecycleRows(candidate, "missing", 0, 0))
		}},
		{name: "reheat without leader", reason: LifecycleFailureRoleDisagreement, run: func(proof *LifecycleProof) error {
			reheat(proof)
			return proof.Observe(candidate.ReheatAt.Add(time.Second), lifecycleRowsWithRoles(candidate, [3]string{"follower", "missing", "missing"}, 11, 11))
		}},
		{name: "sequence proof", reason: LifecycleFailureSequenceProof, run: func(proof *LifecycleProof) error {
			reheat(proof)
			return proof.Observe(candidate.ReheatAt.Add(time.Second), lifecycleRows(candidate, "active", 10, 10))
		}},
		{name: "sequence reset", reason: LifecycleFailureSequenceProof, run: func(proof *LifecycleProof) error {
			initial := lifecycleRows(candidate, "active", 10, 10)
			for node := range initial {
				initial[node].Channels[0].CheckpointHW = 8
			}
			if err := proof.Observe(now, initial); err != nil {
				t.Fatal(err)
			}
			if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
				t.Fatal(err)
			}
			if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
				t.Fatal(err)
			}
			return proof.Observe(candidate.ReheatAt.Add(time.Second), lifecycleRows(candidate, "active", 9, 9))
		}},
		{name: "reheat invalid watermark", reason: LifecycleFailureWatermarkRegression, run: func(proof *LifecycleProof) error {
			reheat(proof)
			return proof.Observe(candidate.ReheatAt.Add(time.Second), lifecycleRowsWithOffsets(candidate, [3][2]uint64{{10, 11}, {11, 11}, {11, 11}}))
		}},
		{name: "unexpected reload", reason: LifecycleFailureUnexpectedReload, run: func(proof *LifecycleProof) error {
			load(proof)
			if err := proof.Observe(candidate.QuietNotBefore, lifecycleRowsWithRoles(candidate, [3]string{"missing", "follower", "follower"}, 10, 10)); err != nil {
				t.Fatal(err)
			}
			return proof.Observe(candidate.QuietNotBefore.Add(time.Second), lifecycleRows(candidate, "active", 10, 10))
		}},
		{name: "control transition", reason: LifecycleFailureControlTransition, run: func(proof *LifecycleProof) error {
			return proof.Reheat(context.Background(), now, candidate.ChannelID, &fakeLifecycleSender{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
			if err != nil {
				t.Fatal(err)
			}
			err = test.run(proof)
			if !errors.Is(err, ErrLifecycleProductFailure) {
				t.Fatalf("error = %v, want product failure", err)
			}
			var reasoned interface {
				Reason() LifecycleProductFailureReason
			}
			if !errors.As(err, &reasoned) || reasoned.Reason() != test.reason {
				t.Fatalf("error reason = %v, want %s", err, test.reason)
			}
			if strings.Contains(err.Error(), candidate.ChannelID) {
				t.Fatalf("error leaked candidate identity: %v", err)
			}
			snapshot := proof.Snapshot()
			if snapshot.ProductFailures != 1 || snapshot.ProductFailureReasons.Count(test.reason) != 1 || snapshot.ProductFailureReasons.Total() != snapshot.ProductFailures {
				t.Fatalf("reason snapshot = %+v, want exactly one %s", snapshot, test.reason)
			}
			encoded, marshalErr := json.Marshal(snapshot)
			if marshalErr != nil || bytes.Contains(encoded, []byte(candidate.ChannelID)) {
				t.Fatalf("product failure JSON leaked candidate identity: %s (error %v)", encoded, marshalErr)
			}
		})
	}
}

func TestLifecycleProofProductFailureReasonRollsBackBatchAndCountsOnce(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidates := lifecycleTestCandidates(t, now)[:2]
	proof, err := NewLifecycleProof(candidates)
	if err != nil {
		t.Fatal(err)
	}
	rows := lifecycleRows(candidates[0], "active", 10, 10)
	failed := lifecycleRows(candidates[1], "error", 10, 10)
	for node := range rows {
		rows[node].Checked = 2
		rows[node].Channels = append(rows[node].Channels, failed[node].Channels[0])
	}
	if err := proof.Observe(now, rows); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("error = %v, want product failure", err)
	}
	snapshot := proof.Snapshot()
	if snapshot.Loaded != 0 || snapshot.ProductFailures != 1 || snapshot.ProductFailureReasons.Count(LifecycleFailureRuntimeState) != 1 || snapshot.ProductFailureReasons.Total() != 1 {
		t.Fatalf("atomic reason snapshot = %+v", snapshot)
	}
}

func TestLifecycleProofConcurrentRollbackFailuresCountExactlyOnce(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	const failures = 32
	var wg sync.WaitGroup
	errorsSeen := make(chan error, failures)
	for range failures {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsSeen <- proof.Observe(now, lifecycleRows(candidate, "error", 10, 10))
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if !errors.Is(err, ErrLifecycleProductFailure) {
			t.Fatalf("error = %v, want product failure", err)
		}
	}
	snapshot := proof.Snapshot()
	if snapshot.ProductFailures != failures || snapshot.ProductFailureReasons.RuntimeState != failures ||
		snapshot.ProductFailureReasons.Total() != snapshot.ProductFailures {
		t.Fatalf("concurrent failure snapshot = %+v", snapshot)
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

func TestLifecycleProofRejectsStuckRuntimeAndSequenceReset(t *testing.T) {
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
		{"reheat without leader", func(p *LifecycleProof) error {
			_ = p.Observe(now, lifecycleRows(candidate, "active", 10, 10))
			_ = p.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0))
			_ = p.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{})
			return p.Observe(candidate.ReheatAt, lifecycleRowsWithRoles(candidate, [3]string{"follower", "missing", "missing"}, 11, 11))
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

func TestLifecycleProofRejectsCheckpointRegressionBeforeCompletion(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, _ := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatal(err)
	}
	rows := lifecycleRows(candidate, "active", 11, 11)
	rows[1].Channels[0].CheckpointHW = 9
	if err := proof.Observe(now.Add(time.Second), rows); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("error = %v, want product failure", err)
	}
}

func TestLifecycleProofCompletedCandidateIsAbsorbingWhilePeerFinishesLater(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidates := lifecycleTestCandidates(t, now)[:2]
	candidates[1].QuietDeadline = now.Add(19 * time.Minute)
	candidates[1].ReheatAt = now.Add(20 * time.Minute)
	proof, err := NewLifecycleProof(candidates)
	if err != nil {
		t.Fatal(err)
	}
	mergeRows := func(left, right []model.ChannelRuntimeProbeResult) []model.ChannelRuntimeProbeResult {
		t.Helper()
		out := make([]model.ChannelRuntimeProbeResult, len(left))
		for node := range out {
			channels := append([]model.ChannelRuntimeProbeChannel(nil), left[node].Channels...)
			channels = append(channels, right[node].Channels...)
			out[node] = model.ChannelRuntimeProbeResult{NodeID: left[node].NodeID, Checked: len(channels), Channels: channels}
		}
		return out
	}
	if err := proof.Observe(now, mergeRows(
		lifecycleRows(candidates[0], "active", 10, 10), lifecycleRows(candidates[1], "active", 10, 10),
	)); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if err := proof.Observe(candidates[0].QuietNotBefore, mergeRows(
		lifecycleRows(candidates[0], "missing", 0, 0), lifecycleRows(candidates[1], "missing", 0, 0),
	)); err != nil {
		t.Fatalf("cool candidates: %v", err)
	}
	for _, candidate := range candidates {
		if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
			t.Fatalf("approve %q: %v", candidate.ChannelID, err)
		}
	}
	if err := proof.Observe(candidates[0].ReheatAt, mergeRows(
		lifecycleRows(candidates[0], "active", 11, 11), lifecycleRows(candidates[1], "missing", 0, 0),
	)); err != nil {
		t.Fatalf("complete first candidate: %v", err)
	}
	proof.mu.Lock()
	completedState := *proof.candidates[candidates[0].ChannelID]
	proof.mu.Unlock()
	if snapshot := proof.Snapshot(); snapshot.Completed != 1 || snapshot.ProductFailures != 0 {
		t.Fatalf("first completion snapshot = %+v", snapshot)
	}
	if err := proof.Observe(candidates[0].ReheatAt.Add(5*time.Minute), mergeRows(
		lifecycleRows(candidates[0], "missing", 0, 0), lifecycleRows(candidates[1], "missing", 0, 0),
	)); err != nil {
		t.Fatalf("completed candidate became non-absorbing: %v", err)
	}
	if snapshot := proof.Snapshot(); snapshot.Completed != 1 || snapshot.ProductFailures != 0 {
		t.Fatalf("absorbed missing probe snapshot = %+v", snapshot)
	}
	if err := proof.Observe(candidates[1].ReheatAt, mergeRows(
		lifecycleRows(candidates[0], "missing", 0, 0), lifecycleRows(candidates[1], "active", 12, 12),
	)); err != nil {
		t.Fatalf("complete later candidate: %v", err)
	}
	invalidRoleRows := lifecycleRowsWithRoles(candidates[1], [3]string{"invalid", "invalid", "invalid"}, 1, 1)
	if err := proof.Observe(candidates[1].ReheatAt.Add(time.Second), mergeRows(
		lifecycleRows(candidates[0], "error", 0, 0), invalidRoleRows,
	)); err != nil {
		t.Fatalf("completed candidates did not absorb invalid runtime rows: %v", err)
	}
	proof.mu.Lock()
	finalState := *proof.candidates[candidates[0].ChannelID]
	proof.mu.Unlock()
	if finalState.phase != completedState.phase || finalState.lastLEO != completedState.lastLEO ||
		finalState.lastHW != completedState.lastHW || finalState.lastCheckpoint != completedState.lastCheckpoint {
		t.Fatalf("completed candidate state changed: before=%+v after=%+v", completedState, finalState)
	}
	if snapshot := proof.Snapshot(); snapshot.Completed != 2 || snapshot.ProductFailures != 0 || snapshot.ReheatLatency.Count != 2 {
		t.Fatalf("final absorbing snapshot = %+v", snapshot)
	}
}

func TestLifecycleProofRejectsPartialCoolingCheckpointRegressionAtomically(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	partial := lifecycleRowsWithRoles(candidate, [3]string{"missing", "follower", "missing"}, 20, 20)
	if err := proof.Observe(candidate.QuietNotBefore, partial); err != nil {
		t.Fatalf("partial cooling checkpoint advance: %v", err)
	}
	for node := range partial {
		if partial[node].Channels[0].Role != "missing" {
			partial[node].Channels[0].CheckpointHW = 15
		}
	}
	err = proof.Observe(candidate.QuietNotBefore.Add(time.Second), partial)
	if !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("checkpoint regression error = %v, want product failure", err)
	}
	var reasoned interface {
		Reason() LifecycleProductFailureReason
	}
	if !errors.As(err, &reasoned) || reasoned.Reason() != LifecycleFailureWatermarkRegression {
		t.Fatalf("checkpoint regression reason = %v, want %s", err, LifecycleFailureWatermarkRegression)
	}
	snapshot := proof.Snapshot()
	if snapshot.Loaded != 1 || snapshot.ColdEligible != 0 || snapshot.ProductFailures != 1 ||
		snapshot.ProductFailureReasons.WatermarkRegression != 1 || snapshot.ProductFailureReasons.Total() != snapshot.ProductFailures {
		t.Fatalf("atomic checkpoint regression snapshot = %+v", snapshot)
	}
}

func TestLifecycleProofFirstReloadCheckpointRegressionIsWatermarkFailure(t *testing.T) {
	now := time.Unix(1_000, 0)
	candidate := lifecycleTestCandidates(t, now)[0]
	proof, err := NewLifecycleProof([]LifecycleCandidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.Observe(now, lifecycleRows(candidate, "active", 10, 10)); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if err := proof.Observe(candidate.QuietNotBefore, lifecycleRows(candidate, "missing", 0, 0)); err != nil {
		t.Fatalf("cold proof: %v", err)
	}
	if err := proof.Reheat(context.Background(), candidate.QuietNotBefore, candidate.ChannelID, &fakeLifecycleSender{}); err != nil {
		t.Fatalf("reheat approval: %v", err)
	}
	reloaded := lifecycleRows(candidate, "active", 11, 11)
	for node := range reloaded {
		reloaded[node].Channels[0].CheckpointHW = 9
	}
	err = proof.Observe(candidate.ReheatAt.Add(time.Second), reloaded)
	if !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("first reload checkpoint regression error = %v, want product failure", err)
	}
	var reasoned interface {
		Reason() LifecycleProductFailureReason
	}
	if !errors.As(err, &reasoned) || reasoned.Reason() != LifecycleFailureWatermarkRegression {
		t.Fatalf("first reload checkpoint reason = %v, want %s", err, LifecycleFailureWatermarkRegression)
	}
	snapshot := proof.Snapshot()
	if snapshot.Completed != 0 || snapshot.ProductFailures != 1 || snapshot.ProductFailureReasons.WatermarkRegression != 1 ||
		snapshot.ProductFailureReasons.Total() != snapshot.ProductFailures {
		t.Fatalf("first reload checkpoint snapshot = %+v", snapshot)
	}
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
	if snapshot := accounting.Snapshot(); snapshot.ExpectedUnique != 1_002_000 || snapshot.Created != 1_002_000 || snapshot.ExternalDemoActivity != 0 {
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

func TestMetaCreateAccountingRejectsReheatSlotDeficitDespiteExternalCreates(t *testing.T) {
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
	if snapshot := accounting.Snapshot(); snapshot.ExternalDemoActivity != 1 || snapshot.ExpectedUnique != 7 ||
		snapshot.Created != 7 || snapshot.Checkpoints != 2 {
		t.Fatalf("failed checkpoint did not retain classified Demo/accounting evidence: %+v", snapshot)
	}
	var caughtUp [formalLogicalSlotGroups]uint64
	caughtUp[0], caughtUp[1] = 6, 2
	if err := accounting.Checkpoint(
		nextPerson, preparedGroups, assignment,
		lifecycleMetaMetricsBySlot(caughtUp, [formalLogicalSlotGroups]uint64{}, [formalLogicalSlotGroups]uint64{}), true,
	); !errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatalf("caught-up checkpoint error = %v, want sticky product failure", err)
	}
	if snapshot := accounting.Snapshot(); snapshot.CreatedBySlot != redistributedCreated || snapshot.Checkpoints != 2 {
		t.Fatalf("caught-up checkpoint erased first product evidence: %+v", snapshot)
	}
}

func TestMetaCreateAccountingAllowsAndReportsExternalDemoCreatesPerSlot(t *testing.T) {
	accounting := NewMetaCreateAccounting()
	if err := lifecycleMetaCheckpoint(accounting, 10, 2, lifecycleMetaMetrics(12, 3, 0), false); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleMetaCheckpoint(accounting, 13, 2, lifecycleMetaMetrics(15, 9, 0), true); err != nil {
		t.Fatalf("expected concurrent growth: %v", err)
	}
	if snapshot := accounting.Snapshot(); snapshot.ExpectedUnique != 15 || snapshot.Created != 15 || snapshot.AlreadyExisting != 9 || snapshot.ExternalDemoActivity != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	excess := NewMetaCreateAccounting()
	_ = lifecycleMetaCheckpoint(excess, 10, 2, lifecycleMetaMetrics(12, 0, 0), false)
	if err := lifecycleMetaCheckpoint(excess, 13, 2, lifecycleMetaMetrics(16, 0, 0), true); err != nil {
		t.Fatalf("external Demo create error = %v", err)
	}
	if snapshot := excess.Snapshot(); snapshot.ExternalDemoActivity != 1 || snapshot.Checkpoints != 2 {
		t.Fatalf("excess snapshot = %+v", snapshot)
	}

	assignment := mustInitialLifecycleSlotAssignment(t)
	var personEdges, preparedGroups MetaCreateHashSlotCounts
	personEdges[0], preparedGroups[22] = 5, 1
	var created [formalLogicalSlotGroups]uint64
	created[0], created[1] = 6, 1
	perSlot := NewMetaCreateAccounting()
	if err := perSlot.Checkpoint(
		personEdges, preparedGroups, assignment,
		lifecycleMetaMetricsBySlot(created, [formalLogicalSlotGroups]uint64{}, [formalLogicalSlotGroups]uint64{}), false,
	); err != nil {
		t.Fatalf("per-Slot external Demo create error = %v", err)
	}
	if snapshot := perSlot.Snapshot(); snapshot.ExpectedUnique != 6 || snapshot.Created != 7 || snapshot.ExternalDemoActivity != 1 {
		t.Fatalf("per-Slot external Demo snapshot = %+v", snapshot)
	}
}

func TestMetaCreateAccountingRejectsCreatedOnReheatErrorsRegressionAndOverflow(t *testing.T) {
	base := lifecycleMetaMetrics(12, 0, 0)
	for _, test := range []struct {
		name string
		run  func(*MetaCreateAccounting) error
		want error
	}{
		{"first checkpoint cannot be reheat", func(a *MetaCreateAccounting) error {
			return lifecycleMetaCheckpoint(a, 10, 2, base, true)
		}, ErrLifecycleHarnessInvalid},
		{"external create outside reheat is allowed", func(a *MetaCreateAccounting) error {
			_ = lifecycleMetaCheckpoint(a, 10, 2, base, false)
			return lifecycleMetaCheckpoint(a, 10, 2, lifecycleMetaMetrics(13, 0, 0), false)
		}, nil},
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

func assertEngineLifecycleCandidateIndexInvariant(t *testing.T, engine *Engine) {
	t.Helper()
	type indexState struct {
		indexed int
		sum     int
		detail  string
	}
	result := make(chan indexState, 1)
	if err := engine.enqueueBlocking(engineCommand{run: func() {
		state := indexState{indexed: engine.lifecycleCandidateIndexed}
		for slot := range formalLogicalSlotGroups {
			bucket := &engine.lifecycleCandidates[slot]
			state.sum += int(bucket.count) + len(engine.lifecycleCandidateStandbys[slot])
			for position := 0; position < int(bucket.count); position++ {
				work := bucket.items[position].work
				if work == nil || work.lifecycleCandidateTier != engineLifecycleCandidatePrimary ||
					work.lifecycleCandidateSlot != uint8(slot+1) || work.lifecycleCandidatePosition != position {
					state.detail = fmt.Sprintf("primary slot=%d position=%d work=%p", slot+1, position, work)
					result <- state
					return
				}
			}
			for position, work := range engine.lifecycleCandidateStandbys[slot] {
				if work == nil || work.lifecycleCandidateTier != engineLifecycleCandidateStandby ||
					work.lifecycleCandidateSlot != uint8(slot+1) || work.lifecycleCandidatePosition != position {
					state.detail = fmt.Sprintf("standby slot=%d position=%d work=%p", slot+1, position, work)
					result <- state
					return
				}
			}
		}
		for _, work := range engine.work {
			if _, eligible := engine.lifecycleCandidateSlotFor(work); eligible && work.lifecycleCandidateTier == engineLifecycleCandidateNone {
				state.detail = fmt.Sprintf("eligible production work is unindexed: %p", work)
				result <- state
				return
			}
		}
		result <- state
	}}); err != nil {
		t.Fatalf("index invariant owner command: %v", err)
	}
	state := <-result
	if state.detail != "" || state.indexed != state.sum || state.indexed < 0 || state.indexed > engine.workCapacity {
		t.Fatalf("candidate index invariant = %+v, capacity=%d", state, engine.workCapacity)
	}
}

func containsRawLifecycleIdentity(value any) bool {
	encoded, _ := json.Marshal(value)
	return bytes.Contains(encoded, []byte("channel_id"))
}
