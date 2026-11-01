//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The public Run boundary must retain the rejected clock facts, not only tick.
func TestGrantTickDiagnosticsRealCoordinatorKeepsStaleBoundary(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "tick-failure-fixture"
	start := time.Unix(1700000000, 0)
	clock := newManualCoordinatorClock(start)
	observerStarted := make(chan struct{})
	typed := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for i := range workers {
		typed[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}}
		workers[i] = typed[i]
	}
	c, err := NewCoordinator(CoordinatorOptions{Generation: 31,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan CoordinatorResult, 1)
	go func() { results <- c.Run(ctx, cfg) }()
	select {
	case <-observerStarted:
	case <-ctx.Done():
		t.Fatal("observer did not start")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-clock.created:
		case <-ctx.Done():
			t.Fatal("tickers not created")
		}
	}
	clock.setNowAndQueue(start.Add(2300*time.Millisecond), map[time.Duration]time.Time{time.Second: start.Add(time.Second)})
	var result CoordinatorResult
	select {
	case result = <-results:
	case <-ctx.Done():
		t.Fatal("tick rejection did not join")
	}
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureTick {
		t.Fatalf("original tick failure changed: %+v", result)
	}
	for _, w := range typed {
		if len(w.grantRequests) != 1 {
			t.Fatal("invalid tick advanced grant")
		}
		stops := 0
		for _, event := range *w.log {
			if strings.HasPrefix(event, "stop-") {
				stops++
			}
		}
		if stops != 1 {
			t.Fatalf("exact worker cleanup not joined: %d", stops)
		}
	}
	field := reflect.ValueOf(result).FieldByName("GrantTickFailure")
	if !field.IsValid() {
		t.Fatal("real rejected tick discarded age/interval/boundary evidence")
	}
	raw, err := json.Marshal(field.Interface())
	if err != nil || len(raw) > 512 {
		t.Fatal("tick evidence unbounded")
	}
	var got struct {
		Boundary         string        `json:"boundary"`
		Reason           string        `json:"reason"`
		HaveLastTick     bool          `json:"have_last_tick"`
		AgeObserved      bool          `json:"age_observed"`
		AgeNanos         time.Duration `json:"age_nanos"`
		IntervalObserved bool          `json:"interval_observed"`
		IntervalNanos    time.Duration `json:"interval_nanos"`
	}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Boundary != "scheduled_grant" || got.Reason != "stale" || got.HaveLastTick || !got.AgeObserved || got.AgeNanos != 1300*time.Millisecond || !got.IntervalObserved || got.IntervalNanos != time.Second {
		t.Fatalf("literal rejected tick facts lost: %s", raw)
	}
}

