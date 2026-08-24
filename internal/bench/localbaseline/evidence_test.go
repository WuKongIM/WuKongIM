package localbaseline

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestEvaluateSingleNodeClusterStepAcceptsCompleteEvidence(t *testing.T) {
	evidence := completeStepEvidence(1000)

	result := EvaluateStep(evidence)

	if result.Outcome != OutcomeClean {
		t.Fatalf("outcome = %q, want %q; reasons = %v", result.Outcome, OutcomeClean, result.Reasons)
	}
	if !result.Clean {
		t.Fatalf("clean = false; reasons = %v", result.Reasons)
	}
}

func TestEvaluateSingleNodeClusterStepAcceptsSubsecondWallClockSlewAtMeasuredDeadline(t *testing.T) {
	evidence := completeStepEvidence(1000)
	shortfall := reviewedCoordinatorBoundaryTolerance / 2
	evidence.Timeline.Measured.EndedAt = evidence.Timeline.Measured.EndedAt.Add(-shortfall)
	evidence.Timeline.Measured.Samples[len(evidence.Timeline.Measured.Samples)-1].ObservedAt = evidence.Timeline.Measured.EndedAt

	result := EvaluateStep(evidence)

	if !result.Clean || result.Outcome != OutcomeClean {
		t.Fatalf("result = %+v, want subsecond serialized-clock slew accepted around the worker-owned deadline", result)
	}
}

func TestEvaluateSingleNodeClusterStepRejectsMeasuredWindowShorterThanBoundaryTolerance(t *testing.T) {
	evidence := completeStepEvidence(1000)
	shortfall := reviewedCoordinatorBoundaryTolerance + time.Millisecond
	evidence.Timeline.Measured.EndedAt = evidence.Timeline.Measured.EndedAt.Add(-shortfall)
	evidence.Timeline.Measured.Samples[len(evidence.Timeline.Measured.Samples)-1].ObservedAt = evidence.Timeline.Measured.EndedAt

	result := EvaluateStep(evidence)

	if result.Clean || !containsReason(result.Reasons, ReasonTimelineIncomplete) {
		t.Fatalf("result = %+v, want a measured window below the bounded deadline tolerance rejected", result)
	}
}

func TestEvaluateSingleNodeClusterStepAllowsSuccessfulResultCounterGrowth(t *testing.T) {
	evidence := completeStepEvidence(1000)
	for index := range evidence.ProductQueues.ResultCounters {
		counter := &evidence.ProductQueues.ResultCounters[index]
		if counter.Result == "ok" {
			counter.TerminalTotal += 100
		}
	}

	result := EvaluateStep(evidence)
	if !result.Clean || result.Outcome != OutcomeClean {
		t.Fatalf("result = %+v, want clean successful counter growth", result)
	}
}

func TestEvaluateSingleNodeClusterStepRejectsUnrequiredReceiveDrain(t *testing.T) {
	evidence := completeStepEvidence(1000)
	notRequired := model.ReceiveDrainNotRequired()
	for _, phase := range []*PhaseEvidence{
		&evidence.Timeline.Warmup,
		&evidence.Timeline.Measured,
		&evidence.Timeline.Drain,
	} {
		for index := range phase.Samples {
			phase.Samples[index].ReceiveDrain = notRequired
		}
	}
	evidence.Timeline.Terminal.ReceiveDrain = notRequired

	result := EvaluateStep(evidence)

	if result.Clean || !containsReason(result.Reasons, ReasonReceiveDrain) {
		t.Fatalf("result = %+v, want required connection coverage to fail closed", result)
	}
}

