//go:build e2e

package send_ban

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
)

const (
	permissionObserverRequests = 200
	permissionObserverCadence  = 20 * time.Millisecond
	permissionObserverWindow   = permissionObserverRequests * permissionObserverCadence
	permissionObserverTimeout  = 250 * time.Millisecond
	permissionObserverMaxBody  = 8 << 20
	// V1 observed 581,107-byte identity bodies: 200 need 110.84MiB.
	// This fixed bound admits the complete workload with over 2x headroom.
	permissionObserverMaxBlockWire = 256 << 20
)

// permissionObserverResponse owns the complete encoded entity body on disk;
// headers are retained separately and HTTP transfer framing is excluded.
type permissionObserverResponse struct {
	Ordinal                int         `json:"ordinal"`
	ScheduledAt            time.Time   `json:"scheduled_at"`
	StartedAt              time.Time   `json:"started_at"`
	NetworkFinishedAt      time.Time   `json:"network_finished_at"`
	FetchFinishedAt        time.Time   `json:"fetch_finished_at"`
	ValidationStartedAt    time.Time   `json:"validation_started_at"`
	ValidationFinishedAt   time.Time   `json:"validation_finished_at"`
	ScheduledLatenessNS    int64       `json:"scheduled_lateness_ns"`
	Status                 int         `json:"status"`
	Headers                http.Header `json:"headers"`
	WirePath               string      `json:"wire_path,omitempty"`
	WireBytes              int         `json:"wire_bytes"`
	ObservedWireBytes      int         `json:"observed_wire_bytes"`
	WireTruncated          bool        `json:"wire_truncated"`
	WireSHA256             string      `json:"wire_sha256,omitempty"`
	LogicalBytes           int         `json:"logical_bytes"`
	LogicalSHA256          string      `json:"logical_sha256,omitempty"`
	FamilyCount            int         `json:"family_count"`
	MetricCount            int         `json:"metric_count"`
	SampleCount            int         `json:"sample_count"`
	WireSavedAt            time.Time   `json:"wire_saved_at"`
	WireRetentionStartedAt time.Time   `json:"wire_retention_started_at"`
	WireRetained           bool        `json:"wire_retained"`
	NetworkCompleted       bool        `json:"network_completed"`
	Validated              bool        `json:"validated"`
	Completed              bool        `json:"completed"`
	Error                  string      `json:"error,omitempty"`
	// Encoded bytes remain bounded in memory until the enclosing CPU cut ends.
	wire []byte
	// The monotonic complete-read boundary determines absolute cadence.
	networkFinished time.Time
}

