package metrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultAggregateMaxErrorSamples = 32

var allowedLabelKeys = map[string]struct{}{
	"worker_id":    {},
	"phase":        {},
	"channel_type": {},
	"profile":      {},
	"traffic":      {},
	"error_kind":   {},
	"reason_code":  {},
	"reason":       {},
}

var forbiddenLabelKeys = map[string]struct{}{
	"uid":           {},
	"channel_id":    {},
	"client_msg_no": {},
	"message_id":    {},
	"run_id":        {},
}

// Labels identifies a low-cardinality metrics series.
type Labels map[string]string

// Validate rejects labels that would create high-cardinality benchmark series.
func (l Labels) Validate() error {
	for key := range l {
		key = strings.TrimSpace(key)
		if _, forbidden := forbiddenLabelKeys[key]; forbidden {
			return fmt.Errorf("metric label %q is forbidden", key)
		}
		if _, allowed := allowedLabelKeys[key]; !allowed {
			return fmt.Errorf("metric label %q is not allowed", key)
		}
	}
	return nil
}

// ErrorSample stores a bounded sample of a recorded workload error.
type ErrorSample struct {
	// Name identifies the error series.
	Name string `json:"name"`
	// Message is the captured error message.
	Message string `json:"message"`
	// At records when the error sample was stored.
	At time.Time `json:"at"`
}

// HistogramSummary contains JSON-friendly latency summary statistics.
// After worker aggregation, percentile fields are max worker-local percentiles,
// not percentiles recomputed from globally merged samples.
type HistogramSummary struct {
	// Count is the number of observed samples.
	Count uint64 `json:"count"`
	// SumSeconds is the total observed latency in seconds.
	SumSeconds float64 `json:"sum_seconds"`
	// MinSeconds is the smallest observed latency in seconds.
	MinSeconds float64 `json:"min_seconds"`
	// MaxSeconds is the largest observed latency in seconds.
	MaxSeconds float64 `json:"max_seconds"`
	// P50Seconds is the median observed latency in seconds.
	P50Seconds float64 `json:"p50_seconds"`
	// P95Seconds is the 95th percentile observed latency in seconds.
	P95Seconds float64 `json:"p95_seconds"`
	// P99Seconds is the 99th percentile observed latency in seconds.
	P99Seconds float64 `json:"p99_seconds"`
}

// SnapshotData is a stable, JSON-friendly metrics registry snapshot.
type SnapshotData struct {
	// Counters contains monotonically increasing counter series by encoded series key.
	Counters map[string]uint64 `json:"counters"`
	// Gauges contains point-in-time gauge series by encoded series key.
	Gauges map[string]float64 `json:"gauges"`
	// Histograms contains latency summaries by encoded series key.
	Histograms map[string]HistogramSummary `json:"histograms"`
	// Errors contains bounded error samples captured by the registry.
	Errors []ErrorSample `json:"errors"`
}

// WorkerSnapshot carries one worker's metrics payload for safe coordinator aggregation.
type WorkerSnapshot struct {
	// WorkerID identifies the reporting worker.
	WorkerID string `json:"worker_id"`
	// Metrics is the worker-local metrics snapshot.
	Metrics SnapshotData `json:"metrics"`
}

// Registry stores lightweight counters, gauges, latency samples, and bounded errors for a benchmark run.
type Registry struct {
	mu sync.Mutex

	counters  map[string]uint64
	gauges    map[string]float64
	latencies map[string][]time.Duration
	errors    []ErrorSample
	maxErrors int
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:  make(map[string]uint64),
		gauges:    make(map[string]float64),
		latencies: make(map[string][]time.Duration),
		maxErrors: 32,
	}
}

// SetMaxErrorSamples changes the registry error sample bound.
func (r *Registry) SetMaxErrorSamples(max int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxErrors = max
	if max >= 0 && len(r.errors) > max {
		r.errors = append([]ErrorSample(nil), r.errors[len(r.errors)-max:]...)
	}
}

