package issueagentmodel_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentmodel"
	"github.com/stretchr/testify/require"
)

type fakeCodexRoundRunner struct {
	responses []issueagentmodel.CodexRoundResponse
	requests  []issueagentmodel.CodexRoundRequest
	err       error
}

func (runner *fakeCodexRoundRunner) RunRound(
	_ context.Context,
	request issueagentmodel.CodexRoundRequest,
) (issueagentmodel.CodexRoundResponse, error) {
	runner.requests = append(runner.requests, request)
	if runner.err != nil {
		return issueagentmodel.CodexRoundResponse{}, runner.err
	}
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	return response, nil
}

func TestCodexAdapterUsesIsolatedStrictToolRounds(t *testing.T) {
	t.Parallel()

	task, result := validAdapterTaskAndResult(t)
	task.Provider = issueagent.ProviderCodex
	task.Model = "policy-codex-model"
	result = modelProposal(t, task, result)
	final, err := json.Marshal(issueagentmodel.CodexEnvelope{
		SchemaVersion: 1, Kind: "final", Result: &result,
	})
	require.NoError(t, err)
	first, err := json.Marshal(issueagentmodel.CodexEnvelope{
		SchemaVersion: 1, Kind: "tool_calls",
		ToolCalls: []issueagentmodel.ToolCall{{
			ID: "call_1", Name: "workspace_read",
			Arguments: json.RawMessage(`{"path":"pkg/example/example.go"}`),
		}},
	})
	require.NoError(t, err)
	runner := &fakeCodexRoundRunner{responses: []issueagentmodel.CodexRoundResponse{
		{Envelope: first, InputTokens: 10, OutputTokens: 5},
		{Envelope: final, InputTokens: 20, OutputTokens: 10},
	}}
	adapter, err := issueagentmodel.NewCodexAdapter(runner)
	require.NoError(t, err)
	executor := &recordingToolExecutor{}
	outcome, err := adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed prompt",
		PromptSHA256: task.PromptDigest, MaxRounds: 4, MaxBytes: 1 << 20,
	}, executor)
	require.NoError(t, err)
	require.Equal(t, 2, outcome.Rounds)
	require.Len(t, executor.calls, 1)
	require.Len(t, runner.requests, 2)
	require.Contains(t, runner.requests[1].Prompt, `"call_1"`)
	require.Equal(t, uint64(30), outcome.Usage.InputTokens)
	require.Equal(t, uint64(15), outcome.Usage.OutputTokens)
	require.Empty(t, outcome.Result.ChangeSet.Files)
	require.Empty(t, outcome.Result.Evidence.ArtifactSHA256)
	require.Contains(t, runner.requests[0].Prompt, "MODEL PROPOSAL CONTRACT")
}

func TestCodexAdapterDoesNotFallBackAfterMalformedEnvelope(t *testing.T) {
	t.Parallel()

	task, _ := validAdapterTaskAndResult(t)
	task.Provider = issueagent.ProviderCodex
	task.Model = "policy-codex-model"
	runner := &fakeCodexRoundRunner{responses: []issueagentmodel.CodexRoundResponse{{
		Envelope: []byte(`{"schema_version":1,"kind":"tool_calls","tool_calls":[{"id":"1","name":"shell","arguments":{}}]}`),
	}}}
	adapter, err := issueagentmodel.NewCodexAdapter(runner)
	require.NoError(t, err)
	_, err = adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed prompt",
		PromptSHA256: task.PromptDigest, MaxRounds: 2, MaxBytes: 1 << 20,
	}, &recordingToolExecutor{})
	require.Error(t, err)
	require.Len(t, runner.requests, 1)
}

func TestCodexAdapterPreservesSafeProviderFailure(t *testing.T) {
	t.Parallel()

	task, _ := validAdapterTaskAndResult(t)
	task.Provider = issueagent.ProviderCodex
	task.Model = "policy-codex-model"
	runner := &fakeCodexRoundRunner{err: &issueagentmodel.ProviderError{
		Class: "authentication",
	}}
	adapter, err := issueagentmodel.NewCodexAdapter(runner)
	require.NoError(t, err)

	_, err = adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed prompt",
		PromptSHA256: task.PromptDigest, MaxRounds: 2, MaxBytes: 1 << 20,
	}, &recordingToolExecutor{})
	var failure *issueagentmodel.ProviderError
	require.True(t, errors.As(err, &failure))
	require.Equal(t, "authentication", failure.Class)
	require.Len(t, runner.requests, 1)
}

func TestCodexAdapterRejectsModelAuthoredTrustedFields(t *testing.T) {
	t.Parallel()

	task, result := validAdapterTaskAndResult(t)
	task.Provider = issueagent.ProviderCodex
	task.Model = "policy-codex-model"
	result.Usage.Provider = task.Provider
	result.Usage.Model = task.Model
	final, err := json.Marshal(issueagentmodel.CodexEnvelope{
		SchemaVersion: 1, Kind: "final", Result: &result,
	})
	require.NoError(t, err)
	runner := &fakeCodexRoundRunner{responses: []issueagentmodel.CodexRoundResponse{{
		Envelope: final, InputTokens: 10, OutputTokens: 5,
	}}}
	adapter, err := issueagentmodel.NewCodexAdapter(runner)
	require.NoError(t, err)
	_, err = adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed prompt",
		PromptSHA256: task.PromptDigest, MaxRounds: 2, MaxBytes: 1 << 20,
	}, &recordingToolExecutor{})
	require.ErrorContains(t, err, "Worker-owned")
}
