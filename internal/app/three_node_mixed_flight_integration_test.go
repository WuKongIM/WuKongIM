//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/trace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/arrival"
	"github.com/WuKongIM/WuKongIM/pkg/bench/counterwindow"
)

const (
	mixedFlightHistory     = 15 * time.Second
	mixedFlightPost        = 2 * time.Second
	mixedFlightTimeout     = 210 * time.Second
	mixedFlightTargetBytes = 32 << 20 // Runtime hint, not a hard memory limit.
	mixedFlightOutputBytes = 64 << 20
	mixedFlightCutBytes    = 1 << 20
	mixedFlightCuts        = 16
)

type mixedFlightTrigger struct {
	At          time.Time `json:"at"`
	CohortStart time.Time `json:"cohort_start"`
	Cohort      int       `json:"cohort"`
	Above       int32     `json:"above_400ms"`
}

// mixedFlightDetector seals one early proof of a one-second cohort's P99
// breach: six completed samples over budget among 500 planned arrivals.
// Dropped/failed requests remain in the authoritative arrival report.
type mixedFlightDetector struct {
	// Counts use planned arrival cohorts, never completion-time buckets.
	above [180]atomic.Int32
	// Only the winner sends to the private one-element trigger channel.
	claimed atomic.Bool
	trigger chan mixedFlightTrigger
}

func (d *mixedFlightDetector) observe(index int, planned time.Time, sample arrival.Sample) {
	if d.claimed.Load() || !sample.Completed || sample.Queue+sample.Service <= 400*time.Millisecond {
		return
	}
	cohort := index / 500
	if index < 0 || cohort >= len(d.above) {
		return
	}
	if above := d.above[cohort].Add(1); above > 5 && d.claimed.CompareAndSwap(false, true) {
		d.trigger <- mixedFlightTrigger{At: time.Now().UTC(), CohortStart: planned.Add(-time.Duration(index%500) * time.Second / 500).UTC(), Cohort: cohort, Above: above}
	}
}

