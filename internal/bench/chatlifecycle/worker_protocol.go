package chatlifecycle

import "time"

const (
	workerProtocolVersion  uint64 = 2
	workerMaxRequestBytes  int64  = 1 << 20
	workerMaxResponseBytes int64  = 4 << 20
)

// WorkerPhase is the closed lifecycle vocabulary exposed by the worker API.
type WorkerPhase string

const (
	WorkerPhaseUnassigned WorkerPhase = "unassigned"
	WorkerPhaseAssigned   WorkerPhase = "assigned"
	WorkerPhaseRunning    WorkerPhase = "running"
	WorkerPhaseStopping   WorkerPhase = "stopping"
	WorkerPhaseFinal      WorkerPhase = "final"
)

// WorkerErrorCode is a stable low-cardinality API error vocabulary.
type WorkerErrorCode string

const (
	WorkerErrorUnauthorized       WorkerErrorCode = "unauthorized"
	WorkerErrorNotFound           WorkerErrorCode = "not_found"
	WorkerErrorMethodNotAllowed   WorkerErrorCode = "method_not_allowed"
	WorkerErrorInvalidJSON        WorkerErrorCode = "invalid_json"
	WorkerErrorRequestTooLarge    WorkerErrorCode = "request_too_large"
	WorkerErrorInvalidRequest     WorkerErrorCode = "invalid_request"
	WorkerErrorInvalidAssignment  WorkerErrorCode = "invalid_assignment"
	WorkerErrorAssignmentConflict WorkerErrorCode = "assignment_conflict"
	WorkerErrorFenceMismatch      WorkerErrorCode = "fence_mismatch"
	WorkerErrorInvalidState       WorkerErrorCode = "invalid_state"
	WorkerErrorRuntimeFailure     WorkerErrorCode = "runtime_failure"
	WorkerErrorGrantGap           WorkerErrorCode = "grant_gap"
	WorkerErrorGrantStale         WorkerErrorCode = "grant_stale"
	WorkerErrorGrantConflict      WorkerErrorCode = "grant_conflict"
)

// WorkerAPIError is returned by both the server protocol and typed client.
// It deliberately carries no raw runtime error strings.
type WorkerAPIError struct {
	Code   WorkerErrorCode `json:"code"`
	Status int             `json:"-"`
}

func (e *WorkerAPIError) Error() string {
	if e == nil {
		return "chat lifecycle worker API error"
	}
	return "chat lifecycle worker API: " + string(e.Code)
}

// WorkerFence identifies one assignment generation. Generation is also the
// exact Engine and logical-identity generation; every mutation is fenced.
type WorkerFence struct {
	RunID        string `json:"run_id"`
	AssignmentID string `json:"assignment_id"`
	Generation   uint64 `json:"generation"`
}

// WorkerAssignment installs one validated worker-owned partition.
type WorkerAssignment struct {
	WorkerFence
	WorkerID    uint64 `json:"worker_id"`
	WorkerCount uint64 `json:"worker_count"`
	// CoordinatorGrants disables worker-local primary-rate release. Login,
	// sync, session lifecycle, and canary construction remain worker-owned.
	CoordinatorGrants bool   `json:"coordinator_grants"`
	Config            Config `json:"config"`
}

// WorkerStartRequest starts the exact installed assignment.
type WorkerStartRequest struct {
	WorkerFence
}

// WorkerCheckpointRequest takes a consistent bounded snapshot without pausing generation.
type WorkerCheckpointRequest struct {
	WorkerFence
}

// WorkerRateRequest changes only the global SEND rate and its two-second burst bound.
type WorkerRateRequest struct {
	WorkerFence
	RatePerSecond uint64 `json:"rate_per_second"`
	MaxBurst      uint64 `json:"max_burst"`
}

// WorkerGrantCounts is an exact fixed-three-worker vector. Named fields avoid
// accepting short JSON arrays as zero-filled fixed Go arrays.
type WorkerGrantCounts struct {
	Worker0 uint64 `json:"worker_0"`
	Worker1 uint64 `json:"worker_1"`
	Worker2 uint64 `json:"worker_2"`
}

