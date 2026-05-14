package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorAssignsWorkersAndRunsPhases(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.NoError(t, err)
	require.Equal(t, StatusCompleted, result.Status)
	require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, workers[0].ObservedPhases())
	require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, workers[1].ObservedPhases())
}

func TestCoordinatorReturnsWorkerFailedWhenPhasePostFails(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[1].FailPhase(PhaseWarmup, http.StatusInternalServerError, "warmup exploded")
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})
	scenario := fakeScenario()
	scenario.Run.FailFast = true

	result, err := coord.Run(context.Background(), scenario)

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
	require.Contains(t, err.Error(), "warmup exploded")
	require.True(t, workers[0].Stopped(), "healthy workers should be stopped on fail-fast failure")
	require.True(t, workers[1].Stopped(), "failed worker should be stopped on fail-fast failure")
}

func TestCoordinatorStopsWorkersOnContextCancellation(t *testing.T) {
	workers := newFakeWorkers(t, 2)
	workers[0].BlockStatus(PhaseConnect)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	workers[0].CancelWhenStatusPolled(cancel)

	result, err := coord.Run(ctx, fakeScenario())

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, StatusCanceled, result.Status)
	require.True(t, workers[0].Stopped())
	require.True(t, workers[1].Stopped())
}

func TestCoordinatorPollTimeoutReturnsWorkerFailed(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	workers[0].LagPhase(PhasePrepare)
	coord := New(CoordinatorConfig{
		Workers:      workers.ClientConfigs(),
		Target:       fakeTargetOK(),
		Preflight:    preflightFunc(func(context.Context, model.Target, model.WorkerSet) error { return nil }),
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.Error(t, err)
	require.Equal(t, StatusWorkerFailed, result.Status)
}

func TestCoordinatorRunsPreflightBeforeAssignment(t *testing.T) {
	workers := newFakeWorkers(t, 1)
	coord := New(CoordinatorConfig{
		Workers: workers.ClientConfigs(),
		Target:  fakeTargetOK(),
		Preflight: preflightFunc(func(context.Context, model.Target, model.WorkerSet) error {
			return errors.New("preflight denied")
		}),
		PollInterval: time.Millisecond,
		PollTimeout:  100 * time.Millisecond,
	})

	result, err := coord.Run(context.Background(), fakeScenario())

	require.ErrorContains(t, err, "preflight denied")
	require.Equal(t, StatusPreflightFailed, result.Status)
	require.False(t, workers[0].Assigned())
}

type preflightFunc func(context.Context, model.Target, model.WorkerSet) error

func (f preflightFunc) Check(ctx context.Context, target model.Target, workers model.WorkerSet) error {
	return f(ctx, target, workers)
}

type fakeWorkers []*fakeWorker

func newFakeWorkers(t *testing.T, count int) fakeWorkers {
	t.Helper()
	out := make(fakeWorkers, 0, count)
	for i := 0; i < count; i++ {
		fw := &fakeWorker{
			id:          string(rune('a' + i)),
			phase:       worker.PhaseIdle,
			failPhases:  make(map[Phase]fakePhaseFailure),
			blockStatus: make(map[Phase]bool),
		}
		fw.server = httptest.NewServer(http.HandlerFunc(fw.handle))
		t.Cleanup(fw.server.Close)
		out = append(out, fw)
	}
	return out
}

func (fws fakeWorkers) ClientConfigs() []model.Worker {
	out := make([]model.Worker, 0, len(fws))
	for _, fw := range fws {
		out = append(out, model.Worker{ID: fw.id, Addr: fw.server.URL, Weight: 1, ControlToken: "secret"})
	}
	return out
}

type fakeWorker struct {
	mu                 sync.Mutex
	id                 string
	server             *httptest.Server
	phase              worker.Phase
	assignment         worker.Assignment
	observed           []Phase
	stopped            bool
	assigned           bool
	failPhases         map[Phase]fakePhaseFailure
	lagPhases          map[Phase]bool
	blockStatus        map[Phase]bool
	cancelOnStatusPoll context.CancelFunc
}

type fakePhaseFailure struct {
	status int
	body   string
}

func (fw *fakeWorker) FailPhase(phase Phase, status int, body string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.failPhases[phase] = fakePhaseFailure{status: status, body: body}
}

func (fw *fakeWorker) BlockStatus(phase Phase) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.blockStatus[phase] = true
}

func (fw *fakeWorker) LagPhase(phase Phase) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.lagPhases == nil {
		fw.lagPhases = make(map[Phase]bool)
	}
	fw.lagPhases[phase] = true
}

