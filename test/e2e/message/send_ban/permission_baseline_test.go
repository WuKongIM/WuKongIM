//go:build e2e

package send_ban

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// Each cut preserves per-node scope. Missing resource samples remain absent;
// aggregate delta helpers never invent a zero for an unavailable metric.
type permissionBaselineCut map[uint64]map[string]float64

type permissionBaselineAck struct {
	ID               string    `json:"client_msg_no"`
	Reason           uint8     `json:"reason"`
	MessageID        int64     `json:"message_id"`
	Seq              uint64    `json:"message_seq"`
	Micros           int64     `json:"elapsed_us"`
	Error            string    `json:"error,omitempty"`
	PendingStartedAt time.Time `json:"pending_started_at,omitempty"`
	WriteStartedAt   time.Time `json:"write_started_at,omitempty"`
	DecodedAt        time.Time `json:"decoded_at,omitempty"`
	BridgeAt         time.Time `json:"bridge_at,omitempty"`
}

// TestPermissionCallerBaseline characterizes today's independent callers. It
// measures real processes and public endpoints, without a candidate cohort.
func TestPermissionCallerBaseline(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_BASELINE") != "1" {
		t.Skip("opt-in fixed permission baseline")
	}
	runPermissionCallerExperiment(t, false)
}

// TestPermissionCallerCohorts uses the identical fixed inputs but requires
// actual cross-caller reduction, fresh policy controls and exact history.
func TestPermissionCallerCohorts(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_COHORTS") != "1" {
		t.Skip("opt-in fixed permission cohort comparison")
	}
	runPermissionCallerExperiment(t, true)
}

