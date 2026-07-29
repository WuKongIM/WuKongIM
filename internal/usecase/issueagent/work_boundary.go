package issueagent

import (
	"errors"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ArtifactWorkOperation is the only action an Artifact Publisher may take
// after re-reading the complete current Agent branch and PR projection.
type ArtifactWorkOperation string

const (
	ArtifactWorkContinue            ArtifactWorkOperation = "continue"
	ArtifactWorkVerifyPendingEffect ArtifactWorkOperation = "verify_pending_effect"
	ArtifactWorkRepairProjection    ArtifactWorkOperation = "repair_projection"
	ArtifactWorkRecordBranchDrift   ArtifactWorkOperation = "record_branch_drift"
	ArtifactWorkRecordObjectDrift   ArtifactWorkOperation = "record_work_drift"
)

// PlanArtifactWorkBoundary gives structural work drift priority over branch
// identity, and branch identity priority over reversible Draft projection.
func PlanArtifactWorkBoundary(
	work issueagentcontract.Work,
	head WorkHeadFacts,
	hasPendingCommit bool,
) (ArtifactWorkOperation, error) {
	if work.Branch == "" || !fullCommitPattern.MatchString(work.HeadSHA) ||
		head.PRNumber != work.PRNumber ||
		!fullCommitPattern.MatchString(head.HeadSHA) {
		return "", errors.New("Artifact work boundary facts are invalid")
	}
	if work.PRNumber == 0 {
		if head.PRState != "" || head.BaseRef != "" || head.HeadRef != "" {
			return "", errors.New("branch-only work has pull request facts")
		}
		if head.HeadSHA != work.HeadSHA {
			if hasPendingCommit {
				return ArtifactWorkVerifyPendingEffect, nil
			}
			return ArtifactWorkRecordBranchDrift, nil
		}
		return ArtifactWorkContinue, nil
	}
	if head.PRState != "open" || head.BaseRef != "main" ||
		head.HeadRef != work.Branch {
		return ArtifactWorkRecordObjectDrift, nil
	}
	if head.HeadSHA != work.HeadSHA {
		if hasPendingCommit {
			return ArtifactWorkVerifyPendingEffect, nil
		}
		return ArtifactWorkRecordBranchDrift, nil
	}
	if !head.Draft {
		return ArtifactWorkRepairProjection, nil
	}
	return ArtifactWorkContinue, nil
}