type permissionObserverBlock struct {
	Ordinal             int       `json:"ordinal"`
	Encoding            string    `json:"accept_encoding"`
	ExpectedRequests    int       `json:"expected_requests"`
	StartedAt           time.Time `json:"started_at"`
	ScheduledFinishedAt time.Time `json:"scheduled_finished_at"`
	FinishedAt          time.Time `json:"finished_at"`
	WallNS              int64     `json:"wall_ns"`
	UTCWallNS           int64     `json:"utc_wall_ns"`
	UTCWallDriftNS      int64     `json:"utc_wall_drift_ns"`
	CPUAfterLatenessNS  int64     `json:"cpu_after_lateness_ns"`
	// Query gaps retain signed UTC evidence from the unchanged native helper.
	CPUBeforeQueryStartGapNS  int64 `json:"cpu_before_query_start_gap_ns"`
	CPUBeforeQueryFinishGapNS int64 `json:"cpu_before_query_finish_gap_ns"`
	CPUBeforeQueryDurationNS  int64 `json:"cpu_before_query_duration_ns"`
	CPUAfterQueryStartGapNS   int64 `json:"cpu_after_query_start_gap_ns"`
	CPUAfterQueryFinishGapNS  int64 `json:"cpu_after_query_finish_gap_ns"`
	CPUAfterQueryDurationNS   int64 `json:"cpu_after_query_duration_ns"`
	// Call gaps/durations use the outer time.Now values' monotonic component.
	CPUBeforeCallStartedAt   time.Time `json:"cpu_before_call_started_at"`
	CPUBeforeCallFinishedAt  time.Time `json:"cpu_before_call_finished_at"`
	CPUAfterCallStartedAt    time.Time `json:"cpu_after_call_started_at"`
	CPUAfterCallFinishedAt   time.Time `json:"cpu_after_call_finished_at"`
	CPUBeforeCallStartGapNS  int64     `json:"cpu_before_call_start_gap_ns"`
	CPUBeforeCallFinishGapNS int64     `json:"cpu_before_call_finish_gap_ns"`
	CPUBeforeCallDurationNS  int64     `json:"cpu_before_call_duration_ns"`
	CPUAfterCallStartGapNS   int64     `json:"cpu_after_call_start_gap_ns"`
	CPUAfterCallFinishGapNS  int64     `json:"cpu_after_call_finish_gap_ns"`
	CPUAfterCallDurationNS   int64     `json:"cpu_after_call_duration_ns"`
	CPUBeforeCallUTCDriftNS  int64     `json:"cpu_before_call_utc_drift_ns"`
	CPUAfterCallUTCDriftNS   int64     `json:"cpu_after_call_utc_drift_ns"`
	CPUBeforeGapUTCDriftNS   int64     `json:"cpu_before_gap_utc_drift_ns"`
	CPUAfterGapUTCDriftNS    int64     `json:"cpu_after_gap_utc_drift_ns"`
	CPUBeforeUTCBoundsValid  bool      `json:"cpu_before_utc_bounds_valid"`
	CPUAfterUTCBoundsValid   bool      `json:"cpu_after_utc_bounds_valid"`
	UTCClockInconsistent     bool      `json:"utc_clock_inconsistent"`
	NativeCPUSpanNS          uint64    `json:"native_cpu_span_ns"`
	NativeCPUInnerSpanNS     uint64    `json:"native_cpu_inner_span_ns"`
	NativeCPUSpanValid       bool      `json:"native_cpu_span_valid"`
	CPUBeforeBoundsValid     bool      `json:"cpu_before_bounds_valid"`
	CPUAfterBoundsValid      bool      `json:"cpu_after_bounds_valid"`
	CPUCutBoundsValid        bool      `json:"cpu_cut_bounds_valid"`
	// CPUValid becomes true only after native interval validation and both
	// monotonic call/native envelopes pass; deferred partial analysis cannot set it.
	CPUValid           bool                         `json:"cpu_valid"`
	CPUBefore          permissionCPUCut             `json:"cpu_before"`
	CPUAfter           permissionCPUCut             `json:"cpu_after"`
	ClusterCPUNS       float64                      `json:"cluster_cpu_ns"`
	RawWireBytes       int                          `json:"raw_wire_bytes"`
	AnalysisStartedAt  time.Time                    `json:"analysis_started_at"`
	AnalysisFinishedAt time.Time                    `json:"analysis_finished_at"`
	Responses          []permissionObserverResponse `json:"responses"`
	Completed          bool                         `json:"completed"`
	Error              string                       `json:"error,omitempty"`
}

