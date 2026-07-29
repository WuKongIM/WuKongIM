package issueagentmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

func decodeModelProposal(
	content []byte,
	maxBytes int64,
	task issueagent.TaskEnvelope,
) (issueagent.AgentResult, error) {
	if len(content) == 0 || int64(len(content)) > maxBytes {
		return issueagent.AgentResult{}, errors.New("model proposal exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var result issueagent.AgentResult
	if err := decoder.Decode(&result); err != nil {
		return issueagent.AgentResult{}, errors.New("decode model proposal")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return issueagent.AgentResult{}, errors.New("model proposal contains trailing JSON")
	}
	if err := issueagent.ValidateModelProposal(result, task); err != nil {
		return issueagent.AgentResult{}, err
	}
	return result, nil
}

func modelProposalInstructions(task issueagent.TaskEnvelope) string {
	success := issueagent.AgentResult{
		SchemaVersion: 1,
		Repository:    task.Repository,
		IssueNumber:   task.IssueNumber,
		Generation:    task.Generation,
		Sequence:      task.Sequence,
		OperationID:   task.OperationID,
		Phase:         task.Phase,
		Status:        issueagent.ResultStatusSuccess,
		ChangeSet:     issueagent.ChangeSet{Files: []issueagent.FileChange{}},
		Evidence: issueagent.EvidenceManifest{
			Commands: []issueagent.CommandEvidence{},
		},
		Usage: issueagent.ModelUsage{
			Provider: task.Provider,
			Model:    task.Model,
		},
	}
	switch task.Phase {
	case issueagent.PhaseReproduce:
		success.RequestedState = issueagent.StateReproduced
		success.RequestedAction = issueagent.ActionOpenDraftPR
		success.Reproduction = &issueagent.ReproductionClaim{
			Assertion:       "replace with the normalized business assertion",
			AssertionSHA256: "sha256:<64 lowercase hex for that assertion>",
			Topology:        task.RequiredTopology,
		}
	case issueagent.PhaseDiagnose:
		success.RequestedState = issueagent.StateDiagnosed
		success.RequestedAction = issueagent.ActionImplementFix
		intendedPath := "internal"
		if len(task.AllowedPaths) > 0 {
			intendedPath = task.AllowedPaths[0]
		}
		success.Diagnosis = &issueagent.Diagnosis{
			Summary:           "replace with the concise root cause",
			ExternalSymptom:   "replace with the observed external symptom",
			CausalPath:        "replace with the evidence-backed internal causal path",
			ViolatedInvariant: "replace with the violated invariant",
			EvidenceReferences: []string{
				"replace with a sorted reference to observed tool evidence",
			},
			EvidenceSHA256:   "",
			IntendedPaths:    []string{intendedPath},
			ClusterSemantics: "replace with the cluster-semantics argument",
			ValidationSuites: []string{"go-e2e", "go-fast"},
			RiskClasses:      []string{},
		}
	case issueagent.PhaseFix, issueagent.PhaseAddressReview:
		success.RequestedState = issueagent.StateValidating
		success.RequestedAction = issueagent.ActionValidate
	}
	successJSON, _ := json.Marshal(success)

	failed := success
	failed.Status = issueagent.ResultStatusFailed
	failed.RequestedState = issueagent.StateReadyForHuman
	failed.RequestedAction = issueagent.ActionWaitForHuman
	failed.Failure = &issueagent.Failure{
		Class:   issueagent.FailureNeedsInfo,
		Summary: "replace with a bounded, evidence-backed failure summary",
	}
	failed.Reproduction = nil
	failed.Diagnosis = nil
	failedJSON, _ := json.Marshal(failed)

	return fmt.Sprintf(`

MODEL PROPOSAL CONTRACT
Return every JSON field shown below and no unknown fields. Identity fields must
match exactly. The model MUST leave change_set.files and evidence.commands
empty, evidence.artifact_sha256 empty, and both usage token counts zero. The
trusted Worker derives repository changes and command evidence, binds the
diagnosis evidence digest, and injects provider-metered usage. Never invent
those values. Arrays that represent sets must be strictly sorted and unique.

Success template (replace descriptive placeholders and any assertion digest):
%s

Classified-failure template (class must be one of needs_info, already_fixed,
product_assertion, test_harness, worker_infrastructure, provider, unsafe_scope,
state_conflict, budget_exhausted, cancelled):
%s
`, successJSON, failedJSON)
}
