package chatlifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSessionPoolLoginSyncExpiryAndFreshRelogin(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(17)

	first, err := fixture.pool.Login(context.Background(), SessionLogin{
		UID: uid, UserIndex: 17, LoginOrdinal: 9,
	})
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}
	wantSchedule, err := fixture.schedule.Login(9)
	if err != nil {
		t.Fatalf("schedule Login: %v", err)
	}
	if !first.TrafficReady || first.Deadline != fixture.clock.Now().Add(wantSchedule.SessionDuration) {
		t.Fatalf("first session = %+v, duration = %v", first, wantSchedule.SessionDuration)
	}
	if got := fixture.events.snapshot(); len(got) != 3 || got[0] != "factory" || got[1] != "connect" || got[2] != "sync" {
		t.Fatalf("startup order = %v, want [factory connect sync]", got)
	}
	firstToken := fixture.factory.tokens()[0]
	if firstToken == "" {
		t.Fatal("deterministic connect token is empty")
	}
	assertFullSyncRequest(t, fixture.syncer.requests()[0], uid)

	fixture.clock.Set(first.Deadline)
	if expired := fixture.pool.Expire(fixture.clock.Now()); expired != 1 {
		t.Fatalf("Expire = %d, want 1", expired)
	}
	if got := fixture.pool.Snapshot().CloseReasons.Expired; got != 1 {
		t.Fatalf("expired close reasons = %d, want 1", got)
	}
	if fixture.pool.IsOnline(uid) || !fixture.factory.clients()[0].closed() {
		t.Fatal("expired session retained online state or open socket")
	}

	fixture.events.reset()
	second, err := fixture.pool.Login(context.Background(), SessionLogin{
		UID: uid, UserIndex: 17, LoginOrdinal: 10,
	})
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}
	if !second.TrafficReady {
		t.Fatal("re-login did not become traffic-ready")
	}
	clients := fixture.factory.clients()
	if len(clients) != 2 || clients[0] == clients[1] {
		t.Fatalf("re-login clients = %d, want two fresh instances", len(clients))
	}
	if tokens := fixture.factory.tokens(); len(tokens) != 2 || tokens[0] != tokens[1] {
		t.Fatalf("same UID tokens = %v, want deterministic equality", tokens)
	}
	requests := fixture.syncer.requests()
	if len(requests) != 2 {
		t.Fatalf("sync requests = %d, want 2", len(requests))
	}
	assertFullSyncRequest(t, requests[1], uid)
	if got := fixture.events.snapshot(); len(got) != 3 || got[0] != "factory" || got[1] != "connect" || got[2] != "sync" {
		t.Fatalf("re-login order = %v, want [factory connect sync]", got)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

func TestSessionPoolHeartbeatsTrafficReadySessionAndLogoutJoinsHeartbeat(t *testing.T) {
	fixture := newSessionTestFixture(t)
	sleepEntered := make(chan time.Duration, 1)
	sleepRelease := make(chan struct{})
	firstSleep := true
	fixture.pool.heartbeatSleep = func(ctx context.Context, duration time.Duration) error {
		if firstSleep {
			firstSleep = false
			sleepEntered <- duration
			select {
			case <-sleepRelease:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	pingEntered := make(chan struct{}, 1)
	pingRelease := make(chan struct{})
	fixture.factory.pingEntered = pingEntered
	fixture.factory.pingRelease = pingRelease

	uid := fixture.identity.UID(18)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 18, LoginOrdinal: 10}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if interval := <-sleepEntered; interval != 30*time.Second {
		t.Fatalf("heartbeat interval = %v, want 30s", interval)
	}
	if got := fixture.events.snapshot(); len(got) != 3 || got[0] != "factory" || got[1] != "connect" || got[2] != "sync" {
		t.Fatalf("heartbeat started before traffic-ready sync: events = %v", got)
	}
	close(sleepRelease)
	select {
	case <-pingEntered:
	case <-time.After(time.Second):
		t.Fatal("traffic-ready session did not send a heartbeat")
	}

	client := fixture.factory.clients()[0]
	closeEntered := make(chan struct{}, 1)
	client.closeEntered = closeEntered
	logoutDone := make(chan error, 1)
	go func() { logoutDone <- fixture.pool.Logout(uid) }()
	select {
	case <-closeEntered:
	case <-time.After(time.Second):
		t.Fatal("Logout did not close the session")
	}
	select {
	case err := <-logoutDone:
		t.Fatalf("Logout returned before heartbeat exited: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(pingRelease)
	select {
	case err := <-logoutDone:
		if err != nil {
			t.Fatalf("Logout: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Logout did not join heartbeat after Ping returned")
	}
}

func TestSessionPoolHeartbeatWriteFailureDetachesSession(t *testing.T) {
	fixture := newSessionTestFixture(t)
	sleepEntered := make(chan struct{}, 1)
	sleepRelease := make(chan struct{})
	firstSleep := true
	fixture.pool.heartbeatSleep = func(ctx context.Context, _ time.Duration) error {
		if firstSleep {
			firstSleep = false
			sleepEntered <- struct{}{}
			select {
			case <-sleepRelease:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	fixture.factory.pingErr = errors.New("redacted heartbeat write failure")

	uid := fixture.identity.UID(21)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 21, LoginOrdinal: 11}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	<-sleepEntered
	client := fixture.factory.clients()[0]
	closeEntered := make(chan struct{}, 2)
	client.closeEntered = closeEntered
	closeRelease := make(chan struct{})
	client.closeRelease = closeRelease
	fixture.pool.mu.RLock()
	session := fixture.pool.online[uid]
	fixture.pool.mu.RUnlock()
	close(sleepRelease)
	select {
	case <-closeEntered:
	case <-time.After(time.Second):
		t.Fatal("heartbeat write failure did not close the session")
	}
	if snapshot := fixture.pool.Snapshot(); snapshot.CloseReasons.HeartbeatFailed != 1 || snapshot.Online != 1 {
		t.Fatalf("heartbeat failure was not observable while close blocked: %+v", snapshot)
	}
	close(closeRelease)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat write failure did not terminate the receive drain")
	}
	if fixture.pool.IsOnline(uid) || fixture.pool.isOwned(uid) {
		t.Fatal("heartbeat write failure retained session ownership")
	}
	snapshot := fixture.pool.Snapshot()
	if snapshot.ReadErrors != 1 || snapshot.CloseReasons.HeartbeatFailed != 1 ||
		fixture.verifier.EvidenceSnapshot().Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("heartbeat write failure evidence = pool %+v verifier %+v", snapshot, fixture.verifier.EvidenceSnapshot())
	}
}

func TestSessionPoolHeartbeatCloseFailureStillDetachesAndCountsCleanup(t *testing.T) {
	fixture := newSessionTestFixture(t)
	sleepEntered := make(chan struct{}, 1)
	sleepRelease := make(chan struct{})
	fixture.pool.heartbeatSleep = func(ctx context.Context, _ time.Duration) error {
		select {
		case sleepEntered <- struct{}{}:
		default:
		}
		select {
		case <-sleepRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	fixture.factory.pingErr = errors.New("redacted heartbeat write failure")
	uid := fixture.identity.UID(22)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 22, LoginOrdinal: 12}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	<-sleepEntered
	client := fixture.factory.clients()[0]
	client.closeErr = errors.New("private close failure")
	client.closeDoesNotStop = true
	fixture.pool.mu.RLock()
	session := fixture.pool.online[uid]
	fixture.pool.mu.RUnlock()
	close(sleepRelease)
	select {
	case <-session.heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat close failure did not finish cleanup")
	}
	if fixture.pool.isOwned(uid) {
		t.Fatal("heartbeat close failure retained session ownership")
	}
	snapshot := fixture.pool.Snapshot()
	if snapshot.CloseReasons.HeartbeatFailed != 1 || snapshot.CloseReasons.TransportCloseFailed != 1 ||
		snapshot.Online != 0 || snapshot.Closing != 0 {
		t.Fatalf("heartbeat close-failure diagnostics = %+v", snapshot)
	}
}

func TestSessionPoolRecordsBoundedConnectAndConversationSyncLatency(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	fixture.factory.onConnect = func() { fixture.clock.Set(fixture.clock.Now().Add(20 * time.Millisecond)) }
	fixture.syncer.onSync = func() { fixture.clock.Set(fixture.clock.Now().Add(50 * time.Millisecond)) }

	uid := fixture.identity.UID(19)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 19, LoginOrdinal: 1}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	snapshot := fixture.pool.Snapshot()
	if snapshot.GatewayConnectLatency.Count != 1 || snapshot.GatewayConnectLatency.SumNanos != uint64(20*time.Millisecond) ||
		snapshot.GatewayConnectLatency.MaxNanos != uint64(20*time.Millisecond) || snapshot.GatewayConnectLatency.Buckets[5] != 1 {
		t.Fatalf("connect latency = %+v", snapshot.GatewayConnectLatency)
	}
	if snapshot.ConversationSyncLatency.Count != 1 || snapshot.ConversationSyncLatency.SumNanos != uint64(50*time.Millisecond) ||
		snapshot.ConversationSyncLatency.MaxNanos != uint64(50*time.Millisecond) || snapshot.ConversationSyncLatency.Buckets[6] != 1 {
		t.Fatalf("sync latency = %+v", snapshot.ConversationSyncLatency)
	}
	if snapshot.ConversationSyncThresholds.Count != 1 || snapshot.ConversationSyncThresholds.AboveP99 != 0 ||
		snapshot.ConversationSyncThresholds.AboveP999 != 0 || snapshot.ConversationSyncThresholds.P999Limit != 3*time.Second {
		t.Fatalf("exact sync thresholds = %+v", snapshot.ConversationSyncThresholds)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

func TestSessionPoolCountsExactSyncLatencyAcrossThreeSecondBoundary(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	fixture.syncer.onSync = func() { fixture.clock.Set(fixture.clock.Now().Add(4 * time.Second)) }
	uid := fixture.identity.UID(20)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 20, LoginOrdinal: 1}); err != nil {
		t.Fatal(err)
	}
	thresholds := fixture.pool.Snapshot().ConversationSyncThresholds
	if thresholds.Count != 1 || thresholds.AboveP99 != 1 || thresholds.AboveP999 != 1 || thresholds.Above10Seconds != 0 {
		t.Fatalf("exact sync thresholds = %+v", thresholds)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPoolRecordsRealConnectAndSyncOutcomesWithStableEvidence(t *testing.T) {
	tests := []struct {
		name                 string
		configure            func(*sessionTestFixture, context.CancelFunc)
		wantFactoryFailed    uint64
		wantFactoryCanceled  uint64
		wantConnectStarted   uint64
		wantConnectCompleted uint64
		wantConnectFailed    uint64
		wantConnectCanceled  uint64
		wantSyncStarted      uint64
		wantSyncCompleted    uint64
		wantSyncFailed       uint64
		wantSyncCanceled     uint64
		wantStage            LoginSyncStage
		wantReason           string
		wantClassification   SyncClassification
		wantEvidenceStage    EvidenceStage
		wantEvidenceCode     FailureCode
		wantEvidenceClass    FailureClass
		wantConnectHistogram bool
		wantSyncHistogram    bool
		wantSuccess          bool
	}{
		{
			name: "factory failure",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.factory.newSessionErr = errors.New("secret factory raw uid")
			},
			wantFactoryFailed: 1, wantStage: LoginSyncStageFactory, wantReason: LoginSyncReasonTransport,
			wantEvidenceStage: EvidenceStageSessionFactory, wantEvidenceCode: FailureCodeSessionFactoryFailed, wantEvidenceClass: FailureClassHarness,
		},
		{
			name: "factory canceled",
			configure: func(fixture *sessionTestFixture, cancel context.CancelFunc) {
				cancel()
				fixture.factory.newSessionErr = context.Canceled
			},
			wantFactoryCanceled: 1, wantStage: LoginSyncStageFactory, wantReason: LoginSyncReasonCanceled,
		},
		{
			name: "factory internal timeout",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.factory.newSessionErr = context.DeadlineExceeded
			},
			wantFactoryFailed: 1, wantStage: LoginSyncStageFactory, wantReason: LoginSyncReasonTransport,
			wantEvidenceStage: EvidenceStageSessionFactory, wantEvidenceCode: FailureCodeSessionFactoryFailed, wantEvidenceClass: FailureClassHarness,
		},
		{
			name: "connect transport failure",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.factory.connectErr = errors.New("secret connect raw uid")
			},
			wantConnectStarted: 1, wantConnectFailed: 1, wantStage: LoginSyncStageConnect, wantReason: LoginSyncReasonTransport,
			wantEvidenceStage: EvidenceStageConnect, wantEvidenceCode: FailureCodeSessionConnectFailed, wantEvidenceClass: FailureClassHarness,
			wantConnectHistogram: true,
		},
		{
			name: "connect internal timeout",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.factory.connectErr = context.DeadlineExceeded
			},
			wantConnectStarted: 1, wantConnectFailed: 1, wantStage: LoginSyncStageConnect, wantReason: LoginSyncReasonTransport,
			wantEvidenceStage: EvidenceStageConnect, wantEvidenceCode: FailureCodeSessionConnectFailed, wantEvidenceClass: FailureClassHarness,
			wantConnectHistogram: true,
		},
		{
			name: "sync transport failure",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.syncer.syncErr = errors.New("secret sync raw uid")
			},
			wantConnectStarted: 1, wantConnectCompleted: 1, wantSyncStarted: 1, wantSyncFailed: 1,
			wantStage: LoginSyncStageSync, wantReason: LoginSyncReasonTransport,
			wantEvidenceStage: EvidenceStageSync, wantEvidenceCode: FailureCodeSessionSyncFailed, wantEvidenceClass: FailureClassHarness,
			wantConnectHistogram: true, wantSyncHistogram: true,
		},
		{
			name: "sync internal timeout",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.syncer.syncErr = context.DeadlineExceeded
			},
			wantConnectStarted: 1, wantConnectCompleted: 1, wantSyncStarted: 1, wantSyncFailed: 1,
			wantStage: LoginSyncStageSync, wantReason: LoginSyncReasonTransport,
			wantEvidenceStage: EvidenceStageSync, wantEvidenceCode: FailureCodeSessionSyncFailed, wantEvidenceClass: FailureClassHarness,
			wantConnectHistogram: true, wantSyncHistogram: true,
		},
		{
			name: "sync validation failure",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.syncer.rows = make([]target.ConversationSyncConversation, conversationSyncMaxConversations)
			},
			wantConnectStarted: 1, wantConnectCompleted: 1, wantSyncStarted: 1, wantSyncFailed: 1,
			wantStage: LoginSyncStageSync, wantReason: "conversation_limit_reached",
			wantEvidenceStage: EvidenceStageSync, wantEvidenceCode: FailureCodeSessionSyncValidation, wantEvidenceClass: FailureClassHarness,
			wantConnectHistogram: true, wantSyncHistogram: true,
		},
		{
			name: "sync product validation failure",
			configure: func(fixture *sessionTestFixture, _ context.CancelFunc) {
				fixture.syncer.rows = []target.ConversationSyncConversation{{}}
			},
			wantConnectStarted: 1, wantConnectCompleted: 1, wantSyncStarted: 1, wantSyncFailed: 1,
			wantStage: LoginSyncStageSync, wantReason: "conversation_identity_invalid", wantClassification: SyncClassificationProductFailure,
			wantEvidenceStage: EvidenceStageSync, wantEvidenceCode: FailureCodeSessionSyncValidation, wantEvidenceClass: FailureClassReceive,
			wantConnectHistogram: true, wantSyncHistogram: true,
		},
		{
			name: "connect canceled",
			configure: func(fixture *sessionTestFixture, cancel context.CancelFunc) {
				fixture.factory.onConnect = func() {
					fixture.clock.Set(fixture.clock.Now().Add(20 * time.Millisecond))
					cancel()
				}
				fixture.factory.connectErr = context.Canceled
			},
			wantConnectStarted: 1, wantConnectCanceled: 1, wantStage: LoginSyncStageConnect, wantReason: LoginSyncReasonCanceled,
			wantConnectHistogram: true,
		},
		{
			name: "sync canceled",
			configure: func(fixture *sessionTestFixture, cancel context.CancelFunc) {
				fixture.syncer.onSync = func() {
					fixture.clock.Set(fixture.clock.Now().Add(50 * time.Millisecond))
					cancel()
				}
			},
			wantConnectStarted: 1, wantConnectCompleted: 1, wantSyncStarted: 1, wantSyncCanceled: 1,
			wantStage: LoginSyncStageSync, wantReason: LoginSyncReasonCanceled,
			wantConnectHistogram: true, wantSyncHistogram: true,
		},
		{
			name:               "success",
			configure:          func(*sessionTestFixture, context.CancelFunc) {},
			wantConnectStarted: 1, wantConnectCompleted: 1, wantSyncStarted: 1, wantSyncCompleted: 1,
			wantConnectHistogram: true, wantSyncHistogram: true, wantSuccess: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSessionTestFixture(t)
			fixture.factory.onConnect = func() { fixture.clock.Set(fixture.clock.Now().Add(20 * time.Millisecond)) }
			fixture.syncer.onSync = func() { fixture.clock.Set(fixture.clock.Now().Add(50 * time.Millisecond)) }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			test.configure(&fixture, cancel)

			uid := fixture.identity.UID(19)
			_, loginErr := fixture.pool.Login(ctx, SessionLogin{UID: uid, UserIndex: 19, LoginOrdinal: 1})
			if test.wantSuccess != (loginErr == nil) {
				t.Fatalf("Login error = %v, success want %v", loginErr, test.wantSuccess)
			}
			if loginErr != nil {
				failure, ok := LoginSyncFailureOf(loginErr)
				wantClassification := test.wantClassification
				if wantClassification == "" {
					wantClassification = SyncClassificationHarnessInvalid
				}
				if !ok || failure.Stage != test.wantStage || failure.Reason != test.wantReason || failure.Classification != wantClassification {
					t.Fatalf("closed login failure = %+v/%v, want stage=%q reason=%q", failure, ok, test.wantStage, test.wantReason)
				}
				if strings.Contains(loginErr.Error(), "secret") || strings.Contains(loginErr.Error(), uid) {
					t.Fatalf("login error leaked raw cause or UID: %q", loginErr)
				}
				if errors.Unwrap(loginErr) != nil {
					t.Fatalf("login error publicly exposed a raw cause: %T", errors.Unwrap(loginErr))
				}
			}

			snapshot := fixture.pool.Snapshot()
			if snapshot.FactoryFailed != test.wantFactoryFailed || snapshot.FactoryCanceled != test.wantFactoryCanceled ||
				snapshot.ConnectStarted != test.wantConnectStarted || snapshot.ConnectCompleted != test.wantConnectCompleted ||
				snapshot.ConnectFailed != test.wantConnectFailed || snapshot.ConnectCanceled != test.wantConnectCanceled ||
				snapshot.SyncStarted != test.wantSyncStarted || snapshot.SyncCompleted != test.wantSyncCompleted ||
				snapshot.SyncFailed != test.wantSyncFailed || snapshot.SyncCanceled != test.wantSyncCanceled {
				t.Fatalf("outcome counters = %+v", snapshot)
			}
			if (snapshot.GatewayConnectLatency.Count == 1) != test.wantConnectHistogram ||
				(snapshot.ConversationSyncLatency.Count == 1) != test.wantSyncHistogram {
				t.Fatalf("outcome histograms = connect=%+v sync=%+v", snapshot.GatewayConnectLatency, snapshot.ConversationSyncLatency)
			}
			if test.wantConnectHistogram && snapshot.GatewayConnectLatency.Buckets[5] != 1 {
				t.Fatalf("connect failure/success latency = %+v", snapshot.GatewayConnectLatency)
			}
			if test.wantSyncHistogram && snapshot.ConversationSyncLatency.Buckets[6] != 1 {
				t.Fatalf("sync failure/success latency = %+v", snapshot.ConversationSyncLatency)
			}
			evidence := fixture.verifier.evidence.Snapshot()
			if test.wantEvidenceCode == 0 {
				if len(evidence.Classes) != 0 {
					t.Fatalf("cancellation/success manufactured evidence: %+v", evidence)
				}
			} else if !evidenceContains(evidence, test.wantEvidenceClass, test.wantEvidenceStage, test.wantEvidenceCode) {
				t.Fatalf("stable outcome evidence = %+v", evidence)
			}
			if test.name == "connect transport failure" && len(fixture.syncer.requests()) != 0 {
				t.Fatalf("connect failure started sync: %+v", fixture.syncer.requests())
			}
			if test.wantSuccess {
				if _, duplicateErr := fixture.pool.Login(ctx, SessionLogin{UID: uid, UserIndex: 19, LoginOrdinal: 2}); !errors.Is(duplicateErr, errSessionOnline) {
					t.Fatalf("duplicate reservation error = %v, want %v", duplicateErr, errSessionOnline)
				}
				afterConflict := fixture.pool.Snapshot()
				if afterConflict.SyncFailed != 0 || afterConflict.SyncStarted != 1 || afterConflict.ConnectStarted != 1 {
					t.Fatalf("reservation conflict polluted real outcomes: %+v", afterConflict)
				}
				if err := fixture.pool.Logout(uid); err != nil {
					t.Fatalf("Logout: %v", err)
				}
			}
		})
	}
}