func (fw *fakeWorker) CancelWhenStatusPolled(cancel context.CancelFunc) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.cancelOnStatusPoll = cancel
}

func (fw *fakeWorker) ObservedPhases() []Phase {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return append([]Phase(nil), fw.observed...)
}

func (fw *fakeWorker) Stopped() bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.stopped
}

func (fw *fakeWorker) Assigned() bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.assigned
}

func (fw *fakeWorker) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer secret" {
		http.Error(w, "missing auth", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/v1/assign":
		fw.handleAssign(w, r)
	case "/v1/phase/prepare":
		fw.handlePhase(w, PhasePrepare, worker.PhasePrepare)
	case "/v1/phase/connect":
		fw.handlePhase(w, PhaseConnect, worker.PhaseConnect)
	case "/v1/phase/warmup":
		fw.handlePhase(w, PhaseWarmup, worker.PhaseWarmup)
	case "/v1/phase/run":
		fw.handlePhase(w, PhaseRun, worker.PhaseRun)
	case "/v1/phase/cooldown":
		fw.handlePhase(w, PhaseCooldown, worker.PhaseCooldown)
	case "/v1/status":
		fw.handleStatus(w, r)
	case "/v1/stop":
		fw.handleStop(w)
	default:
		http.NotFound(w, r)
	}
}

func (fw *fakeWorker) handleAssign(w http.ResponseWriter, r *http.Request) {
	var assignment worker.Assignment
	if err := json.NewDecoder(r.Body).Decode(&assignment); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fw.mu.Lock()
	fw.assigned = true
	fw.assignment = assignment
	fw.phase = worker.PhaseAssigned
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handlePhase(w http.ResponseWriter, phase Phase, workerPhase worker.Phase) {
	fw.mu.Lock()
	if failure, ok := fw.failPhases[phase]; ok {
		fw.mu.Unlock()
		http.Error(w, failure.body, failure.status)
		return
	}
	fw.observed = append(fw.observed, phase)
	if !fw.lagPhases[phase] {
		fw.phase = workerPhase
	}
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handleStatus(w http.ResponseWriter, r *http.Request) {
	fw.mu.Lock()
	if cancel := fw.cancelOnStatusPoll; cancel != nil {
		fw.cancelOnStatusPoll = nil
		go cancel()
	}
	blocked := fw.blockStatus[Phase(fw.phase)]
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	if blocked {
		<-r.Context().Done()
		return
	}
	writeRunTestJSON(w, status)
}

func (fw *fakeWorker) handleStop(w http.ResponseWriter) {
	fw.mu.Lock()
	fw.stopped = true
	fw.phase = worker.PhaseStopped
	status := worker.Status{Phase: fw.phase, Assignment: fw.assignment}
	fw.mu.Unlock()
	writeRunTestJSON(w, status)
}

func writeRunTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fakeTargetOK() model.Target {
	return model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{"http://127.0.0.1:1"}},
		Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}
}

func fakeScenario() model.Scenario {
	rate, err := model.ParseRate("1/s")
	if err != nil {
		panic(err)
	}
	return model.Scenario{
		Version: "wkbench/v1",
		Run:     model.RunConfig{ID: "run-1", FailFast: true},
		Online:  model.OnlineConfig{TotalUsers: 10},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name:        "group-hot",
			ChannelType: model.ChannelTypeGroup,
			Count:       1,
			Members:     model.MembersConfig{Count: 5},
		}}},
		Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{
			Name:           "group-send",
			ChannelRef:     "group-hot",
			RatePerChannel: rate,
		}}},
	}
}