// Pending status work must not hide a rejected future grant tick.
func TestGrantTickDiagnosticsPendingStatusKeepsFutureBoundary(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "tick-pending-status-fixture"
	cfg.Observation.Cadence = 2 * time.Second
	start := time.Unix(1700000000, 0)
	clock := newManualCoordinatorClock(start)
	statusEntered := make(chan uint64, coordinatorWorkerCount)
	statusRelease := make(chan struct{})
	grantCalled := make(chan uint64, coordinatorWorkerCount*8)
	observerStarted := make(chan struct{})
	typed := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for i := range workers {
		typed[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}, grantCalled: grantCalled}
		workers[i] = &blockingStatusRecordingGrantCoordinatorWorker{recordingCoordinatorWorker: typed[i], entered: statusEntered, release: statusRelease}
	}
	c, err := NewCoordinator(CoordinatorOptions{Generation: 32,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer close(statusRelease)
	results := make(chan CoordinatorResult, 1)
	go func() { results <- c.Run(ctx, cfg) }()
	select {
	case <-observerStarted:
	case <-ctx.Done():
		t.Fatal("observer did not start")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-clock.created:
		case <-ctx.Done():
			t.Fatal("tickers not created")
		}
	}
	drainGrant := func(want uint64) {
		t.Helper()
		for i := 0; i < coordinatorWorkerCount; i++ {
			select {
			case seq := <-grantCalled:
				if seq != want {
					t.Fatalf("grant sequence=%d want%d", seq, want)
				}
			case result := <-results:
				t.Fatalf("Run ended before grant: %+v", result)
			case <-ctx.Done():
				t.Fatal("grant did not complete")
			}
		}
	}
	drainGrant(1)
	clock.advance(time.Second)
	drainGrant(2)
	clock.advance(time.Second)
	for i := 0; i < coordinatorWorkerCount; i++ {
		select {
		case <-statusEntered:
		case <-ctx.Done():
			t.Fatal("status did not block")
		}
	}
	drainGrant(3)
	clock.setNowAndQueue(start.Add(3*time.Second), map[time.Duration]time.Time{time.Second: start.Add(4 * time.Second)})
	var result CoordinatorResult
	select {
	case result = <-results:
	case <-ctx.Done():
		t.Fatal("future tick rejection did not join")
	}
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureTick {
		t.Fatalf("original future tick verdict changed: %+v", result)
	}
	for _, w := range typed {
		if len(w.grantRequests) != 3 {
			t.Fatal("future tick advanced grant")
		}
		stops := 0
		for _, event := range *w.log {
			if strings.HasPrefix(event, "stop-") {
				stops++
			}
		}
		if stops != 1 {
			t.Fatal("pending status cancellation did not join exact cleanup")
		}
	}
	got := result.GrantTickFailure
	if got.Boundary != "pending_status" || got.Reason != "future" || !got.HaveLastTick || !got.AgeObserved || got.AgeNanos != -time.Second || !got.IntervalObserved || got.IntervalNanos != 2*time.Second {
		t.Fatalf("pending status discarded literal clock facts: %+v", got)
	}
}

// A queued tick at cutoff must preserve the early timestamp rejection boundary.
func TestGrantTickDiagnosticsCutoffKeepsFutureBoundary(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "tick-cutoff-fixture"
	cfg.Observation.Cadence = 500 * time.Millisecond
	cfg.Thresholds.Timeline = TimelineThresholds{LatencyWarmup: 100 * time.Millisecond, Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 2500 * time.Millisecond}
	start := time.Unix(1700000000, 0)
	clock := &queuedGrantTickDiagnosticClock{manualCoordinatorClock: newManualCoordinatorClock(start), nowAtTicker: start.Add(2500 * time.Millisecond), tickAt: start.Add(4 * time.Second)}
	observerStarted := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	typed := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	for i := range workers {
		typed[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}}
		workers[i] = typed[i]
	}
	c, err := NewCoordinator(CoordinatorOptions{Generation: 33,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan CoordinatorResult, 1)
	go func() { results <- c.Run(ctx, cfg) }()
	select {
	case <-observerStarted:
	case <-ctx.Done():
		t.Fatal("observer not started")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-clock.created:
		case <-ctx.Done():
			t.Fatal("tickers not created")
		}
	}
	var result CoordinatorResult
	select {
	case result = <-results:
	case <-ctx.Done():
		t.Fatal("cutoff rejection did not join")
	}
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureTick {
		t.Fatalf("cutoff future tick verdict changed: %+v", result)
	}
	got := result.GrantTickFailure
	if (got.Boundary != "cutoff_poll" && got.Boundary != "cutoff_wait") || got.Reason != "future" || got.HaveLastTick || !got.AgeObserved || got.AgeNanos != -1500*time.Millisecond || !got.IntervalObserved || got.IntervalNanos != 4*time.Second {
		t.Fatalf("cutoff discarded literal future tick facts: %+v", got)
	}
	for _, w := range typed {
		if len(w.grantRequests) != 1 {
			t.Fatal("cutoff future tick advanced grant")
		}
	}
}