type mixedFlightCut struct {
	Started  time.Time       `json:"started_at"`
	Ended    time.Time       `json:"ended_at"`
	Snapshot json.RawMessage `json:"snapshot,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type mixedFlightReceipt struct {
	Schema               string              `json:"schema"`
	Status               string              `json:"status"`
	Started              time.Time           `json:"started_at"`
	Stopped              time.Time           `json:"stopped_at"`
	ExportStarted        time.Time           `json:"export_started_at,omitempty"`
	ExportEnded          time.Time           `json:"export_ended_at,omitempty"`
	Trigger              *mixedFlightTrigger `json:"trigger,omitempty"`
	TraceBytes           int                 `json:"trace_bytes"`
	TraceComplete        bool                `json:"trace_complete"`
	CountersComplete     bool                `json:"counters_complete"`
	HandlersCompleted    bool                `json:"handlers_completed"`
	Error                string              `json:"error,omitempty"`
	HistoryTargetSeconds int                 `json:"history_target_seconds"`
	RuntimeByteTarget    int                 `json:"runtime_byte_target"`
	OutputByteLimit      int                 `json:"output_byte_limit"`
	Cuts                 []mixedFlightCut    `json:"cuts"`
}

// startMixedSendFlight arms only the fixed measured qualification. It never
// restarts the fixture, changes the arrival gate or samples setup/warmup.
func startMixedSendFlight(b *testing.B, apps []*App, rate int) (func(int, time.Time, arrival.Sample), func()) {
	dir := os.Getenv("WK_BENCH_SEND_FLIGHT_DIR")
	if dir == "" {
		return nil, func() {}
	}
	// The launcher owns this fresh child directory in verified tmpfs and retains
	// it after process exit, including fatal test timeouts that skip Go cleanups.
	unavailable := func(reason string) (func(int, time.Time, arrival.Sample), func()) {
		b.Logf("rolling SEND trace unavailable: %s", reason)
		return nil, func() {}
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || runtime.GOMAXPROCS(0) != 4 || b.N != 90000 || rate != 500 || b.Name() != "BenchmarkThreeNodeMixedSendPath500QPS" || os.Getenv("WK_BENCH_QUALIFY") != "1" || os.Getenv("WK_BENCH_SEND_COUNTERS_DIR") == "" || os.Getenv("WK_BENCH_SEND_DIAGNOSTICS_DIR") != "" {
		return unavailable("fixed_qualification_required")
	}
	if !filepath.IsAbs(dir) {
		return unavailable("absolute_tmpfs_directory_required")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return unavailable("fresh_directory_failed")
	}
	detector := &mixedFlightDetector{trigger: make(chan mixedFlightTrigger, 1)}
	finish, err := runMixedFlight(dir, detector.trigger, func() (counterwindow.Snapshot, error) {
		return counterwindow.ReadSnapshot(mixedSendGatherers(apps)...)
	})
	if err != nil {
		_ = writeMixedFlightReceipt(dir, mixedFlightReceipt{Schema: "mixed-send-flight/v1", Status: "incomplete", Error: "recorder_start_failed"})
		return unavailable("recorder_start_failed")
	}
	var once sync.Once
	stop := func(completed bool) {
		once.Do(func() {
			receipt := finish()
			receipt.HandlersCompleted = completed
			if err := writeMixedFlightReceipt(dir, receipt); err != nil {
				b.Log("rolling SEND trace receipt write failed")
			}
			if receipt.Error != "" {
				b.Logf("rolling SEND trace incomplete: %s", receipt.Error)
			}
		})
	}
	b.Cleanup(func() { stop(false) })
	return detector.observe, func() { stop(true) }
}

// writeMixedFlightReceipt atomically checkpoints tmpfs evidence independently
// of workload completion. A killed process leaves its last complete receipt.
func writeMixedFlightReceipt(dir string, receipt mixedFlightReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "window.json")
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// runMixedFlight owns the sole recorder and sampler goroutine. The caller must
// join finish before retaining files or closing its registries. MaxBytes is a
// runtime retention hint; output and retained counter cuts have separate caps.
func runMixedFlight(stage string, triggers <-chan mixedFlightTrigger, read func() (counterwindow.Snapshot, error)) (func() mixedFlightReceipt, error) {
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{MinAge: mixedFlightHistory, MaxBytes: mixedFlightTargetBytes})
	if err := recorder.Start(); err != nil {
		return nil, err
	}
	r := mixedFlightReceipt{Schema: "mixed-send-flight/v1", Status: "not_triggered", Started: time.Now().UTC(), CountersComplete: true,
		HistoryTargetSeconds: 15, RuntimeByteTarget: mixedFlightTargetBytes, OutputByteLimit: mixedFlightOutputBytes}
	trace.Log(context.Background(), "mixed-send-flight", "armed")
	sample := func() {
		cut := mixedFlightCut{Started: time.Now().UTC()}
		snapshot, err := read()
		if err == nil {
			cut.Snapshot, err = json.Marshal(snapshot)
			if len(cut.Snapshot) > mixedFlightCutBytes {
				err = fmt.Errorf("counter cut exceeds byte budget")
			}
		}
		cut.Ended = time.Now().UTC()
		if len(snapshot.Missing) > 0 {
			r.CountersComplete = false
		}
		if err != nil {
			cut.Snapshot, cut.Error = nil, "counter_collection_failed"
			r.CountersComplete = false
		}
		if len(r.Cuts) == mixedFlightCuts {
			r.Cuts = append(r.Cuts[:0], r.Cuts[1:]...)
		}
		r.Cuts = append(r.Cuts, cut)
	}
	initial := r
	initial.Status = "recording"
	if err := writeMixedFlightReceipt(stage, initial); err != nil {
		recorder.Stop()
		return nil, err
	}
	sample() // Retain a baseline before any measured arrival.
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			recorder.Stop()
			r.Stopped = time.Now().UTC()
			if err := writeMixedFlightReceipt(stage, r); err != nil {
				r.Error = "receipt_write_failed"
			}
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		deadline := time.NewTimer(mixedFlightTimeout)
		defer deadline.Stop()
		var post *time.Timer
		var capture <-chan time.Time
		defer func() {
			if post != nil {
				post.Stop()
			}
		}()
		stopSignal := stop
		trigger := func(t mixedFlightTrigger) {
			r.Trigger, r.Status = &t, "triggered"
			if err := writeMixedFlightReceipt(stage, r); err != nil {
				r.Error = "receipt_write_failed"
			}
			trace.Log(context.Background(), "mixed-send-flight", fmt.Sprintf("cohort=%d above=%d", t.Cohort, t.Above))
			post = time.NewTimer(mixedFlightPost)
			capture = post.C
		}
		for {
			select {
			case t := <-triggers:
				if r.Trigger == nil {
					trigger(t)
				}
			case <-ticker.C:
				sample()
			case <-stopSignal:
				stopSignal = nil
				// A last completion can race the stop signal; consume its queued
				// trigger before declaring the run untriggered.
				if r.Trigger == nil {
					select {
					case t := <-triggers:
						trigger(t)
					default:
						sample()
						return
					}
				}
			case <-capture:
				sample()
				r.ExportStarted = time.Now().UTC()
				f, err := os.OpenFile(filepath.Join(stage, "execution.trace"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					r.Status, r.Error = "incomplete", "trace_open_failed"
					return
				}
				w := &mixedSendDiagnosticFile{file: f, limit: mixedFlightOutputBytes}
				_, writeErr := recorder.WriteTo(w)
				closeErr := f.Close()
				r.ExportEnded, r.TraceBytes = time.Now().UTC(), w.written
				r.TraceComplete = writeErr == nil && closeErr == nil && !w.overflow && w.written > 0
				if !r.TraceComplete {
					r.Status, r.Error = "incomplete", "trace_export_failed"
				} else {
					r.Status = "captured"
				}
				return
			case <-deadline.C:
				r.Status, r.Error = "incomplete", "recorder_deadline"
				return
			}
		}
	}()
	var once sync.Once
	return func() mixedFlightReceipt { once.Do(func() { close(stop) }); <-done; return r }, nil
}
