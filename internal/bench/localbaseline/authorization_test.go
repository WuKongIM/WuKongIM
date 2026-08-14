package localbaseline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeThreeNodeDiagnosticAcceptsOnlyReviewedFourStepBaseline(t *testing.T) {
	evidence := completeBaselineEvidence()

	result := AuthorizeThreeNodeDiagnostic(evidence)

	if result.Schema != AuthorizationResultSchema {
		t.Fatalf("schema = %q, want %q", result.Schema, AuthorizationResultSchema)
	}
	if !result.Authorizes || !result.ReviewedContractSatisfied {
		t.Fatalf("authorization = %+v, want authorized", result)
	}
	if result.Outcome != OutcomeClean || result.Reason != "complete" || result.ExitCode != 0 ||
		result.HighestCleanRate != 1000 || result.FirstFailingRate != 0 {
		t.Fatalf("typed final decision = %+v", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticOwnsClosedFinalDecision(t *testing.T) {
	evidence := completeBaselineEvidence()
	evidence.Settings.Channels = 10
	SealBaselineEvidence(&evidence)

	result := AuthorizeThreeNodeDiagnostic(evidence)

	if result.Authorizes || result.Outcome == OutcomeClean || result.ExitCode == 0 {
		t.Fatalf("closed authorization exposed a clean success = %+v", result)
	}
	if result.Outcome != OutcomeInsufficientEvidence || result.Reason != string(AuthorizationReasonReviewedDefaults) {
		t.Fatalf("typed closed decision = %+v", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticRejectsMixedExecutionTargets(t *testing.T) {
	evidence := completeBaselineEvidence()
	evidence.StepClosures[2].Evidence.Target.WorkerAddress = "http://127.0.0.1:19131"
	evidence.StepClosures[2].Result = CloseStepResult(
		evidence.StepClosures[2].Evidence,
		evidence.StepClosures[2].Result.PayloadManifestSHA256,
	)
	SealBaselineEvidence(&evidence)

	result := AuthorizeThreeNodeDiagnostic(evidence)
	if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonExecutionTarget) {
		t.Fatalf("authorization = %+v, want execution-target rejection", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticRejectsMixedBaselineInvocation(t *testing.T) {
	evidence := completeBaselineEvidence()
	evidence.StepClosures[2].Evidence.ExecutionSeal.BaselineInvocationID = strings.Repeat("f", 32)
	resealBaselineStep(&evidence, 2)

	if !evidence.StepClosures[2].Result.Clean {
		t.Fatalf("mutated step result = %+v, want independently clean evidence", evidence.StepClosures[2].Result)
	}
	result := AuthorizeThreeNodeDiagnostic(evidence)
	if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonExecutionSeal) {
		t.Fatalf("authorization = %+v, want mixed-invocation rejection", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticRejectsMixedExecutionArtifactDigests(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExecutionSealEvidence)
	}{
		{
			name: "source config",
			mutate: func(seal *ExecutionSealEvidence) {
				seal.SourceConfigSHA256 = strings.Repeat("7", 64)
			},
		},
		{
			name: "effective config",
			mutate: func(seal *ExecutionSealEvidence) {
				seal.EffectiveConfigSHA256 = strings.Repeat("4", 64)
			},
		},
		{
			name: "wukongim binary",
			mutate: func(seal *ExecutionSealEvidence) {
				seal.WukongIMBinarySHA256 = strings.Repeat("5", 64)
			},
		},
		{
			name: "wkbench binary",
			mutate: func(seal *ExecutionSealEvidence) {
				seal.WkbenchBinarySHA256 = strings.Repeat("6", 64)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeBaselineEvidence()
			test.mutate(&evidence.StepClosures[1].Evidence.ExecutionSeal)
			resealBaselineStep(&evidence, 1)

			if !evidence.StepClosures[1].Result.Clean {
				t.Fatalf("mutated step result = %+v, want independently clean evidence", evidence.StepClosures[1].Result)
			}
			result := AuthorizeThreeNodeDiagnostic(evidence)
			if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonExecutionSeal) {
				t.Fatalf("authorization = %+v, want mixed-artifact rejection", result)
			}
		})
	}
}

func TestAuthorizeThreeNodeDiagnosticRejectsNonAdjacentServerGenerationReuse(t *testing.T) {
	evidence := completeBaselineEvidence()
	reused := evidence.StepClosures[0].Evidence.Timeline.Terminal.Server
	setStepProcessEvidence(&evidence.StepClosures[2].Evidence, reused, nil)
	resealBaselineStep(&evidence, 2)

	if !evidence.StepClosures[2].Result.Clean {
		t.Fatalf("mutated step result = %+v, want independently clean evidence", evidence.StepClosures[2].Result)
	}
	result := AuthorizeThreeNodeDiagnostic(evidence)
	if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonServerGeneration) {
		t.Fatalf("authorization = %+v, want duplicate server-generation rejection", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticRejectsOverlappingServerGenerations(t *testing.T) {
	evidence := completeBaselineEvidence()
	previousTerminalAt := evidence.StepClosures[0].Evidence.Timeline.Terminal.ObservedAt
	step := &evidence.StepClosures[1].Evidence
	shiftStepEvidenceTimestamps(step, previousTerminalAt.Sub(step.Timeline.Warmup.StartedAt))
	resealBaselineStep(&evidence, 1)

	if !evidence.StepClosures[1].Result.Clean {
		t.Fatalf("shifted step result = %+v, want independently clean evidence", evidence.StepClosures[1].Result)
	}
	result := AuthorizeThreeNodeDiagnostic(evidence)
	if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonServerGeneration) {
		t.Fatalf("authorization = %+v, want overlapping-generation rejection", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticAllowsOneWorkerAcrossAllServerGenerations(t *testing.T) {
	evidence := completeBaselineEvidence()
	worker := ProcessEvidence{PID: 202, StartToken: "shared-worker-generation", Alive: true}
	for index := range evidence.StepClosures {
		setStepProcessEvidence(&evidence.StepClosures[index].Evidence, ProcessEvidence{}, &worker)
		resealBaselineStep(&evidence, index)
	}

	result := AuthorizeThreeNodeDiagnostic(evidence)
	if !result.Authorizes || !result.ReviewedContractSatisfied {
		t.Fatalf("authorization = %+v, want shared worker with distinct server generations authorized", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticPreservesSealedTerminalPreflightDecision(t *testing.T) {
	for _, test := range []struct {
		outcome Outcome
		reason  string
		exit    int
	}{
		{outcome: OutcomeStorageConfounded, reason: "filesystem_free_below_10_percent", exit: 2},
		{outcome: OutcomeInsufficientEvidence, reason: "artifact_seal_verification_failed", exit: 6},
	} {
		evidence := completeBaselineEvidence()
		evidence.DiagnosticOutcome = string(test.outcome)
		evidence.DiagnosticReason = test.reason
		evidence.StepClosures = nil
		evidence.Seal = SealEvidence{}
		if test.outcome == OutcomeInsufficientEvidence {
			evidence.ObservedFilesystemFreePercent = evidence.Settings.MinimumFreePercent - 1
		}
		SealBaselineEvidence(&evidence)

		result := AuthorizeThreeNodeDiagnostic(evidence)
		if result.Authorizes || result.Outcome != test.outcome || result.Reason != test.reason || result.ExitCode != test.exit {
			t.Fatalf("preflight result = %+v", result)
		}
	}
}

func TestAuthorizeThreeNodeDiagnosticNeverAuthorizesBelowTerminalFilesystemFloor(t *testing.T) {
	evidence := completeBaselineEvidence()
	evidence.ObservedFilesystemFreePercent = evidence.Settings.MinimumFreePercent - 1
	SealBaselineEvidence(&evidence)

	result := AuthorizeThreeNodeDiagnostic(evidence)
	if result.Authorizes || result.ReviewedContractSatisfied || result.Outcome != OutcomeStorageConfounded || result.ExitCode != 2 {
		t.Fatalf("terminal filesystem result = %+v", result)
	}
}

func TestAuthorizeThreeNodeDiagnosticRequiresCanonicalDataFilesystemIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BaselineEvidence)
	}{
		{name: "relative data directory", mutate: func(e *BaselineEvidence) { e.CanonicalDataDir = "data" }},
		{name: "filesystem device absent", mutate: func(e *BaselineEvidence) { e.DataFilesystemDevice = "" }},
		{name: "total blocks absent", mutate: func(e *BaselineEvidence) { e.DataFilesystemTotalBlocks = 0 }},
		{name: "block size absent", mutate: func(e *BaselineEvidence) { e.DataFilesystemBlockSize = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := completeBaselineEvidence()
			test.mutate(&evidence)
			SealBaselineEvidence(&evidence)
			result := AuthorizeThreeNodeDiagnostic(evidence)
			if result.Authorizes || !containsAuthorizationReason(result.Reasons, AuthorizationReasonFilesystem) {
				t.Fatalf("authorization = %+v, want typed filesystem identity rejection", result)
			}
		})
	}
}

func TestAuthorizeThreeNodeDiagnosticStopsDecisionAtFirstFailedStep(t *testing.T) {
	evidence := completeBaselineEvidence()
	evidence.StepClosures[2].Evidence.RequiredActiveConnections++
	setStepReceiveDrainConnections(&evidence.StepClosures[2].Evidence, evidence.StepClosures[2].Evidence.RequiredActiveConnections)
	evidence.StepClosures = evidence.StepClosures[:3]
	SealBaselineEvidence(&evidence)

	result := AuthorizeThreeNodeDiagnostic(evidence)

	if result.Outcome != OutcomeRateFailed || result.ExitCode != 3 ||
		result.HighestCleanRate != 500 || result.FirstFailingRate != 750 {
		t.Fatalf("typed first-failure decision = %+v", result)
	}
}

func setStepReceiveDrainConnections(evidence *StepEvidence, connections int) {
	for _, phase := range []*PhaseEvidence{&evidence.Timeline.Warmup, &evidence.Timeline.Measured, &evidence.Timeline.Drain} {
		for index := range phase.Samples {
			phase.Samples[index].ReceiveDrain.ClientCount = uint64(connections)
			phase.Samples[index].ReceiveDrain.ActiveDrains = uint64(connections)
			phase.Samples[index].ReceiveDrain.QueueSnapshotClients = uint64(connections)
		}
	}
	evidence.Timeline.Terminal.ReceiveDrain.ClientCount = uint64(connections)
	evidence.Timeline.Terminal.ReceiveDrain.ActiveDrains = uint64(connections)
	evidence.Timeline.Terminal.ReceiveDrain.QueueSnapshotClients = uint64(connections)
	rebindTerminalReceiveDrain(evidence)
}

func TestAuthorizeThreeNodeDiagnosticPreservesStorageOverlapAsObservation(t *testing.T) {
	evidence := completeBaselineEvidence()
	for index := 1; index < len(evidence.StepClosures[1].Evidence.StorageOverlap.Samples); index++ {
		evidence.StepClosures[1].Evidence.StorageOverlap.Samples[index].SnapshotIdentity = strings.Repeat("c", 64)
	}
	evidence.StepClosures[1].Result = CloseStepResult(evidence.StepClosures[1].Evidence, evidence.StepClosures[1].Result.PayloadManifestSHA256)
	evidence.StepClosures[2].Evidence.StorageOverlap.Samples[1].CompactionCount++
	for index := 2; index < len(evidence.StepClosures[2].Evidence.StorageOverlap.Samples); index++ {
		evidence.StepClosures[2].Evidence.StorageOverlap.Samples[index].CompactionCount++
	}
	evidence.StepClosures[2].Result = CloseStepResult(evidence.StepClosures[2].Evidence, evidence.StepClosures[2].Result.PayloadManifestSHA256)
	SealBaselineEvidence(&evidence)

	result := AuthorizeThreeNodeDiagnostic(evidence)

	if !result.Authorizes || !result.ReviewedContractSatisfied {
		t.Fatalf("authorization = %+v, want overlap-observing authorization", result)
	}
	if got := result.Steps[1].Observations; len(got) != 1 || got[0] != ObservationSnapshotOverlap {
		t.Fatalf("snapshot observations = %v", got)
	}
	if got := result.Steps[2].Observations; len(got) != 1 || got[0] != ObservationCompactionOverlap {
		t.Fatalf("compaction observations = %v", got)
	}
}

func TestAuthorizeThreeNodeDiagnosticFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*BaselineEvidence)
		wantReason AuthorizationReason
	}{
		{
			name: "non-canonical source config",
			mutate: func(e *BaselineEvidence) {
				e.Settings.CanonicalSourceConfig = false
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "external worker",
			mutate: func(e *BaselineEvidence) {
				e.Settings.OwnedWorker = false
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "legacy baseline classifier not clean",
			mutate: func(e *BaselineEvidence) {
				e.DiagnosticOutcome = "storage_confounded"
			},
			wantReason: AuthorizationReasonBaselineOutcome,
		},
		{
			name: "missing rate step",
			mutate: func(e *BaselineEvidence) {
				e.StepClosures = e.StepClosures[:3]
			},
			wantReason: AuthorizationReasonRateSteps,
		},
		{
			name: "wrong rate order",
			mutate: func(e *BaselineEvidence) {
				e.StepClosures[1], e.StepClosures[2] = e.StepClosures[2], e.StepClosures[1]
			},
			wantReason: AuthorizationReasonRateSteps,
		},
		{
			name: "one failed step",
			mutate: func(e *BaselineEvidence) {
				e.StepClosures[3].Evidence.Traffic.SendACKs--
				e.StepClosures[3].Evidence.Traffic.TerminalErrors++
			},
			wantReason: AuthorizationReasonStepNotClean,
		},
		{
			name: "short measured duration",
			mutate: func(e *BaselineEvidence) {
				e.Settings.MeasuredSeconds = 299
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "low online population",
			mutate: func(e *BaselineEvidence) {
				e.Settings.ActiveConnections = 2499
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "wrong topology",
			mutate: func(e *BaselineEvidence) {
				e.Settings.HashSlots = 16
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "step group membership differs from reviewed topology",
			mutate: func(e *BaselineEvidence) {
				closure := &e.StepClosures[2]
				closure.Evidence.ConfiguredGroupMembers = 9
				closure.Result = CloseStepResult(closure.Evidence, closure.Result.PayloadManifestSHA256)
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "async commit",
			mutate: func(e *BaselineEvidence) {
				e.Settings.SyncCommit = false
			},
			wantReason: AuthorizationReasonReviewedDefaults,
		},
		{
			name: "dirty source",
			mutate: func(e *BaselineEvidence) {
				e.Source.Dirty = true
			},
			wantReason: AuthorizationReasonSource,
		},
		{
			name: "binary-only source identity",
			mutate: func(e *BaselineEvidence) {
				e.Source.RebuildableFromRevision = false
			},
			wantReason: AuthorizationReasonSource,
		},
		{
			name: "incomplete full seal",
			mutate: func(e *BaselineEvidence) {
				e.Seal.PayloadComplete = false
			},
			wantReason: AuthorizationReasonSeal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := completeBaselineEvidence()
			tt.mutate(&evidence)
			SealBaselineEvidence(&evidence)

			result := AuthorizeThreeNodeDiagnostic(evidence)

			if result.Authorizes || result.ReviewedContractSatisfied {
				t.Fatalf("authorization = %+v, want closed", result)
			}
			if !containsAuthorizationReason(result.Reasons, tt.wantReason) {
				t.Fatalf("reasons = %v, want %q", result.Reasons, tt.wantReason)
			}
		})
	}
}

func TestParseBaselineEvidenceIsStrictAndBounded(t *testing.T) {
	want := completeBaselineEvidence()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got, err := ParseBaselineEvidence(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseBaselineEvidence() error = %v", err)
	}
	if result := AuthorizeThreeNodeDiagnostic(got); !result.Authorizes {
		t.Fatalf("parsed authorization = %+v", result)
	}

	unknown := bytes.Replace(data, []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1)
	if _, err := ParseBaselineEvidence(bytes.NewReader(unknown)); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := ParseBaselineEvidence(strings.NewReader(string(data) + `{}`)); err == nil {
		t.Fatal("trailing JSON document was accepted")
	}
	if _, err := ParseBaselineEvidence(strings.NewReader(strings.Repeat(" ", MaximumEvidenceBytes+1))); err == nil {
		t.Fatal("oversized evidence was accepted")
	}
}

func completeBaselineEvidence() BaselineEvidence {
	closures := make([]StepClosure, 0, len(ReviewedOfferedSendQPS))
	for index, qps := range ReviewedOfferedSendQPS {
		evidence := completeStepEvidence(qps)
		payloadDigest := strings.Repeat(string(rune('a'+index)), 64)
		closures = append(closures, StepClosure{
			Schema: StepClosureSchema, ClosureManifest: fmt.Sprintf("reports/%04d-qps/evidence/step-closure.json", qps),
			ClosureManifestSHA256: strings.Repeat(string(rune('e'-index)), 64), Evidence: evidence,
			Result: CloseStepResult(evidence, payloadDigest),
		})
	}
	evidence := BaselineEvidence{
		Schema:                        BaselineEvidenceSchema,
		BaselineInvocationID:          "0123456789abcdef0123456789abcdef",
		DiagnosticOutcome:             string(OutcomeClean),
		FilesystemObservationComplete: true,
		ObservedFilesystemFreePercent: 50,
		CanonicalDataDir:              "/var/lib/wukongim",
		DataFilesystemDevice:          "2049",
		DataFilesystemTotalBlocks:     100000,
		DataFilesystemBlockSize:       4096,
		Settings: ReviewedSettings{
			Channels:                1000,
			ActiveConnections:       2500,
			GroupMembers:            10,
			SendConcurrency:         2800,
			PayloadBytes:            128,
			WarmupSeconds:           60,
			MeasuredSeconds:         300,
			DrainBudgetSeconds:      90,
			ACKTimeoutSeconds:       15,
			ReceiveACK:              true,
			HeartbeatEnabled:        true,
			SenderPickRoundRobin:    true,
			MinimumFreePercent:      10,
			LogicalSlotGroups:       12,
			HashSlots:               256,
			SlotReplicas:            1,
			ChannelReplicas:         1,
			CommitFlushWindowMicros: 200,
			CommitCoordinatorShards: 1,
			SyncCommit:              true,
			CleanCluster:            true,
			OwnedCluster:            true,
			OwnedWorker:             true,
			CanonicalSourceConfig:   true,
			MetricsEndpointCount:    1,
		},
		Source: SourceEvidence{
			Revision:                strings.Repeat("a", 40),
			Dirty:                   false,
			RebuildableFromRevision: true,
		},
		Seal:         SealEvidence{PayloadComplete: true, ChecksumsVerified: true},
		StepClosures: closures,
	}
	SealBaselineEvidence(&evidence)
	return evidence
}

func resealBaselineStep(evidence *BaselineEvidence, index int) {
	closure := &evidence.StepClosures[index]
	closure.Result = CloseStepResult(closure.Evidence, closure.Result.PayloadManifestSHA256)
	SealBaselineEvidence(evidence)
}

func setStepProcessEvidence(evidence *StepEvidence, server ProcessEvidence, worker *ProcessEvidence) {
	for _, phase := range []*PhaseEvidence{
		&evidence.Timeline.Warmup,
		&evidence.Timeline.Measured,
		&evidence.Timeline.Drain,
	} {
		for index := range phase.Samples {
			if server.PID > 0 {
				phase.Samples[index].Server = server
			}
			if worker != nil {
				phase.Samples[index].Worker = *worker
			}
		}
	}
	if server.PID > 0 {
		evidence.Timeline.Terminal.Server = server
	}
	if worker != nil {
		evidence.Timeline.Terminal.Worker = *worker
	}
}

func shiftStepEvidenceTimestamps(evidence *StepEvidence, delta time.Duration) {
	shiftPhase := func(phase *PhaseEvidence) {
		phase.StartedAt = phase.StartedAt.Add(delta)
		phase.EndedAt = phase.EndedAt.Add(delta)
		for index := range phase.Samples {
			phase.Samples[index].ObservedAt = phase.Samples[index].ObservedAt.Add(delta)
			shiftTerminalCutBinding(phase.Samples[index].TerminalCut, delta)
		}
	}
	shiftPhase(&evidence.Timeline.Warmup)
	shiftPhase(&evidence.Timeline.Measured)
	shiftPhase(&evidence.Timeline.Drain)
	evidence.Timeline.Terminal.ObservedAt = evidence.Timeline.Terminal.ObservedAt.Add(delta)
	shiftTerminalCutBinding(evidence.Timeline.Terminal.TerminalCut, delta)
	evidence.ProductQueues.PostWarmupCut.ObservedAt = evidence.ProductQueues.PostWarmupCut.ObservedAt.Add(delta)
	evidence.ProductQueues.TerminalCut.ObservedAt = evidence.ProductQueues.TerminalCut.ObservedAt.Add(delta)
	for index := range evidence.StorageOverlap.Samples {
		evidence.StorageOverlap.Samples[index].ObservedAt = evidence.StorageOverlap.Samples[index].ObservedAt.Add(delta)
	}
}

func shiftTerminalCutBinding(binding *TerminalCutBinding, delta time.Duration) {
	if binding == nil {
		return
	}
	binding.ReadyAt = binding.ReadyAt.Add(delta)
	binding.DeadlineAt = binding.DeadlineAt.Add(delta)
	binding.ObservedAt = binding.ObservedAt.Add(delta)
	binding.AcknowledgedAt = binding.AcknowledgedAt.Add(delta)
}

func containsAuthorizationReason(reasons []AuthorizationReason, want AuthorizationReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