// IncCounter increments one counter series by one.
func (r *Registry) IncCounter(name string, labels Labels) {
	r.AddCounter(name, labels, 1)
}

// AddCounter increments one counter series by the supplied delta.
func (r *Registry) AddCounter(name string, labels Labels, delta uint64) {
	if r == nil || labels.Validate() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[seriesKey(name, labels)] += delta
}

// CounterValue returns the current value for one counter series.
func (r *Registry) CounterValue(name string, labels Labels) uint64 {
	if r == nil || labels.Validate() != nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters[seriesKey(name, labels)]
}

// SetGauge sets one gauge series to a point-in-time value.
func (r *Registry) SetGauge(name string, labels Labels, value float64) {
	if r == nil || labels.Validate() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[seriesKey(name, labels)] = value
}

// AddGauge increments one gauge series by the supplied delta.
func (r *Registry) AddGauge(name string, labels Labels, delta float64) {
	if r == nil || labels.Validate() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[seriesKey(name, labels)] += delta
}

// GaugeValue returns the current value for one gauge series.
func (r *Registry) GaugeValue(name string, labels Labels) float64 {
	if r == nil || labels.Validate() != nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gauges[seriesKey(name, labels)]
}

// ObserveLatency appends one latency sample for the supplied series.
func (r *Registry) ObserveLatency(name string, labels Labels, d time.Duration) {
	if r == nil || labels.Validate() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := seriesKey(name, labels)
	r.latencies[key] = append(r.latencies[key], d)
}

// LatencyValues returns a copy of the current latency samples for one series.
func (r *Registry) LatencyValues(name string, labels Labels) []time.Duration {
	if r == nil || labels.Validate() != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values := append([]time.Duration(nil), r.latencies[seriesKey(name, labels)]...)
	return values
}

// RecordErrorSample stores one bounded error sample.
func (r *Registry) RecordErrorSample(name string, err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxErrors <= 0 {
		return
	}
	sample := ErrorSample{Name: strings.TrimSpace(name), Message: err.Error(), At: time.Now()}
	if len(r.errors) >= r.maxErrors {
		copy(r.errors, r.errors[1:])
		r.errors[len(r.errors)-1] = sample
		return
	}
	r.errors = append(r.errors, sample)
}

// ErrorSamples returns the bounded error samples recorded so far.
func (r *Registry) ErrorSamples() []ErrorSample {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ErrorSample(nil), r.errors...)
}

