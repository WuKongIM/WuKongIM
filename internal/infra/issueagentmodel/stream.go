package issueagentmodel

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func modelToolDefinitions() []any {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{
			"type": "object", "properties": properties,
			"required": required, "additionalProperties": false,
		}
	}
	function := func(name string, parameters map[string]any) any {
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name, "description": "WuKongIM Issue Agent broker tool",
				"parameters": parameters,
			},
		}
	}
	stringProperty := map[string]any{"type": "string", "maxLength": 4096}
	positiveInteger := map[string]any{"type": "integer", "minimum": 1}
	return []any{
		function("workspace_list", object(map[string]any{
			"path": stringProperty, "max_entries": positiveInteger,
		}, "path", "max_entries")),
		function("workspace_read", object(map[string]any{
			"path": stringProperty,
		}, "path")),
		function("workspace_search", object(map[string]any{
			"literal": map[string]any{"type": "string", "maxLength": 1024},
			"path":    stringProperty, "max_matches": positiveInteger,
		}, "literal", "path", "max_matches")),
		function("workspace_apply_patch", object(map[string]any{
			"path": stringProperty,
			"expected_sha256": map[string]any{
				"type": "string", "pattern": `^(|sha256:[0-9a-f]{64})$`,
			},
			"content_base64": map[string]any{
				"type": "string", "contentEncoding": "base64",
			},
		}, "path", "content_base64")),
		function("command_run", object(map[string]any{
			"argv": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 33,
				"items": stringProperty,
			},
			"working_dir":  stringProperty,
			"timeout_ms":   positiveInteger,
			"output_limit": positiveInteger,
		}, "argv", "working_dir", "timeout_ms", "output_limit")),
	}
}

func decodeDeepSeekStream(reader io.Reader, maxBytes int64) (deepSeekResponse, error) {
	if reader == nil || maxBytes <= 0 {
		return deepSeekResponse{}, errors.New("DeepSeek stream input is invalid")
	}
	scanner := bufio.NewScanner(io.LimitReader(reader, maxBytes+1))
	scanner.Buffer(make([]byte, 64<<10), int(maxBytes))
	var result deepSeekResponse
	var bytesRead int64
	var finishReason string
	toolCalls := make(map[int]*deepSeekToolCall)
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += int64(len(line)) + 1
		if bytesRead > maxBytes {
			return deepSeekResponse{}, errors.New("DeepSeek stream exceeds byte limit")
		}
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			return deepSeekResponse{}, errors.New("DeepSeek stream line is malformed")
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			done = true
			continue
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Index        int     `json:"index"`
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Role             string `json:"role"`
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     uint64 `json:"prompt_tokens"`
				CompletionTokens uint64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return deepSeekResponse{}, errors.New("decode DeepSeek stream chunk")
		}
		if chunk.Model != "" {
			if result.Model != "" && result.Model != chunk.Model {
				return deepSeekResponse{}, errors.New("DeepSeek stream changed model")
			}
			result.Model = chunk.Model
		}
		if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			result.Usage.PromptTokens = chunk.Usage.PromptTokens
			result.Usage.CompletionTokens = chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				return deepSeekResponse{}, errors.New("DeepSeek stream choice index is invalid")
			}
			if len(result.Choices) == 0 {
				result.Choices = make([]struct {
					Index        int             `json:"index"`
					FinishReason string          `json:"finish_reason"`
					Message      deepSeekMessage `json:"message"`
				}, 1)
				result.Choices[0].Index = 0
				result.Choices[0].Message.Role = "assistant"
			}
			message := &result.Choices[0].Message
			if choice.Delta.Content != "" {
				current, _ := message.Content.(string)
				message.Content = current + choice.Delta.Content
			}
			message.ReasoningContent += choice.Delta.ReasoningContent
			for _, streamedCall := range choice.Delta.ToolCalls {
				call := toolCalls[streamedCall.Index]
				if call == nil {
					call = &deepSeekToolCall{}
					toolCalls[streamedCall.Index] = call
				}
				if streamedCall.ID != "" {
					call.ID = streamedCall.ID
				}
				if streamedCall.Type != "" {
					call.Type = streamedCall.Type
				}
				call.Function.Name += streamedCall.Function.Name
				call.Function.Arguments += streamedCall.Function.Arguments
			}
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil || !done || len(result.Choices) != 1 ||
		finishReason == "" {
		return deepSeekResponse{}, errors.New("DeepSeek stream is truncated")
	}
	for index := 0; index < len(toolCalls); index++ {
		call := toolCalls[index]
		if call == nil {
			return deepSeekResponse{}, errors.New("DeepSeek stream tool calls are sparse")
		}
		result.Choices[0].Message.ToolCalls = append(
			result.Choices[0].Message.ToolCalls, *call,
		)
	}
	result.Choices[0].FinishReason = finishReason
	return result, nil
}