// TestPermissionObserverCostDiagnosticV2 isolates server encoding work with fixed
// raw acquisition windows. Complete client validation follows CPUAfter.
// Whole-node CPU includes background work and never qualifies SEND CPU.
func TestPermissionObserverCostDiagnosticV2(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_OBSERVER_V2") != "1" {
		t.Skip("opt-in observer-only compression diagnosis")
	}
	require.Equal(t, "darwin", runtime.GOOS)
	path := os.Getenv("WK_E2E_PERMISSION_OBSERVER_REPORT")
	require.True(t, filepath.IsAbs(path), "observer report requires a new absolute path")
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err), "do not overwrite an existing receipt")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	rawDir := path + ".raw"
	require.NoError(t, os.Mkdir(rawDir, 0755), "do not overwrite retained raw responses")
	var blocks []*permissionObserverBlock
	report := map[string]any{
		"started_at": time.Now().UTC(), "diagnostic_only": true, "performance_qualified": false, "diagnostic_version": 2,
		"nodes": 3, "hash_slots": 256, "physical_slots": 12, "metadata_replicas": 1,
		"host_os": runtime.GOOS, "host_arch": runtime.GOARCH, "host_cpus": runtime.NumCPU(), "driver_gomaxprocs": runtime.GOMAXPROCS(0),
		"message_replicas": 3, "node_gomaxprocs": 4, "warm_sends": 1, "measured_sends": 0,
		"block_order":        []string{"gzip", "identity", "identity", "gzip"},
		"requests_per_block": permissionObserverRequests, "cadence_ns": int64(permissionObserverCadence),
		"window_ns": int64(permissionObserverWindow), "request_timeout_ns": int64(permissionObserverTimeout),
		"actual_cpu_cut_gap_limit_ns":    int64(permissionObserverCadence),
		"actual_cpu_cut_clock_basis":     "outer time.Now monotonic call gaps/durations and native CLOCK_MONOTONIC spans; signed UTC evidence is reported independently",
		"max_wire_or_logical_body_bytes": permissionObserverMaxBody, "max_block_raw_wire_bytes": permissionObserverMaxBlockWire, "raw_directory": rawDir,
		"wire_scope":                     "HTTP entity body before Content-Encoding decoding; transfer framing excluded; headers separate",
		"cpu_scope":                      "three owned node user+system counters; fixed four-second observer-only blocks plus native cut overhead; no subtraction or SEND qualification",
		"calibration_requirement":        "outer runner executes TestPermissionCPUProbeCalibration first with this exact native probe",
		"transport_scope":                "same fresh-connection transport in all blocks to prevent implicit reused-connection GET retries",
		"acquisition_scope":              "fresh GET and complete bounded raw read plus status/header/budget checks; no hash, decoding, expfmt or file writes inside CPU windows",
		"validation_scope":               "after CPUAfter, fully decode/hash/parse/retain each body; analysis failures stay failed; v1 and product/fixed-load thresholds unchanged",
		"previous_failed_receipt_sha256": "9bfda7d0da76a85a4530edfbcdd878320ba897f645122bdcacaf01fb6b904b53",
	}
	defer func() {
		report["passed"], report["finished_at"], report["blocks"] = !t.Failed(), time.Now().UTC(), blocks
		body, marshalErr := json.MarshalIndent(report, "", "  ")
		require.NoError(t, marshalErr)
		require.NoError(t, os.WriteFile(path, append(body, '\n'), 0644))
	}()
	binary := os.Getenv("WK_E2E_BINARY")
	require.NotEmpty(t, binary, "use the exact frozen old binary")
	report["binary_sha256"] = permissionBaselineHash(t, binary)
	info, err := buildinfo.ReadFile(binary)
	require.NoError(t, err)
	report["binary_build"] = info
	var revision, modified string
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value
		}
	}
	report["product_revision"] = revision
	require.Equal(t, "424d03eb298b972ec572261d617972eecb5523c5", revision)
	require.Equal(t, "false", modified)
	probe := os.Getenv("WK_E2E_PERMISSION_CPU_PROBE")
	require.NotEmpty(t, probe, "use the calibrated original Darwin native probe")
	report["cpu_probe_sha256"] = permissionBaselineHash(t, probe)
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sources := map[string]string{}
	for _, name := range []string{"permission_observer_cost_test.go", "permission_baseline_test.go", "permission_cpu_probe_test.go", "fixtures/permission-cpu-darwin.c"} {
		sources[name] = permissionBaselineHash(t, filepath.Join(filepath.Dir(source), name))
	}
	report["harness_sources"] = sources
	report["harness_sha256"] = sources["permission_observer_cost_test.go"]
	opts := []suite.Option{suite.WithManagerHTTP()}
	for id := uint64(1); id <= 3; id++ {
		opts = append(opts, suite.WithNodeConfigOverrides(id, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
			"WK_CLUSTER_SLOT_REPLICA_N": "1", "WK_CLUSTER_CHANNEL_REPLICA_N": "3",
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h",
			"WK_DEBUG_API_ENABLE": "true",
		}), suite.WithNodeEnv(id, "GOMAXPROCS=4"))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	pids := make([]int, 0, 3)
	configs := map[uint64]any{}
	for id := uint64(1); id <= 3; id++ {
		node := cluster.MustNode(id)
		require.NotNil(t, node.Process.Cmd.Process)
		pids = append(pids, node.Process.Cmd.Process.Pid)
		configs[id] = map[string]any{"sha256": permissionBaselineHash(t, node.Spec.ConfigPath), "overrides": node.Spec.ConfigOverrides}
	}
	report["cpu_node_pids_in_id_order"], report["node_configs"] = pids, configs
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
	const uid, historyUID = "____system", "permission-observer-offline-reader"
	userSlot := placements[uint16(crc32.ChecksumIEEE([]byte(uid))%256)]
	require.NotZero(t, userSlot.Runtime.LeaderID)
	require.Len(t, userSlot.Runtime.CurrentVoters, 1)
	var channel string
	var channelSlot suite.SlotDTO
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("permission-observer-cost-%04d", i)
		slot := placements[uint16(crc32.ChecksumIEEE([]byte(key))%256)]
		if slot.SlotID == userSlot.SlotID {
			channel, channelSlot = key, slot
			break
		}
	}
	require.NotEmpty(t, channel)
	ingressID := uint64(1)
	if ingressID == userSlot.Runtime.LeaderID {
		ingressID = 2
	}
	ingress := cluster.MustNode(ingressID)
	report["ingress"], report["user_slot"], report["channel_slot"], report["channel"] = ingressID, userSlot, channelSlot, channel
	require.NoError(t, suite.PostChannel(ctx, ingress.APIAddr(), map[string]any{"channel_id": channel, "channel_type": 2, "subscribers": []string{historyUID}}))
	client, err := suite.NewWKProtoClientWithTimeout(10 * time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.ConnectContext(ctx, ingress.GatewayAddr(), uid, "permission-observer-one-client")
	require.NoError(t, err)
	warm := permissionBaselineSend(ctx, client, channel, "permission-observer-warm", 1)
	report["warm_ack"] = warm
	require.Empty(t, warm.Error)
	require.Equal(t, uint8(frame.ReasonSuccess), warm.Reason)
	require.Positive(t, warm.MessageID)
	require.Positive(t, warm.Seq)
	meta, err := suite.GetChannelRuntimeMeta(ctx, ingress, channel, 2)
	require.NoError(t, err)
	report["channel_runtime"] = meta
	require.Len(t, meta.Replicas, 3)
	require.NotZero(t, meta.Leader)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy, transport.DisableCompression, transport.DisableKeepAlives = nil, true, true
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: permissionObserverTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for index, encoding := range []string{"gzip", "identity", "identity", "gzip"} {
		block := &permissionObserverBlock{Ordinal: index + 1, Encoding: encoding, ExpectedRequests: permissionObserverRequests}
		blocks = append(blocks, block)
		// Even fatal native-cut failures retain already-read wire bytes before
		// the outer deferred JSON receipt; normal completion drains this early.
		defer permissionObserverAnalyzeAndRetain(block)
		beforeCallStarted := time.Now()
		block.CPUBeforeCallStartedAt = beforeCallStarted.UTC()
		block.CPUBefore = permissionCPUQuery(t, ctx, pids)
		beforeCallFinished := time.Now()
		block.CPUBeforeCallFinishedAt = beforeCallFinished.UTC()
		begin := time.Now()
		end := begin.Add(permissionObserverWindow)
		block.StartedAt, block.ScheduledFinishedAt = begin.UTC(), end.UTC()
		// Preserve signed UTC evidence, including rollback; it cannot determine
		// actual elapsed cut bounds independently of the monotonic clock.
		block.CPUBeforeQueryStartGapNS = block.StartedAt.Sub(block.CPUBefore.QueryStartedAt).Nanoseconds()
		block.CPUBeforeQueryFinishGapNS = block.StartedAt.Sub(block.CPUBefore.QueryFinishedAt).Nanoseconds()
		block.CPUBeforeQueryDurationNS = block.CPUBefore.QueryFinishedAt.Sub(block.CPUBefore.QueryStartedAt).Nanoseconds()
		block.CPUBeforeUTCBoundsValid = !block.CPUBefore.QueryStartedAt.IsZero() && !block.CPUBefore.QueryFinishedAt.IsZero() &&
			block.CPUBeforeQueryStartGapNS >= 0 && block.CPUBeforeQueryStartGapNS < int64(permissionObserverCadence) &&
			block.CPUBeforeQueryFinishGapNS >= 0 && block.CPUBeforeQueryFinishGapNS < int64(permissionObserverCadence) &&
			block.CPUBeforeQueryDurationNS >= 0 && block.CPUBeforeQueryDurationNS < int64(permissionObserverCadence)
		block.CPUBeforeCallStartGapNS = begin.Sub(beforeCallStarted).Nanoseconds()
		block.CPUBeforeCallFinishGapNS = begin.Sub(beforeCallFinished).Nanoseconds()
		block.CPUBeforeCallDurationNS = beforeCallFinished.Sub(beforeCallStarted).Nanoseconds()
		block.CPUBeforeBoundsValid = !beforeCallStarted.IsZero() && !beforeCallFinished.IsZero() &&
			block.CPUBeforeCallStartGapNS >= 0 && block.CPUBeforeCallStartGapNS < int64(permissionObserverCadence) &&
			block.CPUBeforeCallFinishGapNS >= 0 && block.CPUBeforeCallFinishGapNS < int64(permissionObserverCadence) &&
			block.CPUBeforeCallDurationNS >= 0 && block.CPUBeforeCallDurationNS < int64(permissionObserverCadence)
		block.CPUBeforeCallUTCDriftNS = block.CPUBeforeCallFinishedAt.Sub(block.CPUBeforeCallStartedAt).Nanoseconds() - block.CPUBeforeCallDurationNS
		block.CPUBeforeGapUTCDriftNS = block.StartedAt.Sub(block.CPUBeforeCallStartedAt).Nanoseconds() - block.CPUBeforeCallStartGapNS
		block.UTCClockInconsistent = block.CPUBeforeCallUTCDriftNS != 0 || block.CPUBeforeGapUTCDriftNS != 0 || !block.CPUBeforeUTCBoundsValid
		for ordinal := 0; ordinal < permissionObserverRequests; ordinal++ {
			scheduled := begin.Add(time.Duration(ordinal) * permissionObserverCadence)
			if err := permissionObserverWaitUntil(ctx, scheduled); err != nil {
				block.Error = err.Error()
				break
			}
			remaining := permissionObserverMaxBlockWire - block.RawWireBytes
			if remaining <= 0 {
				block.Error = "block raw wire exceeds 256MiB; no replacement requests"
				break
			}
			response := permissionObserverFetch(ctx, httpClient, ingress.APIAddr(), encoding, rawDir, index+1, ordinal, scheduled, remaining)
			block.Responses = append(block.Responses, response)
			block.RawWireBytes += response.WireBytes
			if response.Error != "" {
				block.Error = response.Error
				break
			}
			if !response.networkFinished.Before(scheduled.Add(permissionObserverCadence)) {
				block.Error = fmt.Sprintf("request %d missed next absolute cadence slot; no catch-up", ordinal)
				break
			}
		}
		if err := permissionObserverWaitUntil(ctx, end); err != nil {
			block.Error = err.Error()
		}
		finished := time.Now()
		block.FinishedAt, block.WallNS, block.CPUAfterLatenessNS = finished.UTC(), finished.Sub(begin).Nanoseconds(), finished.Sub(end).Nanoseconds()
		block.UTCWallNS = block.FinishedAt.Sub(block.StartedAt).Nanoseconds()
		block.UTCWallDriftNS = block.UTCWallNS - block.WallNS
		block.UTCClockInconsistent = block.UTCClockInconsistent || block.UTCWallDriftNS != 0
		if finished.Sub(end) >= permissionObserverCadence {
			block.Error += " closing lateness reached 20ms"
		}
		afterCallStarted := time.Now()
		block.CPUAfterCallStartedAt = afterCallStarted.UTC()
		block.CPUAfter = permissionCPUQuery(t, ctx, pids)
		afterCallFinished := time.Now()
		block.CPUAfterCallFinishedAt = afterCallFinished.UTC()
		block.CPUAfterQueryStartGapNS = block.CPUAfter.QueryStartedAt.Sub(block.ScheduledFinishedAt).Nanoseconds()
		block.CPUAfterQueryFinishGapNS = block.CPUAfter.QueryFinishedAt.Sub(block.ScheduledFinishedAt).Nanoseconds()
		block.CPUAfterQueryDurationNS = block.CPUAfter.QueryFinishedAt.Sub(block.CPUAfter.QueryStartedAt).Nanoseconds()
		block.CPUAfterUTCBoundsValid = !block.CPUAfter.QueryStartedAt.IsZero() && !block.CPUAfter.QueryFinishedAt.IsZero() &&
			block.CPUAfterQueryStartGapNS >= 0 && block.CPUAfterQueryStartGapNS < int64(permissionObserverCadence) &&
			block.CPUAfterQueryFinishGapNS >= 0 && block.CPUAfterQueryFinishGapNS < int64(permissionObserverCadence) &&
			block.CPUAfterQueryDurationNS >= 0 && block.CPUAfterQueryDurationNS < int64(permissionObserverCadence)
		// The untouched time.Now values preserve monotonic subtraction across
		// a wall-clock step and include all native helper call overhead.
		block.CPUAfterCallStartGapNS = afterCallStarted.Sub(end).Nanoseconds()
		block.CPUAfterCallFinishGapNS = afterCallFinished.Sub(end).Nanoseconds()
		block.CPUAfterCallDurationNS = afterCallFinished.Sub(afterCallStarted).Nanoseconds()
		block.CPUAfterBoundsValid = !afterCallStarted.IsZero() && !afterCallFinished.IsZero() &&
			block.CPUAfterCallStartGapNS >= 0 && block.CPUAfterCallStartGapNS < int64(permissionObserverCadence) &&
			block.CPUAfterCallFinishGapNS >= 0 && block.CPUAfterCallFinishGapNS < int64(permissionObserverCadence) &&
			block.CPUAfterCallDurationNS >= 0 && block.CPUAfterCallDurationNS < int64(permissionObserverCadence)
		block.CPUAfterCallUTCDriftNS = block.CPUAfterCallFinishedAt.Sub(block.CPUAfterCallStartedAt).Nanoseconds() - block.CPUAfterCallDurationNS
		block.CPUAfterGapUTCDriftNS = block.CPUAfterCallFinishedAt.Sub(block.ScheduledFinishedAt).Nanoseconds() - block.CPUAfterCallFinishGapNS
		block.UTCClockInconsistent = block.UTCWallDriftNS != 0 || block.CPUBeforeCallUTCDriftNS != 0 || block.CPUAfterCallUTCDriftNS != 0 ||
			block.CPUBeforeGapUTCDriftNS != 0 || block.CPUAfterGapUTCDriftNS != 0 || !block.CPUBeforeUTCBoundsValid || !block.CPUAfterUTCBoundsValid
		if block.CPUAfter.End >= block.CPUBefore.Begin && block.CPUAfter.Begin >= block.CPUBefore.End {
			block.NativeCPUSpanNS = block.CPUAfter.End - block.CPUBefore.Begin
			block.NativeCPUInnerSpanNS = block.CPUAfter.Begin - block.CPUBefore.End
			minSpan, maxSpan := uint64(permissionObserverWindow), uint64(permissionObserverWindow+2*permissionObserverCadence)
			block.NativeCPUSpanValid = block.NativeCPUSpanNS >= minSpan && block.NativeCPUSpanNS < maxSpan &&
				block.NativeCPUInnerSpanNS >= minSpan && block.NativeCPUInnerSpanNS < maxSpan
		}
		block.CPUCutBoundsValid = block.CPUBeforeBoundsValid && block.CPUAfterBoundsValid && block.NativeCPUSpanValid
		if !block.CPUCutBoundsValid {
			block.Error += " monotonic/native CPU bounds are missing, reversed or outside the strict cut limits"
		}
		block.ClusterCPUNS = permissionCPUInterval(t, block.CPUBefore, block.CPUAfter)
		block.CPUValid = block.CPUCutBoundsValid
		// Client analysis and raw retention start only after native CPUAfter.
		permissionObserverAnalyzeAndRetain(block)
		if !block.Completed {
			t.Errorf("observer block %d %s incomplete: %d/%d responses: %s", index+1, encoding, len(block.Responses), permissionObserverRequests, block.Error)
		}
	}
	var history struct {
		More     int `json:"more"`
		Messages []struct {
			MessageSeq  uint64 `json:"message_seq"`
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	_, err = suite.PostJSON(ctx, "http://"+ingress.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": historyUID, "channel_id": channel, "channel_type": 2, "start_message_seq": 0, "end_message_seq": 0, "pull_mode": 0, "limit": 100}, &history)
	report["history"] = history
	require.NoError(t, err)
	require.Zero(t, history.More, "full history must terminate")
	require.Len(t, history.Messages, 1)
	require.Equal(t, warm.ID, history.Messages[0].ClientMsgNo)
	require.Equal(t, warm.Seq, history.Messages[0].MessageSeq)
	report["exact_history_completed"] = true
	final, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	report["final_topology_fingerprint"] = final.Fingerprint
	require.Equal(t, stable.Fingerprint, final.Fingerprint)
}

func permissionObserverWaitUntil(ctx context.Context, deadline time.Time) error {
	if delay := time.Until(deadline); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// permissionObserverAnalyzeAndRetain validates and persists after CPUAfter only.
// The deferred call also preserves bytes when a native after-cut fails fatally.
func permissionObserverAnalyzeAndRetain(block *permissionObserverBlock) {
	if !block.AnalysisFinishedAt.IsZero() {
		return
	}
	block.AnalysisStartedAt = time.Now().UTC()
	if !block.CPUValid && block.Error == "" {
		block.Error = "native CPU interval or actual cut bounds invalid/incomplete"
	}
	allComplete := len(block.Responses) == permissionObserverRequests
	for i := range block.Responses {
		response := &block.Responses[i]
		if response.WirePath == "" || response.wire == nil {
			allComplete = false
			continue
		}
		permissionObserverValidate(response, block.Encoding)
		response.WireRetentionStartedAt = time.Now().UTC()
		file, err := os.OpenFile(response.WirePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, err = file.Write(response.wire)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		response.WireSavedAt = time.Now().UTC()
		response.wire = nil
		if err != nil {
			block.Error += " raw response retention failed: " + err.Error()
		} else {
			response.WireRetained = true
		}
		response.Completed = response.NetworkCompleted && response.Validated && response.WireRetained && response.Error == ""
		if !response.Completed {
			allComplete = false
			if response.Error != "" {
				block.Error += fmt.Sprintf(" response %d failed validation/acquisition: %s", response.Ordinal, response.Error)
			}
		}
	}
	block.AnalysisFinishedAt = time.Now().UTC()
	block.Completed = block.Error == "" && allComplete && block.CPUAfter.End != 0 && block.CPUValid &&
		block.CPUBeforeBoundsValid && block.CPUAfterBoundsValid && block.NativeCPUSpanValid && block.CPUCutBoundsValid
}

// permissionObserverFetch acquires raw bytes only. Complete body hashing,
// decoding, expfmt validation and raw retention follow the CPUAfter cut.
func permissionObserverFetch(ctx context.Context, client *http.Client, addr, encoding, rawDir string, block, ordinal int, scheduled time.Time, remaining int) (out permissionObserverResponse) {
	start := time.Now()
	out.Ordinal, out.ScheduledAt, out.StartedAt = ordinal, scheduled.UTC(), start.UTC()
	out.ScheduledLatenessNS = max(int64(0), start.Sub(scheduled).Nanoseconds())
	defer func() { out.FetchFinishedAt = time.Now().UTC() }()
	if start.Sub(scheduled) >= permissionObserverCadence {
		out.Error = "request start lateness reached 20ms; no catch-up"
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, permissionObserverTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+"/metrics", nil)
	if err != nil {
		out.Error = err.Error()
		return
	}
	req.Header.Set("Accept-Encoding", encoding)
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		out.Error = err.Error()
		return
	}
	out.Status, out.Headers = resp.StatusCode, resp.Header.Clone()
	limit := min(permissionObserverMaxBody, remaining)
	if resp.ContentLength > int64(limit) {
		_ = resp.Body.Close()
		out.NetworkFinishedAt = time.Now().UTC()
		out.Error = "Content-Length exceeds body/block wire budget"
		return
	}
	wire, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(limit+1)))
	closeErr := resp.Body.Close()
	out.networkFinished = time.Now()
	out.NetworkFinishedAt = out.networkFinished.UTC()
	out.ObservedWireBytes = len(wire)
	if len(wire) > limit {
		out.WireTruncated = true
		wire = wire[:limit]
	}
	out.WireBytes = len(wire)
	out.WirePath = filepath.Join(rawDir, fmt.Sprintf("block-%d-%s-%03d.metrics", block, encoding, ordinal))
	out.wire = wire
	if readErr != nil {
		out.Error = readErr.Error()
		return
	}
	if closeErr != nil {
		out.Error = closeErr.Error()
		return
	}
	if out.WireTruncated {
		out.Error = "encoded metrics exceed 8MiB body/256MiB block budget; bounded raw prefix retained"
		return
	}
	if resp.StatusCode != http.StatusOK || resp.Uncompressed {
		out.Error = "metrics status/decompression contract failed"
		return
	}
	if age := resp.Header.Get("Age"); age != "" && age != "0" {
		out.Error = "cached metrics response"
		return
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" {
		out.Error = "unexpected metrics media type"
		return
	}
	contentEncoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if encoding == "gzip" {
		if contentEncoding != "gzip" {
			out.Error = "explicit gzip response missing gzip encoding"
			return
		}
	} else if contentEncoding != "" && contentEncoding != "identity" {
		out.Error = "identity response is compressed"
		return
	}
	if time.Since(start) >= permissionObserverTimeout {
		out.Error = "network response processing exceeds 250ms"
		return
	}
	out.NetworkCompleted = true
	return
}

// permissionObserverValidate verifies a complete acquired body after CPUAfter.
// Failed acquisition bytes still receive their raw hash for independent replay.
func permissionObserverValidate(out *permissionObserverResponse, encoding string) {
	start := time.Now()
	out.ValidationStartedAt = start.UTC()
	defer func() { out.ValidationFinishedAt = time.Now().UTC() }()
	out.WireSHA256 = fmt.Sprintf("%x", sha256.Sum256(out.wire))
	if !out.NetworkCompleted || out.Error != "" {
		return
	}
	logical := out.wire
	if encoding == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(out.wire))
		if err != nil {
			out.Error = err.Error()
			return
		}
		decoded, decodeErr := io.ReadAll(io.LimitReader(reader, permissionObserverMaxBody+1))
		gzipCloseErr := reader.Close()
		if decodeErr != nil {
			out.Error = decodeErr.Error()
			return
		}
		if gzipCloseErr != nil {
			out.Error = gzipCloseErr.Error()
			return
		}
		logical = decoded
	}
	out.LogicalBytes, out.LogicalSHA256 = len(logical), fmt.Sprintf("%x", sha256.Sum256(logical))
	if len(logical) > permissionObserverMaxBody {
		out.Error = "decoded metrics exceed 8MiB"
		return
	}
	var err error
	out.FamilyCount, out.MetricCount, out.SampleCount, err = permissionObserverParseFamilies(logical)
	if err != nil {
		out.Error = err.Error()
		return
	}
	if time.Since(start) >= permissionObserverTimeout {
		out.Error = "post-cut complete validation exceeds 250ms"
		return
	}
	out.Validated = true
}

