package arrival

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReportIncludesQueueDelayAndRejectsUnderload(t *testing.T) {
	samples := make([]Sample, 1000)
	for i := range samples {
		samples[i] = Sample{Started: true, Completed: true, Queue: 450 * time.Millisecond, Service: 20 * time.Millisecond}
	}
	r := summarize(samples, 500, 2*time.Second)
	if r.TotalP99MS != 470 || r.ServiceP99MS != 20 {
		t.Fatalf("queue omitted: %+v", r)
	}
	if len(r.Failures(400)) == 0 {
		t.Fatal("accepted a 470ms total P99")
	}
	r = summarize(samples, 500, 3*time.Second)
	if r.StartedPerSecond >= 475 || len(r.Failures(500)) == 0 {
		t.Fatal("accepted underload")
	}
}

func TestReportRejectsMissingDroppedAndFailedSamples(t *testing.T) {
	for _, sample := range []Sample{{}, {Dropped: true}, {Started: true, Completed: true, Failed: true}} {
		r := summarize([]Sample{sample}, 500, 2*time.Millisecond)
		if len(r.Failures(400)) == 0 {
			t.Fatalf("accepted %+v", sample)
		}
	}
}

func TestWindowsCannotHideOneSlowWindow(t *testing.T) {
	samples := make([]Sample, 90000)
	for i := range samples {
		samples[i] = Sample{Started: true, Completed: true, Service: time.Millisecond}
	}
	for i := 30000; i < 30600; i++ {
		samples[i].Service = time.Second
	}
	result := Result{Samples: samples, Rate: 500, Duration: 180 * time.Second}
	windows := result.Windows(60 * time.Second)
	if len(windows) != 3 || len(windows[1].Failures(400)) == 0 {
		t.Fatal("lost bad middle window")
	}
	if len(summarize(samples, 500, 180*time.Second).Failures(400)) != 0 {
		t.Fatal("fixture must expose aggregate masking")
	}
}

func TestWarmupEvidenceRetainsFailedAndDroppedWork(t *testing.T) {
	r := Result{Rate: 500, Samples: make([]Sample, 20)}
	r.Samples[0] = Sample{Started: true, Completed: true, Failed: true, FailureKind: "deadline"}
	r.Samples[1] = Sample{Dropped: true}
	path := filepath.Join(t.TempDir(), "warmup.json")
	if err := writeReport(path, "warmup", false, r); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Phase         string
		Qualification bool
		Windows       []Summary
		Failures      []struct {
			Index int
			Kind  string
		}
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Phase != "warmup" || report.Qualification || len(report.Windows) != 1 || report.Windows[0].Errors != 1 || report.Windows[0].Dropped != 1 || len(report.Failures) != 8 || report.Failures[0].Kind != "deadline" || report.Failures[1].Kind != "queue_drop" {
		t.Fatalf("lost failed warmup: %s", data)
	}
}
