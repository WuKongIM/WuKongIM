//go:build e2e

package send_ban

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/assert"
)

const (
	permissionFixedArrivalWindow = 30 * time.Second
	permissionFixedCPUWindow     = 32 * time.Second
	permissionFixedScrapeBudget  = 250 * time.Millisecond
	permissionFixedScrapeCount   = 32
	permissionFixedHistoryCap    = 200
)

// permissionFixedArrival retains every predeclared ordinal, including drops
// and work whose result arrives after the fixed drain deadline.
type permissionFixedArrival struct {
	Ordinal            int       `json:"ordinal"`
	Caller             int       `json:"caller"`
	ScheduledOffsetNS  int64     `json:"scheduled_offset_ns"`
	ScheduledAt        time.Time `json:"scheduled_at"`
	OfferedAt          time.Time `json:"offered_at,omitempty"`
	WorkerStartedAt    time.Time `json:"worker_started_at,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
	ScheduleLatenessNS int64     `json:"schedule_lateness_ns"`
	WorkerLatenessNS   int64     `json:"worker_lateness_ns"`
	Dropped            bool      `json:"dropped"`
	DropReason         string    `json:"drop_reason,omitempty"`
	UnfinishedAtCutoff bool      `json:"unfinished_at_cutoff"`
}

func permissionFixedPlan(concurrency int) (waves, messages int, interval time.Duration) {
	if concurrency == 1 {
		return 750, 750, 40 * time.Millisecond
	}
	interval = 64 * time.Millisecond
	waves = int(permissionFixedArrivalWindow / interval)
	return waves, waves * concurrency, interval
}

func permissionFixedWaitUntil(ctx context.Context, target time.Time) bool {
	if delay := time.Until(target); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

// permissionFixedWorker owns only one outstanding request on its connection.
// A dispatch that reaches the worker after its slot expires never sends traffic.
func permissionFixedWorker(ctx context.Context, client *suite.WKProtoClient, channel string, jobs <-chan int, busy *atomic.Bool, began *time.Time, interval time.Duration, mu *sync.Mutex, arrivals []permissionFixedArrival, acks []permissionBaselineAck) {
	for {
		select {
		case <-ctx.Done():
			return
		case index := <-jobs:
			started := time.Now()
			mu.Lock()
			arrivals[index].WorkerStartedAt = started.UTC()
			arrivals[index].WorkerLatenessNS = max(int64(0), started.Sub(began.Add(time.Duration(arrivals[index].ScheduledOffsetNS))).Nanoseconds())
			late := arrivals[index].WorkerLatenessNS >= int64(interval)
			if late {
				arrivals[index].Dropped, arrivals[index].DropReason = true, "worker_arrival_late"
				acks[index].Error = "worker_arrival_late"
			}
			mu.Unlock()
			if !late {
				ack := permissionBaselineSend(ctx, client, channel, acks[index].ID, uint64(index+2))
				finished := time.Now()
				mu.Lock()
				acks[index] = ack
				arrivals[index].CompletedAt = finished.UTC()
				mu.Unlock()
			}
			busy.Store(false)
		}
	}
}

// permissionFixedWindow owns a fixed clock, one unqueued worker per connection,
// and exactly the declared observation slots. It never retries or grows a window
// to obtain successful completions. CPU evidence includes all observer work.
func permissionFixedWindow(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, ingressID uint64, clients []*suite.WKProtoClient, channel, name, prefix string, window map[string]any, cpuPIDs []int, cohorts bool) []permissionBaselineAck {
	t.Helper()
	concurrency := len(clients)
	waves, count, interval := permissionFixedPlan(concurrency)
	acks := make([]permissionBaselineAck, count)
	arrivals := make([]permissionFixedArrival, count)
	for index := range arrivals {
		acks[index].ID = fmt.Sprintf("%s-%03d", prefix, index)
		arrivals[index] = permissionFixedArrival{Ordinal: index, Caller: index % concurrency, ScheduledOffsetNS: int64(index/concurrency) * int64(interval)}
	}
	window["planned_messages"], window["planned_waves"] = count, waves
	window["arrival_interval_ns"], window["arrival_window_ms"] = int64(interval), permissionFixedArrivalWindow.Milliseconds()
	window["planned_offered_per_second"] = float64(count) / permissionFixedArrivalWindow.Seconds()
	window["wave_rounding"] = "floor(30s/64ms); no final partial wave"
	if concurrency == 1 {
		window["wave_rounding"] = "exact 30s*25/s"
	}
	window["late_threshold_ns"] = int64(interval)
	window["planned_samples"], window["scrape_deadline_ms"] = permissionFixedScrapeCount, permissionFixedScrapeBudget.Milliseconds()
	window["arrival_schedule"] = &arrivals
	window["acks"] = &acks

	workerCtx, stopWorkers := context.WithCancel(ctx)
	var mu sync.Mutex
	var workers sync.WaitGroup
	var ready sync.WaitGroup
	jobs := make([]chan int, concurrency)
	busy := make([]atomic.Bool, concurrency)
	var began time.Time
	ready.Add(concurrency)
	for caller, client := range clients {
		jobs[caller] = make(chan int)
		workers.Add(1)
		go func(caller int, client *suite.WKProtoClient) {
			defer workers.Done()
			ready.Done()
			permissionFixedWorker(workerCtx, client, channel, jobs[caller], &busy[caller], &began, interval, &mu, arrivals, acks)
		}(caller, client)
	}
	ready.Wait()
	sampleCtx, stopSamples := context.WithCancel(ctx)
	sampleStart := make(chan time.Time, 1)
	samplesDone := permissionFixedSamples(sampleCtx, cluster.MustNode(ingressID).APIAddr(), sampleStart, cohorts)
	joined := false
	join := func() {
		if joined {
			return
		}
		stopWorkers()
		stopSamples()
		for caller := range clients {
			if busy[caller].Load() {
				_ = clients[caller].Close()
			}
		}
		workers.Wait()
		window["cohort_ownership_samples"] = <-samplesDone
		joined = true
	}
	defer join()
	cpuBefore := permissionCPUQuery(t, ctx, cpuPIDs)
	window["cpu_before"] = cpuBefore
	began = time.Now()
	deadline := began.Add(permissionFixedCPUWindow)
	windowCtx, stopWindow := context.WithDeadline(ctx, deadline)
	defer stopWindow()
	window["started_at"], window["planned_finished_at"] = began.UTC(), deadline.UTC()
	beforeGap := max(time.Duration(0), began.Sub(cpuBefore.QueryStartedAt))
	window["cpu_before_to_arrivals_ns"] = int64(beforeGap)
	sampleStart <- began
	late, dropped := 0, 0
	for wave := 0; wave < waves; wave++ {
		target := began.Add(time.Duration(wave) * interval)
		awake := permissionFixedWaitUntil(windowCtx, target)
		for caller := range clients {
			index := wave*concurrency + caller
			offered := time.Now()
			lateness := max(int64(0), offered.Sub(target).Nanoseconds())
			reason := ""
			if !awake {
				reason = "window_context_canceled"
			} else if lateness >= int64(interval) {
				reason = "scheduled_arrival_late"
				late++
			} else if !busy[caller].CompareAndSwap(false, true) {
				reason = "scheduled_client_busy"
			}
			mu.Lock()
			arrivals[index].OfferedAt, arrivals[index].ScheduleLatenessNS = offered.UTC(), lateness
			if reason != "" {
				arrivals[index].Dropped, arrivals[index].DropReason = true, reason
				acks[index].Error = reason
				dropped++
			}
			mu.Unlock()
			if reason == "" {
				select {
				case jobs[caller] <- index:
				case <-windowCtx.Done():
					busy[caller].Store(false)
					mu.Lock()
					arrivals[index].Dropped, arrivals[index].DropReason = true, "window_context_canceled"
					acks[index].Error = "window_context_canceled"
					dropped++
					mu.Unlock()
				}
			}
		}
	}
	window["last_offer_returned_at"] = time.Now().UTC()
	permissionFixedWaitUntil(windowCtx, deadline)
	cutoff := time.Now()
	unfinished := 0
	mu.Lock()
	for index := range arrivals {
		arrivals[index].ScheduledAt = began.Add(time.Duration(arrivals[index].ScheduledOffsetNS)).UTC()
		if !arrivals[index].Dropped && (arrivals[index].CompletedAt.IsZero() || arrivals[index].CompletedAt.After(deadline)) {
			arrivals[index].UnfinishedAtCutoff = true
			unfinished++
		}
	}
	mu.Unlock()
	window["finished_at"], window["elapsed_ms"] = cutoff.UTC(), cutoff.Sub(began).Milliseconds()
	window["window_deadline_lateness_ns"] = max(int64(0), cutoff.Sub(deadline).Nanoseconds())
	window["late_arrivals"], window["dropped_arrivals"], window["unfinished_arrivals"] = late, dropped, unfinished
	cpuAfter := permissionCPUQuery(t, ctx, cpuPIDs)
	window["cpu_after"] = cpuAfter
	window["cluster_cpu_ns"] = permissionCPUInterval(t, cpuBefore, cpuAfter)
	cpuDeadlineLateness := max(time.Duration(0), cpuAfter.QueryFinishedAt.Sub(deadline))
	window["cpu_cut_deadline_lateness_ns"] = int64(cpuDeadlineLateness)
	var reconnect []int
	for caller := range clients {
		if busy[caller].Load() {
			reconnect = append(reconnect, caller)
		}
	}
	join()
	for _, arrival := range arrivals {
		if arrival.DropReason == "worker_arrival_late" {
			late++
			dropped++
		}
	}
	window["late_arrivals"], window["dropped_arrivals"] = late, dropped
	protocolPassed := ctx.Err() == nil && late == 0 && dropped == 0 && unfinished == 0 && beforeGap < permissionFixedScrapeBudget && cutoff.Sub(deadline) < permissionFixedScrapeBudget && cpuDeadlineLateness < permissionFixedScrapeBudget
	samples := window["cohort_ownership_samples"].([]map[string]any)
	protocolPassed = protocolPassed && len(samples) == permissionFixedScrapeCount
	for _, sample := range samples {
		if sample["valid"] != true {
			protocolPassed = false
		}
	}
	for _, ack := range acks {
		if ack.Error != "" || ack.Reason != uint8(frame.ReasonSuccess) || ack.MessageID <= 0 || ack.Seq == 0 {
			protocolPassed = false
		}
	}
	window["fixed_protocol_passed"] = protocolPassed
	if !protocolPassed {
		t.Errorf("fixed permission protocol failed: late=%d dropped=%d unfinished=%d before_gap=%s cutoff_lateness=%s cpu_cut_lateness=%s; all receipts retained", late, dropped, unfinished, beforeGap, cutoff.Sub(deadline), cpuDeadlineLateness)
	}
	window["reconnected_callers"] = reconnect
	for _, caller := range reconnect {
		_, err := clients[caller].ConnectContext(ctx, cluster.MustNode(ingressID).GatewayAddr(), "____system", fmt.Sprintf("baseline-%s-%02d", name, caller))
		if err != nil {
			t.Errorf("reconnect owned caller %d for policy/history controls: %v", caller, err)
		}
	}
	return acks
}

// permissionFixedSamples records every planned observation slot. A missed or
// failed request remains explicit and never causes a retry or a catch-up scrape.
func permissionFixedSamples(ctx context.Context, addr string, start <-chan time.Time, cohorts bool) <-chan []map[string]any {
	done := make(chan []map[string]any, 1)
	go func() {
		var began time.Time
		select {
		case began = <-start:
		case <-ctx.Done():
			done <- nil
			return
		}
		out := make([]map[string]any, 0, permissionFixedScrapeCount)
		for index := 0; index < permissionFixedScrapeCount; index++ {
			offset := time.Duration(index)*time.Second + 500*time.Millisecond
			target := began.Add(offset)
			one := map[string]any{"ordinal": index, "scheduled_offset_ns": int64(offset), "scheduled_at": target.UTC(), "valid": false}
			out = append(out, one)
			if !permissionFixedWaitUntil(ctx, target) {
				one["error"] = "observation_context_canceled"
				continue
			}
			lateness := max(time.Duration(0), time.Since(target))
			one["lateness_ns"] = int64(lateness)
			if lateness >= permissionFixedScrapeBudget {
				one["error"] = "observation_slot_missed"
				continue
			}
			queryCtx, cancel := context.WithDeadline(ctx, target.Add(permissionFixedScrapeBudget))
			samples, receipt, err := suite.FetchMetricSamplesWithReceipt(queryCtx, addr)
			cancel()
			one["scrape"] = receipt
			one["at"] = receipt.FinishedAt
			if err != nil {
				one["error"] = err.Error()
				continue
			}
			owned := map[string]float64{}
			for _, sample := range samples {
				if sample.Name == "wukongim_message_permission_cohort_owned" {
					owned[sample.Labels["kind"]] = sample.Value
				}
			}
			one["owned"] = owned
			valid := receipt.StatusCode == 200 && receipt.RequestedEncoding == "identity" && receipt.ReceivedEncoding == "identity" && receipt.BodyBytes != nil && *receipt.BodyBytes > 0 && len(receipt.BodySHA256) == 64 && receipt.DurationNS <= int64(permissionFixedScrapeBudget) && !receipt.FinishedAt.After(target.Add(permissionFixedScrapeBudget))
			for key, limit := range map[string]float64{"calls": 1024, "cohorts": 64, "budget_bytes": 16 << 20} {
				value, present := owned[key]
				if (cohorts && !present) || (present && (value < 0 || value > limit)) {
					valid = false
				}
			}
			one["valid"] = valid
		}
		done <- out
	}()
	return done
}

// permissionFixedValidateWindow preserves functional failures without abandoning
// the subsequent ban/unban and exact-history controls of the diagnostic case.
func permissionFixedValidateWindow(t *testing.T, cohorts bool, concurrency int, name string, acks []permissionBaselineAck, counts map[string]float64, before, after permissionBaselineCut) bool {
	t.Helper()
	ok := true
	check := func(valid bool) { ok = valid && ok }
	_, count, _ := permissionFixedPlan(concurrency)
	check(assert.Len(t, acks, count))
	ids, seqs := map[int64]bool{}, map[uint64]bool{}
	invalidACKs, duplicateIDs, duplicateSeqs := 0, 0, 0
	for _, ack := range acks {
		if ack.Error == "" && ack.Reason == uint8(frame.ReasonSuccess) && ack.MessageID > 0 && ack.Seq > 0 {
			if ids[ack.MessageID] {
				duplicateIDs++
			}
			if seqs[ack.Seq] {
				duplicateSeqs++
			}
			ids[ack.MessageID], seqs[ack.Seq] = true, true
		} else {
			invalidACKs++
		}
	}
	check(assert.Zero(t, invalidACKs, "all raw ACK errors/reasons/identifiers retained in receipt"))
	check(assert.Zero(t, duplicateIDs, "duplicate measured ACK message IDs"))
	check(assert.Zero(t, duplicateSeqs, "duplicate measured ACK sequences"))
	groups, envelopes := float64(2*count), float64(count)
	if name == "same-slot-remote" {
		groups = float64(count)
	}
	if name == "two-remote-leaders" {
		envelopes = float64(2 * count)
	} else if name == "two-slots-local-leader" {
		envelopes = 0
	}
	check(assert.EqualValues(t, count, counts["messages"]))
	check(assert.EqualValues(t, count, counts["plans"]))
	check(assert.EqualValues(t, 2*count, counts["facts"]))
	if cohorts && concurrency == 32 {
		if envelopes > 0 {
			check(assert.Less(t, counts["node_envelopes"], envelopes, "independent callers must reduce actual RPC envelopes"))
		} else {
			check(assert.Zero(t, counts["node_envelopes"]))
			check(assert.Less(t, permissionBaselineDelta(before, after, "local_envelopes"), float64(count)))
		}
		check(assert.Positive(t, counts["barrier_ok"]))
		check(assert.Less(t, counts["barrier_ok"], groups, "independent callers must share sealed fresh reads"))
		check(assert.Equal(t, counts["slot_groups"], counts["barrier_ok"]))
	} else {
		check(assert.Equal(t, envelopes, counts["node_envelopes"]))
		check(assert.Equal(t, groups, counts["slot_groups"]))
		check(assert.Equal(t, groups, counts["barrier_ok"]))
	}
	check(assert.Zero(t, counts["barrier_failed"]))
	check(assert.Zero(t, counts["admission_busy"]))
	for _, node := range after {
		check(assert.Zero(t, node["inflight"]))
		if cohorts {
			for _, kind := range []string{"calls", "cohorts", "budget_bytes"} {
				value, present := node["cohort_owned_"+kind]
				check(assert.True(t, present, "candidate ownership metric missing"))
				check(assert.Zero(t, value, "cohort ownership must drain after joined ACKs"))
			}
		}
	}
	return ok
}