func permissionObserverParseFamilies(body []byte) (int, int, int, error) {
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return 0, 0, 0, err
	}
	if len(families) == 0 {
		return 0, 0, 0, fmt.Errorf("no complete metrics families")
	}
	for _, name := range []string{"go_goroutines", "wukongim_message_permission_counts_total", "wukongim_message_permission_duration_seconds"} {
		if _, ok := families[name]; !ok {
			return 0, 0, 0, fmt.Errorf("required warm family %s missing", name)
		}
	}
	metricCount := 0
	for name, family := range families {
		if family.GetName() != name || family.Type == nil || len(family.Metric) == 0 {
			return 0, 0, 0, fmt.Errorf("incomplete family %s", name)
		}
		for _, metric := range family.Metric {
			if metric == nil {
				return 0, 0, 0, fmt.Errorf("nil metric in %s", name)
			}
			valid := false
			switch family.GetType() {
			case dto.MetricType_COUNTER:
				valid = metric.Counter != nil
			case dto.MetricType_GAUGE:
				valid = metric.Gauge != nil
			case dto.MetricType_SUMMARY:
				valid = metric.Summary != nil
			case dto.MetricType_UNTYPED:
				valid = metric.Untyped != nil
			case dto.MetricType_HISTOGRAM:
				valid = metric.Histogram != nil
			}
			if !valid {
				return 0, 0, 0, fmt.Errorf("metric/type mismatch in %s", name)
			}
		}
		metricCount += len(family.Metric)
	}
	sampleCount := 0
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] != '#' {
			sampleCount++
		}
	}
	return len(families), metricCount, sampleCount, nil
}
