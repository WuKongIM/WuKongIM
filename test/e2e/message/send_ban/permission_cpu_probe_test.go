//go:build e2e

package send_ban

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type permissionCPUProcess struct {
	PID       int    `json:"pid"`
	Start     uint64 `json:"start_abstime"`
	User      uint64 `json:"user_ticks"`
	System    uint64 `json:"system_ticks"`
	SampledAt uint64 `json:"sampled_monotonic_ns"`
}

// permissionCPUCut preserves raw public process counters and sample bounds.
// CPU ticks are not nanoseconds; only same-identity differences are converted.
type permissionCPUCut struct {
	Numer           uint32                 `json:"timebase_numer"`
	Denom           uint32                 `json:"timebase_denom"`
	Begin           uint64                 `json:"started_monotonic_ns"`
	End             uint64                 `json:"finished_monotonic_ns"`
	Processes       []permissionCPUProcess `json:"processes"`
	QueryStartedAt  time.Time              `json:"query_started_at"`
	QueryFinishedAt time.Time              `json:"query_finished_at"`
}

func permissionCPUQuery(t *testing.T, ctx context.Context, pids []int) permissionCPUCut {
	t.Helper()
	require.Equal(t, "darwin", runtime.GOOS, "the optional native probe is Darwin-specific")
	probe := os.Getenv("WK_E2E_PERMISSION_CPU_PROBE")
	require.NotEmpty(t, probe)
	require.NotEmpty(t, pids)
	require.LessOrEqual(t, len(pids), 3)
	args := make([]string, len(pids))
	for i, pid := range pids {
		require.Positive(t, pid)
		args[i] = strconv.Itoa(pid)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	begin := time.Now().UTC()
	raw, err := exec.CommandContext(queryCtx, probe, args...).Output()
	end := time.Now().UTC()
	require.NoError(t, err, "owned-process CPU snapshot must be available")
	require.LessOrEqual(t, len(raw), 4096)
	var cut permissionCPUCut
	require.NoError(t, json.Unmarshal(raw, &cut))
	cut.QueryStartedAt, cut.QueryFinishedAt = begin, end
	require.Positive(t, cut.Numer)
	require.Positive(t, cut.Denom)
	require.GreaterOrEqual(t, cut.End, cut.Begin)
	require.Len(t, cut.Processes, len(pids))
	for i, process := range cut.Processes {
		require.Equal(t, pids[i], process.PID)
		require.NotZero(t, process.Start)
		require.GreaterOrEqual(t, process.SampledAt, cut.Begin)
		require.LessOrEqual(t, process.SampledAt, cut.End)
	}
	return cut
}

func permissionCPUInterval(t *testing.T, before, after permissionCPUCut) float64 {
	t.Helper()
	require.Equal(t, before.Numer, after.Numer)
	require.Equal(t, before.Denom, after.Denom)
	require.Len(t, after.Processes, len(before.Processes))
	require.GreaterOrEqual(t, after.Begin, before.End)
	var ns float64
	for i, b := range before.Processes {
		a := after.Processes[i]
		require.Equal(t, b.PID, a.PID)
		require.Equal(t, b.Start, a.Start, "process reuse invalidates the measured interval")
		require.GreaterOrEqual(t, a.User, b.User)
		require.GreaterOrEqual(t, a.System, b.System)
		ns += (float64(a.User-b.User) + float64(a.System-b.System)) * float64(before.Numer) / float64(before.Denom)
	}
	return ns
}