// NewTicker queues the audited timestamp before Run can choose a ready branch.
// This clock port prevents a pre-cutoff loop iteration from consuming the tick.
type queuedGrantTickDiagnosticClock struct {
	*manualCoordinatorClock
	nowAtTicker time.Time
	tickAt      time.Time
}

func (c *queuedGrantTickDiagnosticClock) NewTicker(period time.Duration) ObserverTicker {
	ticker := c.manualCoordinatorClock.NewTicker(period)
	if period == time.Second {
		c.setNowAndQueue(c.nowAtTicker, map[time.Duration]time.Time{period: c.tickAt})
	}
	return ticker
}

// The capacity phase-end predicate must not be mislabeled as cadence failure.
func TestGrantTickDiagnosticsCapacityPhaseEndKeepsActualPredicate(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	elapsed := cfg.Capacity.Step.Stabilize + time.Millisecond
	clock := &queuedGrantTickDiagnosticClock{manualCoordinatorClock: newManualCoordinatorClock(start), nowAtTicker: start.Add(elapsed), tickAt: start.Add(elapsed)}
	observerStarted := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	typed := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	for i := range workers {
		typed[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}}
		workers[i] = typed[i]
	}
	c, err := NewCoordinator(CoordinatorOptions{Generation: 34,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}), Clock: clock,
		CapacityAdmission: &admission, CapacityEvidence: &scriptedCapacityEvidence{requests: make(chan CapacityEvidenceRequest, 1)},
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan CoordinatorResult, 1)
	go func() { results <- c.Run(ctx, cfg) }()
	var result CoordinatorResult
	select {
	case result = <-results:
	case <-ctx.Done():
		t.Fatal("capacity phase-end rejection did not join")
	}
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureTick {
		t.Fatalf("capacity timestamp verdict changed: %+v", result)
	}
	got := result.GrantTickFailure
	if got.Boundary != "capacity_phase_end" || got.Reason != "phase_end" || got.HaveLastTick || !got.AgeObserved || got.AgeNanos != 0 || !got.IntervalObserved || got.IntervalNanos != 600001*time.Millisecond {
		t.Fatalf("capacity phase-end predicate/facts lost: %+v", got)
	}
	for _, w := range typed {
		if len(w.grantRequests) != 1 {
			t.Fatal("rejected phase-end tick advanced grant")
		}
	}
}

// Both status-poll paths must retain their own early future-tick guard.
func TestGrantTickDiagnosticsStatusPollKeepsFutureBoundary(t *testing.T) {
	for _, capacity := range []bool{false, true} {
		name := "ordinary"
		boundary := "status_poll"
		if capacity {
			name = "capacity"
			boundary = "capacity_status_poll"
		}
		t.Run(name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "tick-status-poll-fixture"
			start := time.Unix(1700000000, 0)
			var admission CapacityAdmission
			if capacity {
				cfg, admission, start = capacityTestAdmission(t)
			}
			clock := &futureStatusTickDiagnosticClock{newCoincidentCoordinatorClock(start)}
			observerStarted := make(chan struct{})
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			typed := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
			for i := range workers {
				typed[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}}
				workers[i] = typed[i]
			}
			options := CoordinatorOptions{Generation: 35,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(observerStarted)
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}), Clock: clock}
			if capacity {
				options.CapacityAdmission = &admission
				options.CapacityEvidence = &scriptedCapacityEvidence{requests: make(chan CapacityEvidenceRequest, 1)}
				options.CapacityDataset = coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
					return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
				})
			}
			c, err := NewCoordinator(options)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			results := make(chan CoordinatorResult, 1)
			go func() { results <- c.Run(ctx, cfg) }()
			defer func() {
				select {
				case <-clock.releaseLoopNow:
				default:
					close(clock.releaseLoopNow)
				}
			}()
			select {
			case <-clock.loopNowEntered:
			case <-ctx.Done():
				t.Fatal("grant loop not entered")
			}
			clock.queueCoincidentStatusAndGrant()
			close(clock.releaseLoopNow)
			var result CoordinatorResult
			select {
			case result = <-results:
			case <-ctx.Done():
				t.Fatal("status-poll rejection did not join")
			}
			if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureTick {
				t.Fatalf("status-poll future tick verdict changed: %+v", result)
			}
			got := result.GrantTickFailure
			if got.Boundary != boundary || got.Reason != "future" || got.HaveLastTick || !got.AgeObserved || got.AgeNanos != -time.Second || !got.IntervalObserved || got.IntervalNanos != 2*time.Second {
				t.Fatalf("status-poll predicate/facts lost: %+v", got)
			}
			for _, w := range typed {
				if len(w.grantRequests) != 1 {
					t.Fatal("status-poll future tick advanced grant")
				}
			}
		})
	}
}

