package chatlifecycle

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestEngineOwnsBoundedSessionsRelationshipsAndFutureWork(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{MaxWorkPerAdvance: 128})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()

	edge := fixture.graph.Incoming(12).Items[0]
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: 1}); err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	if activated, err := fixture.engine.ActivateRelationship(edge, 100); err != nil || activated {
		t.Fatalf("offline ActivateRelationship = %v, %v", activated, err)
	}
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: 2}); err != nil {
		t.Fatalf("peer Login: %v", err)
	}
	considered, activated, err := fixture.engine.ObserveNewUser(edge.PeerIndex)
	if err != nil || activated != 1 || considered > MaxForwardRelationships {
		t.Fatalf("ObserveNewUser = considered %d, activated %d, err %v", considered, activated, err)
	}
	relationshipOrdinal := edge.OwnerIndex * MaxForwardRelationships
	schedule, err := fixture.schedule.Channel(relationshipOrdinal, edge.OwnerIndex, edge.PeerIndex)
	if err != nil {
		t.Fatalf("Channel schedule: %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Online != 2 || snapshot.ActivityCurrent != schedule.InitialBurst.MessageCount ||
		snapshot.FutureCurrent < schedule.InitialBurst.MessageCount || snapshot.FutureCurrent > schedule.InitialBurst.MessageCount+1 {
		t.Fatalf("engine snapshot = %+v, burst = %+v", snapshot, schedule.InitialBurst)
	}
	if snapshot.RelationshipLookback != MaxForwardRelationships || snapshot.ActiveLifecycleTimers > 1 {
		t.Fatalf("relationship/lifecycle bounds = %+v", snapshot)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatalf("Advance before global grant: %v", err)
	}
	if got := fixture.factory.sentCount(); got != 0 {
		t.Fatalf("relationship SEND bypassed global grant: %d", got)
	}
	if tick, err := fixture.engine.Tick(fixture.clock.Now(), []uint64{22, 21, 21}); err != nil || tick.Released != 64 {
		t.Fatalf("Tick = %+v, %v", tick, err)
	}
	afterTick, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after Tick: %v", err)
	}
	if afterTick.ActivityCurrent != schedule.InitialBurst.MessageCount-1 {
		t.Fatalf("due activity did not consume one person grant: %+v", afterTick)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatalf("Advance granted traffic: %v", err)
	}
	if got := fixture.factory.sentCount(); got < 1 || got > 64 {
		t.Fatalf("sparse-online transport sends = %d, want activity included within 64 grants", got)
	}
}

func TestEngineTickAdmitsOneGlobalGrantWithoutHistoryRetention(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	tick, err := fixture.engine.Tick(fixture.clock.Now(), []uint64{1_000, 1_000, 1_000})
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tick.Released != 100 || tick.Person != 90 || tick.Group != 10 {
		t.Fatalf("local global grant = %+v", tick)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.QueueCurrent != 100 || snapshot.FutureCurrent != 100 || snapshot.InflightCurrent != 0 {
		t.Fatalf("tick ownership = %+v", snapshot)
	}
}

func TestEngineRevisitRequiresExplicitColdRuntimeEvidence(t *testing.T) {
	t.Parallel()
	for _, confirm := range []bool{false, true} {
		confirm := confirm
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirm], func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{})
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer fixture.engine.Stop()
			edge := fixture.graph.Incoming(18).Items[0]
			relationshipOrdinal, schedule := findLifecycleSchedule(t, fixture.schedule, edge, LifecycleRevisit)
			ownerLogin := findLoginLongerThan(t, fixture.schedule, schedule.RevisitAfter)
			peerLogin := findLoginLongerThan(t, fixture.schedule, schedule.RevisitAfter+time.Minute)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: ownerLogin}); err != nil {
				t.Fatalf("owner Login: %v", err)
			}
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: peerLogin}); err != nil {
				t.Fatalf("peer Login: %v", err)
			}
			if activated, err := fixture.engine.ActivateRelationship(edge, relationshipOrdinal); err != nil || !activated {
				t.Fatalf("ActivateRelationship = %v, %v", activated, err)
			}
			before, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot before: %v", err)
			}
			if before.ColdEvidencePending != 1 {
				t.Fatalf("cold pending before = %+v", before)
			}
			if confirm {
				approved, err := fixture.engine.ApproveColdRevisit(edge.PersonChannelID)
				if err != nil || !approved {
					t.Fatalf("ApproveColdRevisit = %v, %v", approved, err)
				}
			}
			due := fixture.clock.Now().Add(schedule.RevisitAfter)
			fixture.clock.Set(due)
			if _, err := fixture.engine.Advance(due); err != nil {
				t.Fatalf("Advance revisit: %v", err)
			}
			after, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot after: %v", err)
			}
			wantActivity := before.ActivityCurrent
			if confirm {
				wantActivity += schedule.RevisitMessages
			}
			if after.ActivityCurrent != wantActivity || after.ColdEvidencePending != 0 {
				t.Fatalf("revisit evidence transition = before %+v after %+v want activity %d", before, after, wantActivity)
			}
		})
	}
}

