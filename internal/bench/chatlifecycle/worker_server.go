package chatlifecycle

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
)

var errWorkerServerConfig = errors.New("chat lifecycle worker server: invalid configuration")

const workerMaxDrainTimeout = 30 * time.Second

// WorkerGeneration is the narrow lifecycle seam around the existing Engine.
// Implementations must make Stop idempotent and must not bind their lifetime to
// an individual HTTP request context. Done signals only after an unexpected
// runtime has performed its own bounded teardown or after explicit Stop joins.
// TrafficReady must be an O(1), non-blocking generation-local read.
type WorkerGeneration interface {
	Start(context.Context) error
	UpdateRate(ctx context.Context, ratePerSecond, maxBurst uint64) error
	ApplyGrant(context.Context, uint64) (WorkerGrantApplication, error)
	TrafficReady() bool
	Checkpoint(context.Context) (WorkerSnapshot, error)
	Drain(context.Context) error
	Stop()
	Snapshot(context.Context) (WorkerSnapshot, error)
	Done() <-chan error
}

// WorkerGrantApplication reports whether grant work crossed generation
// admission. Only admitted results are stable sequence replay boundaries.
type WorkerGrantApplication struct {
	Admitted bool
}

// WorkerGenerationFactory validates and constructs one assignment generation.
type WorkerGenerationFactory interface {
	New(WorkerAssignment) (WorkerGeneration, error)
}

// WorkerGenerationFactoryFunc adapts a function to WorkerGenerationFactory.
type WorkerGenerationFactoryFunc func(WorkerAssignment) (WorkerGeneration, error)

func (f WorkerGenerationFactoryFunc) New(assignment WorkerAssignment) (WorkerGeneration, error) {
	return f(assignment)
}

// WorkerLifecycleCandidateLeaser is an optional generation capability for
// reconstructing a bounded transient lease without retaining channel history.
type WorkerLifecycleCandidateLeaser interface {
	LeaseLifecycleCandidates(context.Context, int) ([]LifecycleCandidate, error)
}

// WorkerLifecycleReheatApprover exposes only existing scheduled SEND admission.
// Its deliberately narrow shape cannot manufacture cold state through eviction.
type WorkerLifecycleReheatApprover interface {
	ApproveLifecycleReheat(context.Context, string, uint64, uint64) (bool, error)
}

// WorkerServerConfig fixes authentication and drain bounds for one dedicated worker server.
type WorkerServerConfig struct {
	ControlToken string
	Factory      WorkerGenerationFactory
	DrainTimeout time.Duration
	Now          func() time.Time
}

// WorkerServer hosts only the authenticated chat-lifecycle worker protocol.
type WorkerServer struct {
	tokenHash    [sha256.Size]byte
	factory      WorkerGenerationFactory
	drainTimeout time.Duration
	now          func() time.Time
	mux          *http.ServeMux

	mu               sync.Mutex
	phase            WorkerPhase
	assignment       WorkerAssignment
	generation       WorkerGeneration
	startedAt        time.Time
	final            WorkerSnapshot
	snapshotSequence uint64
	unexpected       bool
	stop             *workerStopTask
	grantInFlight    *workerGrantTask
	// generationExited fences new mutations after Done while one admitted grant
	// commits its replay result before final snapshot publication.
	generationExited bool
	lastGrantRequest WorkerGrantRequest
	lastGrantResult  WorkerGrantResponse
	lastGrantFailed  bool

	unexpectedExit chan struct{}
}

type workerStopTask struct {
	done chan struct{}
}

type workerGrantTask struct {
	request WorkerGrantRequest
	done    chan struct{}
}

type workerControlState struct {
	generation WorkerGeneration
	phase      WorkerPhase
	fence      WorkerFence
}

// NewWorkerServer creates a dedicated authenticated worker API.
func NewWorkerServer(config WorkerServerConfig) (*WorkerServer, error) {
	if config.ControlToken == "" || config.Factory == nil {
		return nil, errWorkerServerConfig
	}
	if config.DrainTimeout < 0 || config.DrainTimeout > workerMaxDrainTimeout {
		return nil, errWorkerServerConfig
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = 10 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	server := &WorkerServer{
		tokenHash:      sha256.Sum256([]byte(config.ControlToken)),
		factory:        config.Factory,
		drainTimeout:   config.DrainTimeout,
		now:            config.Now,
		mux:            http.NewServeMux(),
		phase:          WorkerPhaseUnassigned,
		unexpectedExit: make(chan struct{}),
	}
	server.routes()
	return server, nil
}

func (s *WorkerServer) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /v1/info", s.handleInfo)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/assign", s.handleAssign)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/start", s.handleStart)
	s.mux.HandleFunc("GET /v1/chat-lifecycle/status", s.handleStatus)
	s.mux.HandleFunc("GET /v1/chat-lifecycle/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/checkpoint", s.handleCheckpoint)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/rate", s.handleRate)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/grant", s.handleGrant)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/lifecycle-candidates", s.handleLifecycleCandidates)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/lifecycle-reheat", s.handleLifecycleReheat)
	s.mux.HandleFunc("POST /v1/chat-lifecycle/stop", s.handleStop)
}

// ServeHTTP authenticates every endpoint before routing.
func (s *WorkerServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !s.authenticated(request) {
		writeWorkerError(response, http.StatusUnauthorized, WorkerErrorUnauthorized)
		return
	}
	expectedMethod, found := workerEndpointMethod(request.URL.Path)
	if !found {
		writeWorkerError(response, http.StatusNotFound, WorkerErrorNotFound)
		return
	}
	if request.Method != expectedMethod {
		writeWorkerError(response, http.StatusMethodNotAllowed, WorkerErrorMethodNotAllowed)
		return
	}
	s.mux.ServeHTTP(response, request)
}

func workerEndpointMethod(path string) (string, bool) {
	switch path {
	case "/healthz", "/v1/info", "/v1/chat-lifecycle/status", "/v1/chat-lifecycle/snapshot":
		return http.MethodGet, true
	case "/v1/chat-lifecycle/assign", "/v1/chat-lifecycle/start", "/v1/chat-lifecycle/checkpoint", "/v1/chat-lifecycle/rate", "/v1/chat-lifecycle/grant", "/v1/chat-lifecycle/lifecycle-candidates", "/v1/chat-lifecycle/lifecycle-reheat", "/v1/chat-lifecycle/stop":
		return http.MethodPost, true
	default:
		return "", false
	}
}

