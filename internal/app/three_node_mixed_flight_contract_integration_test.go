//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/trace"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/arrival"
	"github.com/WuKongIM/WuKongIM/pkg/bench/counterwindow"
)

func TestMixedFlightTriggerUsesOriginalArrivalCohort(t *testing.T) {
	d := &mixedFlightDetector{trigger: make(chan mixedFlightTrigger, 1)}
	start := time.Now()
	slow := arrival.Sample{Completed: true, Queue: 350 * time.Millisecond, Service: 51 * time.Millisecond}
	for i := 0; i < 5; i++ {
		d.observe(i, start.Add(time.Duration(i)*time.Second/500), slow)
	}
	// Exactly one percent over budget is still a passing nearest-rank P99.
	d.observe(5, start, arrival.Sample{Completed: true, Service: 400 * time.Millisecond})
	d.observe(6, start, arrival.Sample{Dropped: true, Queue: time.Second})
	d.observe(500, start.Add(time.Second), slow) // Another cohort cannot combine.
	select {
	case <-d.trigger:
		t.Fatal("premature trigger")
	default:
	}
	d.observe(499, start.Add(499*time.Second/500), slow)
	got := <-d.trigger
	if got.Cohort != 0 || got.Above != 6 || !got.CohortStart.Equal(start) {
		t.Fatalf("wrong cohort: %+v", got)
	}
	for i := range 90000 {
		d.observe(i, start, slow)
	}
	select {
	case <-d.trigger:
		t.Fatal("trigger repeated")
	default:
	}
}

func TestMixedFlightTriggerConcurrentCompletions(t *testing.T) {
	d := &mixedFlightDetector{trigger: make(chan mixedFlightTrigger, 1)}
	var wg sync.WaitGroup
	for i := range 500 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.observe(i, time.Now(), arrival.Sample{Completed: true, Service: time.Second})
		}()
	}
	wg.Wait()
	if len(d.trigger) != 1 {
		t.Fatal("concurrent trigger was lost or duplicated")
	}
}

func TestMixedFlightRetainsPreTriggerExecutionAndJoins(t *testing.T) {
	dir := t.TempDir()
	triggers := make(chan mixedFlightTrigger, 1)
	finish, err := runMixedFlight(dir, triggers, func() (counterwindow.Snapshot, error) {
		return counterwindow.Snapshot{At: time.Now().UTC()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finish() })
	region := trace.StartRegion(context.Background(), "mixed-flight-before-breach-test")
	region.End()
	triggerAt := time.Now().UTC()
	triggers <- mixedFlightTrigger{At: triggerAt, Cohort: 0, Above: 6}
	// Joining immediately must consume an already queued last-arrival trigger.
	r := finish()
	if !r.TraceComplete || r.Status != "captured" || r.TraceBytes <= 0 || r.TraceBytes > mixedFlightOutputBytes || r.ExportStarted.Before(triggerAt.Add(mixedFlightPost)) || r.Stopped.Before(r.ExportEnded) || len(r.Cuts) < 3 {
		t.Fatalf("incomplete capture: %+v", r)
	}
	data, err := os.ReadFile(filepath.Join(dir, "execution.trace"))
	if err != nil || !bytes.Contains(data, []byte("mixed-flight-before-breach-test")) {
		t.Fatalf("lost pre-trigger trace event: %v", err)
	}
	if again := finish(); again.TraceBytes != r.TraceBytes || !again.Stopped.Equal(r.Stopped) {
		t.Fatal("join rewrote capture")
	}
	// A joined recorder must relinquish process-wide trace ownership.
	var next bytes.Buffer
	if err := trace.Start(&next); err != nil {
		t.Fatal(err)
	}
	trace.Stop()
}

func TestMixedFlightNoTriggerRemainsExplicit(t *testing.T) {
	dir := t.TempDir()
	finish, err := runMixedFlight(dir, make(chan mixedFlightTrigger, 1), func() (counterwindow.Snapshot, error) {
		return counterwindow.Snapshot{At: time.Now().UTC()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := finish()
	if r.Status != "not_triggered" || r.TraceComplete || r.Trigger != nil || len(r.Cuts) != 2 {
		t.Fatalf("invented capture: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "execution.trace")); !os.IsNotExist(err) {
		t.Fatal("untriggered run wrote a trace")
	}
}

func TestMixedFlightCheckpointSurvivesBeforeWorkloadJoin(t *testing.T) {
	dir := t.TempDir()
	triggers := make(chan mixedFlightTrigger, 1)
	finish, err := runMixedFlight(dir, triggers, func() (counterwindow.Snapshot, error) {
		return counterwindow.Snapshot{At: time.Now().UTC()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finish() })
	// Refuse to overwrite an existing trace and checkpoint the failed export
	// before the hypothetical measured workload ever calls finish.
	path := filepath.Join(dir, "execution.trace")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	triggers <- mixedFlightTrigger{At: time.Now().UTC(), Above: 6}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "window.json"))
		var r mixedFlightReceipt
		if err != nil || json.Unmarshal(data, &r) != nil {
			t.Fatalf("invalid atomic checkpoint: %s %v", data, err)
		}
		if r.Status == "incomplete" {
			if r.TraceComplete || r.HandlersCompleted || r.Error != "trace_open_failed" {
				t.Fatalf("invented complete evidence: %+v", r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("export was not checkpointed before workload join")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if data, _ := os.ReadFile(path); string(data) != "existing" {
		t.Fatal("overwrote trace")
	}
}