func findLifecycleSchedule(t *testing.T, model ScheduleModel, edge RelationshipEdge, class LifecycleClass) (uint64, ChannelSchedule) {
	t.Helper()
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		schedule, err := model.Channel(ordinal, edge.OwnerIndex, edge.PeerIndex)
		if err != nil {
			t.Fatalf("Channel(%d): %v", ordinal, err)
		}
		if schedule.Class == class {
			return ordinal, schedule
		}
	}
	t.Fatalf("no lifecycle class %d in exact cycle", class)
	return 0, ChannelSchedule{}
}

func findLoginLongerThan(t *testing.T, model ScheduleModel, minimum time.Duration) uint64 {
	t.Helper()
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		schedule, err := model.Login(ordinal)
		if err != nil {
			t.Fatalf("Login(%d): %v", ordinal, err)
		}
		if schedule.SessionDuration > minimum {
			return ordinal
		}
	}
	t.Fatalf("no session exceeds %v", minimum)
	return 0
}

func TestEngineDelayedCompletionUsesExactlyThreeStableRetries(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	fixture.factory.sendErrors = []error{errors.New("temporary one"), errors.New("temporary two"), errors.New("temporary three")}
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()

	uid := fixture.identity.UID(4)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 4, LoginOrdinal: 3}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	intent := fixture.intent(t, uid, "retry-group", 41, TrafficGroup)
	if err := fixture.engine.SubmitGranted(intent, fixture.clock.Now()); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	now := fixture.clock.Now()
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("attempt zero: %v", err)
	}
	for completed := uint8(0); completed < 3; completed++ {
		attempt, err := fixture.retry.Attempt(intent.Logical, completed+1)
		if err != nil {
			t.Fatalf("retry Attempt(%d): %v", completed+1, err)
		}
		now = now.Add(attempt.Delay)
		fixture.clock.Set(now)
		if _, err := fixture.engine.Advance(now); err != nil {
			t.Fatalf("retry %d Advance: %v", completed+1, err)
		}
	}
	packets := fixture.factory.sentPackets()
	if len(packets) != 4 {
		t.Fatalf("SEND attempts = %d, want 4", len(packets))
	}
	for index, packet := range packets {
		if packet.ClientMsgNo != intent.Logical.ClientMsgNo {
			t.Fatalf("attempt %d client_msg_no = %q, want %q", index, packet.ClientMsgNo, intent.Logical.ClientMsgNo)
		}
	}
	ack := &frame.SendackPacket{
		ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 901, MessageSeq: 77, ReasonCode: frame.ReasonSuccess,
	}
	verificationErr := fixture.verifier.HandleSendack(ack)
	if verificationErr != nil {
		t.Fatalf("HandleSendack: %v", verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err != nil {
		t.Fatalf("ObserveSendack: %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryAttempts != 3 || snapshot.InflightCurrent != 0 || snapshot.FutureCurrent != 0 || snapshot.FinalFailures != 0 {
		t.Fatalf("retry snapshot = %+v", snapshot)
	}
	verifierSnapshot := fixture.verifier.Snapshot()
	if verifierSnapshot.Attempts != 4 || verifierSnapshot.RetryAttempts != 3 || verifierSnapshot.Acknowledged != 1 {
		t.Fatalf("verifier attempts = %+v", verifierSnapshot)
	}
}

func TestEngineNonRetriableSendackFailsWithoutRetry(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(5)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 5}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	intent := fixture.intent(t, uid, "non-retry-group", 45, TrafficGroup)
	if err := fixture.engine.SubmitGranted(intent, fixture.clock.Now()); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(fixture.clock.Now()); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	ack := &frame.SendackPacket{ClientMsgNo: intent.Logical.ClientMsgNo, ReasonCode: frame.ReasonAuthFail}
	verificationErr := fixture.verifier.HandleSendack(ack)
	var rejected *SendackRejectedError
	if !errors.As(verificationErr, &rejected) {
		t.Fatalf("HandleSendack = %v, want rejection decision", verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err == nil {
		t.Fatal("non-retriable rejection did not return terminal product failure")
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryQueueDepth != 0 || snapshot.RetryAttempts != 0 || snapshot.InflightCurrent != 0 || snapshot.FinalFailures != 1 {
		t.Fatalf("non-retriable snapshot = %+v", snapshot)
	}
}

func TestEngineLateSuccessfulSendackCancelsScheduledRetry(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{AttemptTimeout: 20 * time.Millisecond})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(7)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 7, LoginOrdinal: 6}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	intent := fixture.intent(t, uid, "late-ack-group", 46, TrafficGroup)
	now := fixture.clock.Now()
	if err := fixture.engine.SubmitGranted(intent, now); err != nil {
		t.Fatalf("SubmitGranted: %v", err)
	}
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("initial Advance: %v", err)
	}
	now = now.Add(20 * time.Millisecond)
	fixture.clock.Set(now)
	if _, err := fixture.engine.Advance(now); err != nil {
		t.Fatalf("timeout Advance: %v", err)
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.RetryQueueDepth != 1 {
		t.Fatalf("retry queue before late ACK = %+v", snapshot)
	}
	ack := &frame.SendackPacket{
		ClientMsgNo: intent.Logical.ClientMsgNo, MessageID: 902, MessageSeq: 78, ReasonCode: frame.ReasonSuccess,
	}
	verificationErr := fixture.verifier.HandleSendack(ack)
	if verificationErr != nil {
		t.Fatalf("HandleSendack: %v", verificationErr)
	}
	if err := fixture.engine.ObserveSendack(uid, ack, verificationErr); err != nil {
		t.Fatalf("ObserveSendack: %v", err)
	}
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RetryQueueDepth != 0 || snapshot.InflightCurrent != 0 || snapshot.RetryAttempts != 0 {
		t.Fatalf("late ACK snapshot = %+v", snapshot)
	}
}

func TestEngineQueueAndCPUSaturationAreHarnessInvalid(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 2, MaxWorkPerAdvance: 1})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(6)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 6, LoginOrdinal: 4}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	now := fixture.clock.Now()
	for ordinal := uint64(1); ordinal <= 3; ordinal++ {
		err := fixture.engine.SubmitGranted(fixture.intent(t, uid, "capacity-group", ordinal, TrafficGroup), now)
		if ordinal <= 2 && err != nil {
			t.Fatalf("Submit(%d): %v", ordinal, err)
		}
		if ordinal == 3 {
			assertRuntimeFailure(t, err, RuntimeFailureEngineQueueSaturated)
		}
	}
	_, err := fixture.engine.Advance(now)
	assertRuntimeFailure(t, err, RuntimeFailureEngineCPUSaturated)
	snapshot, err := fixture.engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.QueuePeak != 2 || snapshot.QueueCurrent != 1 || snapshot.InflightCurrent != 1 || snapshot.HarnessInvalid != 2 {
		t.Fatalf("saturation snapshot = %+v", snapshot)
	}
	if snapshot.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("classification = %q, want harness_invalid", snapshot.Classification)
	}
}

