package conversation_qps

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixedProfileAndAcceptance(t *testing.T) {
	p, raw, err := loadProfile()
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	c := p.Cases[0]
	good := phaseResult{DriverWorkers: p.Workers, QueueCapacity: queuedArrivalLimit(p, c), Nodes: 1, Case: c, Scheduled: c.OfferedQPS * p.DurationSeconds, Completed: c.OfferedQPS * p.DurationSeconds, CompletedInWindow: c.OfferedQPS * p.DurationSeconds, DurationSeconds: p.DurationSeconds, ActualQPS: float64(c.OfferedQPS), P99MS: 10, AllocatedBytes: 1}
	require.NoError(t, evaluate(p, good))
	for name, mutate := range map[string]func(*phaseResult){
		"allocation_regression": func(r *phaseResult) {
			r.AllocatedBytes = float64(r.Completed) * (r.Case.MaxAllocatedBytesPerRequest[r.Nodes] + 1)
		},
		"missing_allocations":  func(r *phaseResult) { r.AllocatedBytes = 0 },
		"nan_allocations":      func(r *phaseResult) { r.AllocatedBytes = math.NaN() },
		"infinite_allocations": func(r *phaseResult) { r.AllocatedBytes = math.Inf(1) },
		"unexpected_error":     func(r *phaseResult) { r.UnexpectedErrors = 1 },
		"oversized_queue":      func(r *phaseResult) { r.QueueCapacity++ },
		"wrong_workers":        func(r *phaseResult) { r.DriverWorkers++ },
		"partial":              func(r *phaseResult) { r.Completed-- },
		"http_error":           func(r *phaseResult) { r.Errors++ },
		"overload_drop":        func(r *phaseResult) { r.Dropped++ },
		"slow":                 func(r *phaseResult) { r.P99MS = p.MaxP99MS + 1 },
		"missing_latency":      func(r *phaseResult) { r.P99MS = 0 },
		"nan_latency":          func(r *phaseResult) { r.P99MS = math.NaN() },
		"warm_fixture":         func(r *phaseResult) { r.ActiveRuntimesBefore = 1 },
		"residency":            func(r *phaseResult) { r.ActiveRuntimesAfter = 1 },
		"activation":           func(r *phaseResult) { r.RuntimeLoads = 1 },
		"membership_write":     func(r *phaseResult) { r.MembershipWrites = 1 },
		"reset_counter":        func(r *phaseResult) { r.RuntimeLoads = -1 },
		"missing_metric":       func(r *phaseResult) { r.RuntimeLoads = math.NaN() },
		"inflated_qps":         func(r *phaseResult) { r.ActualQPS++ },
		"nan_qps":              func(r *phaseResult) { r.ActualQPS = math.NaN() },
		"infinite_qps":         func(r *phaseResult) { r.ActualQPS = math.Inf(1) },
		"late_completions": func(r *phaseResult) {
			r.CompletedInWindow = r.Completed * 9 / 10
			r.ActualQPS = float64(r.CompletedInWindow) / float64(p.DurationSeconds)
		},
		"invalid_window":     func(r *phaseResult) { r.CompletedInWindow++ },
		"shortened_duration": func(r *phaseResult) { r.DurationSeconds-- },
		"no_requests":        func(r *phaseResult) { r.Scheduled = 0; r.Completed = 0; r.CompletedInWindow = 0; r.ActualQPS = 0 },
	} {
		t.Run(name, func(t *testing.T) { r := good; mutate(&r); require.Error(t, evaluate(p, r)) })
	}
}

func TestPercentileIncludesTailWithoutChangingSamples(t *testing.T) {
	values := []float64{1000, 1, 2, 3, 4}
	require.Equal(t, float64(1000), percentile(values, .99))
	require.Equal(t, float64(3), percentile(values, .50))
	require.Equal(t, float64(1000), values[0])
	require.Zero(t, percentile(nil, .99))
}

// A scheduler delay below the latency budget must not manufacture dropped work.
func TestArrivalQueueAbsorbsJitterWithinLatencyBudget(t *testing.T) {
	p, _, err := loadProfile()
	require.NoError(t, err)
	w := p.Cases[0]
	q := make(chan struct{}, queuedArrivalLimit(p, w))
	arrivals := int(math.Ceil(float64(w.OfferedQPS) * 0.080))
	for i := 0; i < arrivals; i++ {
		select {
		case q <- struct{}{}:
		default:
			t.Fatalf("dropped arrival %d during an 80 ms delay within the %.0f ms budget", i, p.MaxP99MS)
		}
	}
	require.LessOrEqual(t, cap(q), max(p.Workers, int(math.Ceil(float64(w.OfferedQPS)*p.MaxP99MS/1000))))
	for len(q) < cap(q) {
		q <- struct{}{}
	}
	select {
	case q <- struct{}{}:
		t.Fatal("queue exceeded its bound")
	default:
	}
}
