package conversation_qps

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

//go:embed profile.json
var profileFiles embed.FS

type workloadCase struct {
	// MaxAllocatedBytesPerRequest fixes topology-specific allocation regression ceilings.
	MaxAllocatedBytesPerRequest map[int]float64 `json:"max_allocated_bytes_per_request,omitempty"`
	Endpoint                    string          `json:"endpoint"`
	PageSize                    int             `json:"page_size"`
	OfferedQPS                  int             `json:"offered_qps"`
}

// profile fixes workload shape and release floors in reviewed source.
type profile struct {
	// captureTimeline is diagnostic-only and never changes the release workload.
	captureTimeline    bool
	StressGate         stressConfig   `json:"stress_gate"`
	StressDiagnosis    stressConfig   `json:"stress_diagnosis"`
	Schema             string         `json:"schema"`
	DurationSeconds    int            `json:"duration_seconds"`
	Workers            int            `json:"workers"`
	Cohorts            int            `json:"cohorts"`
	UsersPerCohort     int            `json:"users_per_cohort"`
	ChannelsPerCohort  int            `json:"channels_per_cohort"`
	MessagesPerChannel int            `json:"messages_per_channel"`
	PayloadBytes       int            `json:"payload_bytes"`
	MinCompletionRatio float64        `json:"min_completion_ratio"`
	MaxP99MS           float64        `json:"max_p99_ms"`
	Cases              []workloadCase `json:"cases"`
}

func loadProfile() (profile, []byte, error) {
	raw, err := profileFiles.ReadFile("profile.json")
	if err != nil {
		return profile{}, nil, err
	}
	var p profile
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, nil, err
	}
	if p.Schema != "wukongim/conversation-qps-profile/v2" || p.DurationSeconds < 10 || p.DurationSeconds > 60 || p.Workers < 1 || p.Workers > 16 || p.Cohorts != 3 || p.UsersPerCohort != 8 || p.ChannelsPerCohort != 200 || p.MessagesPerChannel != 3 || p.PayloadBytes != 256 || p.MinCompletionRatio < 0.95 || p.MinCompletionRatio > 1 || p.MaxP99MS <= 0 || p.MaxP99MS > 1000 || len(p.Cases) != 6 {
		return p, nil, fmt.Errorf("invalid fixed QPS profile")
	}
	if err := validateStressConfig(p.StressDiagnosis); err != nil {
		return p, nil, err
	}
	if err := validateStressGate(p.StressGate); err != nil {
		return p, nil, err
	}
	seen := map[string]bool{}
	for _, c := range p.Cases {
		key := fmt.Sprintf("%s/%d", c.Endpoint, c.PageSize)
		if (c.Endpoint != "/conversation/list" && c.Endpoint != "/conversation/sync") || (c.PageSize != 25 && c.PageSize != 100 && c.PageSize != 200) || c.OfferedQPS < 10 || c.OfferedQPS > 1000 || seen[key] {
			return p, nil, fmt.Errorf("invalid QPS case")
		}
		if len(c.MaxAllocatedBytesPerRequest) != 2 {
			return p, nil, fmt.Errorf("missing allocation ceilings")
		}
		for _, nodes := range []int{1, 3} {
			ceiling := c.MaxAllocatedBytesPerRequest[nodes]
			if ceiling <= 0 || math.IsNaN(ceiling) || math.IsInf(ceiling, 0) || ceiling > 128<<20 {
				return p, nil, fmt.Errorf("invalid allocation ceiling")
			}
		}
		seen[key] = true
	}
	return p, raw, nil
}