func evidenceContains(snapshot EvidenceSnapshot, class FailureClass, stage EvidenceStage, code FailureCode) bool {
	for _, classSnapshot := range snapshot.Classes {
		if classSnapshot.Class != class {
			continue
		}
		for _, example := range append(append([]EvidenceExample(nil), classSnapshot.First...), classSnapshot.Last...) {
			if example.Stage == stage && example.Code == code {
				return true
			}
		}
	}
	return false
}

func TestSessionWKProtoAdapterBindsExistingClientWithoutDialing(t *testing.T) {
	t.Parallel()
	if _, err := NewWKProtoSessionAdapter(nil); !errors.Is(err, errSessionConfig) {
		t.Fatalf("nil adapter error = %v", err)
	}
	client, err := wkproto.NewClient(wkproto.ClientConfig{Addr: "127.0.0.1:1", FrameBufferSize: 8})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	adapter, err := NewWKProtoSessionAdapter(client)
	if err != nil {
		t.Fatalf("NewWKProtoSessionAdapter: %v", err)
	}
	snapshot, snapshotErr := adapter.QueueSnapshot(context.Background())
	if snapshotErr != nil || snapshot.Depth != 0 || snapshot.Capacity != 8 || snapshot.Inflight != 0 {
		t.Fatalf("disconnected queue snapshot = %+v", snapshot)
	}
}

