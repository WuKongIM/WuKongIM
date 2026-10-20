package conversation_qps

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFixedHostPreparationAgeRefusesLateControl(t *testing.T) {
	start := time.Unix(1000, 0)
	wait, err := fixedHostPreparationWait(start, start.Add(45*time.Second))
	require.NoError(t, err)
	require.Equal(t, 135*time.Second, wait)
	wait, err = fixedHostPreparationWait(start, start.Add(180*time.Second))
	require.NoError(t, err)
	require.Zero(t, wait)
	_, err = fixedHostPreparationWait(start, start.Add(180*time.Second+time.Nanosecond))
	require.ErrorContains(t, err, "exceeded")
}

func TestFixedHostProcessIdentityReadsKernelFields(t *testing.T) {
	// Fields 3..21 followed by field 22 (starttime). comm contains ')' and spaces.
	stat := "42 (worker ) name) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 12345 0"
	ticks, affinity, err := parseFixedHostProcess(stat, "Name:\tworker\nCpus_allowed_list:\t2-5\n")
	require.NoError(t, err)
	require.Equal(t, uint64(12345), ticks)
	require.Equal(t, "2-5", affinity)
	for _, broken := range []string{"42 (x) S 1", strings.Replace(stat, "12345", "bad", 1)} {
		_, _, err = parseFixedHostProcess(broken, "Cpus_allowed_list:\t2-5\n")
		require.Error(t, err)
	}
	_, _, err = parseFixedHostProcess(stat, "Name:\tx\n")
	require.ErrorContains(t, err, "affinity")
}

func TestFixedHostAffinityRequiresExactlyFourDistinctCPUs(t *testing.T) {
	for _, affinity := range []string{"0-3", "2,4,6,8", "1-2,5-6"} {
		require.NoError(t, validateFixedHostAffinity(affinity))
	}
	for _, affinity := range []string{"0-2", "0-4", "0-2,2", "3-1", "x", "", "0-999999999"} {
		require.Error(t, validateFixedHostAffinity(affinity))
	}
}

func TestFixedHostMonotonicAgeEstimateHasExplicitUncertainty(t *testing.T) {
	before := time.Unix(1000, 0)
	after := before.Add(4 * time.Millisecond)
	start, uncertainty, err := fixedHostStartEstimate(before, after, "300.25 400.00\n", 100, 20000)
	require.NoError(t, err)
	require.Equal(t, before.Add(-100250*time.Millisecond+2*time.Millisecond), start)
	require.Equal(t, 22*time.Millisecond, uncertainty)
	for _, uptime := range []string{"NaN 0", "Inf 0", "bad 0", "100.00 0"} {
		_, _, err = fixedHostStartEstimate(before, after, uptime, 100, 20000)
		require.Error(t, err)
	}
	_, _, err = fixedHostStartEstimate(before, after, "300.25 0", 0, 20000)
	require.Error(t, err)
}

func TestFixedHostCgroupResolvesMountedSubtree(t *testing.T) {
	location, err := resolveFixedHostCgroup("0::/jobs/run/test\n", "29 23 0:26 /jobs/run /sys/fs/cgroup\\040bench rw - cgroup2 cgroup rw\n")
	require.NoError(t, err)
	require.Equal(t, "/sys/fs/cgroup bench/test", location.Directory)
	require.Equal(t, "/jobs/run", location.MountRoot)
	require.Equal(t, "/sys/fs/cgroup bench", location.MountPoint)
	for _, group := range []string{"1:cpu:/jobs/run/test\n", "0::/other/test\n", "0::/jobs/run/../other\n"} {
		_, err = resolveFixedHostCgroup(group, "29 23 0:26 /jobs/run /sys/fs/cgroup rw - cgroup2 cgroup rw\n")
		require.Error(t, err)
	}
}

func TestFixedHostConfigurationNeverFallsBackToSecrets(t *testing.T) {
	redacted, err := redactFixedHostTOML([]byte("[node]\nid=1\n[bench]\napi_token=\"private-canary\"\n[gateway]\ntoken_auth_on=false\n"))
	require.NoError(t, err)
	require.Contains(t, redacted, "id = 1")
	require.NotContains(t, redacted, "private-canary")
	require.Contains(t, redacted, "[REDACTED]")
	require.Contains(t, redacted, "token_auth_on = false")
	redacted, err = redactFixedHostTOML([]byte("not valid = \"private-canary"))
	require.Error(t, err)
	require.Empty(t, redacted)
	redacted, err = redactFixedHostTOML([]byte("[unknown]\nopaque=\"private-canary\"\n"))
	require.Error(t, err)
	require.Empty(t, redacted)
}
