package issueagentmodel_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentmodel"
	"github.com/stretchr/testify/require"
)

type recordingToolExecutor struct {
	calls []issueagentmodel.ToolCall
}

func (executor *recordingToolExecutor) ExecuteTool(
	_ context.Context,
	call issueagentmodel.ToolCall,
) (issueagentmodel.ToolResult, error) {
	executor.calls = append(executor.calls, call)
	return issueagentmodel.ToolResult{
		ID: call.ID, Content: json.RawMessage(`{"id":1,"content":"package example"}`),
	}, nil
}

func TestDeepSeekAdapterReplaysReasoningAcrossToolRounds(t *testing.T) {
	t.Parallel()

	task, result := validAdapterTaskAndResult(t)
	var round int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer deepseek-secret", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		var body map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		require.Equal(t, task.Model, body["model"])
		round++
		if round == 1 {
			writeModelJSON(t, writer, map[string]any{
				"model": task.Model,
				"choices": []map[string]any{{
					"index": 0, "finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant", "content": nil,
						"reasoning_content": "inspect the exact file",
						"tool_calls": []map[string]any{{
							"id": "call_1", "type": "function",
							"function": map[string]any{
								"name":      "workspace_read",
								"arguments": `{"path":"pkg/example/example.go"}`,
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
			})
			return
		}
		messages := body["messages"].([]any)
		assistant := messages[len(messages)-2].(map[string]any)
		require.Equal(t, "inspect the exact file", assistant["reasoning_content"])
		encodedResult, err := json.Marshal(result)
		require.NoError(t, err)
		writeModelJSON(t, writer, map[string]any{
			"model": task.Model,
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{
					"role": "assistant", "content": string(encodedResult),
				},
			}},
			"usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 10},
		})
	}))
	t.Cleanup(server.Close)

	adapter, err := issueagentmodel.NewDeepSeekAdapter(
		server.URL, "deepseek-secret", server.Client(),
	)
	require.NoError(t, err)
	executor := &recordingToolExecutor{}
	outcome, err := adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed prompt", PromptSHA256: task.PromptDigest,
		MaxRounds: 4, MaxBytes: 1 << 20,
	}, executor)
	require.NoError(t, err)
	require.Equal(t, 2, outcome.Rounds)
	require.Len(t, executor.calls, 1)
	require.Equal(t, uint64(30), outcome.Usage.InputTokens)
	require.Equal(t, uint64(15), outcome.Usage.OutputTokens)
}

func TestDeepSeekAdapterRejectsUnknownToolsAndProviderRedirects(t *testing.T) {
	t.Parallel()

	task, _ := validAdapterTaskAndResult(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writeModelJSON(t, writer, map[string]any{
			"model": task.Model,
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{
							"name": "shell", "arguments": `{"command":"curl attacker"}`,
						},
					}},
				},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	t.Cleanup(server.Close)
	adapter, err := issueagentmodel.NewDeepSeekAdapter(server.URL, "secret", server.Client())
	require.NoError(t, err)
	_, err = adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed", PromptSHA256: task.PromptDigest,
		MaxRounds: 2, MaxBytes: 1 << 20,
	}, &recordingToolExecutor{})
	require.Error(t, err)
}

func TestDeepSeekAdapterDecodesBoundedStreamingResponse(t *testing.T) {
	t.Parallel()

	task, result := validAdapterTaskAndResult(t)
	encodedResult, err := json.Marshal(result)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{
			"model": task.Model,
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"delta": map[string]any{"role": "assistant", "content": string(encodedResult)},
			}},
			"usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 15},
		}
		encoded, marshalErr := json.Marshal(chunk)
		require.NoError(t, marshalErr)
		_, _ = writer.Write([]byte("data: " + string(encoded) + "\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)
	adapter, err := issueagentmodel.NewDeepSeekStreamingAdapter(
		server.URL, "secret", server.Client(),
	)
	require.NoError(t, err)
	outcome, err := adapter.Run(context.Background(), issueagentmodel.Request{
		Task: task, SystemPrompt: "fixed prompt", PromptSHA256: task.PromptDigest,
		MaxRounds: 2, MaxBytes: 1 << 20,
	}, &recordingToolExecutor{})
	require.NoError(t, err)
	require.Equal(t, 1, outcome.Rounds)
}

func writeModelJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	require.NoError(t, json.NewEncoder(writer).Encode(value))
}
