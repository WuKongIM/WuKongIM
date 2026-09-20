//go:build integration

package app

import (
	"os"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/counterwindow"
)

// startMixedSendCounterWindow preserves the mixed benchmark's post-warmup scope.
func startMixedSendCounterWindow(b testing.TB, apps []*App, dir string, operations, rate int) func() {
	if os.Getenv("WK_BENCH_SEND_FLIGHT_DIR") != "" {
		return counterwindow.StartWithProfile(b, dir, "mixed-send-counters/v1",
			"post-warmup through completed handlers; rolling trace and one-second sampler enabled; see flight receipt for actual instrumentation interval",
			operations, rate, mixedSendGatherers(apps)...)
	}
	return counterwindow.Start(b, dir, "mixed-send-counters/v1",
		"post-warmup through completed handlers; includes boundary snapshot and benchmark timer overhead",
		operations, rate, mixedSendGatherers(apps)...)
}

// startMixedSendWarmupCounters retains pre-measurement pressure independently;
// these boundary snapshots never change the gate or retry failed work.
func startMixedSendWarmupCounters(b testing.TB, apps []*App, operations, rate int) func() {
	dir := os.Getenv("WK_BENCH_SEND_COUNTERS_DIR")
	if dir == "" {
		return func() {}
	}
	dir += ".warmup"
	if err := os.Mkdir(dir, 0700); err != nil {
		b.Fatal(err)
	}
	return counterwindow.Start(b, dir, "mixed-send-warmup-counters/v1",
		"sustained warmup only; includes boundary snapshots; excludes measured traffic",
		operations, rate, mixedSendGatherers(apps)...)
}