// The existing coincident status clock gates Run before the blocking select.
// Its next status timestamp queues a future grant while keeping Now unchanged.
type futureStatusTickDiagnosticClock struct{ *coincidentCoordinatorClock }

func (c *futureStatusTickDiagnosticClock) Now() time.Time {
	c.mu.Lock()
	if c.transitionDue {
		c.transitionDue = false
		now := c.now
		c.grantTicker.ticks <- now.Add(time.Second)
		c.mu.Unlock()
		return now
	}
	c.mu.Unlock()
	return c.coincidentCoordinatorClock.Now()
}

// Literal edge cases exercise Run, including accepted guard boundaries and
// absent evidence on joined operator cancellation; they are not native passes.
func TestGrantTickDiagnosticsRunClockEdgesAndHealthyAbsence(t *testing.T) {
	start := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		name                          string
		now, tick                     time.Time
		reason                        string
		ageObserved, intervalObserved bool
		age, interval                 time.Duration
		accept                        bool
	}{
		{name: "zero_clock", now: time.Time{}, tick: start.Add(time.Second), reason: "zero_clock", intervalObserved: true, interval: time.Second},
		{name: "zero_tick", now: start.Add(time.Second), tick: time.Time{}, reason: "zero_tick"},
		{name: "first_early", now: start.Add(999 * time.Millisecond), tick: start.Add(999 * time.Millisecond), reason: "first_early", ageObserved: true, intervalObserved: true, interval: 999 * time.Millisecond},
		{name: "first_late", now: start.Add(2 * time.Second), tick: start.Add(2 * time.Second), reason: "first_late", ageObserved: true, intervalObserved: true, interval: 2 * time.Second},
		{name: "age_rejected", now: start.Add(2250*time.Millisecond + time.Nanosecond), tick: start.Add(time.Second), reason: "stale", ageObserved: true, intervalObserved: true, age: 1250*time.Millisecond + time.Nanosecond, interval: time.Second},
		{name: "age_limit_accepted", now: start.Add(2250 * time.Millisecond), tick: start.Add(time.Second), accept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "tick-clock-edge-fixture"
			clock := &queuedGrantTickDiagnosticClock{manualCoordinatorClock: newManualCoordinatorClock(start), nowAtTicker: tc.now, tickAt: tc.tick}
			grantCalls := make(chan uint64, coordinatorWorkerCount*2)
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			typed := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
			for i := range workers {
				typed[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}, grantCalled: grantCalls}
				workers[i] = typed[i]
			}
			c, err := NewCoordinator(CoordinatorOptions{Generation: 36,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}), Clock: clock})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			results := make(chan CoordinatorResult, 1)
			go func() { results <- c.Run(ctx, cfg) }()
			if tc.accept {
				for i := 0; i < coordinatorWorkerCount*2; i++ {
					select {
					case <-grantCalls:
					case <-ctx.Done():
						t.Fatal("accepted boundary failed to advance")
					}
				}
				cancel()
			}
			var result CoordinatorResult
			select {
			case result = <-results:
			case <-time.After(3 * time.Second):
				t.Fatal("edge Run did not join")
			}
			if tc.accept {
				if result.Outcome != CoordinatorStopped || result.GrantFailure == CoordinatorGrantFailureTick || result.GrantTickFailure != (CoordinatorGrantTickFailure{}) {
					t.Fatalf("healthy guard created failure evidence: %+v", result)
				}
				return
			}
			if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant || result.GrantFailure != CoordinatorGrantFailureTick {
				t.Fatalf("clock edge changed original verdict: %+v", result)
			}
			got := result.GrantTickFailure
			if got.Boundary != "scheduled_grant" || got.Reason != tc.reason || got.HaveLastTick || got.AgeObserved != tc.ageObserved || got.IntervalObserved != tc.intervalObserved || got.AgeNanos != tc.age || got.IntervalNanos != tc.interval {
				t.Fatalf("literal clock edge facts lost: %+v", got)
			}
			if got.Bounded() != got {
				t.Fatalf("actual observed clock edge was omitted by export: %+v", got)
			}
			for _, w := range typed {
				if len(w.grantRequests) != 1 {
					t.Fatal("invalid clock edge advanced grant")
				}
			}
		})
	}
}

