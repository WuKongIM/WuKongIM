//go:build integration

package app

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/counterwindow"
)

// startMixedSendCounterWindow preserves the mixed benchmark's post-warmup scope.
func startMixedSendCounterWindow(b testing.TB, apps []*App, dir string, operations, rate int) func() {
	return counterwindow.Start(b, dir, "mixed-send-counters/v1",
		"post-warmup through completed handlers; includes boundary snapshot and benchmark timer overhead",
		operations, rate, mixedSendGatherers(apps)...)
}
