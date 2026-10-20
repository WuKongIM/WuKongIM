//go:build linux && integration

package conversation_qps

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCPUWindowSamplerChild is an owned, real multi-thread process, not a product
// fixture. The parent bounds its lifetime and reaps it even when assertions fail.
func TestCPUWindowSamplerChild(t *testing.T) {
	if os.Getenv("CPU_WINDOW_SAMPLER_CHILD") != "1" {
		return
	}
	var ready sync.WaitGroup
	ready.Add(6)
	for i := 0; i < 6; i++ {
		go func() {
			runtime.LockOSThread()
			ready.Done()
			for {
				time.Sleep(time.Millisecond)
			}
		}()
	}
	ready.Wait()
	_, _ = os.Stdout.WriteString("CPU_SAMPLER_READY\n")
	for {
		time.Sleep(time.Second)
	}
}

func TestCPUWindowSamplerLinuxPreflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCPUWindowSamplerChild$")
	cmd.Env = append(os.Environ(), "CPU_WINDOW_SAMPLER_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	scan := bufio.NewScanner(stdout)
	require.True(t, scan.Scan())
	require.Equal(t, "CPU_SAMPLER_READY", scan.Text())
	base := filepath.Join("/proc", itoaCPU(cmd.Process.Pid))
	raw, err := os.ReadFile(filepath.Join(base, "stat"))
	require.NoError(t, err)
	stat, err := parseCPUSampleStat(string(raw))
	require.NoError(t, err)
	raw, err = os.ReadFile(filepath.Join(base, "status"))
	require.NoError(t, err)
	affinity, err := cpuStatusAffinity(string(raw))
	require.NoError(t, err)
	membership, err := os.ReadFile(filepath.Join(base, "cgroup"))
	require.NoError(t, err)
	mounts, err := os.ReadFile(filepath.Join(base, "mountinfo"))
	require.NoError(t, err)
	loc, err := resolveFixedHostCgroup(string(membership), string(mounts))
	require.NoError(t, err)
	ps := []cpuSampleProcess{{Role: "child", PID: stat.PID, StartTimeTicks: stat.StartTimeTicks, Affinity: affinity}}
	stop, err := startCPUWindowSampler(ps, []string{loc.Directory, loc.Directory}, time.Now().Add(50*time.Millisecond), 2*time.Second)
	require.NoError(t, err)
	defer stop()
	time.Sleep(1300 * time.Millisecond)
	result := stop()
	require.True(t, result.Valid, "%+v", result.Issues)
	require.GreaterOrEqual(t, len(result.Points), 2)
	require.Positive(t, result.SamplerCPUNS)
	require.Positive(t, result.ReadBytes)
	require.Positive(t, result.SamplerTID)
	require.Equal(t, "caller_stop", result.StopReason)
	require.Equal(t, result, stop(), "stop must be idempotent and already joined")
	for _, point := range result.Points {
		require.Len(t, point.Cgroups, 1)
		require.GreaterOrEqual(t, point.Processes[0].ValidThreads, 6)
		require.True(t, point.Valid)
	}
	require.GreaterOrEqual(t, cpuSchedstatProgressThreads(result), 2,
		"active child must demonstrate runtime or timeslice progress on distinct matched thread lifetimes")
	require.LessOrEqual(t, result.ConservativeReceiptBytes, int64(cpuSamplerMaxBytes))
	ps[0].Affinity = "999999"
	stop, err = startCPUWindowSampler(ps, []string{loc.Directory}, time.Now().Add(20*time.Millisecond), time.Second)
	require.NoError(t, err)
	defer stop()
	time.Sleep(100 * time.Millisecond)
	rejected := stop()
	require.False(t, rejected.Valid)
	require.NotEmpty(t, rejected.Points)
	require.False(t, rejected.Points[0].Processes[0].Valid)
}