func (s *WorkerServer) handleLifecycleCandidates(response http.ResponseWriter, request *http.Request) {
	var lease WorkerLifecycleCandidateLeaseRequest
	if !decodeWorkerJSON(response, request, &lease) {
		return
	}
	if !validWorkerFence(lease.WorkerFence) || lease.Requested == 0 || int(lease.Requested) > lifecycleCohortSize {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}
	s.mu.Lock()
	if !s.runningFenceLocked(response, lease.WorkerFence) {
		s.mu.Unlock()
		return
	}
	generation := s.generation
	leaser, ok := generation.(WorkerLifecycleCandidateLeaser)
	assignment := s.assignment
	control := workerControlState{generation: generation, phase: s.phase, fence: lease.WorkerFence}
	s.mu.Unlock()
	if !ok {
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	candidates, err := leaser.LeaseLifecycleCandidates(request.Context(), int(lease.Requested))
	s.mu.Lock()
	if !s.controlStateMatchesLocked(control) {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	s.mu.Unlock()
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	if !validWorkerLifecycleCandidateLease(candidates, int(lease.Requested), assignment) {
		writeWorkerError(response, http.StatusInternalServerError, WorkerErrorRuntimeFailure)
		return
	}
	writeWorkerJSON(response, http.StatusOK, WorkerLifecycleCandidateLeaseResponse{
		WorkerFence: lease.WorkerFence, WorkerID: assignment.WorkerID, WorkerCount: assignment.WorkerCount,
		Candidates: append([]LifecycleCandidate(nil), candidates...),
	})
}

func (s *WorkerServer) handleLifecycleReheat(response http.ResponseWriter, request *http.Request) {
	var reheat WorkerLifecycleReheatRequest
	if !decodeWorkerJSON(response, request, &reheat) {
		return
	}
	if !validWorkerFence(reheat.WorkerFence) || !validLifecyclePersonChannelID(reheat.ChannelID) || reheat.TimerToken == 0 || reheat.ActivityVersion == 0 {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}
	s.mu.Lock()
	if !s.runningFenceLocked(response, reheat.WorkerFence) {
		s.mu.Unlock()
		return
	}
	generation := s.generation
	approver, ok := generation.(WorkerLifecycleReheatApprover)
	assignment := s.assignment
	control := workerControlState{generation: generation, phase: s.phase, fence: reheat.WorkerFence}
	s.mu.Unlock()
	if !ok {
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	approved, err := approver.ApproveLifecycleReheat(request.Context(), reheat.ChannelID, reheat.TimerToken, reheat.ActivityVersion)
	s.mu.Lock()
	if !s.controlStateMatchesLocked(control) {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	s.mu.Unlock()
	if err != nil || !approved {
		if request.Context().Err() != nil {
			return
		}
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	writeWorkerJSON(response, http.StatusOK, WorkerLifecycleReheatResponse{WorkerFence: reheat.WorkerFence, WorkerID: assignment.WorkerID, WorkerCount: assignment.WorkerCount, Approved: true})
}

func (s *WorkerServer) authenticated(request *http.Request) bool {
	const prefix = "Bearer "
	provided := request.Header.Get("Authorization")
	if len(provided) < len(prefix) || provided[:len(prefix)] != prefix {
		return false
	}
	providedHash := sha256.Sum256([]byte(provided[len(prefix):]))
	return subtle.ConstantTimeCompare(providedHash[:], s.tokenHash[:]) == 1
}

func (s *WorkerServer) handleHealth(response http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	phase := s.phase
	s.mu.Unlock()
	writeWorkerJSON(response, http.StatusOK, WorkerHealth{OK: true, Phase: phase})
}

func (s *WorkerServer) handleInfo(response http.ResponseWriter, _ *http.Request) {
	writeWorkerJSON(response, http.StatusOK, WorkerInfo{
		ProtocolVersion:  workerProtocolVersion,
		MaxRequestBytes:  workerMaxRequestBytes,
		MaxResponseBytes: workerMaxResponseBytes,
	})
}

func (s *WorkerServer) handleAssign(response http.ResponseWriter, request *http.Request) {
	var assignment WorkerAssignment
	if !decodeWorkerJSON(response, request, &assignment) {
		return
	}
	if !validWorkerAssignment(assignment) {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidAssignment)
		return
	}

	s.mu.Lock()
	if s.phase != WorkerPhaseUnassigned && s.phase != WorkerPhaseFinal {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorAssignmentConflict)
		return
	}
	if s.phase == WorkerPhaseFinal && assignment.Generation <= s.assignment.Generation {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	if request.Context().Err() != nil {
		s.mu.Unlock()
		return
	}
	generation, err := s.factory.New(assignment)
	if err != nil || generation == nil {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	if request.Context().Err() != nil {
		s.mu.Unlock()
		generation.Stop()
		return
	}
	s.assignment = assignment
	s.generation = generation
	s.phase = WorkerPhaseAssigned
	s.startedAt = time.Time{}
	s.final = WorkerSnapshot{}
	s.snapshotSequence = 0
	s.unexpected = false
	s.stop = nil
	s.grantInFlight = nil
	s.generationExited = false
	s.lastGrantRequest = WorkerGrantRequest{}
	s.lastGrantResult = WorkerGrantResponse{}
	s.lastGrantFailed = false
	status := s.statusLocked()
	s.mu.Unlock()
	writeWorkerJSON(response, http.StatusOK, status)
}

func (s *WorkerServer) handleStart(response http.ResponseWriter, request *http.Request) {
	var start WorkerStartRequest
	if !decodeWorkerJSON(response, request, &start) {
		return
	}
	if !validWorkerFence(start.WorkerFence) {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == WorkerPhaseUnassigned {
		writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
		return
	}
	if !sameWorkerFence(start.WorkerFence, s.assignment.WorkerFence) {
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	if s.phase != WorkerPhaseAssigned {
		writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
		return
	}
	if request.Context().Err() != nil {
		return
	}
	if err := s.generation.Start(request.Context()); err != nil {
		if request.Context().Err() != nil {
			s.beginCanceledStartCleanupLocked()
			return
		}
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	if request.Context().Err() != nil {
		s.beginCanceledStartCleanupLocked()
		return
	}
	s.startedAt = s.now()
	s.phase = WorkerPhaseRunning
	go s.watchGeneration(s.generation)
	writeWorkerJSON(response, http.StatusOK, s.statusLocked())
}

// beginCanceledStartCleanupLocked fences a generation whose Start crossed its
// request deadline so it cannot be started again after being stopped.
func (s *WorkerServer) beginCanceledStartCleanupLocked() {
	task := &workerStopTask{done: make(chan struct{})}
	s.stop = task
	s.phase = WorkerPhaseStopping
	go s.runStop(s.generation, task, false)
}

func (s *WorkerServer) handleStatus(response http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	status := s.statusLocked()
	s.mu.Unlock()
	writeWorkerJSON(response, http.StatusOK, status)
}

func (s *WorkerServer) handleSnapshot(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	generation := s.generation
	if s.phase == WorkerPhaseFinal {
		final := s.final
		s.mu.Unlock()
		writeWorkerJSON(response, http.StatusOK, final)
		return
	}
	if generation == nil {
		phase := s.phase
		s.mu.Unlock()
		writeWorkerJSON(response, http.StatusOK, WorkerSnapshot{Phase: phase})
		return
	}
	control := workerControlState{generation: generation, phase: s.phase, fence: s.assignment.WorkerFence}
	s.mu.Unlock()

	snapshot, err := generation.Snapshot(request.Context())
	s.mu.Lock()
	if !s.controlStateMatchesLocked(control) {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	if err != nil {
		s.mu.Unlock()
		if request.Context().Err() != nil {
			return
		}
		writeWorkerError(response, http.StatusInternalServerError, WorkerErrorRuntimeFailure)
		return
	}
	snapshot = s.overlaySnapshotLocked(snapshot)
	s.mu.Unlock()
	if !validWorkerSnapshot(snapshot) {
		writeWorkerError(response, http.StatusInternalServerError, WorkerErrorRuntimeFailure)
		return
	}
	writeWorkerJSON(response, http.StatusOK, snapshot)
}

func (s *WorkerServer) handleCheckpoint(response http.ResponseWriter, request *http.Request) {
	var checkpoint WorkerCheckpointRequest
	if !decodeWorkerJSON(response, request, &checkpoint) {
		return
	}
	if !validWorkerFence(checkpoint.WorkerFence) {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}

	s.mu.Lock()
	if !s.runningFenceLocked(response, checkpoint.WorkerFence) {
		s.mu.Unlock()
		return
	}
	generation := s.generation
	control := workerControlState{generation: generation, phase: s.phase, fence: checkpoint.WorkerFence}
	s.mu.Unlock()

	snapshot, err := generation.Checkpoint(request.Context())
	s.mu.Lock()
	if !s.controlStateMatchesLocked(control) {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	if err != nil {
		s.mu.Unlock()
		if request.Context().Err() != nil {
			return
		}
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	snapshot = s.overlaySnapshotLocked(snapshot)
	s.mu.Unlock()
	if !validWorkerSnapshot(snapshot) {
		writeWorkerError(response, http.StatusInternalServerError, WorkerErrorRuntimeFailure)
		return
	}
	writeWorkerJSON(response, http.StatusOK, snapshot)
}

func (s *WorkerServer) handleRate(response http.ResponseWriter, request *http.Request) {
	var rate WorkerRateRequest
	if !decodeWorkerJSON(response, request, &rate) {
		return
	}
	if !validWorkerFence(rate.WorkerFence) || rate.RatePerSecond == 0 || rate.RatePerSecond > ^uint64(0)/2 || rate.MaxBurst != 2*rate.RatePerSecond {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}

	s.mu.Lock()
	if !s.runningFenceLocked(response, rate.WorkerFence) {
		s.mu.Unlock()
		return
	}
	generation := s.generation
	control := workerControlState{generation: generation, phase: s.phase, fence: rate.WorkerFence}
	s.mu.Unlock()

	err := generation.UpdateRate(request.Context(), rate.RatePerSecond, rate.MaxBurst)
	s.mu.Lock()
	if !s.controlStateMatchesLocked(control) {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	if err != nil {
		s.mu.Unlock()
		if request.Context().Err() != nil {
			return
		}
		writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
		return
	}
	status := s.statusLocked()
	s.mu.Unlock()
	writeWorkerJSON(response, http.StatusOK, status)
}

func (s *WorkerServer) handleGrant(response http.ResponseWriter, request *http.Request) {
	var grant WorkerGrantRequest
	if !decodeWorkerJSON(response, request, &grant) {
		return
	}
	if !validWorkerGrant(grant) {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}

	for {
		s.mu.Lock()
		if s.phase == WorkerPhaseUnassigned {
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
			return
		}
		if !sameWorkerFence(grant.WorkerFence, s.assignment.WorkerFence) {
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
			return
		}
		if !s.assignment.CoordinatorGrants {
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
			return
		}
		if grant.Sequence == s.lastGrantRequest.Sequence && grant.Sequence != 0 {
			if grant != s.lastGrantRequest {
				s.mu.Unlock()
				writeWorkerError(response, http.StatusConflict, WorkerErrorGrantConflict)
				return
			}
			result, failed := s.lastGrantResult, s.lastGrantFailed
			s.mu.Unlock()
			if failed {
				writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
			} else {
				writeWorkerJSON(response, http.StatusOK, result)
			}
			return
		}
		if active := s.grantInFlight; active != nil {
			if grant != active.request {
				s.mu.Unlock()
				writeWorkerError(response, http.StatusConflict, WorkerErrorGrantConflict)
				return
			}
			s.mu.Unlock()
			select {
			case <-request.Context().Done():
				return
			case <-active.done:
				continue
			}
		}
		if s.phase != WorkerPhaseRunning || s.generationExited {
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
			return
		}
		if grant.Sequence < s.lastGrantRequest.Sequence {
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorGrantStale)
			return
		}
		if grant.Sequence != s.lastGrantRequest.Sequence+1 {
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorGrantGap)
			return
		}
		generation := s.generation
		workerID := s.assignment.WorkerID
		control := workerControlState{generation: generation, phase: s.phase, fence: grant.WorkerFence}
		task := &workerGrantTask{request: grant, done: make(chan struct{})}
		s.grantInFlight = task
		s.mu.Unlock()

		released, _ := grant.Released.worker(workerID)
		application, grantErr := generation.ApplyGrant(request.Context(), released)
		s.mu.Lock()
		if !s.controlStateMatchesLocked(control) {
			if s.grantInFlight == task {
				s.grantInFlight = nil
			}
			close(task.done)
			s.mu.Unlock()
			writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
			return
		}
		result := WorkerGrantResponse{
			WorkerFence: grant.WorkerFence, WorkerID: s.assignment.WorkerID,
			WorkerCount: s.assignment.WorkerCount, Sequence: grant.Sequence, Released: released,
		}
		if !application.Admitted {
			s.grantInFlight = nil
			close(task.done)
			s.mu.Unlock()
			if request.Context().Err() != nil {
				return
			}
			writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
			return
		}
		if runtimeFailureTerminatesGeneration(grantErr) {
			s.generationExited = true
		}
		s.lastGrantRequest = grant
		s.lastGrantResult = result
		s.lastGrantFailed = grantErr != nil
		s.grantInFlight = nil
		close(task.done)
		s.mu.Unlock()
		if grantErr != nil {
			if request.Context().Err() != nil {
				return
			}
			writeWorkerError(response, http.StatusUnprocessableEntity, WorkerErrorRuntimeFailure)
			return
		}
		writeWorkerJSON(response, http.StatusOK, result)
		return
	}
}

func (s *WorkerServer) handleStop(response http.ResponseWriter, request *http.Request) {
	var stop WorkerStopRequest
	if !decodeWorkerJSON(response, request, &stop) {
		return
	}
	if !validWorkerFence(stop.WorkerFence) {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidRequest)
		return
	}

	s.mu.Lock()
	if s.phase == WorkerPhaseUnassigned {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
		return
	}
	if !sameWorkerFence(stop.WorkerFence, s.assignment.WorkerFence) {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return
	}
	if s.phase == WorkerPhaseFinal {
		final := s.final
		s.mu.Unlock()
		writeWorkerJSON(response, http.StatusOK, final)
		return
	}
	if s.phase != WorkerPhaseAssigned && s.phase != WorkerPhaseRunning && s.phase != WorkerPhaseStopping {
		s.mu.Unlock()
		writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
		return
	}
	task := s.stop
	if task == nil {
		task = &workerStopTask{done: make(chan struct{})}
		s.stop = task
		drain := s.phase == WorkerPhaseRunning
		s.phase = WorkerPhaseStopping
		go s.runStop(s.generation, task, drain)
	}
	s.mu.Unlock()

	select {
	case <-task.done:
		s.mu.Lock()
		final := s.final
		s.mu.Unlock()
		writeWorkerJSON(response, http.StatusOK, final)
	case <-request.Context().Done():
		return
	}
}

func (s *WorkerServer) runStop(generation WorkerGeneration, task *workerStopTask, drain bool) {
	var drainErr error
	drainTimedOut := false
	ctx, cancel := context.WithTimeout(context.Background(), s.drainTimeout)
	if drain {
		drainErr = generation.Drain(ctx)
		drainTimedOut = drainErr != nil && (errors.Is(drainErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded))
	}
	generation.Stop()
	cancel()
	snapshotCtx, snapshotCancel := context.WithTimeout(context.Background(), s.drainTimeout)
	final, snapshotErr := generation.Snapshot(snapshotCtx)
	snapshotCancel()

	s.mu.Lock()
	s.phase = WorkerPhaseFinal
	final = s.overlaySnapshotLocked(final)
	if drainErr != nil {
		addWorkerHarnessFailure(&final)
		final.Harness.DrainTimedOut = final.Harness.DrainTimedOut || drainTimedOut
	}
	if snapshotErr != nil {
		addWorkerHarnessFailure(&final)
	}
	final = s.closedFinalSnapshotLocked(final)
	s.final = final
	close(task.done)
	s.mu.Unlock()
}

func (s *WorkerServer) watchGeneration(generation WorkerGeneration) {
	_, open := <-generation.Done()
	if !open {
		// A closed generation channel is still a terminal runtime event.
	}
	s.mu.Lock()
	if s.generation != generation || s.phase != WorkerPhaseRunning {
		s.mu.Unlock()
		return
	}
	// Fence new controls before waiting for the one grant that may have caused
	// terminal teardown. That admitted grant owns a stable replay boundary and
	// must commit its result before the worker publishes the final snapshot.
	s.generationExited = true
	grant := s.grantInFlight
	s.mu.Unlock()
	if grant != nil {
		<-grant.done
	}

	snapshotCtx, snapshotCancel := context.WithTimeout(context.Background(), s.drainTimeout)
	final, snapshotErr := generation.Snapshot(snapshotCtx)
	snapshotCancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation || s.phase != WorkerPhaseRunning {
		return
	}
	s.unexpected = true
	s.phase = WorkerPhaseFinal
	final = s.overlaySnapshotLocked(final)
	addWorkerHarnessFailure(&final)
	if snapshotErr != nil {
		addWorkerHarnessFailure(&final)
	}
	final.Harness.UnexpectedExit = true
	final = s.closedFinalSnapshotLocked(final)
	s.final = final
	select {
	case <-s.unexpectedExit:
	default:
		close(s.unexpectedExit)
	}
}

// UnexpectedExit closes if an active generation terminates without an explicit stop.
func (s *WorkerServer) UnexpectedExit() <-chan struct{} { return s.unexpectedExit }

func (s *WorkerServer) runningFenceLocked(response http.ResponseWriter, fence WorkerFence) bool {
	if s.phase == WorkerPhaseUnassigned {
		writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
		return false
	}
	if !sameWorkerFence(fence, s.assignment.WorkerFence) {
		writeWorkerError(response, http.StatusConflict, WorkerErrorFenceMismatch)
		return false
	}
	if s.phase != WorkerPhaseRunning || s.generationExited {
		writeWorkerError(response, http.StatusConflict, WorkerErrorInvalidState)
		return false
	}
	return true
}

func (s *WorkerServer) controlStateMatchesLocked(control workerControlState) bool {
	return s.generation == control.generation && s.phase == control.phase &&
		sameWorkerFence(s.assignment.WorkerFence, control.fence)
}

func (s *WorkerServer) statusLocked() WorkerStatus {
	trafficReady := false
	if s.phase == WorkerPhaseRunning && !s.generationExited && s.generation != nil {
		trafficReady = s.generation.TrafficReady()
	}
	return WorkerStatus{
		RunID:        s.assignment.RunID,
		AssignmentID: s.assignment.AssignmentID,
		Phase:        s.phase,
		Generation:   s.assignment.Generation,
		WorkerID:     s.assignment.WorkerID,
		WorkerCount:  s.assignment.WorkerCount,
		Unexpected:   s.unexpected,
		TrafficReady: trafficReady,
	}
}

func (s *WorkerServer) overlaySnapshotLocked(snapshot WorkerSnapshot) WorkerSnapshot {
	snapshot.RunID = s.assignment.RunID
	snapshot.AssignmentID = s.assignment.AssignmentID
	snapshot.Phase = s.phase
	if s.snapshotSequence < ^uint64(0) {
		s.snapshotSequence++
	}
	snapshot.SnapshotSequence = s.snapshotSequence
	snapshot.Generation = s.assignment.Generation
	snapshot.WorkerID = s.assignment.WorkerID
	snapshot.WorkerCount = s.assignment.WorkerCount
	if !s.startedAt.IsZero() {
		now := s.now()
		if now.After(s.startedAt) {
			snapshot.Uptime = now.Sub(s.startedAt)
		}
	}
	return snapshot
}

func (s *WorkerServer) closedFinalSnapshotLocked(snapshot WorkerSnapshot) WorkerSnapshot {
	mergeWorkerSnapshotClassification(&snapshot)
	if validWorkerSnapshot(snapshot) {
		return snapshot
	}
	addWorkerHarnessFailure(&snapshot)
	harness := snapshot.Harness
	return WorkerSnapshot{
		RunID:            s.assignment.RunID,
		AssignmentID:     s.assignment.AssignmentID,
		Phase:            WorkerPhaseFinal,
		Generation:       s.assignment.Generation,
		WorkerID:         s.assignment.WorkerID,
		WorkerCount:      s.assignment.WorkerCount,
		Uptime:           snapshot.Uptime,
		SnapshotSequence: snapshot.SnapshotSequence,
		Harness:          harness,
		Evidence:         EvidenceSnapshot{Classification: harness.Classification},
	}
}

func addWorkerHarnessFailure(snapshot *WorkerSnapshot) {
	if snapshot.Harness.Failures != ^uint64(0) {
		snapshot.Harness.Failures++
	}
	mergeWorkerSnapshotClassification(snapshot, SyncClassificationHarnessInvalid)
}

func mergeWorkerSnapshotClassification(snapshot *WorkerSnapshot, additions ...SyncClassification) {
	values := make([]SyncClassification, 0, len(additions)+2)
	values = append(values, snapshot.Evidence.Classification, snapshot.Harness.Classification)
	values = append(values, additions...)
	classification := mergeSyncClassification(values...)
	snapshot.Evidence.Classification = classification
	snapshot.Harness.Classification = classification
}

func mergeSyncClassification(values ...SyncClassification) SyncClassification {
	result := SyncClassification("")
	for _, value := range values {
		switch value {
		case SyncClassificationProductFailure:
			return SyncClassificationProductFailure
		case SyncClassificationHarnessInvalid:
			result = SyncClassificationHarnessInvalid
		case "":
		default:
			// Unknown values fail closed as invalid harness output.
			result = SyncClassificationHarnessInvalid
		}
	}
	return result
}

func validWorkerFence(fence WorkerFence) bool {
	return validWorkerIDString(fence.RunID) && validWorkerIDString(fence.AssignmentID) && fence.Generation > 0
}

func validWorkerIDString(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value
}

func sameWorkerFence(left, right WorkerFence) bool {
	return left.RunID == right.RunID && left.AssignmentID == right.AssignmentID && left.Generation == right.Generation
}

func validWorkerAssignment(assignment WorkerAssignment) bool {
	if !validWorkerFence(assignment.WorkerFence) || assignment.RunID != assignment.Config.RunID || assignment.WorkerCount == 0 || assignment.WorkerID >= assignment.WorkerCount {
		return false
	}
	if assignment.WorkerCount > uint64(^uint(0)>>1) || int(assignment.WorkerCount) != assignment.Config.Workload.Workers {
		return false
	}
	return assignment.Config.Validate() == nil
}

func validWorkerGrant(grant WorkerGrantRequest) bool {
	if !validWorkerFence(grant.WorkerFence) || grant.Sequence == 0 || grant.RatePerSecond == 0 ||
		grant.RatePerSecond > ^uint64(0)/2 || grant.MaxBurst != 2*grant.RatePerSecond {
		return false
	}
	fresh, freshOK := sumWorkerGrantCounts(grant.Fresh)
	released, releasedOK := sumWorkerGrantCounts(grant.Released)
	credit, creditOK := sumWorkerGrantCounts(grant.Credit)
	return freshOK && releasedOK && creditOK && fresh == grant.RatePerSecond &&
		released <= grant.MaxBurst && credit <= grant.MaxBurst
}

func sumWorkerGrantCounts(counts WorkerGrantCounts) (uint64, bool) {
	if ^uint64(0)-counts.Worker0 < counts.Worker1 {
		return 0, false
	}
	total := counts.Worker0 + counts.Worker1
	if ^uint64(0)-total < counts.Worker2 {
		return 0, false
	}
	return total + counts.Worker2, true
}

func validWorkerSnapshot(snapshot WorkerSnapshot) bool {
	if snapshot.Messages.FirstAttemptFailures > snapshot.Messages.FirstAttempts {
		return false
	}
	thresholds := snapshot.Sync.Thresholds
	if thresholds != (LatencyThresholdCounters{}) &&
		(thresholds.P99Limit <= 0 || thresholds.P999Limit < thresholds.P99Limit ||
			thresholds.AboveP99 > thresholds.Count || thresholds.AboveP999 > thresholds.AboveP99 ||
			thresholds.Above10Seconds > thresholds.AboveP999) {
		return false
	}
	if len(snapshot.Evidence.Classes) > int(FailureClassHarness) {
		return false
	}
	seen := [FailureClassHarness + 1]bool{}
	for _, class := range snapshot.Evidence.Classes {
		if class.Class < FailureClassSend || class.Class > FailureClassHarness || seen[class.Class] ||
			len(class.First) > maxEvidenceExamplesPerSide || len(class.Last) > maxEvidenceExamplesPerSide {
			return false
		}
		seen[class.Class] = true
	}
	return true
}

func decodeWorkerJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(response, request.Body, workerMaxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeWorkerError(response, http.StatusRequestEntityTooLarge, WorkerErrorRequestTooLarge)
		} else {
			writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidJSON)
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeWorkerError(response, http.StatusBadRequest, WorkerErrorInvalidJSON)
		return false
	}
	return true
}

func writeWorkerError(response http.ResponseWriter, status int, code WorkerErrorCode) {
	writeWorkerJSON(response, status, WorkerAPIError{Code: code})
}

func writeWorkerJSON(response http.ResponseWriter, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil || int64(len(encoded))+1 > workerMaxResponseBytes {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusInternalServerError)
		_, _ = response.Write([]byte(`{"code":"runtime_failure"}` + "\n"))
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(append(encoded, '\n'))
}

type engineWorkerGenerationFactory struct {
	clock             SessionClock
	newSessionFactory func(WorkerAssignment) (SessionClientFactory, error)
	newSyncer         func(WorkerAssignment) (ConversationSyncer, error)
	newTicker         func(time.Duration) workerGenerationTicker
}

// NewEngineWorkerGenerationFactory composes the existing deterministic models,
// real conversation-sync client, WKProto adapter, verifier, and Engine. It does
// not introduce a parallel traffic runtime.
func NewEngineWorkerGenerationFactory() WorkerGenerationFactory {
	return engineWorkerGenerationFactory{}
}

func (f engineWorkerGenerationFactory) New(assignment WorkerAssignment) (WorkerGeneration, error) {
	if !validWorkerAssignment(assignment) || assignment.Generation > maxLogicalGeneration {
		return nil, errWorkerServerConfig
	}
	config := assignment.Config
	identity, err := NewIdentitySpace(config.RunID, config.Seed, assignment.WorkerCount)
	if err != nil {
		return nil, err
	}
	schedule, err := NewScheduleModel(identity, config.Workload)
	if err != nil {
		return nil, err
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		return nil, err
	}
	traffic, err := NewTrafficModel(identity, config.Workload)
	if err != nil {
		return nil, err
	}
	retry, err := NewRetryPolicy(identity, config.Workload.Retry)
	if err != nil {
		return nil, err
	}
	catalog, err := NewGroupCatalog(identity, config.Workload.Groups)
	if err != nil {
		return nil, err
	}
	evidence, err := NewEvidenceRecorder(maxEvidenceExamplesPerSide, maxEvidenceExamplesPerSide)
	if err != nil {
		return nil, err
	}
	limits, err := workerEngineLimitsFor(assignment)
	if err != nil {
		return nil, err
	}
	verifier, err := NewVerifier(traffic, VerifierConfig{
		PendingCapacity:     limits.pending,
		SequenceCapacity:    limits.sequence,
		CorrelationCapacity: limits.correlation,
		CorrelationDeadline: config.Thresholds.Latency.SingleAnomaly,
	}, evidence)
	if err != nil {
		return nil, err
	}
	clock := f.clock
	if clock == nil {
		clock = wallSessionClock{}
	}
	newSessionFactory := f.newSessionFactory
	if newSessionFactory == nil {
		newSessionFactory = func(assignment WorkerAssignment) (SessionClientFactory, error) {
			gatewayAddress := assignment.Config.Observation.GatewayTCPAddrs[int(assignment.WorkerID)%len(assignment.Config.Observation.GatewayTCPAddrs)]
			return engineWorkerSessionFactory{
				address: gatewayAddress, ackTimeout: assignment.Config.Thresholds.Latency.SingleAnomaly,
			}, nil
		}
	}
	sessionFactory, err := newSessionFactory(assignment)
	if err != nil || sessionFactory == nil {
		return nil, errWorkerServerConfig
	}
	newSyncer := f.newSyncer
	if newSyncer == nil {
		newSyncer = func(assignment WorkerAssignment) (ConversationSyncer, error) {
			return target.NewClient(target.Config{APIAddrs: assignment.Config.Observation.APIAddrs}), nil
		}
	}
	syncer, err := newSyncer(assignment)
	if err != nil || syncer == nil {
		return nil, errWorkerServerConfig
	}
	sessions, err := NewSessionPool(SessionPoolConfig{
		Identity:         identity,
		Schedule:         schedule,
		Catalog:          catalog,
		Factory:          sessionFactory,
		Syncer:           syncer,
		Verifier:         verifier,
		Clock:            clock,
		DeviceID:         "wkbench-chat-lifecycle-worker-" + strconv.FormatUint(assignment.WorkerID, 10),
		StartingCapacity: limits.starting,
		SyncLatency:      config.Thresholds.Latency.Sync, SingleAnomaly: config.Thresholds.Latency.SingleAnomaly,
	})
	if err != nil {
		return nil, err
	}
	generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
		Identity:    identity,
		Model:       traffic,
		Catalog:     catalog,
		Workload:    config.Workload,
		Start:       clock.Now(),
		WorkerID:    assignment.WorkerID,
		WorkerCount: assignment.WorkerCount,
	})
	if err != nil {
		return nil, err
	}
	engine, err := NewEngine(EngineConfig{
		Clock: clock, Sessions: sessions, Schedule: schedule, Graph: graph, Traffic: traffic,
		Generator: generator, Retry: retry, Verifier: verifier, Evidence: evidence,
		WorkerID: assignment.WorkerID, WorkerCount: assignment.WorkerCount,
		CommandCapacity: limits.command, WorkCapacity: limits.work, RetryCapacity: limits.retry,
		InflightCapacity: limits.inflight, MaxWorkPerAdvance: limits.maxWork,
		AttemptTimeout:            config.Thresholds.Latency.HotSendACK.P999,
		ActivityEligibilityWindow: config.Thresholds.Latency.SustainedBreachWindow,
	})
	if err != nil {
		return nil, err
	}
	newTicker := f.newTicker
	if newTicker == nil {
		newTicker = newWallWorkerGenerationTicker
	}
	trafficDemand := make([]uint64, assignment.WorkerCount)
	for index := range trafficDemand {
		trafficDemand[index] = ^uint64(0)
	}
	lifecycleSlots, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		return nil, err
	}
	return &engineWorkerGeneration{
		engine: engine, verifier: verifier, evidence: evidence,
		// The reviewed no-migration profile uses this validated continuous table.
		// A migration-aware coordinator must inject its copied live mapping.
		lifecycleSlots: lifecycleSlots,
		onlineTarget:   limits.online,
		trafficDemand:  trafficDemand,
		externalGrants: assignment.CoordinatorGrants,
		generation:     assignment.Generation,
		clock:          clock,
		newTicker:      newTicker,
		done:           make(chan error, 1),
	}, nil
}

type workerEngineLimits struct {
	command, work, retry, inflight, maxWork  int
	pending, sequence, correlation, starting int
	online                                   int
}

func workerEngineLimitsFor(assignment WorkerAssignment) (workerEngineLimits, error) {
	config := assignment.Config
	online, err := workerOnlineTarget(config.Workload.OnlineUsers, assignment.WorkerID, assignment.WorkerCount)
	if err != nil {
		return workerEngineLimits{}, err
	}
	localRate, err := workerOnlineTarget(config.Workload.SendRatePerSecond, assignment.WorkerID, assignment.WorkerCount)
	if err != nil {
		return workerEngineLimits{}, err
	}
	localBurst, err := workerOnlineTarget(config.Workload.MaxGlobalBurst, assignment.WorkerID, assignment.WorkerCount)
	if err != nil {
		return workerEngineLimits{}, err
	}
	deadlineSeconds := uint64(config.Thresholds.Latency.SingleAnomaly / time.Second)
	if config.Thresholds.Latency.SingleAnomaly%time.Second != 0 {
		deadlineSeconds++
	}
	pending := boundedWorkerCapacity(cappedWorkerSum(cappedWorkerProduct(uint64(localRate), deadlineSeconds), uint64(localBurst), 1024), 4096)
	work := boundedWorkerCapacity(cappedWorkerSum(cappedWorkerProduct(uint64(online), uint64(MaxForwardRelationships)), cappedWorkerProduct(2, uint64(localBurst))), 4096)
	sequenceChannelsPerOnline := cappedWorkerSum(uint64(MaxUserRelationships), uint64(MaxFixedGroupMembershipsPerUser))
	sequence := boundedWorkerCapacity(cappedWorkerProduct(uint64(online), sequenceChannelsPerOnline), 4096)
	correlation := boundedWorkerCapacity(cappedWorkerProduct(uint64(config.Workload.RuntimeSampling.Size), 2), 1024)
	if correlation > pending {
		correlation = pending
	}
	starting := min(online, 256)
	if starting < 1 {
		starting = 1
	}
	command := boundedWorkerCapacity(cappedWorkerProduct(uint64(starting), 4), 2048)
	maxWork := boundedWorkerCapacity(cappedWorkerSum(cappedWorkerProduct(uint64(starting), uint64(MaxForwardRelationships)), uint64(localBurst)), 1024)
	if maxWork > work {
		maxWork = work
	}
	return workerEngineLimits{
		command: command, work: work, retry: pending, inflight: pending, maxWork: maxWork,
		pending: pending, sequence: sequence, correlation: correlation, starting: starting, online: online,
	}, nil
}

func boundedWorkerCapacity(value uint64, minimum int) int {
	if value < uint64(minimum) {
		return minimum
	}
	if value > maxVerifierCapacity {
		return maxVerifierCapacity
	}
	return int(value)
}

func cappedWorkerProduct(left, right uint64) uint64 {
	maximum := uint64(maxVerifierCapacity)
	if left == 0 || right == 0 {
		return 0
	}
	if left > maximum/right {
		return maximum
	}
	product := left * right
	if product > maximum {
		return maximum
	}
	return product
}

func cappedWorkerSum(values ...uint64) uint64 {
	maximum := uint64(maxVerifierCapacity)
	var sum uint64
	for _, value := range values {
		if value >= maximum-sum {
			return maximum
		}
		sum += value
	}
	return sum
}

type wallSessionClock struct{}

func (wallSessionClock) Now() time.Time { return time.Now() }

type workerGenerationTicker interface {
	Wait(context.Context) bool
	Stop()
}

type wallWorkerGenerationTicker struct{ ticker *time.Ticker }

func newWallWorkerGenerationTicker(interval time.Duration) workerGenerationTicker {
	return &wallWorkerGenerationTicker{ticker: time.NewTicker(interval)}
}

func (t *wallWorkerGenerationTicker) Wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-t.ticker.C:
		return true
	}
}

func (t *wallWorkerGenerationTicker) Stop() { t.ticker.Stop() }

type engineWorkerSessionFactory struct {
	address    string
	ackTimeout time.Duration
}

func (f engineWorkerSessionFactory) NewSession(_ context.Context, _, token string) (SessionClient, error) {
	client, err := wkproto.NewClient(wkproto.ClientConfig{
		Addr: f.address, Token: token,
		OperationTimeout: 5 * time.Second, AckTimeout: f.ackTimeout,
		SendQueueCapacity: 32, MaxInflight: 32, ReadBufferSize: 64 << 10, FrameBufferSize: 32,
	})
	if err != nil {
		return nil, err
	}
	return NewWKProtoSessionAdapter(client)
}

type engineWorkerGeneration struct {
	engine         *Engine
	verifier       *Verifier
	evidence       *EvidenceRecorder
	lifecycleSlots LifecycleSlotAssignment
	// onlineTarget and trafficDemand are immutable O(1) tick inputs derived
	// from the assignment; coordinator snapshots are never used by the latch.
	onlineTarget   int
	trafficDemand  []uint64
	externalGrants bool
	generation     uint64
	clock          SessionClock
	newTicker      func(time.Duration) workerGenerationTicker

	mu      sync.Mutex
	started bool
	// trafficReady remains sticky across churn once the initial synchronized
	// online target has been observed. Status reads it without engine I/O.
	trafficReady atomic.Bool
	tickCancel   context.CancelFunc
	tickDone     chan struct{}
	done         chan error
	doneOnce     sync.Once
}

func (g *engineWorkerGeneration) Start(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.started {
		return errEngineRunning
	}
	if err := g.engine.StartGeneration(context.Background(), g.generation); err != nil {
		return err
	}
	tickContext, cancel := context.WithCancel(context.Background())
	g.tickCancel = cancel
	g.tickDone = make(chan struct{})
	g.started = true
	go g.runTicks(tickContext, g.tickDone)
	return nil
}

func (g *engineWorkerGeneration) runTicks(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := g.newTicker(time.Second)
	if ticker == nil {
		g.terminateFromTick(errWorkerServerConfig)
		return
	}
	defer ticker.Stop()
	for ticker.Wait(ctx) {
		if err := g.step(ctx, g.clock.Now()); err != nil {
			if ctx.Err() != nil {
				return
			}
			if workerStepErrorIsEvidence(err) {
				continue
			}
			g.terminateFromTick(err)
			return
		}
	}
}

func (g *engineWorkerGeneration) step(ctx context.Context, now time.Time) error {
	var demand []uint64
	if !g.externalGrants && g.trafficReady.Load() {
		demand = g.trafficDemand
	}
	step, err := g.engine.Step(ctx, now, demand)
	if !g.trafficReady.Load() && step.Online >= g.onlineTarget {
		g.trafficReady.Store(true)
	}
	return err
}

func workerStepErrorIsEvidence(err error) bool {
	if err == nil {
		return true
	}
	// Clock rollback is classified but deliberately records no evidence because
	// admission must fail before mutation. Replay saturation is likewise
	// terminal because its due work has already left the owner heap.
	if runtimeFailureTerminatesGeneration(err) {
		return false
	}
	if classified, ok := err.(interface{ Classification() SyncClassification }); ok {
		classification := classified.Classification()
		return classification == SyncClassificationHarnessInvalid || classification == SyncClassificationProductFailure
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !workerStepErrorIsEvidence(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return workerStepErrorIsEvidence(wrapped.Unwrap())
	}
	return false
}

func (g *engineWorkerGeneration) UpdateRate(ctx context.Context, ratePerSecond, maxBurst uint64) error {
	return g.engine.ScheduleRateContext(ctx, ratePerSecond, maxBurst)
}

func (g *engineWorkerGeneration) ApplyGrant(ctx context.Context, released uint64) (WorkerGrantApplication, error) {
	if !g.externalGrants || !g.trafficReady.Load() {
		return WorkerGrantApplication{}, errWorkerServerConfig
	}
	result, err := g.engine.ApplyGrant(ctx, g.clock.Now(), released)
	if runtimeFailureTerminatesGeneration(err) {
		g.terminateFromExternal(err)
	}
	return WorkerGrantApplication{Admitted: result.Admitted}, err
}

func (g *engineWorkerGeneration) LeaseLifecycleCandidates(ctx context.Context, requested int) ([]LifecycleCandidate, error) {
	return g.engine.LeaseLifecycleCandidates(ctx, requested, g.lifecycleSlots)
}

func (g *engineWorkerGeneration) ApproveLifecycleReheat(ctx context.Context, identity string, timerToken, activityVersion uint64) (bool, error) {
	return g.engine.ApproveColdRevisitContext(ctx, identity, timerToken, activityVersion)
}

func (g *engineWorkerGeneration) TrafficReady() bool { return g.trafficReady.Load() }

func (g *engineWorkerGeneration) Checkpoint(ctx context.Context) (WorkerSnapshot, error) {
	return g.workerSnapshot(ctx)
}

func (g *engineWorkerGeneration) Drain(ctx context.Context) error {
	g.stopTicks()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		drain := g.verifier.DrainSnapshot()
		if drain.PendingUnfinished == 0 && drain.CorrelationOutstanding == 0 {
			return nil
		}
		if _, err := g.engine.AdvanceContext(ctx, g.clock.Now()); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (g *engineWorkerGeneration) Stop() {
	g.stopTicks()
	_ = g.engine.Stop()
	g.finish(nil)
}

func (g *engineWorkerGeneration) stopTicks() {
	done := g.cancelTicks()
	if done != nil {
		<-done
	}
}

// cancelTicks fences future ticker work without joining it. External terminal
// cleanup uses this split phase to avoid a tick/Engine.Stop dependency cycle.
func (g *engineWorkerGeneration) cancelTicks() <-chan struct{} {
	g.mu.Lock()
	cancel, done := g.tickCancel, g.tickDone
	g.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return done
}

// terminateFromTick never joins tickDone because it runs on that goroutine.
func (g *engineWorkerGeneration) terminateFromTick(err error) {
	_ = g.engine.Stop()
	g.finish(err)
}

// terminateFromExternal cancels ticks, stops and joins all Engine operations,
// then joins the ticker before publishing the one terminal generation result.
func (g *engineWorkerGeneration) terminateFromExternal(err error) {
	done := g.cancelTicks()
	_ = g.engine.Stop()
	if done != nil {
		<-done
	}
	g.finish(err)
}

func (g *engineWorkerGeneration) Snapshot(ctx context.Context) (WorkerSnapshot, error) {
	return g.workerSnapshot(ctx)
}

func (g *engineWorkerGeneration) workerSnapshot(ctx context.Context) (WorkerSnapshot, error) {
	runtime, err := g.engine.WorkerRuntimeSnapshotContext(ctx)
	if err != nil {
		return WorkerSnapshot{}, err
	}
	engine := runtime.Engine
	generated := runtime.Generated
	verification := g.verifier.Snapshot()
	evidence := g.evidence.Snapshot()
	return WorkerSnapshot{
		Generation: engine.Generation, WorkerID: engine.WorkerID, WorkerCount: engine.WorkerCount,
		Sessions: WorkerSessionSnapshot{
			Target: engine.OnlineTarget, Online: engine.Online, Starting: engine.LoginStarting, TrafficReady: engine.TrafficReady,
			PlannedNew: engine.LoginPlannedNew, PlannedReturning: engine.LoginPlannedReturning,
			CompletedNew: engine.LoginCompletedNew, CompletedReturning: engine.LoginCompletedReturning, Expired: engine.SessionsExpired,
		},
		Generated: WorkerGeneratedSnapshot{
			Primary: generated.PrimaryReleased, Person: generated.Person, Group: generated.Group,
			Canary: generated.Canaries, PayloadBytes: generated.PayloadBytes,
		},
		MetaCreate: WorkerMetaCreateSnapshot{PersonByHashSlot: engine.MetaCreatePersonByHashSlot},
		Messages: WorkerMessageSnapshot{
			Sent: verification.Sent, SendAttempts: verification.Attempts, SendAcknowledged: verification.Acknowledged,
			FirstAttempts: verification.FirstAttempts, FirstAttemptFailures: verification.FirstAttemptFailures,
			SendRejected: verification.SendackRejections, Received: verification.Received,
			ReceiveAcknowledged: verification.ReceiveAcknowledged, ReceiveAckFailures: verification.ReceiveAckFailures,
			RetryAttempts: verification.RetryAttempts, Terminal: verification.Terminal,
			Losses: verification.Losses, Duplicates: verification.Duplicates,
			Corruptions: verification.Corruptions, SequenceRegressions: verification.SequenceRegressions,
		},
		Sync: WorkerSyncSnapshot{
			CompletedNew: engine.LoginCompletedNew, CompletedReturning: engine.LoginCompletedReturning,
			FactoryFailed: engine.FactoryFailed, FactoryCanceled: engine.FactoryCanceled,
			ConnectStarted: engine.ConnectStarted, ConnectCompleted: engine.ConnectCompleted,
			ConnectFailed: engine.ConnectFailed, ConnectCanceled: engine.ConnectCanceled,
			SyncStarted: engine.SyncStarted, SyncCompleted: engine.SyncCompleted,
			SyncFailed: engine.SyncFailed, SyncCanceled: engine.SyncCanceled, Failures: engine.SyncFailed,
			ConnectLatency: engine.GatewayConnectLatency, Latency: engine.ConversationSyncLatency,
			Thresholds: engine.ConversationSyncThresholds,
		},
		SendackLatency: verification.SendackLatency,
		RecvackLatency: verification.RecvackLatency,
		Correlation: WorkerCorrelationSnapshot{
			PendingUnfinished: verification.PendingUnfinished, Outstanding: verification.CorrelationCurrent,
			Sampled: verification.Sampled, Delivered: verification.SampledDelivered, Expired: verification.SampledExpired,
			DuplicateCompletions: verification.DuplicateCompletions, ConflictingCompletions: verification.ConflictingCompletions,
			UnknownAcknowledgments: verification.UnknownSendacks,
		},
		Queues: WorkerQueueSnapshot{
			WorkCurrent: engine.QueueCurrent, WorkPeak: engine.QueuePeak, WorkCapacity: engine.QueueCapacity,
			RetryCurrent: engine.RetryQueueDepth, RetryPeak: engine.RetryQueuePeak, RetryCapacity: engine.RetryQueueCapacity,
			InflightCurrent: engine.InflightCurrent, InflightPeak: engine.InflightPeak, InflightCapacity: engine.InflightCapacity,
			TransportCurrent: engine.TransportQueueDepth, TransportCapacity: engine.TransportQueueCapacity,
		},
		Harness: WorkerHarnessSnapshot{
			Classification: evidence.Classification, Failures: engine.HarnessInvalid,
			CommandSaturation: engine.CommandSaturation, OfferedUnderdelivery: engine.ActivityUnderDelivered,
		},
		Evidence: evidence,
	}, nil
}

func (g *engineWorkerGeneration) Done() <-chan error { return g.done }

func (g *engineWorkerGeneration) finish(err error) {
	g.doneOnce.Do(func() {
		g.done <- err
		close(g.done)
	})
}

var _ SessionClientFactory = engineWorkerSessionFactory{}
var _ WorkerGeneration = (*engineWorkerGeneration)(nil)