func TestEngineRepeatedStartStopReturnsToBaselineWithoutRetainedIdentity(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 1})
	firstUID := fixture.identity.UID(21)
	secondUID := fixture.identity.UID(22)
	for run, uid := range []string{firstUID, secondUID} {
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatalf("Start(%d): %v", run, err)
		}
		if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: uint64(21 + run), LoginOrdinal: uint64(run)}); err != nil {
			t.Fatalf("Login(%d): %v", run, err)
		}
		if run == 0 {
			due := fixture.clock.Now().Add(time.Hour)
			if err := fixture.engine.SubmitGranted(fixture.intent(t, uid, "first-generation", 901, TrafficGroup), due); err != nil {
				t.Fatalf("first generation Submit: %v", err)
			}
			assertRuntimeFailure(t, fixture.engine.SubmitGranted(fixture.intent(t, uid, "first-generation", 902, TrafficGroup), due), RuntimeFailureEngineQueueSaturated)
		} else {
			snapshot, err := fixture.engine.Snapshot()
			if err != nil {
				t.Fatalf("second generation Snapshot: %v", err)
			}
			if snapshot.Classification != "" || snapshot.HarnessInvalid != 0 {
				t.Fatalf("second generation retained first evidence: %+v", snapshot)
			}
		}
		if run == 1 && fixture.pool.IsOnline(firstUID) {
			t.Fatal("second start retained first-run identity")
		}
		if err := fixture.engine.Stop(); err != nil {
			t.Fatalf("Stop(%d): %v", run, err)
		}
		snapshot, err := fixture.engine.Snapshot()
		if err != nil {
			t.Fatalf("stopped Snapshot(%d): %v", run, err)
		}
		if snapshot.Running || snapshot.ActiveLoops != 0 || snapshot.Online != 0 || snapshot.QueueCurrent != 0 || snapshot.RetryQueueDepth != 0 || snapshot.InflightCurrent != 0 {
			t.Fatalf("stopped baseline(%d) = %+v", run, snapshot)
		}
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snapshot.Generation)
	}
	for _, client := range fixture.factory.clients() {
		select {
		case <-client.readExited:
		default:
			t.Fatal("Stop returned before client drain exited")
		}
	}
}