func TestGrantTickDiagnosticsRunLaterIntervalEdges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		reason   string
	}{
		{"early", 989 * time.Millisecond, "interval_early"}, {"late", 1011 * time.Millisecond, "interval_late"},
		{"early_limit", 990 * time.Millisecond, ""}, {"late_limit", 1010 * time.Millisecond, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "tick-interval-fixture"
			start := time.Unix(1700000000, 0)
			clock := newManualCoordinatorClock(start)
			grantCalls := make(chan uint64, coordinatorWorkerCount*3)
			observerStarted := make(chan struct{})
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for i := range workers {
				workers[i] = &recordingCoordinatorWorker{id: uint64(i), log: &[]string{}, grantCalled: grantCalls}
			}
			c, err := NewCoordinator(CoordinatorOptions{Generation: 37,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup: coordinatorSetupFunc(func(context.Context, Config) error { return nil }), Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(observerStarted)
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}), Clock: clock})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			results := make(chan CoordinatorResult, 1)
			go func() { results <- c.Run(ctx, cfg) }()
			select {
			case <-observerStarted:
			case <-ctx.Done():
				t.Fatal("observer not started")
			}
			for i := 0; i < 2; i++ {
				select {
				case <-clock.created:
				case <-ctx.Done():
					t.Fatal("tickers not created")
				}
			}
			drain := func() {
				t.Helper()
				for i := 0; i < coordinatorWorkerCount; i++ {
					select {
					case <-grantCalls:
					case <-ctx.Done():
						t.Fatal("accepted grant did not advance")
					}
				}
			}
			drain()
			clock.advance(time.Second)
			drain()
			tick := start.Add(time.Second + tc.interval)
			clock.setNowAndQueue(tick, map[time.Duration]time.Time{time.Second: tick})
			if tc.reason == "" {
				drain()
				cancel()
			}
			var result CoordinatorResult
			select {
			case result = <-results:
			case <-time.After(3 * time.Second):
				t.Fatal("interval Run did not join")
			}
			if tc.reason == "" {
				if result.Outcome != CoordinatorStopped || result.GrantTickFailure != (CoordinatorGrantTickFailure{}) {
					t.Fatalf("accepted cadence edge created diagnostic: %+v", result)
				}
				return
			}
			got := result.GrantTickFailure
			if result.Outcome != CoordinatorHarnessInvalid || result.GrantFailure != CoordinatorGrantFailureTick || got.Reason != tc.reason || !got.HaveLastTick || !got.AgeObserved || got.AgeNanos != 0 || !got.IntervalObserved || got.IntervalNanos != tc.interval {
				t.Fatalf("later interval facts lost: %+v", result)
			}
		})
	}
}
