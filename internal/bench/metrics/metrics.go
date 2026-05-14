package metrics

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Labels identifies a low-cardinality metrics series.
type Labels map[string]string

// ErrorSample stores a bounded sample of a recorded workload error.
type ErrorSample struct {
	// Name identifies the error series.
	Name string `json:"name"`
	// Message is the captured error message.
	Message string `json:"message"`
	// At records when the error sample was stored.
	At time.Time `json:"at"`
}

// Registry stores lightweight counters, latency samples, and bounded errors for a benchmark run.
type Registry struct {
	mu sync.Mutex

	counters  map[string]uint64
	latencies map[string][]time.Duration
	errors    []ErrorSample
	maxErrors int
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:  make(map[string]uint64),
		latencies: make(map[string][]time.Duration),
		maxErrors: 32,
	}
}

// IncCounter increments one counter series by one.
func (r *Registry) IncCounter(name string, labels Labels) {
	r.AddCounter(name, labels, 1)
}

// AddCounter increments one counter series by the supplied delta.
func (r *Registry) AddCounter(name string, labels Labels, delta uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[seriesKey(name, labels)] += delta
}

// CounterValue returns the current value for one counter series.
func (r *Registry) CounterValue(name string, labels Labels) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters[seriesKey(name, labels)]
}

// ObserveLatency appends one latency sample for the supplied series.
func (r *Registry) ObserveLatency(name string, labels Labels, d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := seriesKey(name, labels)
	r.latencies[key] = append(r.latencies[key], d)
}

// LatencyValues returns a copy of the current latency samples for one series.
func (r *Registry) LatencyValues(name string, labels Labels) []time.Duration {
	if r == nil {
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
		r.maxErrors = 32
	}
	if len(r.errors) >= r.maxErrors {
		copy(r.errors, r.errors[1:])
		r.errors[len(r.errors)-1] = ErrorSample{Name: name, Message: err.Error(), At: time.Now()}
		return
	}
	r.errors = append(r.errors, ErrorSample{Name: name, Message: err.Error(), At: time.Now()})
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

// Snapshot returns the current registry contents in a JSON-friendly shape.
func (r *Registry) Snapshot() map[string]any {
	if r == nil {
		return map[string]any{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	counters := make(map[string]uint64, len(r.counters))
	for k, v := range r.counters {
		counters[k] = v
	}
	latencies := make(map[string][]time.Duration, len(r.latencies))
	for k, values := range r.latencies {
		latencies[k] = append([]time.Duration(nil), values...)
	}
	errors := append([]ErrorSample(nil), r.errors...)
	return map[string]any{
		"counters":  counters,
		"latencies": latencies,
		"errors":    errors,
	}
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
