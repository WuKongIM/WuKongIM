package worker

import "testing"

func TestNewDefaultWorkloadRunnerExposesMetricsReporter(t *testing.T) {
	runner := NewDefaultWorkloadRunner(nil)
	if runner == nil {
		t.Fatal("expected default workload runner")
	}
	if _, ok := runner.(MetricsReporter); !ok {
		t.Fatal("expected default workload runner to expose metrics")
	}
	if _, ok := runner.(ConnectionStatusReporter); !ok {
		t.Fatal("expected default workload runner to expose connection status")
	}
}