func runPermissionCallerExperiment(t *testing.T, cohorts bool) {
	started := time.Now().UTC()
	report := map[string]any{
		"started_at": started, "nodes": 3, "hash_slots": 256, "physical_slots": 12,
		"metadata_replicas": 1, "message_replicas": 3, "connections": 32, "sends_per_window": 64,
		"permission_cache_ttl": "1h", "node_gomaxprocs": 4,
		"host_os": runtime.GOOS, "host_arch": runtime.GOARCH, "host_cpus": runtime.NumCPU(),
		"driver_gomaxprocs": runtime.GOMAXPROCS(0), "performance_qualified": false,
		"queue_owned_bytes": nil, "queue_byte_limit": 16 << 20, "queue_envelope_limit": 1024,
		"executing_envelope_limit": 64,
		"resource_scope":           "whole node; scrape cuts include scrape overhead; CPU/RSS gauges are periodic; allocation counters may be scrape-cached; profiles use a separate window",
		"fixture_scope":            "system UID; two mandatory facts; no auxiliary membership reads or recipient fanout; one-voter metadata routing proof, not HA",
	}
	report["cohort_candidate"] = cohorts
	timeline := os.Getenv("WK_E2E_PERMISSION_TIMELINE") == "1"
	report["request_timeline_enabled"] = timeline
	var cases []map[string]any
	path := os.Getenv("WK_E2E_PERMISSION_BASELINE_REPORT")
	if cohorts {
		path = os.Getenv("WK_E2E_PERMISSION_COHORT_REPORT")
	}
	if path == "" {
		path = filepath.Join(os.TempDir(), "wukongim-permission-baseline.json")
	}
	defer func() {
		report["passed"], report["finished_at"], report["cases"] = !t.Failed(), time.Now().UTC(), cases
		raw, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0644))
	}()
	// Pin the product independently of the changing characterization harness.
	binary := os.Getenv("WK_E2E_BINARY")
	require.NotEmpty(t, binary, "baseline requires a frozen prebuilt product binary")
	report["binary_sha256"] = permissionBaselineHash(t, binary)
	info, err := buildinfo.ReadFile(binary)
	require.NoError(t, err)
	report["binary_build"] = info
	var productRevision, modified string
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			productRevision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value
		}
	}
	report["product_revision"] = productRevision
	require.NotEmpty(t, productRevision, "baseline binary must identify its committed source")
	require.Equal(t, "false", modified, "baseline binary must use a clean source tree")
	_, harness, _, ok := runtime.Caller(0)
	require.True(t, ok)
	report["harness_sha256"] = permissionBaselineHash(t, harness)
	report["harness_sources"] = map[string]string{
		"permission_baseline_test.go":      permissionBaselineHash(t, harness),
		"permission_timeline_test.go":      permissionBaselineHash(t, filepath.Join(filepath.Dir(harness), "permission_timeline_test.go")),
		"suite/wkproto_client.go":          permissionBaselineHash(t, filepath.Join(filepath.Dir(harness), "../../suite/wkproto_client.go")),
		"permission_cpu_probe_test.go":     permissionBaselineHash(t, filepath.Join(filepath.Dir(harness), "permission_cpu_probe_test.go")),
		"fixtures/permission-cpu-darwin.c": permissionBaselineHash(t, filepath.Join(filepath.Dir(harness), "fixtures/permission-cpu-darwin.c")),
	}
	cpuProbe := os.Getenv("WK_E2E_PERMISSION_CPU_PROBE")
	if cpuProbe != "" {
		report["cpu_probe_sha256"] = permissionBaselineHash(t, cpuProbe)
		report["cpu_scope"] = "three owned node processes; public cumulative user+system CPU converted from raw Mach ticks; cuts enclose SEND window plus bounded snapshot/scrape scheduling overhead; no CPU profile"
	}

	opts := []suite.Option{suite.WithManagerHTTP()}
	for id := uint64(1); id <= 3; id++ {
		if timeline {
			opts = append(opts, suite.WithNodeConfigOverrides(id, map[string]string{
				"WK_DIAGNOSTICS_ENABLE": "true", "WK_DIAGNOSTICS_BUFFER_SIZE": "8192",
				"WK_DIAGNOSTICS_SAMPLE_RATE": "1", "WK_DIAGNOSTICS_DEEP_SAMPLE_RATE": "1",
				"WK_DIAGNOSTICS_DEEP_MAX_ITEMS_PER_BATCH": "16",
			}))
		}
		opts = append(opts, suite.WithNodeConfigOverrides(id, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
			"WK_CLUSTER_SLOT_REPLICA_N": "1", "WK_CLUSTER_CHANNEL_REPLICA_N": "3", "WK_GATEWAY_TOKEN_AUTH_ON": "false",
			"WK_MESSAGE_PERMISSION_CACHE_TTL": "1h", "WK_DEBUG_API_ENABLE": "true",
		}), suite.WithNodeEnv(id, "GOMAXPROCS=4"))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	var cpuPIDs []int
	if cpuProbe != "" {
		for id := uint64(1); id <= 3; id++ {
			node := cluster.MustNode(id)
			require.NotNil(t, node.Process.Cmd.Process)
			cpuPIDs = append(cpuPIDs, node.Process.Cmd.Process.Pid)
		}
		report["cpu_node_pids_in_id_order"] = cpuPIDs
	}
	configs := map[uint64]any{}
	for _, node := range cluster.Nodes {
		configs[node.Spec.ID] = map[string]any{"sha256": permissionBaselineHash(t, node.Spec.ConfigPath), "overrides": node.Spec.ConfigOverrides}
	}
	report["node_configs"] = configs
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(cluster.DumpDiagnostics())
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	stable, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	report["topology_fingerprint"] = stable.Fingerprint
	placements := map[uint16]suite.SlotDTO{}
	for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
		if slot.HashSlots != nil {
			for _, hash := range slot.HashSlots.Items {
				placements[hash] = slot
			}
		}
	}
	require.Len(t, placements, 256)
	const uid = "____system"
	const historyUID = "permission-baseline-offline-reader"
	userSlot := placements[uint16(crc32.ChecksumIEEE([]byte(uid))%256)]
	require.NotZero(t, userSlot.Runtime.LeaderID)
	require.Len(t, userSlot.Runtime.CurrentVoters, 1)

	for _, name := range []string{"same-slot-remote", "two-slots-one-remote-leader", "two-remote-leaders", "two-slots-local-leader"} {
		t.Run(name, func(t *testing.T) {
			var channel string
			var channelSlot suite.SlotDTO
			for i := 0; i < 10000; i++ {
				key := fmt.Sprintf("permission-baseline-%s-%04d", name, i)
				slot := placements[uint16(crc32.ChecksumIEEE([]byte(key))%256)]
				matches := slot.SlotID == userSlot.SlotID
				if name == "two-remote-leaders" {
					matches = slot.Runtime.LeaderID != userSlot.Runtime.LeaderID
				} else if name != "same-slot-remote" {
					matches = slot.SlotID != userSlot.SlotID && slot.Runtime.LeaderID == userSlot.Runtime.LeaderID
				}
				if matches && slot.Runtime.LeaderID != 0 {
					channel, channelSlot = key, slot
					break
				}
			}
			require.NotEmpty(t, channel)
			require.Len(t, channelSlot.Runtime.CurrentVoters, 1)
			ingressID := userSlot.Runtime.LeaderID
			if name != "two-slots-local-leader" {
				for id := uint64(1); id <= 3; id++ {
					if id != userSlot.Runtime.LeaderID && id != channelSlot.Runtime.LeaderID {
						ingressID = id
						break
					}
				}
				require.NotEqual(t, userSlot.Runtime.CurrentVoters[0], ingressID)
				require.NotEqual(t, channelSlot.Runtime.CurrentVoters[0], ingressID)
			}
			ingress := cluster.MustNode(ingressID)
			require.NoError(t, suite.PostChannel(ctx, ingress.APIAddr(), map[string]any{"channel_id": channel, "channel_type": 2, "subscribers": []string{historyUID}}))
			var clients []*suite.WKProtoClient
			for i := 0; i < 32; i++ {
				client, err := suite.NewWKProtoClientWithTimeout(10 * time.Second)
				require.NoError(t, err)
				t.Cleanup(func() { _ = client.Close() })
				_, err = client.ConnectContext(ctx, ingress.GatewayAddr(), uid, fmt.Sprintf("baseline-%s-%02d", name, i))
				require.NoError(t, err)
				clients = append(clients, client)
			}
			caseEvidence := map[string]any{"name": name, "channel": channel, "uid": uid, "ingress": ingressID, "user_slot": userSlot, "channel_slot": channelSlot}
			cases = append(cases, caseEvidence)
			var expected []string
			// Warm Channel runtime outside every measured window. No SEND retry.
			warm := permissionBaselineSend(ctx, clients[0], channel, name+"-warm", 1)
			caseEvidence["warm_ack"] = warm
			require.Empty(t, warm.Error)
			require.Equal(t, uint8(frame.ReasonSuccess), warm.Reason)
			expected = append(expected, warm.ID)
			meta, err := suite.GetChannelRuntimeMeta(ctx, ingress, channel, 2)
			require.NoError(t, err)
			caseEvidence["channel_runtime"] = meta
			require.Len(t, meta.Replicas, 3)
			require.NotZero(t, meta.Leader)
			var windows []map[string]any
			caseEvidence["windows"] = &windows
			for _, concurrency := range []int{1, 32} {
				window := map[string]any{"concurrency": concurrency, "completed": false}
				windows = append(windows, window)
				before := permissionBaselineMetrics(t, ctx, cluster)
				window["before"] = before
				sampleCtx, stopSamples := context.WithCancel(ctx)
				sampled := permissionCohortOwnershipSamples(sampleCtx, ingress.APIAddr())
				joined := false
				joinSamples := func() {
					if !joined {
						stopSamples()
						window["cohort_ownership_samples"] = <-sampled
						joined = true
					}
				}
				// Fatal CPU/query evidence failures must still join this sampler
				// before the parent writes its partial receipt.
				defer joinSamples()
				var cpuBefore permissionCPUCut
				if cpuProbe != "" {
					cpuBefore = permissionCPUQuery(t, ctx, cpuPIDs)
					window["cpu_before"] = cpuBefore
				}
				begin := time.Now()
				acks := permissionBaselineWave(ctx, clients[:concurrency], channel, fmt.Sprintf("%s-c%d", name, concurrency), 64)
				end := time.Now()
				window["acks"], window["started_at"], window["finished_at"] = acks, begin.UTC(), end.UTC()
				window["elapsed_ms"] = end.Sub(begin).Milliseconds()
				if cpuProbe != "" {
					cpuAfter := permissionCPUQuery(t, ctx, cpuPIDs)
					window["cpu_after"] = cpuAfter
					window["cluster_cpu_ns"] = permissionCPUInterval(t, cpuBefore, cpuAfter)
				}
				joinSamples()
				require.NoError(t, ctx.Err())
				require.Len(t, acks, 64, "stopped baseline retains partial responses")
				after := permissionBaselineMetrics(t, ctx, cluster)
				window["after"] = after
				counts := map[string]float64{}
				for _, key := range []string{"plans", "messages", "users", "channels", "facts", "facts_before", "node_envelopes", "local_envelopes", "slot_groups", "request_bytes", "response_bytes", "barrier_ok", "barrier_failed", "admission_busy", "cohorts", "cohort_requests", "cohort_facts", "cohort_busy"} {
					counts[key] = permissionBaselineDelta(before, after, key)
				}
				latencies := make([]int64, len(acks))
				for i, ack := range acks {
					latencies[i] = ack.Micros
				}
				sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
				window["counts"], window["sendack_p99_us"], window["sendack_max_us"] = counts, latencies[int(math.Ceil(float64(len(latencies))*.99))-1], latencies[len(latencies)-1]
				if timeline && name == "same-slot-remote" && concurrency == 32 {
					var timelines []map[string]any
					window["request_timelines"] = &timelines
					permissionRequestTimeline(t, ctx, cluster, ingressID, acks, &timelines)
				}
				for _, ack := range acks {
					require.Empty(t, ack.Error)
					require.Equal(t, uint8(frame.ReasonSuccess), ack.Reason)
					require.Positive(t, ack.MessageID)
					require.Positive(t, ack.Seq)
					expected = append(expected, ack.ID)
				}
				groups, envelopes := float64(128), float64(64)
				if name == "same-slot-remote" {
					groups = 64
				}
				if name == "two-remote-leaders" {
					envelopes = 128
				} else if name == "two-slots-local-leader" {
					envelopes = 0
				}
				require.EqualValues(t, 64, counts["messages"])
				require.EqualValues(t, 64, counts["plans"])
				require.EqualValues(t, 128, counts["facts"])
				if cohorts && concurrency == 32 {
					if envelopes > 0 {
						require.Less(t, counts["node_envelopes"], envelopes, "independent callers must reduce actual RPC envelopes")
					} else {
						require.Zero(t, counts["node_envelopes"])
						require.Less(t, permissionBaselineDelta(before, after, "local_envelopes"), float64(64))
					}
					require.Positive(t, counts["barrier_ok"])
					require.Less(t, counts["barrier_ok"], groups, "independent callers must share sealed fresh reads")
					require.Equal(t, counts["slot_groups"], counts["barrier_ok"])
				} else {
					require.Equal(t, envelopes, counts["node_envelopes"])
					require.Equal(t, groups, counts["slot_groups"])
					require.Equal(t, groups, counts["barrier_ok"])
				}
				require.Zero(t, counts["barrier_failed"])
				require.Zero(t, counts["admission_busy"])
				for _, node := range after {
					require.Zero(t, node["inflight"])
					if cohorts {
						for _, kind := range []string{"calls", "cohorts", "budget_bytes"} {
							value, ok := node["cohort_owned_"+kind]
							require.True(t, ok, "candidate ownership metric missing")
							require.Zero(t, value, "cohort ownership must drain after joined ACKs")
						}
					}
				}
				window["completed"] = true
			}
			// The next independent calls must observe completed writes, even with
			// one-hour auxiliary TTL. These controls are outside measured windows.
			_, err = suite.SetChannelSendBan(ctx, cluster.MustNode(3).APIAddr(), channel, 2, 1)
			require.NoError(t, err)
			denied := permissionBaselineWave(ctx, clients, channel, name+"-banned", 32)
			caseEvidence["after_ban"] = denied
			require.Len(t, denied, 32)
			for _, ack := range denied {
				require.Empty(t, ack.Error)
				require.Equal(t, uint8(frame.ReasonSendBan), ack.Reason)
				require.Zero(t, ack.MessageID)
				require.Zero(t, ack.Seq)
			}
			_, err = suite.SetChannelSendBan(ctx, cluster.MustNode(2).APIAddr(), channel, 2, 0)
			require.NoError(t, err)
			unbanned := permissionBaselineWave(ctx, clients, channel, name+"-unbanned", 32)
			caseEvidence["after_unban"] = unbanned
			require.Len(t, unbanned, 32)
			for _, ack := range unbanned {
				require.Empty(t, ack.Error)
				require.Equal(t, uint8(frame.ReasonSuccess), ack.Reason)
				require.Positive(t, ack.MessageID)
				require.Positive(t, ack.Seq)
				expected = append(expected, ack.ID)
			}
			if os.Getenv("WK_E2E_PERMISSION_BASELINE_PROFILES") == "1" && name == "two-slots-one-remote-leader" {
				owner := cluster.MustNode(userSlot.Runtime.LeaderID)
				profileDone := make(chan error, 1)
				go func() {
					profileDone <- permissionBaselineProfile(ctx, owner.APIAddr(), "/debug/pprof/profile?seconds=2", path+".cpu.pprof")
				}()
				profileAcks := permissionBaselineWave(ctx, clients, channel, name+"-profile", 256)
				profileErr := <-profileDone
				caseEvidence["profile_acks"] = profileAcks
				require.Len(t, profileAcks, 256)
				require.NoError(t, profileErr)
				require.NoError(t, permissionBaselineProfile(ctx, owner.APIAddr(), "/debug/pprof/heap", path+".heap.pprof"))
				caseEvidence["profiles"] = []string{path + ".cpu.pprof", path + ".heap.pprof"}
				for _, ack := range profileAcks {
					require.Empty(t, ack.Error)
					require.Equal(t, uint8(frame.ReasonSuccess), ack.Reason)
					expected = append(expected, ack.ID)
				}
			}
			caseEvidence["expected_history"] = expected
			// Product sync routes committed reads to the Channel authority. The
			// sole reader is offline, so no online recipient fanout enters SENDs.
			caseEvidence["history_reader_node"], caseEvidence["history_reader_uid"] = ingressID, historyUID
			var seen []string
			cursor := uint64(0)
			var historyPages []any
			caseEvidence["history_pages"] = &historyPages
			for page := 0; page < 8; page++ {
				var history struct {
					More     int `json:"more"`
					Messages []struct {
						MessageSeq  uint64 `json:"message_seq"`
						ClientMsgNo string `json:"client_msg_no"`
					} `json:"messages"`
				}
				_, err = suite.PostJSON(ctx, "http://"+ingress.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": historyUID, "channel_id": channel, "channel_type": 2, "start_message_seq": cursor, "end_message_seq": 0, "pull_mode": 0, "limit": 100}, &history)
				historyPages = append(historyPages, history)
				require.NoError(t, err)
				require.NotEmpty(t, history.Messages)
				for _, item := range history.Messages {
					seen = append(seen, item.ClientMsgNo)
				}
				caseEvidence["exact_history"], caseEvidence["history_has_more"] = seen, history.More != 0
				if history.More == 0 {
					break
				}
				require.Less(t, page, 7, "complete history exceeds eight-page bound")
				require.Greater(t, history.Messages[0].MessageSeq, uint64(1))
				next := history.Messages[0].MessageSeq - 1
				if cursor != 0 {
					require.Less(t, next, cursor)
				}
				cursor = next
			}
			require.ElementsMatch(t, expected, seen)
			final, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
			require.NoError(t, err)
			caseEvidence["final_topology_fingerprint"] = final.Fingerprint
			require.Equal(t, stable.Fingerprint, final.Fingerprint, "routing must not change inside the baseline")
		})
	}
}

