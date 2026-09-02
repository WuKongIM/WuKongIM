package devsim

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestRunnerDefaultsAndDeterministicHelpers(t *testing.T) {
	cfg := testRunnerConfig()
	runner := NewRunner(RunnerConfig{Config: cfg})
	if !strings.HasPrefix(runner.runID, "dev-sim-") || runner.status == nil || runner.probe == nil || runner.workload == nil || runner.sleep == nil {
		t.Fatalf("NewRunner(defaults) = %+v", runner)
	}
	if got, want := NewRunID(time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC)), "dev-sim-20260902-030405"; got != want {
		t.Fatalf("NewRunID() = %q, want %q", got, want)
	}
	if got := (&Runner{cfg: Config{}}).counterInterval(); got != time.Second {
		t.Fatalf("counterInterval(zero window) = %v, want 1s", got)
	}
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("sleepContext(0) error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext(canceled) error = %v", err)
	}

	inputs, err := cfg.BuildBenchInputs("run-1")
	if err != nil {
		t.Fatalf("BuildBenchInputs() error = %v", err)
	}
	delete(inputs.Plan.Workers, simulatorWorkerID)
	if _, err := assignmentFromInputs(inputs, 0); err == nil {
		t.Fatal("assignmentFromInputs(missing worker) error = nil")
	}
	inputs, err = cfg.BuildBenchInputs("run-1")
	if err != nil {
		t.Fatal(err)
	}
	inputs.Scenario.Identity.ClientMsgPrefix = " "
	assignment, err := assignmentFromInputs(inputs, 2)
	if err != nil || assignment.Scenario.Identity.ClientMsgPrefix != "sim-msg-r2" {
		t.Fatalf("assignmentFromInputs(retry) = (%q, %v)", assignment.Scenario.Identity.ClientMsgPrefix, err)
	}

	if probe := NewTargetProbe(model.Target{API: model.TargetAPIConfig{Addrs: []string{"http://api"}}}); probe == nil || probe.client == nil {
		t.Fatal("NewTargetProbe(API fallback) returned nil")
	}
	if probe := NewTargetProbe(model.Target{BenchAPI: model.BenchAPIConfig{Addrs: []string{"http://bench"}}}); probe == nil || probe.client == nil {
		t.Fatal("NewTargetProbe(Bench API) returned nil")
	}
}

func TestHTTPReadyProbeChecksEndpointsInOrderAndRequiresCapability(t *testing.T) {
	tests := []struct {
		name      string
		transport *readinessTransport
		wantErr   bool
		wantPaths []string
	}{
		{name: "ready", transport: &readinessTransport{enabled: true}, wantPaths: []string{"/healthz", "/readyz", "/bench/v1/capabilities"}},
		{name: "health failure", transport: &readinessTransport{statusByPath: map[string]int{"/healthz": http.StatusServiceUnavailable}}, wantErr: true, wantPaths: []string{"/healthz"}},
		{name: "readiness failure", transport: &readinessTransport{statusByPath: map[string]int{"/readyz": http.StatusServiceUnavailable}}, wantErr: true, wantPaths: []string{"/healthz", "/readyz"}},
		{name: "capability failure", transport: &readinessTransport{statusByPath: map[string]int{"/bench/v1/capabilities": http.StatusServiceUnavailable}}, wantErr: true, wantPaths: []string{"/healthz", "/readyz", "/bench/v1/capabilities"}},
		{name: "bench API disabled", transport: &readinessTransport{}, wantErr: true, wantPaths: []string{"/healthz", "/readyz", "/bench/v1/capabilities"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &HTTPReadyProbe{client: target.NewClient(target.Config{
				APIAddrs:   []string{"http://target.invalid"},
				HTTPClient: &http.Client{Transport: tt.transport},
			})}
			err := probe.CheckReady(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckReady() error = %v, wantErr=%v", err, tt.wantErr)
			}
			if strings.Join(tt.transport.paths, ",") != strings.Join(tt.wantPaths, ",") {
				t.Fatalf("endpoint order = %v, want %v", tt.transport.paths, tt.wantPaths)
			}
		})
	}
}

