package arrival

import (
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