func TestSessionPoolRecordsSendackAtTransportObservationTime(t *testing.T) {
	fixture := newSessionTestFixture(t)
	completed := make(chan struct{}, 1)
	if err := fixture.pool.setEngineObservers(
		func(_ string, _ *frame.SendackPacket, _ error) { completed <- struct{}{} },
		func(string, uint64, string) {},
	); err != nil {
		t.Fatalf("setEngineObservers: %v", err)
	}
	uid := fixture.identity.UID(30)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 30, LoginOrdinal: 1}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer fixture.pool.CloseAll()

	registeredAt := fixture.clock.Now()
	logical, err := fixture.traffic.NewLogicalSend(0, 1, TrafficPerson, uid, fixture.identity.UID(31))
	if err != nil {
		t.Fatalf("NewLogicalSend: %v", err)
	}
	if err := fixture.verifier.RegisterSend(logical, registeredAt, SendLatencyHot); err != nil {
		t.Fatalf("RegisterSend: %v", err)
	}
	client := fixture.factory.clients()[0]
	client.setTiming(SessionFrameTiming{
		PendingStartedAt: registeredAt.Add(5 * time.Millisecond),
		WriteStartedAt:   registeredAt.Add(25 * time.Millisecond),
		ObservedAt:       registeredAt.Add(100 * time.Millisecond),
	})
	fixture.clock.Set(registeredAt.Add(2 * time.Second))
	client.frames <- &frame.SendackPacket{
		ClientSeq: 1, ClientMsgNo: logical.ClientMsgNo, MessageID: 1, MessageSeq: 1, ReasonCode: frame.ReasonSuccess,
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("SENDACK was not verified")
	}

	histogram := fixture.verifier.Snapshot().HotSendackLatency
	if histogram.Count != 1 || histogram.SumNanos != uint64(100*time.Millisecond) || histogram.Buckets[7] != 1 {
		t.Fatalf("hot SENDACK latency = %+v, want exact transport-observed 100ms", histogram)
	}
	sessions := fixture.pool.Snapshot()
	if sessions.SendPendingToWriteLatency.Count != 1 ||
		sessions.SendPendingToWriteLatency.SumNanos != uint64(20*time.Millisecond) {
		t.Fatalf("pending-to-write latency = %+v, want 20ms", sessions.SendPendingToWriteLatency)
	}
	if sessions.SendWriteToAckLatency.Count != 1 ||
		sessions.SendWriteToAckLatency.SumNanos != uint64(75*time.Millisecond) {
		t.Fatalf("write-to-ACK latency = %+v, want 75ms", sessions.SendWriteToAckLatency)
	}
}

