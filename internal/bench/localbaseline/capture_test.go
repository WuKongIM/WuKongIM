package localbaseline

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestBuildStepEvidenceFromRawLifecycleAndProductQueueCuts(t *testing.T) {
	input := completeStepCaptureInput(t)

	evidence := BuildStepEvidence(input)
	result := EvaluateStep(evidence)

	if !result.Clean {
		t.Fatalf("built evidence = %+v, result = %+v", evidence, result)
	}
	if evidence.Traffic.SendACKs != uint64(input.OfferedSendQPS*input.ConfiguredMeasuredSeconds) {
		t.Fatalf("terminal traffic = %+v", evidence.Traffic)
	}
}

func TestBuildStepEvidenceFailsClosedAfterCaptureErrorOrWrongRun(t *testing.T) {
	t.Run("capture error", func(t *testing.T) {
		input := completeStepCaptureInput(t)
		input.Lifecycle = append(input.Lifecycle, LifecycleCapture{
			Schema: LifecycleCaptureSchema, SampledAt: input.PhaseWindows[1].StartedAt.Add(time.Second), Error: "worker_status_unavailable",
		})
		result := EvaluateStep(BuildStepEvidence(input))
		if result.Outcome != OutcomeInsufficientEvidence || !containsReason(result.Reasons, ReasonTimelineIncomplete) {
			t.Fatalf("result = %+v, want incomplete timeline", result)
		}
	})

	t.Run("wrong terminal run", func(t *testing.T) {
		input := completeStepCaptureInput(t)
		input.Lifecycle[len(input.Lifecycle)-1].Status.Assignment.RunID = "replacement-run"
		result := EvaluateStep(BuildStepEvidence(input))
		if result.Clean || !containsReason(result.Reasons, ReasonTrafficAccounting) {
			t.Fatalf("result = %+v, want missing terminal traffic", result)
		}
	})

	t.Run("assignment generation changed", func(t *testing.T) {
		input := completeStepCaptureInput(t)
		input.Lifecycle[len(input.Lifecycle)/2].Status.Assignment.AssignmentID = "replacement-assignment"
		result := EvaluateStep(BuildStepEvidence(input))
		if result.Outcome != OutcomeInsufficientEvidence || !containsReason(result.Reasons, ReasonTimelineIncomplete) {
			t.Fatalf("result = %+v, want incomplete timeline", result)
		}
	})

	t.Run("terminal process exited", func(t *testing.T) {
		input := completeStepCaptureInput(t)
		input.Lifecycle[len(input.Lifecycle)-1].Server.Alive = false
		result := EvaluateStep(BuildStepEvidence(input))
		if result.Outcome != OutcomeProductFailure || !containsReason(result.Reasons, ReasonServerProcessExit) {
			t.Fatalf("result = %+v, want terminal server exit", result)
		}
	})
}

func TestBuildProductQueueEvidenceRequiresEveryRawMetric(t *testing.T) {
	baseline := completeProductQueuePrometheus(4, testProductQueueCut("warmup", "run", time.Now().UTC()))
	terminal := completeProductQueuePrometheus(0, testProductQueueCut("stopped", "", time.Now().UTC()))
	evidence, err := BuildProductQueueEvidence(strings.NewReader(baseline), strings.NewReader(terminal))
	if err != nil {
		t.Fatalf("BuildProductQueueEvidence() error = %v", err)
	}
	if complete, converged := evaluateProductQueues(evidence); !complete || !converged {
		t.Fatalf("queue evidence = %+v, want complete/converged", evidence)
	}
	if complete, unchanged := evaluateProductResultCounters(evidence); !complete || !unchanged {
		t.Fatalf("result-counter evidence = %+v, want complete/unchanged", evidence)
	}

	missing := strings.Replace(terminal, "wukongim_storage_commit_queue_depth 0\n", "", 1)
	evidence, err = BuildProductQueueEvidence(strings.NewReader(baseline), strings.NewReader(missing))
	if err != nil {
		t.Fatalf("BuildProductQueueEvidence(missing) error = %v", err)
	}
	if complete, _ := evaluateProductQueues(evidence); complete {
		t.Fatalf("missing family was complete: %+v", evidence)
	}
}

