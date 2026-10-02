//go:build e2e && darwin

package send_ban

import (
	"context"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPermissionCPUProbeCalibration checks the real process CPU clock against
// getrusage, catching missing Apple-silicon timebase conversion (125/3 here).
// It is process/elapsed-time evidence, so remains in the opt-in E2E tier.
func TestPermissionCPUProbeCalibration(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_CPU_PROBE") == "" {
		t.Skip("opt-in owned-process CPU probe")
	}
	require.Equal(t, "darwin", runtime.GOOS)
	var before, after syscall.Rusage
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &before))
	a := permissionCPUQuery(t, context.Background(), []int{os.Getpid()})
	end := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(end) {
		runtime.KeepAlive(end)
	}
	b := permissionCPUQuery(t, context.Background(), []int{os.Getpid()})
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &after))
	actual := float64(after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano())
	observed := permissionCPUInterval(t, a, b)
	t.Logf("getrusage_cpu_ns=%.0f probe_cpu_ns=%.0f timebase=%d/%d cuts=%+v -> %+v", actual, observed, a.Numer, a.Denom, a, b)
	require.Positive(t, observed)
	require.InDelta(t, actual, observed, actual*.25, "CPU units must match getrusage")
}