func TestSessionPoolActivatesRelationshipOnlyWhileBothEndpointsOnline(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	edge := fixture.graph.Incoming(8).Items[0]

	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: 1}); err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	if fixture.pool.CanActivate(edge) {
		t.Fatal("edge activated with peer offline")
	}
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: 2}); err != nil {
		t.Fatalf("peer Login: %v", err)
	}
	if !fixture.pool.CanActivate(edge) {
		t.Fatal("edge did not activate with both endpoints online")
	}
	if err := fixture.pool.Logout(edge.OwnerUID); err != nil {
		t.Fatalf("owner Logout: %v", err)
	}
	if fixture.pool.CanActivate(edge) {
		t.Fatal("edge remained active after owner logout")
	}
	if err := fixture.pool.Logout(edge.PeerUID); err != nil {
		t.Fatalf("peer Logout: %v", err)
	}
}

func TestSessionPoolLogoutJoinsOrderedDrainBeforeReleaseRecipient(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(31)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 31, LoginOrdinal: 3}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	for ordinal := uint64(1); ordinal <= 2; ordinal++ {
		logical, err := fixture.traffic.NewLogicalSend(0, ordinal, TrafficGroup, fixture.identity.UID(2), "group-drain")
		if err != nil {
			t.Fatalf("NewLogicalSend(%d): %v", ordinal, err)
		}
		payload, err := fixture.traffic.BuildPayload(logical, 256)
		if err != nil {
			t.Fatalf("BuildPayload(%d): %v", ordinal, err)
		}
		client.frames <- &frame.RecvPacket{
			MessageID: int64(ordinal), MessageSeq: ordinal, ClientMsgNo: logical.ClientMsgNo,
			FromUID: logical.Sender, ChannelID: logical.Target, ChannelType: frame.ChannelTypeGroup, Payload: payload,
		}
	}
	for want := uint64(1); want <= 2; want++ {
		got := <-client.acked
		if got != want {
			t.Fatalf("RECVACK order = %d, want %d", got, want)
		}
	}
	if got := fixture.verifier.Snapshot().SequenceCurrent; got != 1 {
		t.Fatalf("sequence current before logout = %d, want 1 channel", got)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := fixture.verifier.Snapshot().SequenceCurrent; got != 0 {
		t.Fatalf("sequence current after joined logout = %d, want 0", got)
	}
	select {
	case <-client.readExited:
	default:
		t.Fatal("Logout returned before receive drain exited")
	}
}