func (c WorkerGrantCounts) worker(workerID uint64) (uint64, bool) {
	switch workerID {
	case 0:
		return c.Worker0, true
	case 1:
		return c.Worker1, true
	case 2:
		return c.Worker2, true
	default:
		return 0, false
	}
}

// WorkerGrantRequest carries the complete coordinator-owned global evidence
// plus one strictly monotonic sequence for an exact assignment fence.
type WorkerGrantRequest struct {
	WorkerFence
	Sequence      uint64            `json:"sequence"`
	RatePerSecond uint64            `json:"rate_per_second"`
	MaxBurst      uint64            `json:"max_burst"`
	Fresh         WorkerGrantCounts `json:"fresh"`
	Released      WorkerGrantCounts `json:"released"`
	Credit        WorkerGrantCounts `json:"credit"`
}

// WorkerGrantResponse is stable across a matching delivery retry.
type WorkerGrantResponse struct {
	WorkerFence
	WorkerID    uint64 `json:"worker_id"`
	WorkerCount uint64 `json:"worker_count"`
	Sequence    uint64 `json:"sequence"`
	Released    uint64 `json:"released"`
}

// WorkerLifecycleCandidateLeaseRequest asks one fenced worker for at most the
// bounded number of transient lifecycle candidates.
type WorkerLifecycleCandidateLeaseRequest struct {
	WorkerFence
	Requested uint16 `json:"requested"`
}

// WorkerLifecycleCandidateLeaseResponse is transient control data. It must
// never be copied into worker snapshots, reports, or durable evidence.
type WorkerLifecycleCandidateLeaseResponse struct {
	WorkerFence
	WorkerID    uint64               `json:"worker_id"`
	WorkerCount uint64               `json:"worker_count"`
	Candidates  []LifecycleCandidate `json:"candidates"`
}

// WorkerLifecycleReheatRequest admits the exact existing deterministic revisit
// SEND after all-node absence proof; token and version reject timer and activity
// ABA races. It never invokes runtime eviction.
type WorkerLifecycleReheatRequest struct {
	WorkerFence
	ChannelID       string `json:"channel_id"`
	TimerToken      uint64 `json:"timer_token"`
	ActivityVersion uint64 `json:"activity_version"`
}

// WorkerLifecycleReheatResponse confirms admission, not SEND completion.
type WorkerLifecycleReheatResponse struct {
	WorkerFence
	WorkerID    uint64 `json:"worker_id"`
	WorkerCount uint64 `json:"worker_count"`
	Approved    bool   `json:"approved"`
}

// WorkerStopRequest explicitly drains and stops the exact assignment.
type WorkerStopRequest struct {
	WorkerFence
}

// WorkerHealth is the authenticated liveness projection.
type WorkerHealth struct {
	OK    bool        `json:"ok"`
	Phase WorkerPhase `json:"phase"`
}

// WorkerInfo describes only fixed protocol capabilities.
type WorkerInfo struct {
	ProtocolVersion  uint64 `json:"protocol_version"`
	MaxRequestBytes  int64  `json:"max_request_bytes"`
	MaxResponseBytes int64  `json:"max_response_bytes"`
}

// WorkerStatus is a bounded lifecycle projection suitable for polling.
type WorkerStatus struct {
	RunID        string      `json:"run_id,omitempty"`
	AssignmentID string      `json:"assignment_id,omitempty"`
	Phase        WorkerPhase `json:"phase"`
	Generation   uint64      `json:"generation"`
	WorkerID     uint64      `json:"worker_id"`
	WorkerCount  uint64      `json:"worker_count"`
	Unexpected   bool        `json:"unexpected"`
	TrafficReady bool        `json:"traffic_ready"`
}

// WorkerHistogramSnapshot uses fixed buckets so response size cannot grow with runtime.
type WorkerHistogramSnapshot struct {
	Count       uint64     `json:"count"`
	SumNanos    uint64     `json:"sum_nanos"`
	MaxNanos    uint64     `json:"max_nanos"`
	BucketUpper [16]uint64 `json:"bucket_upper_nanos"`
	Buckets     [16]uint64 `json:"buckets"`
}

