package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestArtifactWorkBoundaryClassifiesCompleteFactsInSafetyOrder(t *testing.T) {
	t.Parallel()

	signedHead := "0123456789abcdef0123456789abcdef01234567"
	changedHead := "89abcdef0123456789abcdef0123456789abcdef"
	work := issueagent.Work{
		Branch: "agent/issue-42", HeadSHA: signedHead, PRNumber: 9,
	}
	head := issueagentusecase.WorkHeadFacts{
		PRNumber: 9, HeadSHA: signedHead, PRState: "open",
		Draft: true, BaseRef: "main", HeadRef: work.Branch,
	}

	operation, err := issueagentusecase.PlanArtifactWorkBoundary(
		work, head, false,
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.ArtifactWorkContinue, operation)

	head.Draft = false
	operation, err = issueagentusecase.PlanArtifactWorkBoundary(
		work, head, false,
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.ArtifactWorkRepairProjection, operation)

	head.HeadSHA = changedHead
	operation, err = issueagentusecase.PlanArtifactWorkBoundary(
		work, head, false,
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.ArtifactWorkRecordBranchDrift, operation)

	operation, err = issueagentusecase.PlanArtifactWorkBoundary(
		work, head, true,
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.ArtifactWorkVerifyPendingEffect, operation)

	head.BaseRef = "release"
	operation, err = issueagentusecase.PlanArtifactWorkBoundary(
		work, head, true,
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.ArtifactWorkRecordObjectDrift, operation)
}
