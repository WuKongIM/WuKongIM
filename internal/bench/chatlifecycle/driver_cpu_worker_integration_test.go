//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// WORKER-FAILURE-MATRIX.md was written before this file. These contracts keep
// the existing fake-clock/factory ports and the real Engine owner/joins; the
// private report inbox is observed independently from native worker evidence.
type driverCPUWorkerFixture struct {
	factory    engineWorkerGenerationFactory
	assignment WorkerAssignment
	clock      *sessionFakeClock
	sessions   *engineFakeFactory
	ticker     *manualWorkerGenerationTicker
}

func newDriverCPUWorkerFixture(t *testing.T, constructor WorkerGenerationFactory) driverCPUWorkerFixture {
	t.Helper()
	factory, ok := constructor.(engineWorkerGenerationFactory)
	if !ok {
		t.Fatalf("diagnostic constructor bypassed the existing Engine factory: %T", constructor)
	}
	config := LocalConfig()
	config.RunID = "abc"
	config.Workload.OnlineUsers = config.Workload.Workers
	config.Workload.Sessions = []DurationShare{{Percent: 100, Min: 3 * time.Hour, Max: 3 * time.Hour}}
	if err := config.Validate(); err != nil {
		t.Fatalf("worker CPU fixture config: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	sessions := &engineFakeFactory{}
	ticker := newManualWorkerGenerationTicker()
	factory.clock = clock
	factory.newSessionFactory = func(WorkerAssignment) (SessionClientFactory, error) { return sessions, nil }
	factory.newSyncer = func(WorkerAssignment) (ConversationSyncer, error) { return engineSyncer{}, nil }
	factory.newTicker = func(time.Duration) workerGenerationTicker { return ticker }
	return driverCPUWorkerFixture{
		factory: factory, clock: clock, sessions: sessions, ticker: ticker,
		assignment: WorkerAssignment{
			WorkerFence: WorkerFence{RunID: config.RunID, AssignmentID: "hello", Generation: 7},
			WorkerID:    1, WorkerCount: uint64(config.Workload.Workers), Config: config,
		},
	}
}

func (f driverCPUWorkerFixture) start(t *testing.T) *engineWorkerGeneration {
	t.Helper()
	worker, err := f.factory.New(f.assignment)
	if err != nil {
		t.Fatalf("New CPU worker generation: %v", err)
	}
	generation, ok := worker.(*engineWorkerGeneration)
	if !ok {
		t.Fatalf("factory returned a parallel generation runtime: %T", worker)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start CPU worker generation: %v", err)
	}
	t.Cleanup(worker.Stop)
	f.ticker.awaitReady(t)
	return generation
}

func (f driverCPUWorkerFixture) login(t *testing.T, generation *engineWorkerGeneration) string {
	t.Helper()
	uid := generation.engine.sessions.identity.UID(f.assignment.WorkerID)
	if _, err := generation.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: f.assignment.WorkerID, LoginOrdinal: 0}); err != nil {
		t.Fatalf("CPU worker Login: %v", err)
	}
	return uid
}

