package chatlifecycle

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Failure inventory: an accepted configured threshold cannot be evaluated at
// the first real worker cut; 400ms works but600ms does not; producer limits and
// verifier wiring differ; or a workaround estimates600ms from400ms/1s buckets.
// This uses the production generation factory and its native snapshots, with
// no product process, HTTP calls, workload clock or fake duration interpolation.
func TestConfiguredHotThresholdProjectsInitialProductionWorkerCut(t *testing.T) {
	for _, limit := range []time.Duration{400 * time.Millisecond, 600 * time.Millisecond} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cfg := LocalConfig()
			cfg.Thresholds.Latency.HotSendACK = LatencyLimit{P99: limit, P999: time.Second}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			snapshots := make([]WorkerSnapshot, 3)
			for worker := uint64(0); worker < 3; worker++ {
				generation, err := NewEngineWorkerGenerationFactory("metering-config-test").New(WorkerAssignment{WorkerFence: WorkerFence{RunID: cfg.RunID, AssignmentID: fmt.Sprintf("configured-meter-%d", worker), Generation: 1}, WorkerID: worker, WorkerCount: 3, Config: cfg})
				if err != nil {
					t.Fatal(err)
				}
				engine := generation.(*engineWorkerGeneration).engine
				if err := engine.StartGeneration(context.Background(), 1); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := engine.Stop(); err != nil {
						t.Error(err)
					}
				})
				snapshots[worker], err = generation.Snapshot(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			}
			_, latency, _, err := projectWorkerVerdictEvidence(cfg, snapshots, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()})
			if err != nil {
				t.Fatalf("accepted configured%v threshold rejected first native worker cut: %v", limit, err)
			}
			if latency.Hot.P99Limit != limit || latency.Hot.P999Limit != time.Second || latency.Hot.Count != 0 {
				t.Fatalf("wrong empty configured counter projection: %+v", latency.Hot)
			}
		})
	}
}