func TestSessionPoolUnexpectedReadExitIsBoundedHarnessEvidence(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(32)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 32, LoginOrdinal: 4}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	fixture.pool.mu.RLock()
	drainDone := fixture.pool.online[uid].done
	fixture.pool.mu.RUnlock()
	if err := client.Close(); err != nil {
		t.Fatalf("unexpected Close: %v", err)
	}
	<-drainDone
	snapshot := fixture.pool.Snapshot()
	if snapshot.ReadErrors != 1 || snapshot.CloseReasons.ReadFailed != 1 ||
		fixture.verifier.EvidenceSnapshot().Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("unexpected read evidence = pool %+v verifier %+v", snapshot, fixture.verifier.EvidenceSnapshot())
	}
	if err := fixture.pool.Logout(uid); !errors.Is(err, errSessionOffline) {
		t.Fatalf("Logout after atomic reader removal = %v, want offline", err)
	}
}

func TestSessionPoolNonTerminalAsyncSendErrorKeepsOrderedDrainOnline(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(33)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 33, LoginOrdinal: 5}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	<-client.readEntered
	client.readErrors <- &sessionFakeReadError{kind: wkproto.ReadErrorNonTerminal, clientSeq: 1, clientMsgNo: "stable-send"}
	<-client.readReturned
	<-client.readEntered
	if !fixture.pool.IsOnline(uid) {
		t.Fatal("non-terminal async SEND error removed the online session")
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

func TestSessionPoolTerminalRemoteReadAtomicallyRemovesOnlineOwnership(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(34)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 34, LoginOrdinal: 6}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	fixture.pool.mu.RLock()
	drainDone := fixture.pool.online[uid].done
	fixture.pool.mu.RUnlock()
	<-client.readEntered
	client.readErrors <- &sessionFakeReadError{kind: wkproto.ReadErrorTerminal}
	<-drainDone
	if fixture.pool.IsOnline(uid) {
		t.Fatal("terminal remote read retained online ownership")
	}
	if !client.closed() {
		t.Fatal("terminal remote read did not close the session")
	}
	if err := fixture.pool.TrySend(context.Background(), uid, &frame.SendPacket{}); !errors.Is(err, errSessionOffline) {
		t.Fatalf("TrySend after terminal read = %v, want offline", err)
	}
	if got := fixture.verifier.EvidenceSnapshot().Classification; got != SyncClassificationProductFailure {
		t.Fatalf("terminal remote read classification = %q, want product_failure", got)
	}
	if got := fixture.pool.Snapshot().CloseReasons.RemoteTerminal; got != 1 {
		t.Fatalf("remote-terminal close reasons = %d, want 1", got)
	}
}

func TestSessionPoolCountsTransportCloseFailureWithoutRawError(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(38)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 38, LoginOrdinal: 10}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	fixture.factory.clients()[0].closeErr = errors.New("private transport close detail")
	if err := fixture.pool.Logout(uid); err == nil {
		t.Fatal("Logout returned nil transport-close error")
	}
	snapshot := fixture.pool.Snapshot()
	if snapshot.CloseReasons.ExplicitLogout != 1 || snapshot.CloseReasons.TransportCloseFailed != 1 {
		t.Fatalf("transport-close diagnostics = %+v", snapshot.CloseReasons)
	}
}