func TestBuildProductQueueEvidenceRejectsOpenOrResetResultPartitions(t *testing.T) {
	cutAt := time.Now().UTC()
	baseline := strings.Replace(
		completeProductQueuePrometheus(0, testProductQueueCut("warmup", "run", cutAt.Add(-time.Minute))),
		"wukongim_delivery_recipient_worker_process_total{result=\"error\"} 0\n",
		"wukongim_delivery_recipient_worker_process_total{result=\"error\"} 2\n",
		1,
	)
	terminal := strings.Replace(
		completeProductQueuePrometheus(0, testProductQueueCut("run", "cooldown", cutAt)),
		"wukongim_delivery_recipient_worker_process_total{result=\"error\"} 0\n",
		"wukongim_delivery_recipient_worker_process_total{result=\"error\"} 1\n",
		1,
	)

	t.Run("counter reset", func(t *testing.T) {
		evidence, err := BuildProductQueueEvidence(strings.NewReader(baseline), strings.NewReader(terminal))
		if err != nil {
			t.Fatal(err)
		}
		if complete, _ := evaluateProductResultCounters(evidence); complete {
			t.Fatalf("reset counter evidence was complete: %+v", evidence)
		}
	})

	t.Run("unknown post-commit result", func(t *testing.T) {
		openPartition := terminal + "wukongim_channelappend_effect_total{stage=\"post_commit\",result=\"raw-identity\"} 1\n"
		evidence, err := BuildProductQueueEvidence(
			strings.NewReader(completeProductQueuePrometheus(0, testProductQueueCut("warmup", "run", cutAt.Add(-time.Minute)))),
			strings.NewReader(openPartition),
		)
		if err != nil {
			t.Fatal(err)
		}
		if complete, _ := evaluateProductResultCounters(evidence); complete {
			t.Fatalf("open result partition was complete: %+v", evidence)
		}
	})
}

func TestBuildProductQueueEvidenceRequiresOnlineDeliveryDrainMetrics(t *testing.T) {
	cutAt := time.Now().UTC()
	baseline := completeProductQueuePrometheus(4, testProductQueueCut("warmup", "run", cutAt.Add(-time.Minute)))
	terminal := completeProductQueuePrometheus(0, testProductQueueCut("run", "cooldown", cutAt))
	for _, metric := range []string{
		"wukongim_delivery_recipient_worker_queue_depth 0\n",
		"wukongim_delivery_recipient_worker_inflight 0\n",
		"wukongim_delivery_ack_bindings 0\n",
	} {
		t.Run(strings.Fields(metric)[0], func(t *testing.T) {
			missing := strings.Replace(terminal, metric, "", 1)
			evidence, err := BuildProductQueueEvidence(strings.NewReader(baseline), strings.NewReader(missing))
			if err != nil {
				t.Fatalf("BuildProductQueueEvidence() error = %v", err)
			}
			if complete, _ := evaluateProductQueues(evidence); complete {
				t.Fatalf("missing Online Delivery drain evidence was complete: %+v", evidence)
			}
		})
	}
}

func TestBuildProductQueueEvidenceRequiresTerminalFailureCounters(t *testing.T) {
	cutAt := time.Now().UTC()
	baseline := completeProductQueuePrometheus(0, testProductQueueCut("warmup", "run", cutAt.Add(-time.Minute)))
	terminal := completeProductQueuePrometheus(0, testProductQueueCut("run", "cooldown", cutAt))

	for _, metric := range []string{
		"wukongim_delivery_recipient_worker_process_total{result=\"error\"} 0\n",
		"wukongim_channelappend_effect_total{stage=\"post_commit\",result=\"commit_failed\"} 0\n",
	} {
		t.Run(strings.Fields(metric)[0], func(t *testing.T) {
			missing := strings.Replace(terminal, metric, "", 1)
			evidence, err := BuildProductQueueEvidence(strings.NewReader(baseline), strings.NewReader(missing))
			if err != nil {
				t.Fatalf("BuildProductQueueEvidence() error = %v", err)
			}
			if complete, _ := evaluateProductResultCounters(evidence); complete {
				t.Fatalf("missing terminal failure family was complete: %+v", evidence)
			}
		})
	}
}