// Collect returns the current registry contents in a typed JSON-friendly shape.
func (r *Registry) Collect() SnapshotData {
	if r == nil {
		return emptySnapshot()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	counters := make(map[string]uint64, len(r.counters))
	for k, v := range r.counters {
		counters[k] = v
	}
	gauges := make(map[string]float64, len(r.gauges))
	for k, v := range r.gauges {
		gauges[k] = v
	}
	histograms := make(map[string]HistogramSummary, len(r.latencies))
	for k, values := range r.latencies {
		histograms[k] = summarizeDurations(values)
	}
	errors := append([]ErrorSample(nil), r.errors...)
	return SnapshotData{Counters: counters, Gauges: gauges, Histograms: histograms, Errors: errors}
}

// Snapshot returns the current registry contents in a JSON-friendly shape.
func (r *Registry) Snapshot() map[string]any {
	snap := r.Collect()
	return map[string]any{
		"counters":   snap.Counters,
		"gauges":     snap.Gauges,
		"histograms": snap.Histograms,
		"errors":     snap.Errors,
	}
}

// Aggregate safely combines worker-local snapshots after validating metric labels.
// Histogram counts, sums, min, and max are merged directly; percentile fields use
// the maximum worker-local p50/p95/p99 because worker snapshots do not carry buckets.
func Aggregate(workers []WorkerSnapshot) (SnapshotData, error) {
	out := emptySnapshot()
	for _, worker := range workers {
		for key, value := range worker.Metrics.Counters {
			if err := validateSeriesKey(key); err != nil {
				return SnapshotData{}, fmt.Errorf("worker %s counter %s: %w", worker.WorkerID, key, err)
			}
			out.Counters[key] += value
		}
		for key, value := range worker.Metrics.Gauges {
			if err := validateSeriesKey(key); err != nil {
				return SnapshotData{}, fmt.Errorf("worker %s gauge %s: %w", worker.WorkerID, key, err)
			}
			out.Gauges[key] += value
		}
		for key, value := range worker.Metrics.Histograms {
			if err := validateSeriesKey(key); err != nil {
				return SnapshotData{}, fmt.Errorf("worker %s histogram %s: %w", worker.WorkerID, key, err)
			}
			out.Histograms[key] = mergeHistogram(out.Histograms[key], value)
		}
		for _, sample := range worker.Metrics.Errors {
			out.Errors = appendBoundedErrorSample(out.Errors, sample, defaultAggregateMaxErrorSamples)
		}
	}
	return out, nil
}

func appendBoundedErrorSample(samples []ErrorSample, sample ErrorSample, max int) []ErrorSample {
	if max <= 0 {
		return nil
	}
	if len(samples) >= max {
		copy(samples, samples[1:])
		samples[len(samples)-1] = sample
		return samples
	}
	return append(samples, sample)
}

func emptySnapshot() SnapshotData {
	return SnapshotData{
		Counters:   map[string]uint64{},
		Gauges:     map[string]float64{},
		Histograms: map[string]HistogramSummary{},
	}
}

func summarizeDurations(values []time.Duration) HistogramSummary {
	if len(values) == 0 {
		return HistogramSummary{}
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, value := range sorted {
		sum += value
	}
	return HistogramSummary{
		Count:      uint64(len(sorted)),
		SumSeconds: sum.Seconds(),
		MinSeconds: sorted[0].Seconds(),
		MaxSeconds: sorted[len(sorted)-1].Seconds(),
		P50Seconds: percentile(sorted, 0.50).Seconds(),
		P95Seconds: percentile(sorted, 0.95).Seconds(),
		P99Seconds: percentile(sorted, 0.99).Seconds(),
	}
}

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func mergeHistogram(a, b HistogramSummary) HistogramSummary {
	if a.Count == 0 {
		return b
	}
	if b.Count == 0 {
		return a
	}
	if b.MinSeconds < a.MinSeconds {
		a.MinSeconds = b.MinSeconds
	}
	if b.MaxSeconds > a.MaxSeconds {
		a.MaxSeconds = b.MaxSeconds
	}
	a.Count += b.Count
	a.SumSeconds += b.SumSeconds
	// Worker snapshots only expose summaries, so aggregate percentiles are the
	// largest worker-local percentiles rather than global distribution percentiles.
	if b.P50Seconds > a.P50Seconds {
		a.P50Seconds = b.P50Seconds
	}
	if b.P95Seconds > a.P95Seconds {
		a.P95Seconds = b.P95Seconds
	}
	if b.P99Seconds > a.P99Seconds {
		a.P99Seconds = b.P99Seconds
	}
	return a
}

func validateSeriesKey(key string) error {
	_, labels, err := parseSeriesKey(key)
	if err != nil {
		return err
	}
	return labels.Validate()
}

func parseSeriesKey(key string) (string, Labels, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil, fmt.Errorf("metric name is required")
	}
	open := strings.IndexByte(key, '{')
	if open < 0 {
		return key, nil, nil
	}
	if !strings.HasSuffix(key, "}") {
		return "", nil, fmt.Errorf("malformed metric series key")
	}
	name := strings.TrimSpace(key[:open])
	if name == "" {
		return "", nil, fmt.Errorf("metric name is required")
	}
	raw := strings.TrimSuffix(key[open+1:], "}")
	labels := Labels{}
	if strings.TrimSpace(raw) == "" {
		return name, labels, nil
	}
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return "", nil, fmt.Errorf("malformed metric label %q", part)
		}
		labels[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return name, labels, nil
}

func seriesKey(name string, labels Labels) string {
	name = strings.TrimSpace(name)
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(labels[key])
	}
	b.WriteByte('}')
	return b.String()
}
