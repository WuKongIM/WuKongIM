package issueagent

import (
	"errors"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// DriftDecision is a bounded response to moving main or external branch writes.
type DriftDecision string

const (
	DriftNone               DriftDecision = "none"
	DriftAlreadyFixedOnMain DriftDecision = "already_fixed_on_main"
	DriftMechanicalRebase   DriftDecision = "mechanical_rebase"
	DriftReadyForHuman      DriftDecision = "ready_for_human"
	DriftAwaitHeadAdoption  DriftDecision = "await_head_adoption"
)

// DriftFacts are freshly read branch and frozen-E2E facts.
type DriftFacts struct {
	ExpectedAgentHead string
	CurrentAgentHead  string
	CurrentMainSHA    string
	MainRuns          []RunObservation
	AssertionSHA256   string
	Topology          string
	Conflict          string
	ConflictAttempts  int
}

// PlanDriftRecovery never overwrites an unexpected branch head and permits at
// most one mechanical conflict attempt.
func PlanDriftRecovery(facts DriftFacts) (DriftDecision, error) {
	for _, sha := range []string{
		facts.ExpectedAgentHead, facts.CurrentAgentHead, facts.CurrentMainSHA,
	} {
		if !fullCommitPattern.MatchString(sha) {
			return DriftNone, errors.New("drift branch identity is invalid")
		}
	}
	if facts.CurrentAgentHead != facts.ExpectedAgentHead {
		return DriftAwaitHeadAdoption, nil
	}
	if len(facts.MainRuns) != 0 {
		if len(facts.MainRuns) != requiredReproductionRuns ||
			!scheduleDigestPattern.MatchString(facts.AssertionSHA256) {
			return DriftNone, errors.New("moving-main E2E evidence is incomplete")
		}
		for _, run := range facts.MainRuns {
			if run.SourceSHA != facts.CurrentMainSHA ||
				run.Outcome != RunPassed ||
				run.AssertionSHA256 != facts.AssertionSHA256 ||
				run.Topology != facts.Topology {
				return DriftNone, errors.New("moving-main E2E evidence is inconsistent")
			}
		}
		return DriftAlreadyFixedOnMain, nil
	}
	switch facts.Conflict {
	case "":
		return DriftNone, nil
	case "mechanical":
		if facts.ConflictAttempts == 0 {
			return DriftMechanicalRebase, nil
		}
		return DriftReadyForHuman, nil
	case "semantic":
		return DriftReadyForHuman, nil
	default:
		return DriftNone, errors.New("unknown drift conflict class")
	}
}

// AlreadyFixedProjection intentionally closes only the Agent Draft PR.
type AlreadyFixedProjection struct {
	CloseDraftPR bool
	CloseIssue   bool
	State        issueagentcontract.State
}

// ProjectAlreadyFixedOnMain preserves maintainer ownership of Issue closure.
func ProjectAlreadyFixedOnMain() AlreadyFixedProjection {
	return AlreadyFixedProjection{
		CloseDraftPR: true, CloseIssue: false,
		State: issueagentcontract.StateAlreadyFixed,
	}
}