func TestEngineConcurrentSnapshotsAndStopsJoinWithoutStrandedCaller(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{})
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const snapshots = 32
	started := make(chan struct{}, snapshots)
	results := make(chan error, snapshots+2)
	for range snapshots {
		go func() {
			started <- struct{}{}
			_, err := fixture.engine.Snapshot()
			if err != nil && !errors.Is(err, errEngineNotRunning) {
				results <- err
				return
			}
			results <- nil
		}()
	}
	for range snapshots {
		<-started
	}
	for range 2 {
		go func() { results <- fixture.engine.Stop() }()
	}
	for range snapshots + 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent snapshot/stop: %v", err)
		}
	}
	if snapshot, _ := fixture.engine.Snapshot(); snapshot.Running || snapshot.ActiveLoops != 0 {
		t.Fatalf("joined concurrent stop snapshot = %+v", snapshot)
	}
}

func TestEngineCompletionPressureCannotDeadlockBatchAdvance(t *testing.T) {
	t.Parallel()
	fixture := newEngineTestFixture(t, engineTestLimits{
		CommandCapacity: 2, WorkCapacity: 256, InflightCapacity: 128, MaxWorkPerAdvance: 128,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fixture.engine.Stop()
	uid := fixture.identity.UID(23)
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 23, LoginOrdinal: 3}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	now := fixture.clock.Now()
	for ordinal := uint64(1_000); ordinal < 1_100; ordinal++ {
		if err := fixture.engine.SubmitGranted(fixture.intent(t, uid, "completion-pressure", ordinal, TrafficGroup), now); err != nil {
			t.Fatalf("SubmitGranted(%d): %v", ordinal, err)
		}
	}
	if processed, err := fixture.engine.Advance(now); err != nil || processed != 100 {
		t.Fatalf("Advance = %d, %v", processed, err)
	}
	var snapshot EngineSnapshot
	for range 10_000 {
		snapshot, _ = fixture.engine.Snapshot()
		if snapshot.InflightCurrent == 0 {
			break
		}
		runtime.Gosched()
	}
	if snapshot.InflightCurrent != 0 || snapshot.FutureCurrent != 0 || fixture.verifier.Snapshot().Acknowledged != 100 {
		t.Fatalf("completion pressure snapshot = engine %+v verifier %+v", snapshot, fixture.verifier.Snapshot())
	}
}

func assertRuntimeFailure(t *testing.T, err error, code RuntimeFailureCode) {
	t.Helper()
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Classification() != SyncClassificationHarnessInvalid || runtimeErr.Code() != code {
		t.Fatalf("runtime error = %#v, want %s harness_invalid", err, code)
	}
}

type engineTestLimits struct {
	CommandCapacity   int
	WorkCapacity      int
	InflightCapacity  int
	MaxWorkPerAdvance int
	AttemptTimeout    time.Duration
}

type engineTestFixture struct {
	identity *IdentitySpace
	schedule ScheduleModel
	graph    RelationshipGraph
	traffic  TrafficModel
	retry    RetryPolicy
	verifier *Verifier
	evidence *EvidenceRecorder
	clock    *sessionFakeClock
	factory  *engineFakeFactory
	pool     *SessionPool
	engine   *Engine
}

func newEngineTestFixture(t *testing.T, limits engineTestLimits) engineTestFixture {
	t.Helper()
	cfg := LocalConfig()
	identity, err := NewIdentitySpace("engine-test", 89, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		t.Fatalf("NewRelationshipGraph: %v", err)
	}
	traffic, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	retry, err := NewRetryPolicy(identity, cfg.Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	evidence, err := NewEvidenceRecorder(4, 4)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder: %v", err)
	}
	verifier, err := NewVerifier(traffic, VerifierConfig{
		PendingCapacity: 512, SequenceCapacity: 512, CorrelationCapacity: 64, CorrelationDeadline: time.Minute,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	factory := &engineFakeFactory{}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: identity, Schedule: schedule, Factory: factory, Syncer: engineSyncer{},
		Verifier: verifier, Clock: clock, DeviceID: "engine-test", StartingCapacity: 128,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
		Identity: identity, Model: traffic, Graph: graph, Catalog: catalog, Workload: cfg.Workload, Start: clock.Now(),
	})
	if err != nil {
		t.Fatalf("NewTrafficGenerator: %v", err)
	}
	workCapacity := limits.WorkCapacity
	if workCapacity == 0 {
		workCapacity = 256
	}
	maxWork := limits.MaxWorkPerAdvance
	if maxWork == 0 {
		maxWork = 64
	}
	attemptTimeout := limits.AttemptTimeout
	if attemptTimeout == 0 {
		attemptTimeout = time.Second
	}
	commandCapacity := limits.CommandCapacity
	if commandCapacity == 0 {
		commandCapacity = 64
	}
	inflightCapacity := limits.InflightCapacity
	if inflightCapacity == 0 {
		inflightCapacity = 64
	}
	engine, err := NewEngine(EngineConfig{
		Clock: clock, Sessions: pool, Schedule: schedule, Graph: graph, Traffic: traffic,
		Generator: generator, Retry: retry, Verifier: verifier, Evidence: evidence,
		CommandCapacity: commandCapacity, WorkCapacity: workCapacity, RetryCapacity: 64,
		InflightCapacity: inflightCapacity, MaxWorkPerAdvance: maxWork, AttemptTimeout: attemptTimeout,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engineTestFixture{
		identity: identity, schedule: schedule, graph: graph, traffic: traffic, retry: retry,
		verifier: verifier, evidence: evidence, clock: clock, factory: factory, pool: pool, engine: engine,
	}
}

func (f engineTestFixture) intent(t *testing.T, sender, target string, ordinal uint64, kind TrafficKind) TrafficIntent {
	t.Helper()
	logical, err := f.traffic.NewLogicalSend(0, ordinal, kind, sender, target)
	if err != nil {
		t.Fatalf("NewLogicalSend: %v", err)
	}
	payload, err := f.traffic.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	return TrafficIntent{Logical: logical, Packet: packetForTrafficIntent(logical, payload), Kind: kind, PayloadBytes: len(payload), ChannelID: target}
}

type engineSyncer struct{}

func (engineSyncer) ConversationSync(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	return nil, nil
}

type engineFakeFactory struct {
	mu         sync.Mutex
	sendErrors []error
	clientsV   []*engineFakeClient
	autoAck    bool
}

func (f *engineFakeFactory) NewSession(_, _ string) (SessionClient, error) {
	f.mu.Lock()
	client := &engineFakeClient{
		factory: f, frames: make(chan frame.Frame, 16), stop: make(chan struct{}), readExited: make(chan struct{}),
	}
	f.clientsV = append(f.clientsV, client)
	f.mu.Unlock()
	return client, nil
}

func (f *engineFakeFactory) nextSendError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sendErrors) == 0 {
		return nil
	}
	err := f.sendErrors[0]
	f.sendErrors = f.sendErrors[1:]
	return err
}

func (f *engineFakeFactory) clients() []*engineFakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*engineFakeClient(nil), f.clientsV...)
}