func permissionBaselineHash(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func permissionBaselineSend(ctx context.Context, client *suite.WKProtoClient, channel, id string, seq uint64) permissionBaselineAck {
	start := time.Now()
	out := permissionBaselineAck{ID: id}
	if err := ctx.Err(); err != nil {
		out.Error = err.Error()
		return out
	}
	err := client.SendFrame(&frame.SendPacket{ChannelID: channel, ChannelType: 2, ClientMsgNo: id, ClientSeq: seq, Payload: []byte("permission-baseline-fixed-payload")})
	if err == nil {
		ack, observation, readErr := client.ReadSendAckWithTiming()
		if os.Getenv("WK_E2E_PERMISSION_TIMELINE") == "1" {
			out.PendingStartedAt, out.WriteStartedAt, out.DecodedAt, out.BridgeAt = observation.PendingStartedAt.UTC(), observation.WriteStartedAt.UTC(), observation.ObservedAt.UTC(), time.Now().UTC()
		}
		err = readErr
		if ack != nil {
			out.Reason, out.MessageID, out.Seq = uint8(ack.ReasonCode), ack.MessageID, ack.MessageSeq
			if ack.ClientMsgNo != id {
				err = fmt.Errorf("unexpected ACK identity %q", ack.ClientMsgNo)
			}
		}
	}
	out.Micros = time.Since(start).Microseconds()
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

// Fixed waves join every caller before admitting more work. Each connection
// has only one outstanding request, preventing same-session gateway batching.
func permissionBaselineWave(ctx context.Context, clients []*suite.WKProtoClient, channel, prefix string, count int) []permissionBaselineAck {
	out := make([]permissionBaselineAck, count)
	for first := 0; first < count; first += len(clients) {
		if ctx.Err() != nil {
			return out[:first]
		}
		gate := make(chan struct{})
		done := make(chan int, len(clients))
		for i := first; i < min(first+len(clients), count); i++ {
			go func(index int, client *suite.WKProtoClient) {
				<-gate
				out[index] = permissionBaselineSend(ctx, client, channel, fmt.Sprintf("%s-%03d", prefix, index), uint64(index+2))
				done <- index
			}(i, clients[i-first])
		}
		close(gate)
		for i := first; i < min(first+len(clients), count); i++ {
			<-done
		}
		for i := first; i < min(first+len(clients), count); i++ {
			if out[i].Error != "" {
				return out[:min(first+len(clients), count)]
			}
		}
	}
	return out
}

func permissionBaselineMetrics(t *testing.T, ctx context.Context, cluster *suite.StartedCluster) permissionBaselineCut {
	t.Helper()
	out := permissionBaselineCut{}
	for id := uint64(1); id <= 3; id++ {
		samples, err := suite.FetchMetricSamples(ctx, cluster.MustNode(id).APIAddr())
		require.NoError(t, err)
		values := map[string]float64{}
		for _, kind := range []string{"messages", "users", "channels", "facts", "facts_before", "slot_groups", "node_envelopes", "local_envelopes", "request_bytes", "response_bytes", "cohorts", "cohort_requests", "cohort_facts", "cohort_busy"} {
			values[kind] = suite.SumMetricSamples(samples, "wukongim_message_permission_counts_total", map[string]string{"kind": kind})
		}
		values["barrier_ok"] = suite.SumMetricSamples(samples, "wukongim_message_permission_duration_seconds_count", map[string]string{"stage": "barrier", "result": "ok"})
		values["plans"] = suite.SumMetricSamples(samples, "wukongim_message_permission_duration_seconds_count", map[string]string{"stage": "plan"})
		values["barrier_failed"] = suite.SumMetricSamples(samples, "wukongim_message_permission_duration_seconds_count", map[string]string{"stage": "barrier"}) - values["barrier_ok"]
		values["admission_busy"] = suite.SumMetricSamples(samples, "wukongim_message_permission_duration_seconds_count", map[string]string{"stage": "admission", "result": "busy"})
		values["inflight"] = suite.SumMetricSamples(samples, "wukongim_message_permission_inflight", nil)
		for _, sample := range samples {
			if sample.Name == "wukongim_message_permission_cohort_owned" {
				values["cohort_owned_"+sample.Labels["kind"]] = sample.Value
			}
		}
		for _, sample := range samples {
			switch sample.Name {
			case "process_cpu_seconds_total", "process_resident_memory_bytes", "wukongim_node_cpu_percent", "wukongim_node_memory_rss_bytes", "go_memstats_alloc_bytes_total", "go_memstats_alloc_bytes", "go_memstats_heap_inuse_bytes", "go_goroutines":
				values[sample.Name] = sample.Value
			}
			// Optional diagnosis preserves fixed public histogram families in the
			// same boundary scrape. It adds no in-window sampling or product hooks.
			if os.Getenv("WK_E2E_PERMISSION_STAGES") == "1" && permissionDiagnosticStageSample(sample.Name) {
				keys := make([]string, 0, len(sample.Labels))
				for key := range sample.Labels {
					if key != "node_id" && key != "node_name" {
						keys = append(keys, key)
					}
				}
				sort.Strings(keys)
				key := "stage_cut|" + sample.Name
				for _, label := range keys {
					key += "|" + label + "=" + sample.Labels[label]
				}
				values[key] = sample.Value
			}
		}
		out[id] = values
	}
	return out
}

// These existing bounded-label stages distinguish fact reads from subsequent
// append/replication/storage waits. Missing series stay absent in each node cut.
func permissionDiagnosticStageSample(name string) bool {
	for _, family := range []string{
		"wukongim_message_permission_duration_seconds",
		"wukongim_channelv2_append_stage_duration_seconds",
		"wukongim_channelv2_append_wait_stage_duration_seconds",
		"wukongim_channelv2_replication_stage_duration_seconds",
		"wukongim_storage_commit_batch_duration_seconds",
		"wukongim_storage_commit_request_duration_seconds",
	} {
		if name == family+"_count" || name == family+"_sum" || name == family+"_bucket" {
			return true
		}
	}
	return false
}

func permissionBaselineDelta(before, after permissionBaselineCut, key string) float64 {
	var sum float64
	for id, values := range after {
		sum += values[key] - before[id][key]
	}
	return sum
}

// Profiles are explicit, bounded diagnostics, separate from latency windows.
func permissionBaselineProfile(ctx context.Context, addr, endpoint, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("profile HTTP %s", strconv.Itoa(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(body) > 8<<20 {
		return fmt.Errorf("profile exceeds 8 MiB")
	}
	return os.WriteFile(path, body, 0644)
}

// Sample one ingress at a fixed 20 ms cadence during both old/new experiments.
// The bounded public scrape measures conservative proxy credits, not allocator
// memory. Missing old-binary gauges stay absent. Every sampler is canceled/joined.
func permissionCohortOwnershipSamples(ctx context.Context, addr string) <-chan []map[string]any {
	done := make(chan []map[string]any, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		observations := make([]map[string]any, 0, 200)
		for len(observations) < 200 {
			select {
			case <-ctx.Done():
				done <- observations
				return
			case <-ticker.C:
			}
			samples, err := suite.FetchMetricSamples(ctx, addr)
			if err != nil {
				if ctx.Err() == nil {
					observations = append(observations, map[string]any{"at": time.Now().UTC(), "error": err.Error()})
				}
				continue
			}
			owned := map[string]float64{}
			for _, sample := range samples {
				if sample.Name == "wukongim_message_permission_cohort_owned" {
					owned[sample.Labels["kind"]] = sample.Value
				}
			}
			observations = append(observations, map[string]any{"at": time.Now().UTC(), "owned": owned})
		}
		done <- observations
	}()
	return done
}