func TestWaitReadyDistinguishesCancellationFromReadinessTimeout(t *testing.T) {
	wantProbeErr := errors.New("target starting")
	status := NewStatus("run-1")
	runner := &Runner{
		cfg:    Config{Retry: RetryConfig{ReadinessTimeout: time.Hour, RestartBackoff: time.Second}},
		status: status,
		probe:  staticProbe{err: wantProbeErr},
		sleep:  func(context.Context, time.Duration) error { return errors.New("deadline") },
	}
	if err := runner.waitReady(context.Background()); err == nil || !strings.Contains(err.Error(), "target readiness timeout") {
		t.Fatalf("waitReady(timeout) error = %v", err)
	}
	if snapshot := status.Snapshot(); snapshot.State != StateWaiting || snapshot.LastError != wantProbeErr.Error() {
		t.Fatalf("status after timeout = %+v", snapshot)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	runner.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	if err := runner.waitReady(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitReady(canceled) error = %v", err)
	}
}

func TestPrepareConnectStopsAtFailingStage(t *testing.T) {
	wantErr := errors.New("stage failed")
	tests := []struct {
		name      string
		warmup    time.Duration
		configure func(*phaseBoundaryWorkload)
		wantCalls string
		wantErr   bool
	}{
		{name: "prepare", warmup: time.Second, configure: func(w *phaseBoundaryWorkload) { w.prepareErr = wantErr }, wantCalls: "begin,prepare", wantErr: true},
		{name: "connect", warmup: time.Second, configure: func(w *phaseBoundaryWorkload) { w.connectErr = wantErr }, wantCalls: "begin,prepare,connect", wantErr: true},
		{name: "no warmup", wantCalls: "begin,prepare,connect"},
		{name: "warmup", warmup: time.Second, configure: func(w *phaseBoundaryWorkload) { w.warmupErr = wantErr }, wantCalls: "begin,prepare,connect,warmup", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workload := &phaseBoundaryWorkload{}
			if tt.configure != nil {
				tt.configure(workload)
			}
			runner := &Runner{workload: workload, status: NewStatus("run-1")}
			assignment := worker.Assignment{Scenario: model.Scenario{Run: model.RunConfig{Warmup: tt.warmup}}}
			err := runner.prepareConnect(context.Background(), assignment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("prepareConnect() error = %v, wantErr=%v", err, tt.wantErr)
			}
			if got := strings.Join(workload.calls, ","); got != tt.wantCalls {
				t.Fatalf("phase calls = %q, want %q", got, tt.wantCalls)
			}
		})
	}

	plain := &plainBoundaryWorkload{}
	runner := &Runner{workload: plain, status: NewStatus("run-1")}
	runner.updateCounters()
	runner.updateConnectionStatus()
	runner.captureCounterBaseline()
}

type readinessTransport struct {
	statusByPath map[string]int
	enabled      bool
	paths        []string
}

func (t *readinessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.paths = append(t.paths, req.URL.Path)
	status := t.statusByPath[req.URL.Path]
	if status == 0 {
		status = http.StatusOK
	}
	body := "{}"
	if req.URL.Path == "/bench/v1/capabilities" && t.enabled {
		body = `{"enabled":true}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

type staticProbe struct{ err error }

func (p staticProbe) CheckReady(context.Context) error { return p.err }

type plainBoundaryWorkload struct{}

func (*plainBoundaryWorkload) Prepare(context.Context, worker.Assignment) error  { return nil }
func (*plainBoundaryWorkload) Connect(context.Context, worker.Assignment) error  { return nil }
func (*plainBoundaryWorkload) Warmup(context.Context, worker.Assignment) error   { return nil }
func (*plainBoundaryWorkload) Run(context.Context, worker.Assignment) error      { return nil }
func (*plainBoundaryWorkload) Cooldown(context.Context, worker.Assignment) error { return nil }

type phaseBoundaryWorkload struct {
	calls      []string
	prepareErr error
	connectErr error
	warmupErr  error
}

func (w *phaseBoundaryWorkload) BeginAssignment(worker.Assignment) {
	w.calls = append(w.calls, "begin")
}
func (w *phaseBoundaryWorkload) Prepare(context.Context, worker.Assignment) error {
	w.calls = append(w.calls, "prepare")
	return w.prepareErr
}
func (w *phaseBoundaryWorkload) Connect(context.Context, worker.Assignment) error {
	w.calls = append(w.calls, "connect")
	return w.connectErr
}
func (w *phaseBoundaryWorkload) Warmup(context.Context, worker.Assignment) error {
	w.calls = append(w.calls, "warmup")
	return w.warmupErr
}
func (w *phaseBoundaryWorkload) Run(context.Context, worker.Assignment) error      { return nil }
func (w *phaseBoundaryWorkload) Cooldown(context.Context, worker.Assignment) error { return nil }
