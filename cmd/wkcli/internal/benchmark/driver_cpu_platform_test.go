package benchmark

import (
	"runtime"
	"testing"
)

// There is no wall-clock or another-platform fallback for process CPU evidence.
func TestDriverCPUClockProbePlatformSupportIsExplicit(t *testing.T) {
	probe, err := newDriverCPUClockProbe()
	if runtime.GOOS != "linux" {
		if err == nil || probe != nil {
			t.Fatal("unsupported platform silently supplied a CPU clock probe")
		}
		return
	}
	if err != nil || probe == nil {
		t.Fatal("Linux did not supply its explicit process CPU clock probe")
	}
	sample := probe()
	if sample.ReadMask != 7 || sample.MonotonicBeforeNanos < 0 || sample.ProcessCPUNanos < 0 || sample.MonotonicAfterNanos < sample.MonotonicBeforeNanos {
		t.Fatalf("Linux triplet was incomplete or reversed: mask=%d", sample.ReadMask)
	}
}