// phaseResult accounts for every scheduled arrival, including overload drops.
// ActualQPS excludes completions after the measurement window.
type phaseResult struct {
	Timeline []arrivalSecond `json:"arrival_timeline,omitempty"`
	Slow     []slowArrival   `json:"slow_arrivals,omitempty"`
	// Separate component percentiles are diagnostic and are not additive.
	DriverWaitP99MS   float64      `json:"driver_wait_p99_ms,omitempty"`
	RequestP99MS      float64      `json:"request_p99_ms,omitempty"`
	DriverWorkers     int          `json:"driver_workers"`
	QueueCapacity     int          `json:"queue_capacity"`
	Nodes             int          `json:"nodes"`
	Case              workloadCase `json:"case"`
	Scheduled         int          `json:"scheduled"`
	Completed         int          `json:"completed"`
	CompletedInWindow int          `json:"completed_in_window"`
	FirstError        string       `json:"first_error,omitempty"`
	// UnexpectedErrors excludes recognized refusal envelopes; all errors still fail release.
	FirstUnexpectedError string         `json:"first_unexpected_error,omitempty"`
	ErrorSamples         map[string]int `json:"error_samples,omitempty"`
	UnexpectedErrors     int            `json:"unexpected_errors"`
	Errors               int            `json:"errors"`
	Dropped              int            `json:"dropped"`
	DurationSeconds      int            `json:"duration_seconds"`
	ActualQPS            float64        `json:"actual_qps"`
	P50MS                float64        `json:"p50_ms"`
	P95MS                float64        `json:"p95_ms"`
	P99MS                float64        `json:"p99_ms"`
	ActiveRuntimesBefore float64        `json:"active_runtimes_before"`
	ActiveRuntimesAfter  float64        `json:"active_runtimes_after"`
	RuntimeLoads         float64        `json:"runtime_loads"`
	MembershipWrites     float64        `json:"membership_writes"`
	CPUSeconds           *float64       `json:"cpu_seconds"`
	HeapBytes            float64        `json:"heap_bytes"`
	AllocatedBytes       float64        `json:"allocated_bytes"`
	Verdict              string         `json:"verdict"`
}

// evaluate rejects missing work, partial success, latency tails and any runtime activation.
func evaluate(p profile, r phaseResult) error {
	if r.DriverWorkers != p.Workers || r.QueueCapacity != queuedArrivalLimit(p, r.Case) || r.Scheduled != r.Case.OfferedQPS*p.DurationSeconds || r.DurationSeconds != p.DurationSeconds || r.Completed != r.Scheduled || r.Errors != 0 || r.UnexpectedErrors != 0 || r.Dropped != 0 || r.CompletedInWindow < 0 || r.CompletedInWindow > r.Completed {
		return fmt.Errorf("incomplete or failed requests")
	}
	expectedQPS := float64(r.CompletedInWindow) / float64(p.DurationSeconds)
	if math.IsNaN(r.ActualQPS) || math.IsInf(r.ActualQPS, 0) || math.Abs(r.ActualQPS-expectedQPS) > 0.001 || r.ActualQPS < float64(r.Case.OfferedQPS)*p.MinCompletionRatio {
		return fmt.Errorf("QPS below fixed floor")
	}
	if r.P99MS <= 0 || math.IsNaN(r.P99MS) || r.P99MS > p.MaxP99MS {
		return fmt.Errorf("P99 exceeds budget")
	}
	if r.RuntimeLoads != 0 || r.MembershipWrites != 0 || r.ActiveRuntimesBefore != 0 || r.ActiveRuntimesAfter != 0 {
		return fmt.Errorf("reads activated runtimes or mutated memberships")
	}
	if len(r.Case.MaxAllocatedBytesPerRequest) > 0 {
		ceiling := r.Case.MaxAllocatedBytesPerRequest[r.Nodes]
		if ceiling <= 0 || r.AllocatedBytes <= 0 || math.IsNaN(r.AllocatedBytes) || math.IsInf(r.AllocatedBytes, 0) || r.AllocatedBytes/float64(r.Completed) > ceiling {
			return fmt.Errorf("allocation regression exceeds fixed ceiling")
		}
	}
	return nil
}

func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	return ordered[max(0, int(math.Ceil(q*float64(len(ordered))))-1)]
}

// queuedArrivalLimit bounds arrivals retained by the driver.
func queuedArrivalLimit(p profile, w workloadCase) int {
	return max(p.Workers, int(math.Ceil(float64(w.OfferedQPS)*p.MaxP99MS/1000)))
}

// arrivalSecond attributes bounded timing evidence to the scheduled second.
type arrivalSecond struct {
	Completed    int     `json:"completed"`
	Dropped      int     `json:"dropped"`
	MaxWaitMS    float64 `json:"max_wait_ms"`
	MaxRequestMS float64 `json:"max_request_ms"`
	MaxLatencyMS float64 `json:"max_latency_ms"`
}

type slowArrival struct {
	Index     int     `json:"index"`
	WaitMS    float64 `json:"wait_ms"`
	RequestMS float64 `json:"request_ms"`
}
