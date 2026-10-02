//go:build e2e

package send_ban

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
)

const (
	permissionFixedV2QueueCapacity = 8
	permissionFixedV2Expiry        = 400 * time.Millisecond
	permissionFixedV2CutBudget     = 20 * time.Millisecond
)

// permissionFixedV2Arrival retains every scheduled task. Negative phase offsets
// explicitly mean unavailable; a canceled or failed task never becomes zero cost.
type permissionFixedV2Arrival struct {
	Ordinal                int       `json:"ordinal"`
	Caller                 int       `json:"caller"`
	ScheduledOffsetNS      int64     `json:"scheduled_offset_ns"`
	OfferedOffsetNS        int64     `json:"offered_offset_ns"`
	WorkerStartedOffsetNS  int64     `json:"worker_started_offset_ns"`
	FrameSubmittedOffsetNS int64     `json:"frame_submitted_offset_ns"`
	CompletedOffsetNS      int64     `json:"completed_offset_ns"`
	ScheduleLatenessNS     int64     `json:"schedule_lateness_ns"`
	ScheduledToWorkerNS    int64     `json:"scheduled_to_worker_ns"`
	QueueWaitNS            int64     `json:"queue_wait_ns"`
	ScheduledToCompletedNS int64     `json:"scheduled_to_completed_ns"`
	ScheduledAt            time.Time `json:"scheduled_at"`
	OfferedAt              time.Time `json:"offered_at"`
	WorkerStartedAt        time.Time `json:"worker_started_at"`
	CompletedAt            time.Time `json:"completed_at"`
	Dropped                bool      `json:"dropped"`
	DropReason             string    `json:"drop_reason,omitempty"`
	Submitted              bool      `json:"submitted"`
	UnfinishedAtCutoff     bool      `json:"unfinished_at_cutoff"`
}

// permissionFixedV2Sample owns complete raw acquisition until CPUAfter. The
// fixed 256MiB bound covers retained payload lengths, not backing capacity/RSS.
type permissionFixedV2Sample struct {
	Ordinal                   int                        `json:"ordinal"`
	ScheduledOffsetNS         int64                      `json:"scheduled_offset_ns"`
	StartedOffsetNS           int64                      `json:"started_offset_ns"`
	NetworkFinishedOffsetNS   int64                      `json:"network_finished_offset_ns"`
	AnalysisStartedOffsetNS   int64                      `json:"analysis_started_offset_ns"`
	AnalysisFinishedOffsetNS  int64                      `json:"analysis_finished_offset_ns"`
	RetentionStartedOffsetNS  int64                      `json:"retention_started_offset_ns"`
	RetentionFinishedOffsetNS int64                      `json:"retention_finished_offset_ns"`
	Response                  permissionObserverResponse `json:"response"`
	Owned                     map[string]float64         `json:"owned"`
	Valid                     bool                       `json:"valid"`
	Error                     string                     `json:"error,omitempty"`
}