func TestEvaluateSingleNodeClusterStepRequiresExactFanoutReceiveAndRecvACKAccounting(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*StepEvidence)
		wantOutcome Outcome
		wantReason  Reason
	}{
		{
			name: "all fanout silently missing",
			mutate: func(e *StepEvidence) {
				setFixtureTerminalFanoutCounts(e, 0, 0, false)
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonReceiveFanoutAccounting,
		},
		{
			name: "one recipient delivery missing",
			mutate: func(e *StepEvidence) {
				terminal := e.Timeline.Terminal.ReceiveDrain
				setFixtureTerminalFanoutCounts(e, terminal.ReceiveFramesObserved-1, terminal.RecvACKSuccesses-1, false)
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonReceiveFanoutAccounting,
		},
		{
			name: "duplicate recipient delivery",
			mutate: func(e *StepEvidence) {
				terminal := e.Timeline.Terminal.ReceiveDrain
				setFixtureTerminalFanoutCounts(e, terminal.ReceiveFramesObserved+1, terminal.RecvACKSuccesses+1, false)
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonReceiveFanoutAccounting,
		},
		{
			name: "recvack success missing",
			mutate: func(e *StepEvidence) {
				terminal := e.Timeline.Terminal.ReceiveDrain
				setFixtureTerminalFanoutCounts(e, terminal.ReceiveFramesObserved, terminal.RecvACKSuccesses-1, false)
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonReceiveFanoutAccounting,
		},
		{
			name: "one missing and another duplicated cannot cancel",
			mutate: func(e *StepEvidence) {
				terminal := &e.Timeline.Terminal.ReceiveDrain
				terminal.FanoutProof.Received.DigestA = strings.Repeat("c", 64)
				terminal.FanoutProof.Received.DigestB = strings.Repeat("d", 64)
				terminal.FanoutProof.RecvACKed = terminal.FanoutProof.Received
				rebindTerminalReceiveDrain(e)
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonReceiveFanoutAccounting,
		},
		{
			name: "warmup denominator missing",
			mutate: func(e *StepEvidence) {
				e.Traffic.WarmupSendACKs = 0
				for i := range e.Timeline.Measured.Samples {
					e.Timeline.Measured.Samples[i].Traffic.WarmupSendACKs = 0
				}
				for i := range e.Timeline.Drain.Samples {
					e.Timeline.Drain.Samples[i].Traffic.WarmupSendACKs = 0
				}
				e.Timeline.Terminal.Traffic.WarmupSendACKs = 0
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonReceiveFanoutEvidence,
		},
		{
			name: "logical sendack sum overflows",
			mutate: func(e *StepEvidence) {
				e.Traffic.WarmupSendACKs = ^uint64(0)
				for i := range e.Timeline.Measured.Samples {
					e.Timeline.Measured.Samples[i].Traffic.WarmupSendACKs = ^uint64(0)
				}
				for i := range e.Timeline.Drain.Samples {
					e.Timeline.Drain.Samples[i].Traffic.WarmupSendACKs = ^uint64(0)
				}
				e.Timeline.Terminal.Traffic.WarmupSendACKs = ^uint64(0)
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonReceiveFanoutEvidence,
		},
		{
			name: "fanout multiplication overflows",
			mutate: func(e *StepEvidence) {
				e.ConfiguredGroupMembers = int(^uint(0) >> 1)
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonReceiveFanoutEvidence,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			tt.mutate(&evidence)
			result := EvaluateStep(evidence)
			if result.Outcome != tt.wantOutcome || !containsReason(result.Reasons, tt.wantReason) {
				t.Fatalf("result = %+v, want outcome %q reason %q", result, tt.wantOutcome, tt.wantReason)
			}
		})
	}
}

func TestEvaluateSingleNodeClusterStepRequiresClosedProfileEvidence(t *testing.T) {
	evidence := completeStepEvidence(1000)
	evidence.Profile = ProfileEvidence{
		Schema: ProfileEvidenceSchema, Status: "partial", Reason: "phase_changed_during_capture",
		Triggered: true, Trigger: &ProfileThresholdTrigger{
			Kind:       ProfileTriggerActualOfferedRatio,
			PreviousAt: evidence.Timeline.Measured.StartedAt,
			CurrentAt:  evidence.Timeline.Measured.StartedAt.Add(time.Second),
		},
	}

	result := EvaluateStep(evidence)

	if result.Outcome != OutcomeInsufficientEvidence || !containsReason(result.Reasons, ReasonProfileEvidence) {
		t.Fatalf("result = %+v, want incomplete profile evidence", result)
	}
}

func TestEvaluateSingleNodeClusterStepTreatsCompleteProfileAsAdditiveEvidence(t *testing.T) {
	evidence := completeStepEvidence(1000)
	helperExit := 0
	evidence.Profile = ProfileEvidence{
		Schema: ProfileEvidenceSchema, Status: "complete", EvidenceComplete: true, CaptureValid: true,
		Reason: "ok", Triggered: true, Trigger: &ProfileThresholdTrigger{
			Kind:       ProfileTriggerActualOfferedRatio,
			PreviousAt: evidence.Timeline.Measured.StartedAt,
			CurrentAt:  evidence.Timeline.Measured.StartedAt.Add(time.Second),
		},
		Metadata: "threshold-pprof/metadata.json", HelperExitStatus: &helperExit,
	}
	evidence.Traffic.SendACKs = evidence.Traffic.Planned * 8 / 10
	evidence.Traffic.Remaining = evidence.Traffic.Planned - evidence.Traffic.SendACKs
	setStepTraffic(&evidence, evidence.Traffic)
	setFixtureTerminalFanout(&evidence)

	result := EvaluateStep(evidence)

	if result.Outcome != OutcomeRateFailed || !containsReason(result.Reasons, ReasonMeasuredThroughput) ||
		containsReason(result.Reasons, ReasonProfileEvidence) ||
		len(result.Observations) != 1 || result.Observations[0] != ObservationThresholdProfileCaptured {
		t.Fatalf("result = %+v, want original rate attribution", result)
	}
}

func TestEvaluateSingleNodeClusterStepReportsStorageOverlapWithoutChangingCleanVerdict(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*StepEvidence)
		wantObservation Observation
	}{
		{
			name: "compaction overlap",
			mutate: func(evidence *StepEvidence) {
				evidence.StorageOverlap.Samples[1].CompactionCount++
				for index := 2; index < len(evidence.StorageOverlap.Samples); index++ {
					evidence.StorageOverlap.Samples[index].CompactionCount++
				}
			},
			wantObservation: ObservationCompactionOverlap,
		},
		{
			name: "snapshot overlap",
			mutate: func(evidence *StepEvidence) {
				for index := 1; index < len(evidence.StorageOverlap.Samples); index++ {
					evidence.StorageOverlap.Samples[index].SnapshotIdentity = strings.Repeat("c", 64)
				}
			},
			wantObservation: ObservationSnapshotOverlap,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			test.mutate(&evidence)

			result := EvaluateStep(evidence)

			if !result.Clean || result.Outcome != OutcomeClean || len(result.Reasons) != 0 {
				t.Fatalf("result = %+v, want clean observational overlap", result)
			}
			if len(result.Observations) != 1 || result.Observations[0] != test.wantObservation {
				t.Fatalf("observations = %v, want [%s]", result.Observations, test.wantObservation)
			}
		})
	}
}

func TestEvaluateSingleNodeClusterStepFailsClosedAtEveryEvidenceBoundary(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*StepEvidence)
		wantOutcome Outcome
		wantReason  Reason
	}{
		{
			name: "warmup connection dip",
			mutate: func(e *StepEvidence) {
				e.Timeline.Warmup.Samples[1].ActiveConnections = 2499
			},
			wantOutcome: OutcomeRateFailed,
			wantReason:  ReasonActiveConnections,
		},
		{
			name: "measured connection dip",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples[3].ActiveConnections = 2499
			},
			wantOutcome: OutcomeRateFailed,
			wantReason:  ReasonActiveConnections,
		},
		{
			name: "periodic sample gap",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples = append(e.Timeline.Measured.Samples[:4], e.Timeline.Measured.Samples[5:]...)
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonSampleGap,
		},
		{
			name: "duplicate lifecycle timestamp",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples[2].ObservedAt = e.Timeline.Measured.Samples[1].ObservedAt
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonTimelineIncomplete,
		},
		{
			name: "periodic lifecycle counter regression",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples[3].Traffic.LogicalSent =
					e.Timeline.Measured.Samples[2].Traffic.LogicalSent - 1
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonTimelineIncomplete,
		},
		{
			name: "measured traffic projection missing",
			mutate: func(e *StepEvidence) {
				for index := range e.Timeline.Measured.Samples {
					e.Timeline.Measured.Samples[index].Traffic = TrafficEvidence{}
				}
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonTimelineIncomplete,
		},
		{
			name: "missing boundary coverage",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples = e.Timeline.Measured.Samples[2:]
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonSampleGap,
		},
		{
			name: "status observation failed",
			mutate: func(e *StepEvidence) {
				e.Timeline.CaptureComplete = false
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonTimelineIncomplete,
		},
		{
			name: "server pid reused",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples[2].Server.StartToken = "replacement-process"
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonServerProcessExit,
		},
		{
			name: "worker exited",
			mutate: func(e *StepEvidence) {
				e.Timeline.Drain.Samples[1].Worker.Alive = false
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonWorkerProcessExit,
		},
		{
			name: "terminal cut was not frozen before session close",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.TerminalPreClose = false
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonTimelineIncomplete,
		},
		{
			name: "receive drain evidence missing",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.ReceiveDrain = model.ReceiveDrainSnapshot{}
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonReceiveDrain,
		},
		{
			name: "receive drain still owns queued work",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.ReceiveDrain.AdapterQueueDepth = 1
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonReceiveDrain,
		},
		{
			name: "receive ack failed",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.ReceiveDrain.RecvACKFailures = 1
				rebindTerminalReceiveDrain(e)
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonReceiveDrainFailure,
		},
		{
			name: "receive observation counter regressed",
			mutate: func(e *StepEvidence) {
				e.Timeline.Measured.Samples[1].ReceiveDrain.ReceiveFramesObserved = 2
				e.Timeline.Measured.Samples[2].ReceiveDrain.ReceiveFramesObserved = 1
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonReceiveDrain,
		},
		{
			name: "terminal pre-close connections dropped",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.ActiveConnections = e.RequiredActiveConnections - 1
			},
			wantOutcome: OutcomeRateFailed,
			wantReason:  ReasonActiveConnections,
		},
		{
			name: "server exited before terminal capture",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.Server.Alive = false
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonServerProcessExit,
		},
		{
			name: "worker replaced before terminal capture",
			mutate: func(e *StepEvidence) {
				e.Timeline.Terminal.Worker.StartToken = "replacement-worker"
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonWorkerProcessExit,
		},
		{
			name: "dispatched mismatch",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.Dispatched-- })
			},
			wantOutcome: OutcomeRateFailed,
			wantReason:  ReasonTrafficAccounting,
		},
		{
			name: "remaining work",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.Remaining = 1 })
			},
			wantOutcome: OutcomeRateFailed,
			wantReason:  ReasonTrafficAccounting,
		},
		{
			name: "terminal send failure",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) {
					traffic.SendACKs--
					traffic.TerminalErrors = 1
				})
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonTerminalSendFailure,
		},
		{
			name: "correctness failure",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.CorrectnessErrors = 1 })
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonCorrectnessFailure,
		},
		{
			name: "attempt accounting mismatch",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.SendAttempts-- })
			},
			wantOutcome: OutcomeRateFailed,
			wantReason:  ReasonTrafficAccounting,
		},
		{
			name: "client message identity changed",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.StableClientMsgNo = false })
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonRetryEvidence,
		},
		{
			name: "incomplete retry evidence",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.RetryEvidenceComplete = false })
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonRetryEvidence,
		},
		{
			name: "unbounded retry policy",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.MaximumRetriesPerMessage = 0 })
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonRetryEvidence,
		},
		{
			name: "weaker retry policy",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) { traffic.MaximumRetriesPerMessage = 2 })
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonRetryEvidence,
		},
		{
			name: "retry exhaustion",
			mutate: func(e *StepEvidence) {
				mutateStepTraffic(e, func(traffic *TrafficEvidence) {
					traffic.SendACKs--
					traffic.TerminalErrors = 1
					traffic.RetryExhausted = 1
				})
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonRetryExhausted,
		},
		{
			name: "missing queue",
			mutate: func(e *StepEvidence) {
				e.ProductQueues.Queues = e.ProductQueues.Queues[1:]
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonQueueEvidence,
		},
		{
			name: "queue not converged",
			mutate: func(e *StepEvidence) {
				e.ProductQueues.Queues[0].TerminalDepth = 1
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonQueueConvergence,
		},
		{
			name: "missing product result counter",
			mutate: func(e *StepEvidence) {
				e.ProductQueues.ResultCounters = e.ProductQueues.ResultCounters[1:]
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonProductResultEvidence,
		},
		{
			name: "delivery failure after warmup",
			mutate: func(e *StepEvidence) {
				for index := range e.ProductQueues.ResultCounters {
					counter := &e.ProductQueues.ResultCounters[index]
					if counter.Name == ProductResultDeliveryPlan && counter.Result == "error" {
						counter.TerminalTotal++
						return
					}
				}
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonProductFailureDelta,
		},
		{
			name: "post-commit failure after warmup",
			mutate: func(e *StepEvidence) {
				for index := range e.ProductQueues.ResultCounters {
					counter := &e.ProductQueues.ResultCounters[index]
					if counter.Name == ProductResultChannelAppendPostCommit && counter.Result == "commit_failed" {
						counter.TerminalTotal++
						return
					}
				}
			},
			wantOutcome: OutcomeProductFailure,
			wantReason:  ReasonProductFailureDelta,
		},
		{
			name: "storage overlap capture missing",
			mutate: func(e *StepEvidence) {
				e.StorageOverlap.CaptureComplete = false
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonStorageOverlap,
		},
		{
			name: "storage overlap periodic gap",
			mutate: func(e *StepEvidence) {
				e.StorageOverlap.Samples = append(e.StorageOverlap.Samples[:2], e.StorageOverlap.Samples[3:]...)
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonStorageOverlap,
		},
		{
			name: "storage compaction counter reset",
			mutate: func(e *StepEvidence) {
				e.StorageOverlap.Samples[2].CompactionCount = 0
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonStorageOverlap,
		},
		{
			name: "storage inventory unverified",
			mutate: func(e *StepEvidence) {
				e.StorageOverlap.Samples[0].InventoryVerified = false
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonStorageOverlap,
		},
		{
			name: "storage terminal cut missing",
			mutate: func(e *StepEvidence) {
				e.StorageOverlap.Samples = e.StorageOverlap.Samples[:len(e.StorageOverlap.Samples)-1]
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonStorageOverlap,
		},
		{
			name: "unverified seal",
			mutate: func(e *StepEvidence) {
				e.Seal.ChecksumsVerified = false
			},
			wantOutcome: OutcomeInsufficientEvidence,
			wantReason:  ReasonArtifactSeal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := completeStepEvidence(1000)
			tt.mutate(&evidence)

			result := EvaluateStep(evidence)

			if result.Outcome != tt.wantOutcome {
				t.Fatalf("outcome = %q, want %q; reasons = %v", result.Outcome, tt.wantOutcome, result.Reasons)
			}
			if !containsReason(result.Reasons, tt.wantReason) {
				t.Fatalf("reasons = %v, want %q", result.Reasons, tt.wantReason)
			}
		})
	}
}

func TestEvaluateSingleNodeClusterStepUsesMeasuredNinetyPercentGate(t *testing.T) {
	evidence := completeStepEvidence(1000)
	setDeliveredLogicalSends(&evidence, 270_000)
	if result := EvaluateStep(evidence); !result.Clean || result.ActualOfferedRatio != 0.90 {
		t.Fatalf("90%% result = %+v, want clean", result)
	}

	setDeliveredLogicalSends(&evidence, 269_999)
	result := EvaluateStep(evidence)
	if result.Outcome != OutcomeRateFailed || !containsReason(result.Reasons, ReasonMeasuredThroughput) {
		t.Fatalf("below 90%% result = %+v, want rate failure", result)
	}
}

func setDeliveredLogicalSends(evidence *StepEvidence, count uint64) {
	mutateStepTraffic(evidence, func(traffic *TrafficEvidence) {
		traffic.Planned = count
		traffic.Dispatched = count
		traffic.LogicalSent = count
		traffic.SendAttempts = count
		traffic.SendACKs = count
		traffic.RetryAttempts = 0
	})
	setFixtureTerminalFanout(evidence)
}

func setFixtureTerminalFanout(evidence *StepEvidence) {
	if evidence == nil || evidence.ConfiguredGroupMembers <= 1 {
		return
	}
	expected := (evidence.Traffic.WarmupSendACKs + evidence.Traffic.SendACKs) * uint64(evidence.ConfiguredGroupMembers-1)
	setFixtureTerminalFanoutCounts(evidence, expected, expected, true)
}

func setFixtureTerminalFanoutCounts(evidence *StepEvidence, received, recvACKed uint64, exact bool) {
	if evidence == nil || evidence.ConfiguredGroupMembers <= 1 {
		return
	}
	logical := evidence.Traffic.WarmupSendACKs + evidence.Traffic.SendACKs
	expected := logical * uint64(evidence.ConfiguredGroupMembers-1)
	terminal := &evidence.Timeline.Terminal.ReceiveDrain
	terminal.ReceiveFramesObserved = received
	terminal.RecvACKSuccesses = recvACKed
	terminal.FanoutProof = fixtureFanoutProof(logical, expected, received, recvACKed, exact)
	rebindTerminalReceiveDrain(evidence)
}

func fixtureFanoutProof(logical, expected, received, recvACKed uint64, exact bool) model.FanoutProofSnapshot {
	expectedSummary := fixtureFanoutSummary(expected, "a", "b")
	receivedSummary := expectedSummary
	ackSummary := expectedSummary
	if !exact {
		receivedSummary = fixtureFanoutSummary(received, "c", "d")
		ackSummary = fixtureFanoutSummary(recvACKed, "c", "d")
	}
	return model.FanoutProofSnapshot{
		Version: model.FanoutProofVersion, Required: true, EvidenceComplete: true,
		LogicalSendACKs: logical, Expected: expectedSummary, Received: receivedSummary, RecvACKed: ackSummary,
	}
}

func fixtureFanoutSummary(count uint64, laneA, laneB string) model.FanoutMultisetSummary {
	if count == 0 {
		laneA, laneB = "0", "0"
	}
	return model.FanoutMultisetSummary{Count: count, DigestA: strings.Repeat(laneA, 64), DigestB: strings.Repeat(laneB, 64)}
}

func mutateStepTraffic(evidence *StepEvidence, mutate func(*TrafficEvidence)) {
	traffic := evidence.Traffic
	mutate(&traffic)
	setStepTraffic(evidence, traffic)
}

func containsReason(reasons []Reason, want Reason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func rebindTerminalReceiveDrain(evidence *StepEvidence) {
	if evidence == nil || evidence.Timeline.Terminal.TerminalCut == nil {
		return
	}
	digest := model.ReceiveDrainFingerprint(evidence.Timeline.Terminal.ReceiveDrain)
	evidence.Timeline.Terminal.TerminalCut.ReceiveDrainSHA256 = digest
	evidence.ProductQueues.TerminalCut.ReceiveDrainSHA256 = digest
}

func completeStepEvidence(offeredQPS int) StepEvidence {
	startedAt := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC).Add(time.Duration(offeredQPS) * time.Minute)
	warmup := phaseEvidence(startedAt, time.Minute, 30*time.Second, 2500)
	measured := phaseEvidence(warmup.EndedAt, 5*time.Minute, 30*time.Second, 2500)
	drain := phaseEvidence(measured.EndedAt, 10*time.Second, 10*time.Second, 2500)
	server := ProcessEvidence{PID: 1000 + offeredQPS, StartToken: fmt.Sprintf("server-start-%d", offeredQPS), Alive: true}
	for _, phase := range []*PhaseEvidence{&warmup, &measured, &drain} {
		for index := range phase.Samples {
			phase.Samples[index].Server = server
		}
	}
	planned := uint64(offeredQPS * 300)
	traffic := TrafficEvidence{
		WarmupSendACKs:           1000,
		Planned:                  planned,
		Dispatched:               planned,
		LogicalSent:              planned,
		SendAttempts:             planned + 2,
		SendACKs:                 planned,
		RetryAttempts:            2,
		StableClientMsgNo:        true,
		RetryEvidenceComplete:    true,
		MaximumRetriesPerMessage: 3,
	}
	setPhaseTraffic(&measured, traffic)
	setConstantPhaseTraffic(&drain, traffic)
	cutAt := drain.StartedAt.Add(3 * time.Second)
	productDigest := strings.Repeat("b", 64)
	storageDigest := strings.Repeat("c", 64)
	binding := &TerminalCutBinding{
		RunID: "run-1", AssignmentID: "assignment-1",
		ReadyAt: drain.StartedAt.Add(2 * time.Second), DeadlineAt: drain.StartedAt.Add(90 * time.Second), ObservedAt: cutAt,
		ProductMetricsSHA256: productDigest, StorageOverlapSHA256: storageDigest,
		AcknowledgedAt: drain.StartedAt.Add(4 * time.Second),
	}
	storageOverlap := completeStorageOverlapEvidence(measured, drain, "run-1")
	storageOverlap.PayloadSHA256 = storageDigest
	storageOverlap.Samples[len(storageOverlap.Samples)-1].ObservedAt = cutAt
	terminalReceiveDrain := completeReceiveDrainEvidence(2500)
	logicalACKs := traffic.WarmupSendACKs + traffic.SendACKs
	expectedReceives := logicalACKs * 9
	terminalReceiveDrain.ReceiveFramesObserved = expectedReceives
	terminalReceiveDrain.RecvACKSuccesses = expectedReceives
	terminalReceiveDrain.FanoutProof = fixtureFanoutProof(logicalACKs, expectedReceives, expectedReceives, expectedReceives, true)
	binding.ReceiveDrainSHA256 = model.ReceiveDrainFingerprint(terminalReceiveDrain)
	return StepEvidence{
		Schema:                       StepEvidenceSchema,
		RunID:                        "run-1",
		AssignmentID:                 "assignment-1",
		OfferedSendQPS:               offeredQPS,
		RequiredActiveConnections:    2500,
		ConfiguredGroupMembers:       10,
		ConfiguredWarmupSeconds:      60,
		ConfiguredMeasuredSeconds:    300,
		ConfiguredDrainBudgetSeconds: 90,
		MaximumSampleGapSeconds:      30,
		Target: ReviewedTargetEvidence{
			APIAddress: "http://127.0.0.1:5001", GatewayAddress: "127.0.0.1:5100",
			MetricsAddress: "http://127.0.0.1:5001", WorkerAddress: "http://127.0.0.1:19130",
		},
		ExecutionSeal: ExecutionSealEvidence{
			BaselineInvocationID:  "0123456789abcdef0123456789abcdef",
			SourceConfigSHA256:    strings.Repeat("4", 64),
			EffectiveConfigSHA256: strings.Repeat("1", 64), WukongIMBinarySHA256: strings.Repeat("2", 64),
			WkbenchBinarySHA256: strings.Repeat("3", 64),
		},
		Timeline: TimelineEvidence{
			CaptureComplete: true,
			Warmup:          warmup,
			Measured:        measured,
			Drain:           drain,
			Terminal: RuntimeSample{
				ObservedAt:          drain.EndedAt.Add(time.Second),
				ActiveConnections:   2500,
				TerminalPreClose:    true,
				TerminalCutRequired: true,
				TerminalCutReady:    true,
				TerminalCut:         binding,
				Server:              server,
				Worker:              ProcessEvidence{PID: 202, StartToken: "worker-start-token", Alive: true},
				Traffic:             traffic,
				ReceiveDrain:        terminalReceiveDrain,
			},
		},
		Traffic: traffic,
		ProductQueues: ProductQueueEvidence{
			BoundaryEvidenceComplete: true,
			PostWarmupCut: ProductQueueCut{
				Schema: ProductQueueCutSchema, ObservedAt: measured.StartedAt.Add(time.Second),
				RunID: "run-1", AssignmentID: "assignment-1", Phase: "warmup", ActivePhase: "run",
				ReceiveDrainSHA256: model.ReceiveDrainFingerprint(measured.Samples[0].ReceiveDrain),
			},
			TerminalCut: ProductQueueCut{
				Schema: ProductQueueCutSchema, ObservedAt: cutAt,
				RunID: "run-1", AssignmentID: "assignment-1", Phase: "run", ActivePhase: "cooldown",
				ReceiveDrainSHA256: model.ReceiveDrainFingerprint(terminalReceiveDrain),
			},
			TerminalPayloadSHA256: productDigest,
			Queues: []ProductQueueBoundary{
				{Name: QueueGatewayAsyncSend, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueChannelMailbox, BaselineDepth: 1, TerminalDepth: 1},
				{Name: QueueChannelWorker, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueRuntimePool, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueChannelAppendPending, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueChannelAppendInflight, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueuePostCommitBacklog, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueuePostCommitHandoff, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueuePostCommitRetry, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueEffectPoolInflight, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueStorageCommit, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueDeliveryPlan, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueDeliveryInflight, BaselineDepth: 0, TerminalDepth: 0},
				{Name: QueueDeliveryAckBindings, BaselineDepth: 0, TerminalDepth: 0},
			},
			ResultCounters: completeProductResultCounterBoundaries(),
		},
		StorageOverlap: storageOverlap,
		StorageMetrics: StorageMetricsEvidence{
			CaptureComplete: true, Tag: fmt.Sprintf("%06d", offeredQPS), Node: "127_0_0_1_5001", Status: "complete",
			RowSHA256: strings.Repeat("d", 64), PhysicalCommits: 100, LogicalRequests: planned, Records: planned,
			Bytes: planned * 2048, RequestSamples: planned, WALBytesIn: planned * 2048, WALBytesWritten: planned * 2048,
			ResultOK: planned - 3, ResultTimeout: 1, ResultCanceled: 1, ResultError: 1,
			LaneLeaderAppend: planned / 3, LaneFollowerApply: planned / 3, LaneMessageAppend: planned - 2*(planned/3),
			AverageRequestsPerCommit: 3000, AverageRecordsPerCommit: 3000, AverageBytesPerCommit: 6144000,
			RequestsPerCommitP50: 1000, RequestsPerCommitP95: 3000, RequestsPerCommitP99: 5000,
			RecordsPerCommitP50: 1000, RecordsPerCommitP95: 3000, RecordsPerCommitP99: 5000,
			BytesPerCommitP50: 2048000, BytesPerCommitP95: 6144000, BytesPerCommitP99: 10240000,
		},
		HostIO: HostIOEvidence{
			CaptureComplete: true, Tag: fmt.Sprintf("%06d", offeredQPS), Host: "host-local", Status: "complete",
			PhysicalDevice: "nvme0n1", RowSHA256: strings.Repeat("e", 64), IOPSAvailable: true, IOPSMax: 1200,
			BytesPerSecondAvailable: true, BytesPerSecondMax: 80000000, UtilizationAvailable: true,
			UtilizationPercentMax: 72, ServiceTimeAvailable: true, ServiceTimeMillisMax: 1.25,
			ReadWriteSplitAvailable: true,
		},
		Profile: ProfileEvidence{
			Schema: ProfileEvidenceSchema, Status: "not_triggered", EvidenceComplete: true,
			CaptureValid: true, Reason: "no_measured_threshold",
		},
		Seal: SealEvidence{PayloadComplete: true, ChecksumsVerified: true},
	}
}

func completeStorageOverlapEvidence(measured, drain PhaseEvidence, runID string) StorageOverlapEvidence {
	samples := make([]StorageOverlapSample, 0, 14)
	const compactionCount = uint64(10)
	for observedAt, index := measured.StartedAt.Add(time.Second), 0; observedAt.Before(measured.EndedAt); observedAt, index = observedAt.Add(25*time.Second), index+1 {
		name := "post-warmup"
		if index > 0 {
			name = "periodic-" + sixDigitString(index)
		}
		samples = append(samples, StorageOverlapSample{
			ObservedAt: observedAt, RunID: runID, Sample: name, Node: "node-1", Status: "complete",
			CompactionCount: compactionCount, SnapshotIdentity: strings.Repeat("a", 64),
			SnapshotInventory: "snapshot-inventory/" + name + "-node-1.tsv", InventoryVerified: true,
		})
	}
	samples = append(samples, StorageOverlapSample{
		ObservedAt: drain.StartedAt.Add(3 * time.Second), RunID: runID, Sample: "terminal", Node: "node-1", Status: "complete",
		CompactionCount: compactionCount, SnapshotIdentity: strings.Repeat("a", 64),
		SnapshotInventory: "snapshot-inventory/terminal-node-1.tsv", InventoryVerified: true,
	})
	return StorageOverlapEvidence{CaptureComplete: true, Samples: samples}
}

func sixDigitString(value int) string {
	return fmt.Sprintf("%06d", value)
}

func phaseEvidence(startedAt time.Time, duration, interval time.Duration, activeConnections int) PhaseEvidence {
	server := ProcessEvidence{PID: 101, StartToken: "server-start-token", Alive: true}
	worker := ProcessEvidence{PID: 202, StartToken: "worker-start-token", Alive: true}
	endedAt := startedAt.Add(duration)
	samples := make([]RuntimeSample, 0, int(duration/interval)+1)
	for observedAt := startedAt; !observedAt.After(endedAt); observedAt = observedAt.Add(interval) {
		samples = append(samples, RuntimeSample{
			ObservedAt:        observedAt,
			ActiveConnections: activeConnections,
			Server:            server,
			Worker:            worker,
			ReceiveDrain:      liveReceiveDrainEvidence(activeConnections),
		})
	}
	return PhaseEvidence{StartedAt: startedAt, EndedAt: endedAt, Samples: samples}
}

func liveReceiveDrainEvidence(clients int) model.ReceiveDrainSnapshot {
	return model.ReceiveDrainSnapshot{
		Required:             true,
		EvidenceComplete:     true,
		ClientCount:          uint64(clients),
		ActiveDrains:         uint64(clients),
		QueueSnapshotClients: uint64(clients),
		FanoutProof:          fixtureFanoutProof(0, 0, 0, 0, true),
	}
}

func completeReceiveDrainEvidence(clients int) model.ReceiveDrainSnapshot {
	snapshot := liveReceiveDrainEvidence(clients)
	snapshot.DrainComplete = true
	snapshot.StableZeroObservations = model.ReceiveDrainStableZeroObservations
	return snapshot
}

func setStepTraffic(evidence *StepEvidence, traffic TrafficEvidence) {
	evidence.Traffic = traffic
	setPhaseTraffic(&evidence.Timeline.Measured, traffic)
	setConstantPhaseTraffic(&evidence.Timeline.Drain, traffic)
	evidence.Timeline.Terminal.Traffic = traffic
}

func setPhaseTraffic(phase *PhaseEvidence, final TrafficEvidence) {
	if phase == nil || len(phase.Samples) == 0 {
		return
	}
	for index := range phase.Samples {
		numerator := uint64(index + 1)
		denominator := uint64(len(phase.Samples))
		traffic := final
		traffic.Planned = final.Planned * numerator / denominator
		traffic.Dispatched = final.Dispatched * numerator / denominator
		traffic.LogicalSent = final.LogicalSent * numerator / denominator
		traffic.SendACKs = final.SendACKs * numerator / denominator
		traffic.TerminalErrors = final.TerminalErrors * numerator / denominator
		traffic.CorrectnessErrors = final.CorrectnessErrors * numerator / denominator
		traffic.RetryAttempts = final.RetryAttempts * numerator / denominator
		traffic.RetryExhausted = final.RetryExhausted * numerator / denominator
		traffic.SendAttempts = final.SendAttempts * numerator / denominator
		traffic.Remaining = 0
		phase.Samples[index].Traffic = traffic
	}
}

func setConstantPhaseTraffic(phase *PhaseEvidence, traffic TrafficEvidence) {
	if phase == nil {
		return
	}
	for index := range phase.Samples {
		phase.Samples[index].Traffic = traffic
	}
}