var workerLatencyBucketUpperNanos = [16]uint64{
	0,
	uint64(time.Millisecond),
	uint64(2 * time.Millisecond),
	uint64(5 * time.Millisecond),
	uint64(10 * time.Millisecond),
	uint64(20 * time.Millisecond),
	uint64(50 * time.Millisecond),
	uint64(100 * time.Millisecond),
	uint64(200 * time.Millisecond),
	uint64(500 * time.Millisecond),
	uint64(time.Second),
	uint64(2 * time.Second),
	uint64(5 * time.Second),
	uint64(10 * time.Second),
	uint64(30 * time.Second),
	uint64(60 * time.Second),
}

func newWorkerHistogramSnapshot() WorkerHistogramSnapshot {
	return WorkerHistogramSnapshot{BucketUpper: workerLatencyBucketUpperNanos}
}

// recordWorkerLatency ignores negative clock movement, records zero in the
// explicit zero bucket, folds values above sixty seconds into the last bucket,
// and saturates every aggregate instead of wrapping.
func recordWorkerLatency(snapshot *WorkerHistogramSnapshot, latency time.Duration) {
	if snapshot == nil || latency < 0 {
		return
	}
	snapshot.BucketUpper = workerLatencyBucketUpperNanos
	value := uint64(latency)
	const maximum = ^uint64(0)
	if snapshot.Count < maximum {
		snapshot.Count++
	}
	if maximum-snapshot.SumNanos < value {
		snapshot.SumNanos = maximum
	} else {
		snapshot.SumNanos += value
	}
	if value > snapshot.MaxNanos {
		snapshot.MaxNanos = value
	}
	bucket := len(snapshot.Buckets) - 1
	for index, upper := range snapshot.BucketUpper {
		if value <= upper {
			bucket = index
			break
		}
	}
	if snapshot.Buckets[bucket] < maximum {
		snapshot.Buckets[bucket]++
	}
}

// WorkerSessionSnapshot exposes aggregate session and real-sync progress only.
type WorkerSessionSnapshot struct {
	Target             int    `json:"target"`
	Online             int    `json:"online"`
	Starting           int    `json:"starting"`
	TrafficReady       int    `json:"traffic_ready"`
	PlannedNew         uint64 `json:"planned_new"`
	PlannedReturning   uint64 `json:"planned_returning"`
	CompletedNew       uint64 `json:"completed_new"`
	CompletedReturning uint64 `json:"completed_returning"`
	Expired            uint64 `json:"expired"`
}

// WorkerGeneratedSnapshot contains monotonic aggregate generation indexes.
type WorkerGeneratedSnapshot struct {
	Primary      uint64 `json:"primary"`
	Person       uint64 `json:"person"`
	Group        uint64 `json:"group"`
	Canary       uint64 `json:"canary"`
	PayloadBytes uint64 `json:"payload_bytes"`
}

// WorkerMessageSnapshot exposes end-to-end aggregate correctness counters.
type WorkerMessageSnapshot struct {
	Sent                 uint64 `json:"sent"`
	SendAttempts         uint64 `json:"send_attempts"`
	FirstAttempts        uint64 `json:"first_attempts"`
	FirstAttemptFailures uint64 `json:"first_attempt_failures"`
	SendAcknowledged     uint64 `json:"send_acknowledged"`
	SendRejected         uint64 `json:"send_rejected"`
	Received             uint64 `json:"received"`
	ReceiveAcknowledged  uint64 `json:"receive_acknowledged"`
	ReceiveAckFailures   uint64 `json:"receive_ack_failures"`
	RetryAttempts        uint64 `json:"retry_attempts"`
	Terminal             uint64 `json:"terminal"`
	Losses               uint64 `json:"losses"`
	Duplicates           uint64 `json:"duplicates"`
	Corruptions          uint64 `json:"corruptions"`
	SequenceRegressions  uint64 `json:"sequence_regressions"`
}