// permissionFixedV2Window adds bounded per-caller FIFO residence to the fixed
// arrival clock. It seals every submit before CPUAfter; ACK waits hold no gate.
func permissionFixedV2Window(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, ingressID uint64, clients []*suite.WKProtoClient, channel, name, prefix string, window map[string]any, cpuPIDs []int, cohorts bool, reportPath string) []permissionBaselineAck {
	t.Helper()
	concurrency := len(clients)
	require.True(t, concurrency == 1 || concurrency == 32)
	waves, count, interval := permissionFixedPlan(concurrency)
	acks := make([]permissionBaselineAck, count)
	arrivals := make([]permissionFixedV2Arrival, count)
	for index := range arrivals {
		acks[index].ID = fmt.Sprintf("%s-%03d", prefix, index)
		arrivals[index] = permissionFixedV2Arrival{Ordinal: index, Caller: index % concurrency, ScheduledOffsetNS: int64(index/concurrency) * int64(interval),
			OfferedOffsetNS: -1, WorkerStartedOffsetNS: -1, FrameSubmittedOffsetNS: -1, CompletedOffsetNS: -1, ScheduleLatenessNS: -1, ScheduledToWorkerNS: -1, QueueWaitNS: -1, ScheduledToCompletedNS: -1}
	}
	rawDir := filepath.Join(reportPath+".raw", name, "metrics")
	require.NoError(t, os.MkdirAll(rawDir, 0755))
	window["planned_messages"], window["planned_waves"] = count, waves
	window["arrival_interval_ns"], window["arrival_window_ms"] = int64(interval), permissionFixedArrivalWindow.Milliseconds()
	window["planned_offered_per_second"], window["wave_rounding"] = float64(count)/permissionFixedArrivalWindow.Seconds(), "floor(30s/64ms); no final partial wave"
	if concurrency == 1 {
		window["wave_rounding"] = "exact 30s*25/s"
	}
	window["caller_queue_capacity"], window["caller_active_limit"], window["scheduled_to_worker_limit_ns"] = permissionFixedV2QueueCapacity, 1, int64(permissionFixedV2Expiry)
	window["queue_capacity_scope"] = "eight queued-only ordinals plus one active per connection; nonblocking producer"
	window["late_threshold_ns"], window["planned_samples"], window["scrape_deadline_ms"] = int64(interval), permissionFixedScrapeCount, permissionFixedScrapeBudget.Milliseconds()
	window["cpu_cut_gap_limit_ns"], window["cpu_valid"], window["fixed_protocol_passed"] = int64(permissionFixedV2CutBudget), false, false
	window["arrival_schedule"], window["acks"] = &arrivals, &acks
	window["monotonic_clock_basis"] = "unmodified time.Now differences relative to this window start; UTC is separate evidence"
	window["raw_metrics_directory"], window["max_retained_raw_payload_bytes"] = rawDir, permissionObserverMaxBlockWire
	window["raw_payload_bound_scope"] = "sum(len(retained wire)); not backing-capacity, metadata or RSS bound"

	workerCtx, stopWorkers := context.WithCancel(ctx)
	sampleCtx, stopSamples := context.WithCancel(ctx)
	var mu sync.Mutex
	var sealed atomic.Bool
	var began, deadline, afterCallFinished time.Time
	var workers, ready sync.WaitGroup
	jobs := make([]chan int, concurrency)
	submit := make([]sync.Mutex, concurrency)
	active := make([]int, concurrency)
	queuePeaks := make([]int, concurrency)
	for caller := range active {
		active[caller] = -1
	}
	ready.Add(concurrency)
	for caller, client := range clients {
		jobs[caller] = make(chan int, permissionFixedV2QueueCapacity)
		workers.Add(1)
		go func(caller int, client *suite.WKProtoClient) {
			defer workers.Done()
			ready.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case index := <-jobs[caller]:
					submit[caller].Lock()
					started := time.Now()
					mu.Lock()
					arrival := &arrivals[index]
					if sealed.Load() || workerCtx.Err() != nil || !started.Before(deadline) {
						arrival.Dropped, arrival.DropReason = true, "window_sealed_before_submit"
						arrival.UnfinishedAtCutoff = true
						acks[index].Error = arrival.DropReason
						mu.Unlock()
						submit[caller].Unlock()
						continue
					}
					arrival.WorkerStartedAt, arrival.WorkerStartedOffsetNS = started.UTC(), started.Sub(began).Nanoseconds()
					arrival.ScheduledToWorkerNS = arrival.WorkerStartedOffsetNS - arrival.ScheduledOffsetNS
					arrival.QueueWaitNS = arrival.WorkerStartedOffsetNS - arrival.OfferedOffsetNS
					if arrival.ScheduledToWorkerNS < 0 || arrival.ScheduledToWorkerNS >= int64(permissionFixedV2Expiry) || arrival.QueueWaitNS < 0 {
						arrival.Dropped, arrival.DropReason = true, "scheduled_to_worker_expired"
						acks[index].Error = arrival.DropReason
						mu.Unlock()
						submit[caller].Unlock()
						continue
					}
					active[caller] = index
					id := acks[index].ID
					mu.Unlock()
					// The seal and deadline checks and actual submission share this
					// caller's gate; a queued task cannot submit beyond the barrier.
					// Recheck immediately at the call boundary; a delayed SendFrame
					// return still fails its separately recorded submission bound.
					if sealed.Load() || workerCtx.Err() != nil || !time.Now().Before(deadline) {
						mu.Lock()
						arrival.Dropped, arrival.DropReason, arrival.UnfinishedAtCutoff = true, "window_sealed_before_submit", true
						acks[index].Error = arrival.DropReason
						active[caller] = -1
						mu.Unlock()
						submit[caller].Unlock()
						continue
					}
					err := client.SendFrame(&frame.SendPacket{ChannelID: channel, ChannelType: 2, ClientMsgNo: id, ClientSeq: uint64(index + 2), Payload: []byte("permission-baseline-fixed-payload")})
					submitted := time.Now()
					mu.Lock()
					arrival.FrameSubmittedOffsetNS, arrival.Submitted = submitted.Sub(began).Nanoseconds(), err == nil
					mu.Unlock()
					submit[caller].Unlock()
					ack := permissionBaselineAck{ID: id}
					if err == nil {
						packet, _, readErr := client.ReadSendAckWithTiming()
						err = readErr
						if packet != nil {
							ack.Reason, ack.MessageID, ack.Seq = uint8(packet.ReasonCode), packet.MessageID, packet.MessageSeq
							if packet.ClientMsgNo != id {
								err = fmt.Errorf("unexpected ACK identity %q", packet.ClientMsgNo)
							}
						}
					}
					completed := time.Now()
					ack.Micros = completed.Sub(started).Microseconds()
					if err != nil {
						ack.Error = err.Error()
					}
					mu.Lock()
					acks[index] = ack
					arrival.CompletedAt, arrival.CompletedOffsetNS = completed.UTC(), completed.Sub(began).Nanoseconds()
					arrival.ScheduledToCompletedNS = arrival.CompletedOffsetNS - arrival.ScheduledOffsetNS
					if arrival.CompletedOffsetNS > int64(permissionFixedCPUWindow) || arrival.FrameSubmittedOffsetNS >= int64(permissionFixedCPUWindow) {
						arrival.UnfinishedAtCutoff = true
					}
					active[caller] = -1
					mu.Unlock()
				}
			}
		}(caller, client)
	}
	ready.Wait()
	sampleStart := make(chan time.Time, 1)
	samplesDone := permissionFixedV2AcquireSamples(sampleCtx, cluster.MustNode(ingressID).APIAddr(), sampleStart, rawDir, concurrency)
	var samples []permissionFixedV2Sample
	joined, fenced := false, false
	seal := func() {
		if fenced {
			return
		}
		sealed.Store(true)
		stopWorkers()
		for caller := range submit {
			submit[caller].Lock()
			submit[caller].Unlock()
		}
		fenced = true
	}
	join := func() {
		if joined {
			return
		}
		seal()
		stopSamples()
		var closed []int
		mu.Lock()
		for caller, index := range active {
			if index >= 0 {
				closed = append(closed, caller)
			}
		}
		mu.Unlock()
		for _, caller := range closed {
			_ = clients[caller].Close()
		}
		workers.Wait()
		mu.Lock()
		for index := range arrivals {
			arrival := &arrivals[index]
			if !arrival.Dropped && arrival.CompletedOffsetNS < 0 {
				arrival.Dropped, arrival.DropReason, arrival.UnfinishedAtCutoff = true, "queued_canceled_at_cutoff", true
				acks[index].Error = arrival.DropReason
			}
		}
		mu.Unlock()
		samples = <-samplesDone
		permissionFixedV2AnalyzeSamples(samples, began, afterCallFinished, cohorts)
		window["cohort_ownership_samples"], window["closed_for_join_callers"] = samples, closed
		joined = true
	}
	defer join()

	beforeCallStarted := time.Now()
	cpuBefore := permissionCPUQuery(t, ctx, cpuPIDs)
	beforeCallFinished := time.Now()
	window["cpu_before"] = cpuBefore
	began = time.Now()
	deadline = began.Add(permissionFixedCPUWindow)
	windowCtx, stopWindow := context.WithDeadline(ctx, deadline)
	defer stopWindow()
	window["started_at"], window["planned_finished_at"] = began.UTC(), deadline.UTC()
	window["cpu_before_call_started_offset_ns"], window["cpu_before_call_finished_offset_ns"] = beforeCallStarted.Sub(began).Nanoseconds(), beforeCallFinished.Sub(began).Nanoseconds()
	window["cpu_before_call_duration_ns"] = beforeCallFinished.Sub(beforeCallStarted).Nanoseconds()
	for index := range arrivals {
		arrivals[index].ScheduledAt = began.Add(time.Duration(arrivals[index].ScheduledOffsetNS)).UTC()
	}
	sampleStart <- began
	for wave := 0; wave < waves; wave++ {
		target := began.Add(time.Duration(wave) * interval)
		awake := permissionFixedWaitUntil(windowCtx, target)
		for caller := range clients {
			index := wave*concurrency + caller
			offered := time.Now()
			mu.Lock()
			arrival := &arrivals[index]
			if !awake || windowCtx.Err() != nil {
				arrival.Dropped, arrival.DropReason = true, "window_context_canceled"
			} else {
				arrival.OfferedAt, arrival.OfferedOffsetNS = offered.UTC(), offered.Sub(began).Nanoseconds()
				arrival.ScheduleLatenessNS = offered.Sub(target).Nanoseconds()
				if arrival.ScheduleLatenessNS < 0 || arrival.ScheduleLatenessNS >= int64(interval) {
					arrival.Dropped, arrival.DropReason = true, "scheduled_arrival_late"
				} else {
					select {
					case jobs[caller] <- index:
						queuePeaks[caller] = max(queuePeaks[caller], len(jobs[caller]))
					default:
						arrival.Dropped, arrival.DropReason = true, "fifo_full"
					}
				}
			}
			if arrival.Dropped {
				acks[index].Error = arrival.DropReason
			}
			mu.Unlock()
		}
	}
	window["last_offer_returned_offset_ns"] = time.Since(began).Nanoseconds()
	permissionFixedWaitUntil(windowCtx, deadline)
	cutoff := time.Now()
	seal()
	mu.Lock()
	for index := range arrivals {
		arrival := &arrivals[index]
		if !arrival.Dropped && (arrival.CompletedOffsetNS < 0 || arrival.CompletedOffsetNS > int64(permissionFixedCPUWindow)) {
			arrival.UnfinishedAtCutoff = true
		}
	}
	mu.Unlock()
	window["finished_at"], window["elapsed_ms"], window["cutoff_offset_ns"] = cutoff.UTC(), cutoff.Sub(began).Milliseconds(), cutoff.Sub(began).Nanoseconds()
	window["window_deadline_lateness_ns"] = cutoff.Sub(deadline).Nanoseconds()
	afterCallStarted := time.Now()
	cpuAfter := permissionCPUQuery(t, ctx, cpuPIDs)
	afterCallFinished = time.Now()
	window["cpu_after"] = cpuAfter
	window["cpu_after_call_started_offset_ns"], window["cpu_after_call_finished_offset_ns"] = afterCallStarted.Sub(began).Nanoseconds(), afterCallFinished.Sub(began).Nanoseconds()
	window["cpu_after_call_duration_ns"] = afterCallFinished.Sub(afterCallStarted).Nanoseconds()
	beforeValid := beforeCallFinished.Sub(beforeCallStarted) >= 0 && beforeCallFinished.Sub(beforeCallStarted) < permissionFixedV2CutBudget && began.Sub(beforeCallStarted) >= 0 && began.Sub(beforeCallStarted) < permissionFixedV2CutBudget && began.Sub(beforeCallFinished) >= 0 && began.Sub(beforeCallFinished) < permissionFixedV2CutBudget
	afterValid := afterCallFinished.Sub(afterCallStarted) >= 0 && afterCallFinished.Sub(afterCallStarted) < permissionFixedV2CutBudget && afterCallStarted.Sub(deadline) >= 0 && afterCallStarted.Sub(deadline) < permissionFixedV2CutBudget && afterCallFinished.Sub(deadline) >= 0 && afterCallFinished.Sub(deadline) < permissionFixedV2CutBudget
	nativeValid := false
	if cpuAfter.End >= cpuBefore.Begin && cpuAfter.Begin >= cpuBefore.End {
		span, inner := cpuAfter.End-cpuBefore.Begin, cpuAfter.Begin-cpuBefore.End
		window["native_cpu_span_ns"], window["native_cpu_inner_span_ns"] = span, inner
		nativeValid = inner >= uint64(permissionFixedCPUWindow) && span < uint64(permissionFixedCPUWindow+2*permissionFixedV2CutBudget) && inner <= span
	}
	window["cpu_before_bounds_valid"], window["cpu_after_bounds_valid"], window["native_cpu_span_valid"] = beforeValid, afterValid, nativeValid
	window["utc_elapsed_ns"], window["utc_monotonic_elapsed_drift_ns"] = cutoff.UTC().Sub(began.UTC()).Nanoseconds(), cutoff.UTC().Sub(began.UTC()).Nanoseconds()-cutoff.Sub(began).Nanoseconds()
	window["cpu_before_call_utc_duration_drift_ns"] = beforeCallFinished.UTC().Sub(beforeCallStarted.UTC()).Nanoseconds() - beforeCallFinished.Sub(beforeCallStarted).Nanoseconds()
	window["cpu_after_call_utc_duration_drift_ns"] = afterCallFinished.UTC().Sub(afterCallStarted.UTC()).Nanoseconds() - afterCallFinished.Sub(afterCallStarted).Nanoseconds()
	window["cluster_cpu_ns"] = permissionCPUInterval(t, cpuBefore, cpuAfter)
	window["cpu_valid"] = beforeValid && afterValid && nativeValid
	join()

	late, dropped, unfinished := 0, 0, 0
	queues, completions := make([]int64, 0, count), make([]int64, 0, count)
	completePopulation := true
	for index, arrival := range arrivals {
		if arrival.DropReason == "scheduled_arrival_late" {
			late++
		}
		if arrival.Dropped {
			dropped++
		}
		if arrival.UnfinishedAtCutoff {
			unfinished++
		}
		ack := acks[index]
		valid := !arrival.Dropped && !arrival.UnfinishedAtCutoff && arrival.OfferedOffsetNS >= 0 && arrival.WorkerStartedOffsetNS >= 0 && arrival.Submitted && arrival.FrameSubmittedOffsetNS < int64(permissionFixedCPUWindow) && arrival.CompletedOffsetNS >= 0 && arrival.CompletedOffsetNS <= int64(permissionFixedCPUWindow) && arrival.QueueWaitNS >= 0 && arrival.ScheduledToCompletedNS >= 0 && ack.Error == "" && ack.Reason == uint8(frame.ReasonSuccess) && ack.MessageID > 0 && ack.Seq > 0
		completePopulation = completePopulation && valid
		queues, completions = append(queues, arrival.QueueWaitNS), append(completions, arrival.ScheduledToCompletedNS)
	}
	window["late_arrivals"], window["dropped_arrivals"], window["unfinished_arrivals"] = late, dropped, unfinished
	window["observed_caller_queue_peak_lengths"] = queuePeaks
	window["queue_wait_population"], window["scheduled_to_completed_population"], window["full_latency_population_valid"] = count, count, completePopulation
	window["queue_wait_p99_ns"], window["scheduled_to_completed_p99_ns"] = nil, nil
	if completePopulation {
		sort.Slice(queues, func(i, j int) bool { return queues[i] < queues[j] })
		sort.Slice(completions, func(i, j int) bool { return completions[i] < completions[j] })
		p99 := int(math.Ceil(float64(count)*.99)) - 1
		window["queue_wait_p99_ns"], window["scheduled_to_completed_p99_ns"] = queues[p99], completions[p99]
	}
	samplesValid := len(samples) == permissionFixedScrapeCount
	for _, sample := range samples {
		samplesValid = samplesValid && sample.Valid
	}
	passed := ctx.Err() == nil && completePopulation && samplesValid && window["cpu_valid"] == true && cutoff.Sub(deadline) >= 0 && cutoff.Sub(deadline) < permissionFixedV2CutBudget
	window["fixed_protocol_passed"] = passed
	if !passed {
		t.Errorf("fixed-load v2 failed: late=%d dropped=%d unfinished=%d full_population=%t samples=%t cpu_valid=%v; all ordinals/raw retained", late, dropped, unfinished, completePopulation, samplesValid, window["cpu_valid"])
	}
	closed, _ := window["closed_for_join_callers"].([]int)
	window["control_phase_reconnected_callers"], window["reconnected_callers"] = closed, closed
	for _, caller := range closed {
		_, err := clients[caller].ConnectContext(ctx, cluster.MustNode(ingressID).GatewayAddr(), "____system", fmt.Sprintf("baseline-%s-%02d", name, caller))
		if err != nil {
			t.Errorf("post-cut control reconnect caller %d: %v", caller, err)
		}
	}
	return acks
}