func TestSessionPoolTerminalEvidencePrecedesOfflinePublicationAndBlockingClose(t *testing.T) {
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(35)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 35, LoginOrdinal: 7}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	closeEntered := make(chan struct{}, 1)
	closeRelease := make(chan struct{})
	client.closeEntered = closeEntered
	client.closeRelease = closeRelease
	defer func() {
		select {
		case <-closeRelease:
		default:
			close(closeRelease)
		}
	}()
	fixture.pool.mu.RLock()
	drainDone := fixture.pool.online[uid].done
	fixture.pool.mu.RUnlock()
	<-client.readEntered
	client.readErrors <- &sessionFakeReadError{kind: wkproto.ReadErrorTerminal}
	<-closeEntered
	if fixture.pool.IsOnline(uid) {
		t.Fatal("terminal session remained online after detach reached socket close")
	}
	if !fixture.pool.isOwned(uid) {
		t.Fatal("terminal session released UID ownership before close and verifier cleanup")
	}
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 35, LoginOrdinal: 8}); !errors.Is(err, errSessionOnline) {
		t.Fatalf("Login during terminal cleanup = %v, want %v", err, errSessionOnline)
	}
	if got := fixture.verifier.EvidenceSnapshot().Classification; got != SyncClassificationProductFailure {
		t.Fatalf("offline state became observable before terminal evidence: %q", got)
	}
	close(closeRelease)
	<-drainDone
	if fixture.pool.isOwned(uid) {
		t.Fatal("terminal session retained UID ownership after verifier cleanup")
	}
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 35, LoginOrdinal: 9}); err != nil {
		t.Fatalf("Login after terminal cleanup: %v", err)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout replacement: %v", err)
	}
}

func TestSessionPoolSnapshotDoesNotHoldOwnershipLockAcrossClientQueueSnapshot(t *testing.T) {
	for _, operation := range []string{"login", "detach"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			fixture := newSessionTestFixture(t)
			uid := fixture.identity.UID(36)
			if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 36, LoginOrdinal: 8}); err != nil {
				t.Fatalf("seed Login: %v", err)
			}
			client := fixture.factory.clients()[0]
			queueEntered := make(chan struct{}, 1)
			queueRelease := make(chan struct{})
			client.queueSnapshotEntered = queueEntered
			client.queueSnapshotRelease = queueRelease
			released := false
			defer func() {
				if !released {
					close(queueRelease)
				}
				_ = fixture.pool.CloseAll()
			}()
			snapshotDone := make(chan struct{})
			go func() {
				_ = fixture.pool.Snapshot()
				close(snapshotDone)
			}()
			<-queueEntered

			operationDone := make(chan error, 1)
			switch operation {
			case "login":
				go func() {
					secondUID := fixture.identity.UID(37)
					_, err := fixture.pool.Login(context.Background(), SessionLogin{UID: secondUID, UserIndex: 37, LoginOrdinal: 9})
					operationDone <- err
				}()
			case "detach":
				closeEntered := make(chan struct{}, 1)
				client.closeEntered = closeEntered
				<-client.readEntered
				client.readErrors <- &sessionFakeReadError{kind: wkproto.ReadErrorTerminal}
				go func() {
					<-closeEntered
					operationDone <- nil
				}()
			}
			select {
			case err := <-operationDone:
				if err != nil {
					t.Fatalf("%s while queue snapshot blocked: %v", operation, err)
				}
			case <-time.After(20 * time.Millisecond):
				close(queueRelease)
				released = true
				<-snapshotDone
				t.Fatalf("%s blocked behind QueueSnapshot", operation)
			}
			close(queueRelease)
			released = true
			<-snapshotDone
		})
	}
}

func TestSessionPoolStartsIndependentLoginsConcurrentlyWithinBound(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	factory := &parallelSessionFactory{entered: entered, release: release}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: fixture.identity, Schedule: fixture.schedule, Catalog: fixture.catalog, Factory: factory,
		Syncer: fixture.syncer, Verifier: fixture.verifier, Clock: fixture.clock,
		DeviceID: "parallel-login", StartingCapacity: 2,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	errs := make(chan error, 2)
	for index := uint64(40); index < 42; index++ {
		index := index
		go func() {
			_, loginErr := pool.Login(context.Background(), SessionLogin{
				UID: fixture.identity.UID(index), UserIndex: index, LoginOrdinal: index,
			})
			errs <- loginErr
		}()
	}
	<-entered
	<-entered
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Login: %v", err)
		}
	}
	if snapshot := pool.Snapshot(); snapshot.Online != 2 || snapshot.Starting != 0 {
		t.Fatalf("parallel pool snapshot = %+v", snapshot)
	}
	if err := pool.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	if got := pool.Snapshot().CloseReasons.GenerationStop; got != 2 {
		t.Fatalf("generation-stop close reasons = %d, want 2", got)
	}
}

func TestSessionPoolOnlineRouteIndexesAllocateNoLookupStateAndReleaseAllChurn(t *testing.T) {
	fixture := newSessionTestFixture(t)
	catalog, err := NewGroupCatalog(fixture.identity, LocalConfig().Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	group, err := catalog.Group(0)
	if err != nil {
		t.Fatalf("Group(0): %v", err)
	}
	memberUIDs := make([]string, 0, 2)
	for memberOrdinal := 0; memberOrdinal < 2; memberOrdinal++ {
		memberIndex, err := group.MemberIndex(memberOrdinal)
		if err != nil {
			t.Fatalf("MemberIndex(%d): %v", memberOrdinal, err)
		}
		uid := fixture.identity.UID(memberIndex)
		memberUIDs = append(memberUIDs, uid)
		if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: memberIndex, LoginOrdinal: uint64(memberOrdinal)}); err != nil {
			t.Fatalf("Login member %d: %v", memberOrdinal, err)
		}
	}
	if allocations := testing.AllocsPerRun(1_000, func() {
		if _, ok := fixture.pool.onlineGroupMember(group, 0, true); !ok {
			panic("online group route disappeared")
		}
	}); allocations != 0 {
		t.Fatalf("online group route lookup allocations = %.2f, want 0", allocations)
	}
	for _, uid := range memberUIDs {
		if err := fixture.pool.Logout(uid); err != nil {
			t.Fatalf("member Logout(%q): %v", uid, err)
		}
	}
	for index := uint64(100); index < 300; index++ {
		uid := fixture.identity.UID(index)
		if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index}); err != nil {
			t.Fatalf("churn Login(%d): %v", index, err)
		}
		if err := fixture.pool.Logout(uid); err != nil {
			t.Fatalf("churn Logout(%d): %v", index, err)
		}
	}
	fixture.pool.mu.RLock()
	online, byIndex := len(fixture.pool.online), len(fixture.pool.onlineByIndex)
	fixture.pool.mu.RUnlock()
	if online != 0 || byIndex != 0 {
		t.Fatalf("online route indexes retained churn history: online=%d by_index=%d", online, byIndex)
	}
}