// WorkerSyncSnapshot exposes cumulative real factory, CONNECT, and full-sync
// outcomes plus fixed latency histograms without scheduler-derived failures.
type WorkerSyncSnapshot struct {
	CompletedNew       uint64 `json:"completed_new"`
	CompletedReturning uint64 `json:"completed_returning"`
	FactoryFailed      uint64 `json:"factory_failed"`
	FactoryCanceled    uint64 `json:"factory_canceled"`
	ConnectStarted     uint64 `json:"connect_started"`
	ConnectCompleted   uint64 `json:"connect_completed"`
	ConnectFailed      uint64 `json:"connect_failed"`
	ConnectCanceled    uint64 `json:"connect_canceled"`
	SyncStarted        uint64 `json:"sync_started"`
	SyncCompleted      uint64 `json:"sync_completed"`
	SyncFailed         uint64 `json:"sync_failed"`
	SyncCanceled       uint64 `json:"sync_canceled"`
	// Failures is the compatibility aggregate for actual failed sync stages. It
	// excludes scheduler skips, factory/connect failures, and cancellations.
	Failures       uint64                  `json:"failures"`
	ConnectLatency WorkerHistogramSnapshot `json:"connect_latency"`
	Latency        WorkerHistogramSnapshot `json:"latency"`
}

// WorkerCorrelationSnapshot exposes only bounded-state gauges and aggregate errors.
type WorkerCorrelationSnapshot struct {
	PendingUnfinished      int    `json:"pending_unfinished"`
	Outstanding            int    `json:"outstanding"`
	Sampled                uint64 `json:"sampled"`
	Delivered              uint64 `json:"delivered"`
	Expired                uint64 `json:"expired"`
	DuplicateCompletions   uint64 `json:"duplicate_completions"`
	ConflictingCompletions uint64 `json:"conflicting_completions"`
	UnknownAcknowledgments uint64 `json:"unknown_acknowledgments"`
}

// WorkerQueueSnapshot exposes fixed capacity and current/peak gauges.
type WorkerQueueSnapshot struct {
	WorkCurrent       int `json:"work_current"`
	WorkPeak          int `json:"work_peak"`
	WorkCapacity      int `json:"work_capacity"`
	RetryCurrent      int `json:"retry_current"`
	RetryPeak         int `json:"retry_peak"`
	RetryCapacity     int `json:"retry_capacity"`
	InflightCurrent   int `json:"inflight_current"`
	InflightPeak      int `json:"inflight_peak"`
	InflightCapacity  int `json:"inflight_capacity"`
	TransportCurrent  int `json:"transport_current"`
	TransportCapacity int `json:"transport_capacity"`
}

// WorkerHarnessSnapshot exposes closed aggregate harness outcomes.
type WorkerHarnessSnapshot struct {
	Classification       SyncClassification `json:"classification,omitempty"`
	Failures             uint64             `json:"failures"`
	CommandSaturation    uint64             `json:"command_saturation"`
	OfferedUnderdelivery uint64             `json:"offered_underdelivery"`
	DrainTimedOut        bool               `json:"drain_timed_out"`
	UnexpectedExit       bool               `json:"unexpected_exit"`
}

// WorkerSnapshot is the complete bounded, identity-free worker evidence response.
type WorkerSnapshot struct {
	RunID            string                    `json:"run_id,omitempty"`
	AssignmentID     string                    `json:"assignment_id,omitempty"`
	Phase            WorkerPhase               `json:"phase"`
	Uptime           time.Duration             `json:"uptime"`
	SnapshotSequence uint64                    `json:"snapshot_sequence"`
	Generation       uint64                    `json:"generation"`
	WorkerID         uint64                    `json:"worker_id"`
	WorkerCount      uint64                    `json:"worker_count"`
	Sessions         WorkerSessionSnapshot     `json:"sessions"`
	Generated        WorkerGeneratedSnapshot   `json:"generated"`
	Messages         WorkerMessageSnapshot     `json:"messages"`
	Sync             WorkerSyncSnapshot        `json:"sync"`
	SendackLatency   WorkerHistogramSnapshot   `json:"sendack_latency"`
	RecvackLatency   WorkerHistogramSnapshot   `json:"recvack_latency"`
	Correlation      WorkerCorrelationSnapshot `json:"correlation"`
	Queues           WorkerQueueSnapshot       `json:"queues"`
	Harness          WorkerHarnessSnapshot     `json:"harness"`
	Evidence         EvidenceSnapshot          `json:"evidence"`
}