func (f driverCPUWorkerFixture) queueSends(t *testing.T, generation *engineWorkerGeneration, uid string, at time.Time, count int) {
	t.Helper()
	intents := make([]TrafficIntent, count)
	for index := range intents {
		logical, err := generation.engine.traffic.NewLogicalSend(f.assignment.WorkerID, uint64(1000+index), TrafficGroup, uid, "driver-private-channel")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := generation.engine.traffic.BuildPayload(logical, 256)
		if err != nil {
			t.Fatal(err)
		}
		intents[index] = TrafficIntent{Logical: logical, Packet: packetForTrafficIntent(logical, payload), Kind: TrafficGroup, PayloadBytes: len(payload), ChannelID: logical.Target}
	}
	queued := make(chan error, 1)
	if err := generation.engine.enqueueBlocking(engineCommand{run: func() {
		for _, intent := range intents {
			if !generation.engine.sessions.acquireSendLease(uid) {
				queued <- errors.New("sender lease unavailable")
				return
			}
			if err := generation.engine.addLeasedSendWorkBefore(intent, 0, at, at.Add(100*time.Millisecond)); err != nil {
				queued <- err
				return
			}
		}
		queued <- nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := <-queued; err != nil {
		t.Fatal(err)
	}
}

func assertDriverCPUWorkerNoReport(t *testing.T, reports <-chan DriverCPUReport, boundary string) {
	t.Helper()
	select {
	case report := <-reports:
		t.Fatalf("CPU report escaped at %s: %+v", boundary, report)
	default:
	}
}

func takeDriverCPUWorkerReport(t *testing.T, reports <-chan DriverCPUReport) DriverCPUReport {
	t.Helper()
	select {
	case report := <-reports:
		return report
	default:
		t.Fatal("joined terminal generation did not publish its CPU report before returning")
		return DriverCPUReport{}
	}
}

func assertDriverCPUWorkerReport(t *testing.T, generation *engineWorkerGeneration, report DriverCPUReport, expectedGeneration uint64) {
	t.Helper()
	// Independent known SHA-256 vectors for the fixture's raw "abc" / "hello".
	if report.RunHash != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" ||
		report.AssignmentHash != "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" || report.WorkerID != 1 {
		t.Fatalf("CPU report did not retain the exact anonymous assignment fence: %+v", report)
	}
	if report.Diagnostics == nil || report.Diagnostics.Generation != expectedGeneration || generation.engine.activeLoops.Load() != 0 {
		t.Fatalf("CPU report is missing, stale or published before Engine join: %+v", report)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"abc"`) || strings.Contains(string(body), `"hello"`) {
		t.Fatalf("raw worker fence entered the private CPU report: %s", body)
	}
}

func TestDriverCPUWorkerFactoryRejectsIncompleteOptInBeforeCollaboratorsOrProbe(t *testing.T) {
	for _, test := range []struct {
		name    string
		driver  bool
		probe   bool
		reports bool
	}{
		{"driver_required", false, true, true},
		{"inbox_required", true, true, false},
		{"probe_required", true, false, true},
		{"report_cannot_enable_driver", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			probeCalls, sessionFactories, syncers := 0, 0, 0
			options := WorkerDiagnosticOptions{Driver: test.driver}
			if test.probe {
				options.CPUClockProbe = func() DriverCPUClockSample { probeCalls++; return DriverCPUClockSample{} }
			}
			if test.reports {
				options.CPUReports = make(chan DriverCPUReport, 1)
			}
			fixture := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", options))
			fixture.factory.newSessionFactory = func(WorkerAssignment) (SessionClientFactory, error) {
				sessionFactories++
				return fixture.sessions, nil
			}
			fixture.factory.newSyncer = func(WorkerAssignment) (ConversationSyncer, error) {
				syncers++
				return engineSyncer{}, nil
			}
			worker, err := fixture.factory.New(fixture.assignment)
			if worker != nil || !errors.Is(err, errWorkerServerConfig) {
				t.Fatalf("incomplete CPU opt-in = %T, %v; want nil, config rejection", worker, err)
			}
			if probeCalls != 0 || sessionFactories != 0 || syncers != 0 {
				t.Fatalf("invalid options reached collaborators: probe=%d sessions=%d syncer=%d", probeCalls, sessionFactories, syncers)
			}
		})
	}
}

func TestDriverCPUWorkerLegacyConstructorsKeepCPUOff(t *testing.T) {
	for _, test := range []struct {
		name        string
		constructor WorkerGenerationFactory
		driver      bool
		attemptLink bool
	}{
		{"default", NewEngineWorkerGenerationFactory("bench-test-token"), false, false},
		{"driver_only", NewEngineWorkerGenerationFactoryWithDriverDiagnostics("bench-test-token"), true, false},
		{"attempt_only", NewEngineWorkerGenerationFactoryWithAttemptLinkDiagnostics("bench-test-token", false), false, true},
		{"driver_and_attempt", NewEngineWorkerGenerationFactoryWithAttemptLinkDiagnostics("bench-test-token", true), true, true},
		{"new_default", NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{}), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDriverCPUWorkerFixture(t, test.constructor)
			generation := fixture.start(t)
			generation.Stop()
			if snapshot := generation.engine.DriverCPUDiagnosticsAfterStop(); snapshot != nil {
				t.Fatalf("legacy constructor implicitly enabled CPU observation: %+v", snapshot)
			}
			snapshot, err := generation.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("legacy terminal Snapshot: %v", err)
			}
			if (snapshot.DriverDiagnostics != nil) != test.driver || (snapshot.AttemptLinkDiagnostics != nil) != test.attemptLink {
				t.Fatalf("legacy driver/attempt opt-ins changed: driver=%v attempt=%v", snapshot.DriverDiagnostics != nil, snapshot.AttemptLinkDiagnostics != nil)
			}
		})
	}
}

func TestDriverCPUWorkerStopPublishesActualProbeOnceWithDetachedAnonymousFence(t *testing.T) {
	entry, miss := driverCPUValidPair()
	probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
	reports := make(chan DriverCPUReport, 4)
	fixture := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{
		Driver: true, AttemptLink: true, CPUClockProbe: probe.read, CPUReports: reports,
	}))
	generation := fixture.start(t)
	uid := fixture.login(t, generation)
	at := fixture.clock.Now()
	assertDriverCPUWorkerNoReport(t, reports, "live generation")
	fixture.queueSends(t, generation, uid, at, 1)
	fixture.clock.Set(at.Add(101 * time.Millisecond))
	_, err := generation.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	assertUnderDeliveryFeedbackJSON(t, generation.evidence.Snapshot(), FailureCode(38), 1)
	if probe.count() != 2 {
		t.Fatalf("factory failed to wire the configured entry/miss probe: calls=%d", probe.count())
	}
	live, err := generation.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("live CPU worker Snapshot: %v", err)
	}
	body, err := json.Marshal(live)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "process_cpu_nanos") || strings.Contains(string(body), "driver_cpu") {
		t.Fatal("private CPU observation entered native WorkerSnapshot")
	}
	assertDriverCPUWorkerNoReport(t, reports, "live native failure snapshot")
	generation.Stop()
	report := takeDriverCPUWorkerReport(t, reports)
	assertDriverCPUWorkerReport(t, generation, report, fixture.assignment.Generation)
	if len(report.Diagnostics.Records) != 1 || report.Diagnostics.Records[0].OwnerEntry != entry || report.Diagnostics.Records[0].Miss != miss {
		t.Fatalf("terminal worker report lost the actual configured probe pair: %+v", report.Diagnostics)
	}
	retained := generation.engine.DriverCPUDiagnosticsAfterStop()
	report.Diagnostics.Generation++
	report.Diagnostics.Records[0].OwnerEntry.ProcessCPUNanos++
	if !reflect.DeepEqual(retained, generation.engine.DriverCPUDiagnosticsAfterStop()) {
		t.Fatal("consumer mutation of worker CPU report changed joined Engine storage")
	}
	generation.Stop()
	assertDriverCPUWorkerNoReport(t, reports, "repeated Stop")

	// The same enabled exporter can receive another generation without sharing
	// its retained records or one-publication fence with the first generation.
	second := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{
		Driver: true, CPUClockProbe: probe.read, CPUReports: reports,
	}))
	second.assignment.Generation = 8
	next := second.start(t)
	next.Stop()
	nextReport := takeDriverCPUWorkerReport(t, reports)
	assertDriverCPUWorkerReport(t, next, nextReport, 8)
	if len(nextReport.Diagnostics.Records) != 0 || nextReport.Diagnostics.DroppedTurns != 0 {
		t.Fatalf("new generation inherited old CPU records: %+v", nextReport.Diagnostics)
	}
	assertDriverCPUWorkerNoReport(t, reports, "second generation repeated stop")
}

func TestDriverCPUWorkerFatalTerminalPathsPublishOneJoinedReport(t *testing.T) {
	for _, path := range []string{"tick", "external"} {
		t.Run(path, func(t *testing.T) {
			reports := make(chan DriverCPUReport, 4)
			fixture := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{
				Driver: true, CPUClockProbe: func() DriverCPUClockSample { return DriverCPUClockSample{} }, CPUReports: reports,
			}))
			fixture.assignment.CoordinatorGrants = path == "external"
			generation := fixture.start(t)
			if path == "tick" {
				fixture.clock.Set(fixture.clock.Now().Add(-time.Second))
				fixture.ticker.tick(t)
			} else {
				// Reuse the existing real fatal-runtime fixture, isolating terminal
				// join from the unrelated bootstrap/first-global-grant protocol.
				generation.trafficReady.Store(true)
				generation.engine.scheduler.onlineTarget = 0
				due := fixture.clock.Now().Add(time.Second)
				installLifecycleReplaySaturationWork(t, generation.engine, due, 2)
				fixture.clock.Set(due)
				if _, err := generation.ApplyGrant(context.Background(), 0); !runtimeFailureTerminatesGeneration(err) {
					t.Fatalf("external terminal fixture was not fatal: %v", err)
				}
			}
			select {
			case err := <-generation.Done():
				if !runtimeFailureTerminatesGeneration(err) {
					t.Fatalf("terminal %s path lost native fatal runtime error: %v", path, err)
				}
			case <-time.After(time.Second):
				t.Fatalf("terminal %s path did not finish", path)
			}
			report := takeDriverCPUWorkerReport(t, reports)
			assertDriverCPUWorkerReport(t, generation, report, fixture.assignment.Generation)
			generation.Stop()
			assertDriverCPUWorkerNoReport(t, reports, "fatal cleanup followed by Stop")
		})
	}
}

func TestDriverCPUWorkerConcurrentTerminalPathsPublishOneJoinedReport(t *testing.T) {
	reports := make(chan DriverCPUReport, 8)
	fixture := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{
		Driver: true, CPUClockProbe: func() DriverCPUClockSample { return DriverCPUClockSample{} }, CPUReports: reports,
	}))
	fixture.assignment.CoordinatorGrants = true
	generation := fixture.start(t)
	// The existing saturation fixture supplies real fatal errors to both
	// cleanup owners while the ordinary Stop path races to join the same work.
	generation.engine.scheduler.onlineTarget = 0
	due := fixture.clock.Now().Add(time.Second)
	installLifecycleReplaySaturationWork(t, generation.engine, due, 2)
	fixture.clock.Set(due)
	_, grantErr := generation.engine.ApplyGrant(context.Background(), due, 0)
	if !runtimeFailureTerminatesGeneration(grantErr) {
		t.Fatalf("concurrent external terminal fixture was not fatal: %v", grantErr)
	}
	_, stepErr := generation.engine.Step(context.Background(), due, nil)
	if !runtimeFailureTerminatesGeneration(stepErr) {
		t.Fatalf("concurrent tick terminal fixture was not fatal: %v", stepErr)
	}
	start := make(chan struct{})
	joined := make(chan struct{})
	var cleanup sync.WaitGroup
	for _, stop := range []func(){
		generation.Stop,
		func() { generation.terminateFromTick(stepErr) },
		func() { generation.terminateFromExternal(grantErr) },
	} {
		cleanup.Add(1)
		go func(stop func()) { defer cleanup.Done(); <-start; stop() }(stop)
	}
	close(start)
	go func() { cleanup.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("concurrent Stop/tick/external cleanup did not join")
	}
	report := takeDriverCPUWorkerReport(t, reports)
	assertDriverCPUWorkerReport(t, generation, report, fixture.assignment.Generation)
	generation.Stop()
	assertDriverCPUWorkerNoReport(t, reports, "concurrent terminal cleanup and repeated Stop")
}

func TestDriverCPUWorkerExporterPressureCannotBlockStopOrRetryDroppedReport(t *testing.T) {
	for _, capacity := range []int{0, 1} {
		name := "unbuffered"
		if capacity > 0 {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			reports := make(chan DriverCPUReport, capacity)
			if capacity > 0 {
				reports <- DriverCPUReport{WorkerID: 99}
			}
			fixture := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{
				Driver: true, CPUClockProbe: func() DriverCPUClockSample { return DriverCPUClockSample{} }, CPUReports: reports,
			}))
			generation := fixture.start(t)
			stopped := make(chan struct{})
			go func() { generation.Stop(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				// Release an incorrect blocking send before failing so cleanup can
				// still join and the test does not abandon an Engine generation.
				select {
				case <-reports:
				case <-time.After(time.Second):
				}
				select {
				case <-stopped:
				case <-time.After(time.Second):
				}
				t.Fatal("private CPU exporter pressure blocked Stop")
			}
			if capacity > 0 {
				if sentinel := <-reports; sentinel.WorkerID != 99 {
					t.Fatalf("full inbox was overwritten: %+v", sentinel)
				}
			}
			generation.Stop()
			assertDriverCPUWorkerNoReport(t, reports, "dropped terminal report retry")
		})
	}
}

func TestDriverCPUWorkerDoesNotPublishUntilAdmittedOwnerJoinsStop(t *testing.T) {
	reports := make(chan DriverCPUReport, 1)
	fixture := newDriverCPUWorkerFixture(t, NewEngineWorkerGenerationFactoryWithDiagnostics("bench-test-token", WorkerDiagnosticOptions{
		Driver: true, CPUClockProbe: func() DriverCPUClockSample { return DriverCPUClockSample{} }, CPUReports: reports,
	}))
	generation := fixture.start(t)
	uid := fixture.login(t, generation)
	fixture.sessions.sendStarted = make(chan context.Context, 1)
	fixture.sessions.sendCanceled = make(chan struct{}, 1)
	fixture.sessions.sendReturn = make(chan struct{})
	fixture.sessions.sendAbort = make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(fixture.sessions.sendAbort) }) })
	at := fixture.clock.Now()
	fixture.queueSends(t, generation, uid, at, 1)
	advanced := make(chan error, 1)
	go func() { _, err := generation.engine.AdvanceContext(context.Background(), at); advanced <- err }()
	select {
	case <-fixture.sessions.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("actual admitted SDK send was not reached")
	}
	stopped := make(chan struct{})
	go func() { generation.Stop(); close(stopped) }()
	select {
	case <-fixture.sessions.sendCanceled:
	case <-time.After(time.Second):
		t.Fatal("generation Stop did not cancel admitted SDK send")
	}
	assertDriverCPUWorkerNoReport(t, reports, "stopping admitted owner")
	select {
	case <-stopped:
		t.Fatal("generation Stop returned before admitted SDK send joined")
	default:
	}
	close(fixture.sessions.sendReturn)
	select {
	case <-advanced:
	case <-time.After(time.Second):
		t.Fatal("admitted SDK send did not join")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("generation Stop did not finish after admitted owner joined")
	}
	assertDriverCPUWorkerReport(t, generation, takeDriverCPUWorkerReport(t, reports), fixture.assignment.Generation)
}
