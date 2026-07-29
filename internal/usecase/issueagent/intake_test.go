package issueagent_test

import (
	"strings"
	"testing"

	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestIntakeParsesMinimalBugFormWithoutInvokingExecution(t *testing.T) {
	t.Parallel()

	body := `### Affected version

v2.1.0

### Environment, topology, and client

Linux; three-node-cluster; Go SDK

### Reproduction steps

1. connect
2. send

### Expected and actual result

Expected one delivery; observed none.

### Frequency

Always

### Logs or configuration

No credentials included.
`
	plan, err := issueagentusecase.PlanIntake(body, nil)
	require.NoError(t, err)
	require.True(t, plan.Complete)
	require.Equal(t, "v2.1.0", plan.Form.AffectedVersion)
	require.Equal(t, []string{"needs-triage"}, plan.Labels)
	require.Empty(t, plan.Message)
	require.False(t, plan.InvokeModel)
	require.False(t, plan.ResolveVersion)
	require.False(t, plan.CreateBranch)
}

func TestIntakeAllowsAffectedVersionToDefaultAtAuthorization(t *testing.T) {
	t.Parallel()

	body := `### Affected version

_No response_

### Environment, topology, and client

Linux; HTTP API

### Reproduction steps

1. create channels under load
2. send personal messages

### Expected and actual result

Expected successful requests; observed low QPS and authority cache pressure.
`
	plan, err := issueagentusecase.PlanIntake(body, nil)
	require.NoError(t, err)
	require.True(t, plan.Complete)
	require.Empty(t, plan.Form.AffectedVersion)
	require.NotContains(t, plan.Missing, "affected version")
}

func TestIntakeRequestsOnlyMissingOrUnusableRequiredFields(t *testing.T) {
	t.Parallel()

	body := `### Affected version

latest

### Environment, topology, and client

Linux; single-node-cluster

### Reproduction steps

_No response_

### Expected and actual result

Expected success; got timeout.
`
	plan, err := issueagentusecase.PlanIntake(body, []string{
		"https://github.com/WuKongIM/WuKongIM/issues/1",
	})
	require.NoError(t, err)
	require.False(t, plan.Complete)
	require.Equal(t, []string{"needs-info"}, plan.Labels)
	require.Contains(t, plan.Missing, "affected version")
	require.Contains(t, plan.Missing, "reproduction steps")
	require.LessOrEqual(t, len(plan.Message), 4096)
	require.Contains(t, plan.Message, "possible duplicate")
	require.NotContains(t, strings.ToLower(plan.Message), "invalid report")
}