func (f *engineFakeFactory) sentPackets() []*frame.SendPacket {
	clients := f.clients()
	var packets []*frame.SendPacket
	for _, client := range clients {
		client.mu.Lock()
		packets = append(packets, client.sent...)
		client.mu.Unlock()
	}
	return packets
}

func (f *engineFakeFactory) sentCount() int { return len(f.sentPackets()) }

type engineFakeClient struct {
	mu         sync.Mutex
	factory    *engineFakeFactory
	frames     chan frame.Frame
	stop       chan struct{}
	readExited chan struct{}
	closeOnce  sync.Once
	sent       []*frame.SendPacket
}

func (c *engineFakeClient) Connect(context.Context, string, string) error { return nil }
func (c *engineFakeClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	select {
	case packet := <-c.frames:
		return packet, nil
	case <-ctx.Done():
		c.closeReadExited()
		return nil, ctx.Err()
	case <-c.stop:
		c.closeReadExited()
		return nil, errors.New("closed")
	}
}
func (c *engineFakeClient) Send(_ context.Context, packet *frame.SendPacket) error {
	c.mu.Lock()
	c.sent = append(c.sent, packet)
	messageID := int64(len(c.sent))
	c.mu.Unlock()
	err := c.factory.nextSendError()
	if err == nil && c.factory.autoAck {
		c.frames <- &frame.SendackPacket{
			ClientMsgNo: packet.ClientMsgNo, MessageID: messageID, MessageSeq: uint64(messageID), ReasonCode: frame.ReasonSuccess,
		}
	}
	return err
}
func (c *engineFakeClient) AckRecv(context.Context, *frame.RecvackPacket) error { return nil }
func (c *engineFakeClient) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	return nil
}
func (c *engineFakeClient) QueueSnapshot() SessionQueueSnapshot { return SessionQueueSnapshot{} }
func (c *engineFakeClient) closeReadExited() {
	select {
	case <-c.readExited:
	default:
		close(c.readExited)
	}
}