func TestSessionPoolGrantSelectorSkipsDeadlineExpiredMemberRetainedBySendLease(t *testing.T) {
	fixture := newSessionTestFixture(t)
	group, err := fixture.catalog.Group(0)
	if err != nil {
		t.Fatalf("Group(0): %v", err)
	}
	members := make([]SessionLogin, 0, 2)
	for memberOrdinal := 0; memberOrdinal < 2; memberOrdinal++ {
		memberIndex, memberErr := group.MemberIndex(memberOrdinal)
		if memberErr != nil {
			t.Fatalf("MemberIndex(%d): %v", memberOrdinal, memberErr)
		}
		login := SessionLogin{UID: fixture.identity.UID(memberIndex), UserIndex: memberIndex, LoginOrdinal: uint64(memberOrdinal)}
		if _, loginErr := fixture.pool.Login(context.Background(), login); loginErr != nil {
			t.Fatalf("Login member %d: %v", memberOrdinal, loginErr)
		}
		members = append(members, login)
	}
	defer func() {
		if err := fixture.pool.CloseAll(); err != nil {
			t.Errorf("CloseAll: %v", err)
		}
	}()

	if !fixture.pool.acquireSendLease(members[0].UID) {
		t.Fatal("acquire existing SEND lease for expiring group member")
	}
	defer func() {
		if !fixture.pool.releaseSendLease(members[0].UID) {
			t.Error("release existing SEND lease for expired group member")
		}
	}()
	fixture.pool.mu.Lock()
	fixture.pool.online[members[0].UID].snapshot.Deadline = fixture.clock.Now()
	fixture.pool.mu.Unlock()

	selected, ok := fixture.pool.onlineGroupMemberExcluding(group, 0, true, fixture.clock.Now(), nil)
	if !ok {
		t.Fatal("no send-eligible group member selected")
	}
	if selected.UID != members[1].UID {
		t.Fatalf("selected UID = %q, want fresh member %q", selected.UID, members[1].UID)
	}
}

func TestSessionPoolTransportAdmissionRejectionsSurviveSessionChurn(t *testing.T) {
	fixture := newSessionTestFixture(t)
	fixture.factory.sendErr = wkclient.ErrSendQueueFull
	uid := fixture.identity.UID(40)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 40, LoginOrdinal: 40}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := fixture.pool.TrySend(context.Background(), uid, &frame.SendPacket{}); !errors.Is(err, wkclient.ErrSendQueueFull) {
		t.Fatalf("TrySend error = %v, want %v", err, wkclient.ErrSendQueueFull)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	snapshot := fixture.pool.Snapshot()
	if snapshot.TransportAdmissionRejected != 1 || snapshot.Online != 0 {
		t.Fatalf("post-churn transport evidence = %+v, want one retained rejection and no online session", snapshot)
	}
}

type sessionTestFixture struct {
	identity *IdentitySpace
	schedule ScheduleModel
	graph    RelationshipGraph
	traffic  TrafficModel
	catalog  GroupCatalog
	verifier *Verifier
	clock    *sessionFakeClock
	events   *sessionEventLog
	factory  *sessionFakeFactory
	syncer   *sessionFakeSyncer
	pool     *SessionPool
}

func newSessionTestFixture(t *testing.T) sessionTestFixture {
	t.Helper()
	cfg := LocalConfig()
	identity, err := NewIdentitySpace("session-test", 71, uint64(cfg.Workload.Workers))
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
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder: %v", err)
	}
	verifier, err := NewVerifier(traffic, VerifierConfig{
		PendingCapacity: 128, SequenceCapacity: 128, CorrelationCapacity: 16, CorrelationDeadline: time.Minute,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	events := &sessionEventLog{}
	factory := &sessionFakeFactory{events: events}
	syncer := &sessionFakeSyncer{events: events}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: identity, Schedule: schedule, Catalog: catalog, Factory: factory, Syncer: syncer,
		Verifier: verifier, Clock: clock, DeviceID: "wkbench-lifecycle", StartingCapacity: 128,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	return sessionTestFixture{
		identity: identity, schedule: schedule, graph: graph, traffic: traffic, catalog: catalog, verifier: verifier,
		clock: clock, events: events, factory: factory, syncer: syncer, pool: pool,
	}
}

type parallelSessionFactory struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (f *parallelSessionFactory) NewSession(_ context.Context, _, _ string) (SessionClient, error) {
	return &parallelSessionClient{
		entered: f.entered, release: f.release, stop: make(chan struct{}), readExited: make(chan struct{}),
	}, nil
}

type parallelSessionClient struct {
	entered    chan<- struct{}
	release    <-chan struct{}
	stop       chan struct{}
	readExited chan struct{}
	closeOnce  sync.Once
}

func (c *parallelSessionClient) Connect(ctx context.Context, _, _ string) error {
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *parallelSessionClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	select {
	case <-ctx.Done():
		c.closeReadExited()
		return nil, ctx.Err()
	case <-c.stop:
		c.closeReadExited()
		return nil, errors.New("closed")
	}
}
func (c *parallelSessionClient) TrySend(context.Context, *frame.SendPacket) error    { return nil }
func (c *parallelSessionClient) Ping(context.Context) error                          { return nil }
func (c *parallelSessionClient) AckRecv(context.Context, *frame.RecvackPacket) error { return nil }
func (c *parallelSessionClient) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	return nil
}
func (c *parallelSessionClient) QueueSnapshot(context.Context) (SessionQueueSnapshot, error) {
	return SessionQueueSnapshot{}, nil
}
func (c *parallelSessionClient) ReadErrorInfo(error) (wkproto.ReadErrorInfo, bool) {
	return wkproto.ReadErrorInfo{}, false
}
func (c *parallelSessionClient) closeReadExited() {
	select {
	case <-c.readExited:
	default:
		close(c.readExited)
	}
}

func assertFullSyncRequest(t *testing.T, got target.ConversationSyncRequest, uid string) {
	t.Helper()
	want := NewConversationSyncRequest(uid)
	if got != want {
		t.Fatalf("sync request = %+v, want %+v", got, want)
	}
}

type sessionFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *sessionFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *sessionFakeClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type sessionEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *sessionEventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *sessionEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *sessionEventLog) reset() {
	l.mu.Lock()
	l.events = nil
	l.mu.Unlock()
}

type sessionFakeFactory struct {
	mu            sync.Mutex
	events        *sessionEventLog
	tokensV       []string
	clientsV      []*sessionFakeClient
	onConnect     func()
	connectErr    error
	newSessionErr error
	pingEntered   chan<- struct{}
	pingRelease   <-chan struct{}
	pingErr       error
	sendErr       error
}