// permissionFixedV2AcquireSamples preserves 32 planned slots and full raw entity
// reads only. Validation, ownership extraction and disk writes happen post-cut.
func permissionFixedV2AcquireSamples(ctx context.Context, addr string, start <-chan time.Time, rawDir string, block int) <-chan []permissionFixedV2Sample {
	done := make(chan []permissionFixedV2Sample, 1)
	go func() {
		out := make([]permissionFixedV2Sample, permissionFixedScrapeCount)
		for index := range out {
			out[index] = permissionFixedV2Sample{Ordinal: index, ScheduledOffsetNS: int64(time.Duration(index)*time.Second + 500*time.Millisecond), StartedOffsetNS: -1, NetworkFinishedOffsetNS: -1,
				AnalysisStartedOffsetNS: -1, AnalysisFinishedOffsetNS: -1, RetentionStartedOffsetNS: -1, RetentionFinishedOffsetNS: -1}
		}
		var began time.Time
		select {
		case began = <-start:
		case <-ctx.Done():
			for index := range out {
				out[index].Error = "observation_context_canceled_before_start"
			}
			done <- out
			return
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy, transport.DisableCompression, transport.DisableKeepAlives = nil, true, true
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: permissionObserverTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		retained := 0
		for index := range out {
			one := &out[index]
			target := began.Add(time.Duration(one.ScheduledOffsetNS))
			if !permissionFixedWaitUntil(ctx, target) {
				one.Error = "observation_context_canceled"
				continue
			}
			started := time.Now()
			one.StartedOffsetNS = started.Sub(began).Nanoseconds()
			queryCtx, cancel := context.WithDeadline(ctx, target.Add(permissionFixedScrapeBudget))
			one.Response = permissionObserverFetch(queryCtx, client, addr, "identity", rawDir, block, index, target, permissionObserverMaxBlockWire-retained)
			cancel()
			retained += one.Response.WireBytes
			if !one.Response.networkFinished.IsZero() {
				one.NetworkFinishedOffsetNS = one.Response.networkFinished.Sub(began).Nanoseconds()
			}
			if one.Response.Error != "" {
				one.Error = one.Response.Error
			} else if one.StartedOffsetNS < one.ScheduledOffsetNS || one.StartedOffsetNS-one.ScheduledOffsetNS >= int64(permissionFixedV2CutBudget) || one.NetworkFinishedOffsetNS < one.StartedOffsetNS || one.NetworkFinishedOffsetNS > one.ScheduledOffsetNS+int64(permissionFixedScrapeBudget) {
				one.Error = "observation_monotonic_slot_bound_failed"
			}
		}
		done <- out
	}()
	return done
}

// permissionFixedV2AnalyzeSamples keeps every body, validates all families, then
// persists without overwrite. Monotonic phase bounds prove post-cut ordering.
func permissionFixedV2AnalyzeSamples(samples []permissionFixedV2Sample, began, afterCallFinished time.Time, cohorts bool) {
	for index := range samples {
		one := &samples[index]
		response := &one.Response
		if began.IsZero() || response.wire == nil || response.WirePath == "" {
			if one.Error == "" {
				one.Error = "raw_observation_unavailable"
			}
			continue
		}
		one.AnalysisStartedOffsetNS = time.Since(began).Nanoseconds()
		permissionObserverValidate(response, "identity")
		one.Owned = map[string]float64{}
		if response.Validated {
			parser := expfmt.TextParser{}
			families, err := parser.TextToMetricFamilies(bytes.NewReader(response.wire))
			if err != nil {
				one.Error = "post-cut full ownership parse: " + err.Error()
			} else if family := families["wukongim_message_permission_cohort_owned"]; family != nil {
				for _, metric := range family.Metric {
					kind := ""
					for _, label := range metric.Label {
						if label.GetName() == "kind" {
							kind = label.GetValue()
						}
					}
					_, duplicate := one.Owned[kind]
					if kind == "" || metric.Gauge == nil || duplicate {
						one.Error = "invalid or duplicate ownership gauge"
					} else {
						one.Owned[kind] = metric.Gauge.GetValue()
					}
				}
			}
		}
		for kind, limit := range map[string]float64{"calls": 1024, "cohorts": 64, "budget_bytes": 16 << 20} {
			value, present := one.Owned[kind]
			if (cohorts && !present) || (present && (math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > limit)) {
				one.Error = "cohort ownership unavailable or outside declared bounds: " + kind
			}
		}
		one.AnalysisFinishedOffsetNS = time.Since(began).Nanoseconds()
		one.RetentionStartedOffsetNS = time.Since(began).Nanoseconds()
		response.WireRetentionStartedAt = time.Now().UTC()
		file, err := os.OpenFile(response.WirePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			written, writeErr := file.Write(response.wire)
			err = writeErr
			if err == nil && written != len(response.wire) {
				err = io.ErrShortWrite
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		response.WireSavedAt = time.Now().UTC()
		one.RetentionFinishedOffsetNS = time.Since(began).Nanoseconds()
		response.wire = nil
		if err != nil {
			one.Error = "raw response retention: " + err.Error()
		} else {
			response.WireRetained = true
		}
		response.Completed = response.NetworkCompleted && response.Validated && response.WireRetained && response.Error == ""
		if response.Error != "" {
			one.Error = response.Error
		}
		phaseValid := !afterCallFinished.IsZero() && one.AnalysisStartedOffsetNS >= afterCallFinished.Sub(began).Nanoseconds() && one.AnalysisFinishedOffsetNS >= one.AnalysisStartedOffsetNS && one.RetentionStartedOffsetNS >= one.AnalysisFinishedOffsetNS && one.RetentionFinishedOffsetNS >= one.RetentionStartedOffsetNS
		if !phaseValid {
			if one.Error != "" {
				one.Error += "; "
			}
			one.Error += "post-cut monotonic phase order failed"
		}
		one.Valid = one.Error == "" && response.Completed && phaseValid
	}
}
