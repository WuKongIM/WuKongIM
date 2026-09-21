package worker

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/stretchr/testify/require"
)

func TestLifecycleProgressMatchesReportsAcrossWorkloadGenerations(t *testing.T) {
	clients := map[string]benchworkload.PersonClient{"u1": &workerPersonClient{}, "u2": &workerPersonClient{}}
	person, err := benchworkload.NewPersonWorkload(benchworkload.PersonConfig{
		SenderUID: "u1", RecipientUID: "u2",
	}, clients)
	require.NoError(t, err)
	group, err := benchworkload.NewGroupWorkload(benchworkload.GroupConfig{
		Channels: []benchworkload.GroupChannel{{ChannelID: "group-a", OnlineMembers: []string{"u1", "u2"}}},
	}, clients)
	require.NoError(t, err)
	runner := &defaultWorkloadRunner{
		metrics:         metrics.NewRegistry(),
		personWorkloads: []*benchworkload.PersonWorkload{person},
		groupWorkloads:  []*benchworkload.GroupWorkload{group},
	}
	for _, registry := range []*metrics.Registry{person.Metrics(), group.Metrics()} {
		for _, phase := range []string{"warmup", "run"} {
			labels := metrics.Labels{"phase": phase}
			for _, counter := range []string{"logical_sent_total", "logical_identity_total", "send_attempt_total", "attempt_record_total", "sendack_success_total"} {
				registry.AddCounter(counter, labels, 3)
			}
			registry.SetGauge("configured_maximum_attempts", labels, 4)
			registry.SetGauge("maximum_observed_attempts", labels, 1)
			for _, ms := range []int{3, 1, 2} {
				registry.ObserveLatency("sendack_latency_seconds", labels, time.Duration(ms)*time.Millisecond)
			}
		}
	}
	before := runner.MetricsSnapshot()
	for i := 0; i < 3; i++ {
		status := runner.LifecycleStatus().Traffic
		require.Equal(t, trafficStatusFromMetrics(before), status)
		require.Equal(t, uint64(6), status.SendACKs)
		require.Equal(t, uint64(6), status.WarmupSendACKs)
		require.True(t, status.RetryEvidenceComplete)
	}
	require.Equal(t, before, runner.MetricsSnapshot())
	runner.mu.Lock()
	err = runner.archiveCurrentWorkloadMetricsLocked()
	runner.personWorkloads, runner.groupWorkloads = nil, nil
	runner.mu.Unlock()
	require.NoError(t, err)
	require.Equal(t, before, runner.MetricsSnapshot(), "archive retains exact latency summaries")
	require.Equal(t, trafficStatusFromMetrics(before), runner.LifecycleStatus().Traffic)
	// A new generation adds counters while retaining the retry-policy maxima.
	runner.personWorkloads = []*benchworkload.PersonWorkload{person}
	after := runner.MetricsSnapshot()
	status := runner.LifecycleStatus().Traffic
	require.Equal(t, trafficStatusFromMetrics(after), status)
	require.Equal(t, uint64(9), status.SendACKs)
	require.True(t, status.RetryEvidenceComplete)
	h := after.Histograms["sendack_latency_seconds{phase=run}"]
	require.Equal(t, uint64(9), h.Count)
	require.Equal(t, .003, h.P99Seconds)
}
