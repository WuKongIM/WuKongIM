package localbaseline

import (
	"io"
	"strings"
)

const (
	// TerminalQueueConvergenceSchema identifies the typed decision for one
	// external pre-close product queue candidate.
	TerminalQueueConvergenceSchema = "wukongim/chat-lifecycle-local-single-node-queue-convergence/v1"

	TerminalQueueConvergenceReasonOK             = "ok"
	TerminalQueueConvergenceReasonPending        = "queues_above_post_warmup_floor"
	TerminalQueueConvergenceReasonIncomplete     = "queue_evidence_incomplete"
	TerminalQueueConvergenceReasonIdentity       = "assignment_identity_mismatch"
	TerminalQueueConvergenceReasonPhase          = "candidate_phase_mismatch"
	TerminalQueueConvergenceReasonProductFailure = "product_failure_counter_increased"
)

// TerminalQueueConvergence is the bounded typed result consumed by the local
// observer. CandidateSHA256 binds the decision to the exact raw Prometheus
// bytes that may later be acknowledged by the worker.
type TerminalQueueConvergence struct {
	Schema           string                         `json:"schema"`
	RunID            string                         `json:"run_id"`
	AssignmentID     string                         `json:"assignment_id"`
	EvidenceComplete bool                           `json:"evidence_complete"`
	Converged        bool                           `json:"converged"`
	Reason           string                         `json:"reason"`
	CandidateSHA256  string                         `json:"candidate_sha256"`
	CandidateCut     ProductQueueCut                `json:"candidate_cut"`
	Queues           []ProductQueueBoundary         `json:"queues"`
	ResultCounters   []ProductResultCounterBoundary `json:"result_counters"`
}

// QueryTerminalProductQueueConvergence compares one exact cooldown candidate
// with the retained post-warmup floor. Parsing, fixed-family coverage, and
// convergence remain wholly in Go; callers must not recreate the decision in
// shell.
func QueryTerminalProductQueueConvergence(baseline, candidate io.Reader, runID, assignmentID string) (TerminalQueueConvergence, error) {
	runID = strings.TrimSpace(runID)
	assignmentID = strings.TrimSpace(assignmentID)
	result := TerminalQueueConvergence{
		Schema: TerminalQueueConvergenceSchema, RunID: runID, AssignmentID: assignmentID,
		Reason: TerminalQueueConvergenceReasonIncomplete,
	}
	evidence, err := BuildProductQueueEvidence(baseline, candidate)
	if err != nil {
		return result, err
	}
	result.CandidateCut = evidence.TerminalCut
	result.CandidateSHA256 = evidence.TerminalPayloadSHA256
	result.Queues = append([]ProductQueueBoundary(nil), evidence.Queues...)
	result.ResultCounters = append([]ProductResultCounterBoundary(nil), evidence.ResultCounters...)
	if runID == "" || assignmentID == "" ||
		!validQueueCutIdentity(evidence.PostWarmupCut, runID, assignmentID) ||
		!validQueueCutIdentity(evidence.TerminalCut, runID, assignmentID) {
		result.Reason = TerminalQueueConvergenceReasonIdentity
		return result, nil
	}
	if evidence.PostWarmupCut.Phase != "warmup" || evidence.PostWarmupCut.ActivePhase != "run" ||
		evidence.TerminalCut.Phase != "run" || evidence.TerminalCut.ActivePhase != "cooldown" {
		result.Reason = TerminalQueueConvergenceReasonPhase
		return result, nil
	}
	if !validSHA256(evidence.TerminalCut.ReceiveDrainSHA256) {
		return result, nil
	}
	queuesComplete, queuesConverged := evaluateProductQueues(evidence)
	resultsComplete, failuresUnchanged := evaluateProductResultCounters(evidence)
	result.EvidenceComplete = queuesComplete && resultsComplete && validSHA256(evidence.TerminalPayloadSHA256)
	if !result.EvidenceComplete {
		return result, nil
	}
	result.Converged = queuesConverged && failuresUnchanged
	if !failuresUnchanged {
		result.Reason = TerminalQueueConvergenceReasonProductFailure
	} else if queuesConverged {
		result.Reason = TerminalQueueConvergenceReasonOK
	} else {
		result.Reason = TerminalQueueConvergenceReasonPending
	}
	return result, nil
}