func completeStepCaptureInput(t *testing.T) StepCaptureInput {
	t.Helper()
	step := completeStepEvidence(1000)
	queues, err := BuildProductQueueEvidence(
		strings.NewReader(completeProductQueuePrometheus(0, ProductQueueCut{
			Schema: ProductQueueCutSchema, ObservedAt: step.Timeline.Measured.StartedAt.Add(time.Second),
			RunID: "run-1", AssignmentID: "assignment-1", Phase: "warmup", ActivePhase: "run",
			ReceiveDrainSHA256: model.ReceiveDrainFingerprint(step.Timeline.Measured.Samples[0].ReceiveDrain),
		})),
		strings.NewReader(completeProductQueuePrometheus(0, ProductQueueCut{
			Schema: ProductQueueCutSchema, ObservedAt: step.Timeline.Terminal.TerminalCut.ObservedAt,
			RunID: "run-1", AssignmentID: "assignment-1", Phase: "run", ActivePhase: "cooldown",
			ReceiveDrainSHA256: model.ReceiveDrainFingerprint(step.Timeline.Terminal.ReceiveDrain),
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	step.Timeline.Terminal.TerminalCut.ProductMetricsSHA256 = queues.TerminalPayloadSHA256
	step.Timeline.Terminal.TerminalCut.ReceiveDrainSHA256 = queues.TerminalCut.ReceiveDrainSHA256
	windows := []PhaseWindow{
		{Phase: "warmup", StartedAt: step.Timeline.Warmup.StartedAt, EndedAt: step.Timeline.Warmup.EndedAt},
		{Phase: "run", StartedAt: step.Timeline.Measured.StartedAt, EndedAt: step.Timeline.Measured.EndedAt},
		{Phase: "cooldown", StartedAt: step.Timeline.Drain.StartedAt, EndedAt: step.Timeline.Drain.EndedAt},
	}
	captures := make([]LifecycleCapture, 0)
	appendPhase := func(name string, phase PhaseEvidence) {
		for _, sample := range phase.Samples {
			captures = append(captures, LifecycleCapture{
				Schema: LifecycleCaptureSchema, SampledAt: sample.ObservedAt,
				Status: &CapturedStatus{
					Phase: name, ActivePhase: name, ObservedAt: sample.ObservedAt,
					Lifecycle: &CapturedLifecycleStatus{
						ActiveConnections:   sample.ActiveConnections,
						TerminalCutRequired: sample.TerminalCutRequired,
						TerminalCutReady:    sample.TerminalCutReady,
						TerminalCut:         cloneTerminalCutBinding(sample.TerminalCut),
						Traffic:             sample.Traffic,
						ReceiveDrain:        sample.ReceiveDrain,
						ReceiveDrainSHA256:  model.ReceiveDrainFingerprint(sample.ReceiveDrain),
					},
					Assignment: CapturedAssignment{RunID: "run-1", AssignmentID: "assignment-1"},
				},
				Server: sample.Server, Worker: sample.Worker,
			})
		}
	}
	appendPhase("warmup", step.Timeline.Warmup)
	appendPhase("run", step.Timeline.Measured)
	appendPhase("cooldown", step.Timeline.Drain)
	terminalAt := step.Timeline.Drain.EndedAt.Add(time.Second)
	captures = append(captures, LifecycleCapture{
		Schema: LifecycleCaptureSchema, SampledAt: terminalAt,
		Status: &CapturedStatus{
			Phase: "stopped", ObservedAt: terminalAt,
			Lifecycle: &CapturedLifecycleStatus{
				ActiveConnections:   2500,
				TerminalPreClose:    true,
				TerminalCutRequired: true,
				TerminalCutReady:    true,
				TerminalCut:         cloneTerminalCutBinding(step.Timeline.Terminal.TerminalCut),
				Traffic:             step.Traffic,
				ReceiveDrain:        step.Timeline.Terminal.ReceiveDrain,
				ReceiveDrainSHA256:  model.ReceiveDrainFingerprint(step.Timeline.Terminal.ReceiveDrain),
			},
			Assignment: CapturedAssignment{RunID: "run-1", AssignmentID: "assignment-1"},
		},
		Server: step.Timeline.Terminal.Server,
		Worker: step.Timeline.Terminal.Worker,
	})
	return StepCaptureInput{
		RunID: "run-1", OfferedSendQPS: 1000, RequiredActiveConnections: 2500,
		ConfiguredGroupMembers:  10,
		ConfiguredWarmupSeconds: 60, ConfiguredMeasuredSeconds: 300,
		ConfiguredDrainBudgetSeconds: 90, MaximumSampleGapSeconds: 30,
		Target:        step.Target,
		ExecutionSeal: step.ExecutionSeal,
		PhaseWindows:  windows, Lifecycle: captures, ProductQueues: queues, StorageOverlap: step.StorageOverlap,
		StorageMetrics: step.StorageMetrics, HostIO: step.HostIO, Profile: step.Profile,
		Seal: SealEvidence{PayloadComplete: true, ChecksumsVerified: true},
	}
}

func completeProductQueuePrometheus(depth int, cut ProductQueueCut) string {
	metadata, _ := json.Marshal(cut)
	return productQueueCutPrefix + string(metadata) + "\n" + `wukongim_gateway_async_send_queue_depth ` + integerString(depth) + `
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} ` + integerString(depth) + `
wukongim_channelv2_worker_queue_depth{pool="store_append"} ` + integerString(depth) + `
wukongim_runtime_pool_queue_depth{component="channel",pool="append"} ` + integerString(depth) + `
wukongim_channelappend_writer_state_items{kind="pending_append"} ` + integerString(depth) + `
wukongim_channelappend_writer_state_items{kind="append_inflight"} ` + integerString(depth) + `
wukongim_channelappend_writer_state_items{kind="post_commit_backlog"} ` + integerString(depth) + `
wukongim_channelappend_post_commit_handoff_depth ` + integerString(depth) + `
wukongim_channelappend_post_commit_retry_queue_depth ` + integerString(depth) + `
wukongim_channelappend_effect_pool_inflight{stage="append"} ` + integerString(depth) + `
wukongim_storage_commit_queue_depth ` + integerString(depth) + `
wukongim_delivery_recipient_worker_queue_depth ` + integerString(depth) + `
wukongim_delivery_recipient_worker_inflight ` + integerString(depth) + `
wukongim_delivery_ack_bindings ` + integerString(depth) + "\n" + completeProductResultCounterPrometheus()
}

func completeProductResultCounterPrometheus() string {
	var builder strings.Builder
	for _, result := range testDeliveryPlanResults {
		builder.WriteString(`wukongim_delivery_recipient_worker_process_total{result="`)
		builder.WriteString(result)
		builder.WriteString(`"} 0` + "\n")
	}
	for _, result := range testChannelAppendPostCommitResults {
		builder.WriteString(`wukongim_channelappend_effect_total{stage="post_commit",result="`)
		builder.WriteString(result)
		builder.WriteString(`"} 0` + "\n")
	}
	return builder.String()
}

func completeProductResultCounterBoundaries() []ProductResultCounterBoundary {
	counters := make([]ProductResultCounterBoundary, 0, len(testDeliveryPlanResults)+len(testChannelAppendPostCommitResults))
	for _, result := range testDeliveryPlanResults {
		counters = append(counters, ProductResultCounterBoundary{
			Name: ProductResultDeliveryPlan, Result: result, PostWarmupTotal: 3, TerminalTotal: 3,
		})
	}
	for _, result := range testChannelAppendPostCommitResults {
		counters = append(counters, ProductResultCounterBoundary{
			Name: ProductResultChannelAppendPostCommit, Result: result, PostWarmupTotal: 2, TerminalTotal: 2,
		})
	}
	return counters
}

var testDeliveryPlanResults = []string{"ok", "panic", "timeout", "canceled", "error", "retry_exhausted", "unknown"}

var testChannelAppendPostCommitResults = []string{
	"ok", "mixed", "canceled", "timeout", "backpressured", "channel_busy", "route_not_ready",
	"stale_route", "stale_completion", "not_authority", "not_leader", "channel_not_found",
	"append_result_missing", "append_failed", "commit_failed", "invalid_subscribers", "invalid_cursor",
	"unsupported", "auth_fail", "invalid_request", "system_error", "other",
}

func testProductQueueCut(phase, activePhase string, observedAt time.Time) ProductQueueCut {
	return ProductQueueCut{
		Schema: ProductQueueCutSchema, ObservedAt: observedAt,
		RunID: "run-1", AssignmentID: "assignment-1", Phase: phase, ActivePhase: activePhase,
		ReceiveDrainSHA256: model.ReceiveDrainFingerprint(completeReceiveDrainEvidence(2500)),
	}
}

func integerString(value int) string {
	if value == 0 {
		return "0"
	}
	return "4"
}
