package conversation_qps

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cpuSamplerStatFixture(pid string, ticks string) string {
	return pid + " (worker ) with space) S 1 2 3 4 5 6 7 8 9 10 110 120 0 0 20 0 4 0 " + ticks + " 1000 20\n"
}

func TestCPUWindowSamplerParsesIdentityAndCounters(t *testing.T) {
	got, err := parseCPUSampleStat(cpuSamplerStatFixture("17", "12345"))
	require.NoError(t, err)
	require.Equal(t, 17, got.PID)
	require.Equal(t, uint64(12345), got.StartTimeTicks)
	require.Equal(t, uint64(110), got.UserTicks)
	require.Equal(t, uint64(120), got.SystemTicks)
	require.Equal(t, uint64(7), got.MinorFaults)
	require.Equal(t, uint64(9), got.MajorFaults)
	for _, raw := range []string{"", "17 (x) S 1", cpuSamplerStatFixture("17", "0"), cpuSamplerStatFixture("17", "-1"), cpuSamplerStatFixture("17", "18446744073709551616")} {
		_, err = parseCPUSampleStat(raw)
		require.Error(t, err)
	}
	sched, err := parseCPUSampleSchedstat("1200000 3500000 8\n")
	require.NoError(t, err)
	require.Equal(t, uint64(3500000), sched.WaitNS)
	for _, raw := range []string{"", "1 2", "1 2 3 4", "1 -2 3", "18446744073709551616 2 3"} {
		_, err = parseCPUSampleSchedstat(raw)
		require.Error(t, err)
	}
	_, err = parseCPUSampleSchedstat("0 0 0")
	require.NoError(t, err, "zero values are observed values, not proof of no scheduling or disabled support")
}

func TestCPUWindowSamplerDifferenceRefusesReusedOrRegressedThread(t *testing.T) {
	a := cpuThreadSample{TID: 17, StartTimeTicks: 8, Valid: true, Schedstat: &cpuSchedstat{RuntimeNS: 100, WaitNS: 200, Timeslices: 3}}
	b := a
	b.Schedstat = &cpuSchedstat{RuntimeNS: 160, WaitNS: 290, Timeslices: 7}
	d, err := cpuThreadDifference(a, b)
	require.NoError(t, err)
	require.Equal(t, uint64(60), d.RuntimeNS)
	require.Equal(t, uint64(90), d.WaitNS)
	b.StartTimeTicks++
	_, err = cpuThreadDifference(a, b)
	require.Error(t, err)
	b = a
	b.Schedstat = &cpuSchedstat{RuntimeNS: 99, WaitNS: 200, Timeslices: 3}
	_, err = cpuThreadDifference(a, b)
	require.Error(t, err)
	b = a
	b.Valid = false
	_, err = cpuThreadDifference(a, b)
	require.Error(t, err)
}

func TestCPUWindowSamplerBoundsAndNoCatchUp(t *testing.T) {
	ps := []cpuSampleProcess{{Role: "driver", PID: 17, StartTimeTicks: 8, Affinity: "0-2"}}
	groups, err := validateCPUSamplerInputs(ps, []string{"/sys/fs/cgroup/x", "/sys/fs/cgroup", "/sys/fs/cgroup/x"}, 60*time.Second)
	require.NoError(t, err)
	require.Len(t, groups, 2)
	_, err = validateCPUSamplerInputs(append(ps, ps...), groups, time.Second)
	require.Error(t, err)
	_, err = validateCPUSamplerInputs(ps, []string{"/tmp/secret"}, time.Second)
	require.Error(t, err)
	_, err = validateCPUSamplerInputs(ps, groups, 0)
	require.Error(t, err)
	_, err = validateCPUSamplerInputs(ps, groups, 61*time.Second)
	require.Error(t, err)
	for _, mask := range []string{"", "1-0", "1,,2", "1,1", "1-1000000", "x"} {
		bad := append([]cpuSampleProcess(nil), ps...)
		bad[0].Affinity = mask
		_, err = validateCPUSamplerInputs(bad, groups, time.Second)
		require.Error(t, err)
	}
	require.True(t, cpuAffinitiesEqual("0-2", "0,1,2"))
	require.False(t, cpuAffinitiesEqual("0-2", "0-3"))
	next, missed := nextCPUSampleSlot(0, 2500*time.Millisecond)
	require.Equal(t, 3, next)
	require.Equal(t, 2, missed)
	next, missed = nextCPUSampleSlot(3, 3100*time.Millisecond)
	require.Equal(t, 4, next)
	require.Zero(t, missed)
	require.False(t, cpuAffinitiesEqual(strings.Repeat("1,", 5000), "1"))
}

