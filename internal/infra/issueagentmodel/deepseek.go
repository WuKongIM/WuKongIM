package issueagentmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ProviderError is a redacted provider failure classification.
type ProviderError struct {
	Class     string
	Retryable bool
}

func (failure *ProviderError) Error() string {
	return "model provider request failed: " + failure.Class
}

// DeepSeekAdapter implements the OpenAI-compatible DeepSeek chat protocol.
type DeepSeekAdapter struct {
	endpoint *url.URL
	apiKey   string
	client   *http.Client
	stream   bool
}

// NewDeepSeekStreamingAdapter enables bounded SSE response decoding.
func NewDeepSeekStreamingAdapter(
	baseURL string,
	apiKey string,
	client *http.Client,
) (*DeepSeekAdapter, error) {
	adapter, err := NewDeepSeekAdapter(baseURL, apiKey, client)
	if err != nil {
		return nil, err
	}
	adapter.stream = true
	return adapter, nil
}

// NewDeepSeekAdapter validates the production endpoint or a loopback test server.
func NewDeepSeekAdapter(
	baseURL string,
	apiKey string,
	client *http.Client,
) (*DeepSeekAdapter, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Scheme != "https" && !loopbackModelEndpoint(endpoint)) ||
		(endpoint.Scheme == "https" &&
			(endpoint.Host != "api.deepseek.com" ||
				strings.TrimSuffix(endpoint.Path, "/") != "")) ||
		apiKey == "" || len(apiKey) > 4096 ||
		strings.ContainsAny(apiKey, "\r\n") || client == nil {
		return nil, errors.New("DeepSeek Adapter configuration is invalid")
	}
	cloned := *client
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("DeepSeek redirect rejected")
	}
	if cloned.Timeout == 0 {
		cloned.Timeout = 2 * time.Minute
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/chat/completions"
	return &DeepSeekAdapter{endpoint: endpoint, apiKey: apiKey, client: &cloned}, nil
}

type deepSeekToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type deepSeekMessage struct {
	Role             string             `json:"role"`
	Content          any                `json:"content,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCalls        []deepSeekToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
}

type deepSeekResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Index        int             `json:"index"`
		FinishReason string          `json:"finish_reason"`
		Message      deepSeekMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     uint64 `json:"prompt_tokens"`
		CompletionTokens uint64 `json:"completion_tokens"`
	} `json:"usage"`
}

// Run performs bounded DeepSeek tool rounds with local validation.
func (adapter *DeepSeekAdapter) Run(
	ctx context.Context,
	request Request,
	executor ToolExecutor,
) (Outcome, error) {
	if adapter == nil {
		return Outcome{}, errors.New("DeepSeek Adapter is nil")
	}
	if err := validateRequest(request, executor); err != nil {
		return Outcome{}, err
	}
	if request.Task.Provider != issueagent.ProviderDeepSeek {
		return Outcome{}, errors.New("DeepSeek Adapter task selects another provider")
	}
	taskJSON, err := json.Marshal(request.Task)
	if err != nil {
		return Outcome{}, errors.New("encode model task")
	}
	messages := []deepSeekMessage{
		{
			Role: "system",
			Content: request.SystemPrompt + modelProposalInstructions(request.Task) +
				"\nUse the declared API tools for tool rounds. On the final round, " +
				"return the direct model-proposal JSON object as message content.",
		},
		{Role: "user", Content: string(taskJSON)},
	}
	var inputTokens uint64
	var outputTokens uint64
	var reasoning strings.Builder
	for round := 1; round <= request.MaxRounds; round++ {
		response, err := adapter.complete(ctx, request, messages)
		if err != nil {
			return Outcome{}, err
		}
		inputTokens += response.Usage.PromptTokens
		outputTokens += response.Usage.CompletionTokens
		if len(response.Choices) != 1 || response.Choices[0].Index != 0 ||
			response.Model != request.Task.Model {
			return Outcome{}, errors.New("DeepSeek response identity is invalid")
		}
		choice := response.Choices[0]
		if reasoning.Len()+len(choice.Message.ReasoningContent) > int(request.MaxBytes) {
			return Outcome{}, errors.New("DeepSeek reasoning exceeds byte limit")
		}
		reasoning.WriteString(choice.Message.ReasoningContent)
		switch choice.FinishReason {
		case "tool_calls":
			if len(choice.Message.ToolCalls) == 0 ||
				len(choice.Message.ToolCalls) > 16 {
				return Outcome{}, errors.New("DeepSeek tool-call batch is invalid")
			}
			messages = append(messages, choice.Message)
			for _, providerCall := range choice.Message.ToolCalls {
				if providerCall.Type != "function" {
					return Outcome{}, errors.New("DeepSeek tool-call type is invalid")
				}
				call := ToolCall{
					ID: providerCall.ID, Name: providerCall.Function.Name,
					Arguments: json.RawMessage(providerCall.Function.Arguments),
				}
				if err := validateToolCall(call); err != nil {
					return Outcome{}, err
				}
				toolResult, err := executor.ExecuteTool(ctx, call)
				if err != nil {
					return Outcome{}, errors.New("tool broker rejected DeepSeek call")
				}
				if toolResult.ID != call.ID || len(toolResult.Content) == 0 ||
					len(toolResult.Content) > 1<<20 ||
					!json.Valid(toolResult.Content) {
					return Outcome{}, errors.New("tool broker result is invalid")
				}
				messages = append(messages, deepSeekMessage{
					Role: "tool", ToolCallID: call.ID,
					Content: string(toolResult.Content),
				})
			}
		case "stop":
			content, ok := choice.Message.Content.(string)
			if !ok || content == "" || int64(len(content)) > request.MaxBytes {
				return Outcome{}, errors.New("DeepSeek final content is invalid")
			}
			result, err := decodeModelProposal(
				[]byte(content), request.MaxBytes, request.Task,
			)
			if err != nil {
				return Outcome{}, err
			}
			result.Usage = issueagent.ModelUsage{
				Provider: issueagent.ProviderDeepSeek, Model: request.Task.Model,
				InputTokens: inputTokens, OutputTokens: outputTokens,
			}
			return Outcome{
				Result: result, Usage: result.Usage,
				ReasoningSHA256: digestText(reasoning.String()), Rounds: round,
			}, nil
		case "length":
			return Outcome{}, &ProviderError{Class: "output_limit"}
		default:
			return Outcome{}, errors.New("DeepSeek finish reason is invalid")
		}
	}
	return Outcome{}, errors.New("DeepSeek tool-round limit exhausted")
}

func (adapter *DeepSeekAdapter) complete(
	ctx context.Context,
	request Request,
	messages []deepSeekMessage,
) (deepSeekResponse, error) {
	body := struct {
		Model    string            `json:"model"`
		Messages []deepSeekMessage `json:"messages"`
		Tools    []any             `json:"tools"`
		Stream   bool              `json:"stream"`
	}{
		Model: request.Task.Model, Messages: messages,
		Tools: modelToolDefinitions(), Stream: adapter.stream,
	}
	encoded, err := json.Marshal(body)
	if err != nil || int64(len(encoded)) > request.MaxBytes {
		return deepSeekResponse{}, errors.New("encode DeepSeek request")
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, adapter.endpoint.String(), bytes.NewReader(encoded),
	)
	if err != nil {
		return deepSeekResponse{}, errors.New("create DeepSeek request")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+adapter.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	if adapter.stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	} else {
		httpRequest.Header.Set("Accept", "application/json")
	}
	response, err := adapter.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return deepSeekResponse{}, ctx.Err()
		}
		return deepSeekResponse{}, &ProviderError{Class: "network", Retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return deepSeekResponse{}, classifyProviderStatus(response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	expectedMediaType := "application/json"
	if adapter.stream {
		expectedMediaType = "text/event-stream"
	}
	if err != nil || mediaType != expectedMediaType {
		return deepSeekResponse{}, errors.New("DeepSeek response content type is invalid")
	}
	if adapter.stream {
		return decodeDeepSeekStream(response.Body, request.MaxBytes)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, request.MaxBytes+1))
	if err != nil || int64(len(responseBody)) > request.MaxBytes {
		return deepSeekResponse{}, errors.New("DeepSeek response exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	var decoded deepSeekResponse
	if err := decoder.Decode(&decoded); err != nil {
		return deepSeekResponse{}, errors.New("decode DeepSeek response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return deepSeekResponse{}, errors.New("DeepSeek response contains trailing JSON")
	}
	return decoded, nil
}

func classifyProviderStatus(status int) error {
	switch status {
	case http.StatusTooManyRequests:
		return &ProviderError{Class: "rate_limit", Retryable: true}
	case http.StatusPaymentRequired:
		return &ProviderError{Class: "quota"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &ProviderError{Class: "authentication"}
	default:
		return &ProviderError{
			Class: "http_" + fmt.Sprint(status), Retryable: status >= 500,
		}
	}
}

func loopbackModelEndpoint(endpoint *url.URL) bool {
	if endpoint.Scheme != "http" {
		return false
	}
	host := endpoint.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}
