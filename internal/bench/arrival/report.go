// Package arrival measures scheduled arrivals without hiding queueing or underload.
package arrival

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"time"
)

// Sample is owned by one request worker. Offsets use the run's monotonic clock.
type Sample struct {
	Started, Completed, Failed, Dropped bool
	StartedAt                           time.Duration
	Queue, Service                      time.Duration
	// FailureKind is a closed category; arbitrary operation errors are not retained.
	FailureKind string
}

// Result retains a bounded sample per planned arrival, including rejected work.
type Result struct {
	Samples  []Sample
	Rate     int
	Duration time.Duration
}

// Summary uses arrival cohorts; queued work remains charged to its original window.
type Summary struct {
	Planned          int     `json:"planned"`
	Started          int     `json:"started"`
	Completed        int     `json:"completed"`
	Errors           int     `json:"errors"`
	Dropped          int     `json:"dropped"`
	TargetPerSecond  int     `json:"target_per_second"`
	StartedPerSecond float64 `json:"started_per_second"`
	TotalP99MS       float64 `json:"scheduled_to_completion_p99_ms"`
	QueueP99MS       float64 `json:"scheduled_to_start_p99_ms"`
	ServiceP99MS     float64 `json:"service_p99_ms"`
}

// writeReport retains phase-specific evidence before callers reject a run.
// At most eight anonymous failed-arrival examples accompany complete counters.
func writeReport(path, phase string, qualification bool, r Result) error {
	type failure struct {
		Index   int           `json:"index"`
		Kind    string        `json:"kind"`
		Queue   time.Duration `json:"queue_ns"`
		Service time.Duration `json:"service_ns"`
	}
	report := struct {
		Schema        string    `json:"schema"`
		Phase         string    `json:"phase"`
		Qualification bool      `json:"qualification"`
		Windows       []Summary `json:"windows"`
		Seconds       []Summary `json:"seconds"`
		Failures      []failure `json:"failures,omitempty"`
	}{Schema: "scheduled-arrival/v1", Phase: phase, Qualification: qualification,
		Windows: r.Windows(60 * time.Second), Seconds: r.Windows(time.Second)}
	for index, sample := range r.Samples {
		kind := sample.FailureKind
		if sample.Dropped {
			kind = "queue_drop"
		} else if !sample.Started || !sample.Completed {
			kind = "incomplete"
		} else if sample.Failed && kind == "" {
			kind = "operation"
		}
		if kind != "" {
			report.Failures = append(report.Failures, failure{index, kind, sample.Queue, sample.Service})
			if len(report.Failures) == 8 {
				break
			}
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func summarize(samples []Sample, rate int, duration time.Duration) Summary {
	r := Summary{Planned: len(samples), TargetPerSecond: rate}
	total, queue, service := make([]time.Duration, 0, len(samples)), make([]time.Duration, 0, len(samples)), make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		if s.Dropped {
			r.Dropped++
		}
		if s.Failed {
			r.Errors++
		}
		if s.Started {
			r.Started++
		}
		if s.Completed {
			r.Completed++
			total = append(total, s.Queue+s.Service)
			queue = append(queue, s.Queue)
			service = append(service, s.Service)
		}
	}
	if duration > 0 {
		r.StartedPerSecond = float64(r.Started) / duration.Seconds()
	}
	r.TotalP99MS, r.QueueP99MS, r.ServiceP99MS = percentile(total), percentile(queue), percentile(service)
	return r
}

func percentile(values []time.Duration) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return float64(values[int(math.Ceil(float64(len(values))*0.99))-1]) / float64(time.Millisecond)
}

// Windows reports every fixed cohort, including the final partial diagnostic window.
func (r Result) Windows(width time.Duration) []Summary {
	count := int(width * time.Duration(r.Rate) / time.Second)
	if count <= 0 {
		return nil
	}
	var windows []Summary
	for start := 0; start < len(r.Samples); start += count {
		end := min(start+count, len(r.Samples))
		elapsed := time.Duration(end-start) * time.Second / time.Duration(r.Rate)
		origin := time.Duration(start) * time.Second / time.Duration(r.Rate)
		for _, s := range r.Samples[start:end] {
			if s.Started {
				elapsed = max(elapsed, s.StartedAt-origin)
			}
		}
		windows = append(windows, summarize(r.Samples[start:end], r.Rate, elapsed))
	}
	return windows
}

// Failures never treats underload, missing work, or rejected work as a fast pass.
func (r Summary) Failures(budgetMS float64) []string {
	var reasons []string
	if r.Planned == 0 || r.Started != r.Planned || r.Completed != r.Planned {
		reasons = append(reasons, "incomplete")
	}
	if r.Errors != 0 {
		reasons = append(reasons, "request_errors")
	}
	if r.Dropped != 0 {
		reasons = append(reasons, "queue_drops")
	}
	if r.TargetPerSecond <= 0 || r.StartedPerSecond < float64(r.TargetPerSecond)*0.95 {
		reasons = append(reasons, "underload")
	}
	if r.TotalP99MS > budgetMS {
		reasons = append(reasons, "scheduled_latency")
	}
	return reasons
}
