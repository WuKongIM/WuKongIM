package issueagent_test

import (
	"testing"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestLifecycleLabelProjectionRetiresTerminalIssues(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"bug"}, issueagentusecase.ProjectLifecycleLabels(
		issueagentcontract.StateMerged,
		[]string{"ready-for-human", "bug", "ready-for-agent"},
	))
	require.Equal(t, []string{"bug", "ready-for-human"},
		issueagentusecase.ProjectLifecycleLabels(
			issueagentcontract.StateReadyForHuman,
			[]string{"bug", "ready-for-human", "ready-for-human"},
		),
	)
}