func TestCPUWindowSamplerCapabilityRequiresDistinctMatchedProgress(t *testing.T) {
	thread := func(tid int, delta *cpuSchedstat) cpuThreadSample {
		return cpuThreadSample{TID: tid, StartTimeTicks: 7, Valid: true, Delta: delta}
	}
	point := func(threads ...cpuThreadSample) cpuWindowPoint {
		return cpuWindowPoint{Processes: []cpuProcessSample{{Expected: cpuSampleProcess{PID: 17}, Threads: threads}}}
	}
	zero := thread(1, &cpuSchedstat{})
	waitOnly := thread(2, &cpuSchedstat{WaitNS: 100})
	unmatched := thread(3, nil)
	invalid := thread(4, &cpuSchedstat{RuntimeNS: 100})
	invalid.Valid = false
	samples := CPUWindowSamples{Points: []cpuWindowPoint{point(zero, waitOnly, unmatched, invalid)}}
	require.Zero(t, cpuSchedstatProgressThreads(samples))
	run := thread(5, &cpuSchedstat{RuntimeNS: 100})
	slice := thread(6, &cpuSchedstat{Timeslices: 1})
	samples.Points = append(samples.Points, point(run, slice), point(run, slice))
	require.Equal(t, 2, cpuSchedstatProgressThreads(samples), "repeated intervals do not create more distinct capabilities")
	run.StartTimeTicks++
	samples.Points = append(samples.Points, point(run))
	require.Equal(t, 3, cpuSchedstatProgressThreads(samples), "task identity includes starttime")
}

func TestCPUWindowSamplerReceiptEscapingKeepsNormalThreadBudget(t *testing.T) {
	for _, raw := range []string{"plain proc stat 1 2 3\n", "\x00\b\f\n\r\t\x1f\"\\<>&", "线程 café 😀", "\u2028\u2029", string([]byte{0xff, 0xfe, 0xc0, 0x80})} {
		encoded, err := json.Marshal(raw)
		require.NoError(t, err)
		require.Equal(t, int64(len(encoded)-2), cpuJSONEscapedBytes(raw))
	}
	stat := strings.Repeat("1 ", 160) + "\n"
	status := strings.Repeat("Field:\tvalue\n", 126)
	perThread := int64(4096+4*1024) + 2*cpuJSONEscapedBytes(stat) + cpuJSONEscapedBytes(status) + cpuJSONEscapedBytes("1000000000 5000000000 12345\n") + int64(2*4*60)
	// Structural, process, host and cgroup margin is deliberately separate from
	// the observed thread payload; the test does not assume arbitrary workloads fit.
	normalWindow := int64(262144) + 60*(int64(128<<10)+55*perThread)
	require.Less(t, normalWindow, int64(cpuSamplerMaxBytes))
}

func TestCPUWindowSamplerConfirmedExitPreservesCoverageWithoutControlFailure(t *testing.T) {
	prior := map[string]cpuThreadSample{
		"1:7": {TID: 1, StartTimeTicks: 7, Valid: true},
		"2:8": {TID: 2, StartTimeTicks: 8, Valid: true},
	}
	exits, issues := classifyCPUThreadGaps(prior, map[int]bool{}, map[int]bool{})
	require.Equal(t, []string{"1:7", "2:8"}, exits)
	require.Empty(t, issues, "confirmed exits lower interval coverage, not affinity control")
	_, issues = classifyCPUThreadGaps(prior, map[int]bool{1: true}, map[int]bool{})
	require.NotEmpty(t, issues, "a thread lost during this read is not a confirmed between-point exit")
	_, issues = classifyCPUThreadGaps(prior, map[int]bool{}, map[int]bool{2: true})
	require.NotEmpty(t, issues, "a reused or still-present TID must not be called an exit")
}