func (f *sessionFakeFactory) NewSession(_ context.Context, _ string, token string) (SessionClient, error) {
	f.events.add("factory")
	if f.newSessionErr != nil {
		return nil, f.newSessionErr
	}
	client := &sessionFakeClient{
		events: f.events, frames: make(chan frame.Frame, 8), acked: make(chan uint64, 8),
		readErrors: make(chan error, 8), readEntered: make(chan struct{}, 8), readReturned: make(chan struct{}, 8),
		stop: make(chan struct{}), readExited: make(chan struct{}), onConnect: f.onConnect, connectErr: f.connectErr,
		pingEntered: f.pingEntered, pingRelease: f.pingRelease, pingErr: f.pingErr, sendErr: f.sendErr,
	}
	f.mu.Lock()
	f.tokensV = append(f.tokensV, token)
	f.clientsV = append(f.clientsV, client)
	f.mu.Unlock()
	return client, nil
}

func (f *sessionFakeFactory) tokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokensV...)
}

func (f *sessionFakeFactory) clients() []*sessionFakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*sessionFakeClient(nil), f.clientsV...)
}

type sessionFakeSyncer struct {
	mu        sync.Mutex
	events    *sessionEventLog
	requestsV []target.ConversationSyncRequest
	onSync    func()
	rows      []target.ConversationSyncConversation
	syncErr   error
}

func (s *sessionFakeSyncer) ConversationSync(_ context.Context, request target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	s.events.add("sync")
	if s.onSync != nil {
		s.onSync()
	}
	s.mu.Lock()
	s.requestsV = append(s.requestsV, request)
	s.mu.Unlock()
	return s.rows, s.syncErr
}

func (s *sessionFakeSyncer) requests() []target.ConversationSyncRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]target.ConversationSyncRequest(nil), s.requestsV...)
}

type sessionFakeClient struct {
	mu                   sync.Mutex
	events               *sessionEventLog
	frames               chan frame.Frame
	readErrors           chan error
	readEntered          chan struct{}
	readReturned         chan struct{}
	acked                chan uint64
	stop                 chan struct{}
	readExited           chan struct{}
	closeEntered         chan struct{}
	closeRelease         <-chan struct{}
	closeErr             error
	closeDoesNotStop     bool
	queueSnapshotEntered chan<- struct{}
	queueSnapshotRelease <-chan struct{}
	closeOnce            sync.Once
	isClosed             bool
	onConnect            func()
	connectErr           error
	pingEntered          chan<- struct{}
	pingRelease          <-chan struct{}
	pingErr              error
	sendErr              error
	observedAt           time.Time
	timing               SessionFrameTiming
}

func (c *sessionFakeClient) Connect(_ context.Context, _, _ string) error {
	c.events.add("connect")
	if c.onConnect != nil {
		c.onConnect()
	}
	return c.connectErr
}

func (c *sessionFakeClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	c.readEntered <- struct{}{}
	select {
	case packet := <-c.frames:
		return packet, nil
	case err := <-c.readErrors:
		c.readReturned <- struct{}{}
		return nil, err
	case <-ctx.Done():
		c.closeReadExited()
		return nil, ctx.Err()
	case <-c.stop:
		c.closeReadExited()
		return nil, errors.New("session closed")
	}
}

func (c *sessionFakeClient) ReadFrameObserved(ctx context.Context) (frame.Frame, time.Time, error) {
	packet, err := c.ReadFrame(ctx)
	c.mu.Lock()
	observedAt := c.observedAt
	c.mu.Unlock()
	return packet, observedAt, err
}

func (c *sessionFakeClient) ReadFrameTiming(ctx context.Context) (frame.Frame, SessionFrameTiming, error) {
	packet, err := c.ReadFrame(ctx)
	c.mu.Lock()
	timing := c.timing
	c.mu.Unlock()
	return packet, timing, err
}

func (c *sessionFakeClient) setObservedAt(observedAt time.Time) {
	c.mu.Lock()
	c.observedAt = observedAt
	c.mu.Unlock()
}

func (c *sessionFakeClient) setTiming(timing SessionFrameTiming) {
	c.mu.Lock()
	c.timing = timing
	c.observedAt = timing.ObservedAt
	c.mu.Unlock()
}

func (c *sessionFakeClient) ReadErrorInfo(err error) (wkproto.ReadErrorInfo, bool) {
	var readErr *sessionFakeReadError
	if !errors.As(err, &readErr) {
		return wkproto.ReadErrorInfo{}, false
	}
	return wkproto.ReadErrorInfo{Kind: readErr.kind, ClientSeq: readErr.clientSeq, ClientMsgNo: readErr.clientMsgNo}, true
}

type sessionFakeReadError struct {
	kind        wkproto.ReadErrorKind
	clientSeq   uint64
	clientMsgNo string
}

func (e *sessionFakeReadError) Error() string { return "redacted fake read error" }

func (c *sessionFakeClient) TrySend(context.Context, *frame.SendPacket) error { return c.sendErr }

func (c *sessionFakeClient) Ping(context.Context) error {
	if c.pingEntered != nil {
		c.pingEntered <- struct{}{}
	}
	if c.pingRelease != nil {
		<-c.pingRelease
	}
	return c.pingErr
}

func (c *sessionFakeClient) AckRecv(_ context.Context, ack *frame.RecvackPacket) error {
	c.acked <- ack.MessageSeq
	return nil
}

func (c *sessionFakeClient) Close() error {
	if c.closeEntered != nil {
		c.closeEntered <- struct{}{}
	}
	if c.closeRelease != nil {
		<-c.closeRelease
	}
	if c.closeDoesNotStop {
		return c.closeErr
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.isClosed = true
		c.mu.Unlock()
		close(c.stop)
	})
	return c.closeErr
}

func (c *sessionFakeClient) QueueSnapshot(ctx context.Context) (SessionQueueSnapshot, error) {
	if c.queueSnapshotEntered != nil {
		select {
		case c.queueSnapshotEntered <- struct{}{}:
		case <-ctx.Done():
			return SessionQueueSnapshot{}, ctx.Err()
		}
	}
	if c.queueSnapshotRelease != nil {
		select {
		case <-c.queueSnapshotRelease:
		case <-ctx.Done():
			return SessionQueueSnapshot{}, ctx.Err()
		}
	}
	return SessionQueueSnapshot{}, nil
}

func (c *sessionFakeClient) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isClosed
}

func (c *sessionFakeClient) closeReadExited() {
	select {
	case <-c.readExited:
	default:
		close(c.readExited)
	}
}
