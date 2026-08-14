package localbaseline

import (
	"strings"
	"testing"
	"time"
)

func TestQueryTerminalProductQueueConvergenceIsTypedAndExact(t *testing.T) {
	at := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	baseline := completeProductQueuePrometheus(2, testProductQueueCut("warmup", "run", at.Add(-time.Minute)))
	candidate := completeProductQueuePrometheus(1, testProductQueueCut("run", "cooldown", at))

	result, err := QueryTerminalProductQueueConvergence(strings.NewReader(baseline), strings.NewReader(candidate), "run-1", "assignment-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Schema != TerminalQueueConvergenceSchema || !result.EvidenceComplete || !result.Converged ||
		result.Reason != TerminalQueueConvergenceReasonOK || result.CandidateCut.ObservedAt != at ||
		!validSHA256(result.CandidateSHA256) {
		t.Fatalf("result = %+v", result)
	}

	notConverged, err := QueryTerminalProductQueueConvergence(
		strings.NewReader(completeProductQueuePrometheus(0, testProductQueueCut("warmup", "run", at.Add(-time.Minute)))),
		strings.NewReader(completeProductQueuePrometheus(4, testProductQueueCut("run", "cooldown", at))),
		"run-1", "assignment-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !notConverged.EvidenceComplete || notConverged.Converged || notConverged.Reason != TerminalQueueConvergenceReasonPending {
		t.Fatalf("not converged = %+v", notConverged)
	}

	wrongGeneration, err := QueryTerminalProductQueueConvergence(strings.NewReader(baseline), strings.NewReader(candidate), "run-1", "replacement")
	if err != nil {
		t.Fatal(err)
	}
	if wrongGeneration.EvidenceComplete || wrongGeneration.Converged || wrongGeneration.Reason != TerminalQueueConvergenceReasonIdentity {
		t.Fatalf("wrong generation = %+v", wrongGeneration)
	}
}

func TestQueryTerminalProductQueueConvergenceRejectsDishonestCandidatePhase(t *testing.T) {
	at := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	baseline := completeProductQueuePrometheus(2, testProductQueueCut("warmup", "run", at.Add(-time.Minute)))
	candidate := completeProductQueuePrometheus(0, testProductQueueCut("stopped", "", at))

	result, err := QueryTerminalProductQueueConvergence(strings.NewReader(baseline), strings.NewReader(candidate), "run-1", "assignment-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.EvidenceComplete || result.Converged || result.Reason != TerminalQueueConvergenceReasonPhase {
		t.Fatalf("result = %+v", result)
	}
}

func TestQueryTerminalProductQueueConvergenceRejectsNewProductFailure(t *testing.T) {
	at := time.Date(2026, 8, 14, 1, 2, 3, 0, time.UTC)
	baseline := completeProductQueuePrometheus(0, testProductQueueCut("warmup", "run", at.Add(-time.Minute)))
	candidate := strings.Replace(
		completeProductQueuePrometheus(0, testProductQueueCut("run", "cooldown", at)),
		"wukongim_channelappend_effect_total{stage=\"post_commit\",result=\"commit_failed\"} 0\n",
		"wukongim_channelappend_effect_total{stage=\"post_commit\",result=\"commit_failed\"} 1\n",
		1,
	)

	result, err := QueryTerminalProductQueueConvergence(strings.NewReader(baseline), strings.NewReader(candidate), "run-1", "assignment-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.EvidenceComplete || result.Converged || result.Reason != TerminalQueueConvergenceReasonProductFailure {
		t.Fatalf("result = %+v", result)
	}
}
