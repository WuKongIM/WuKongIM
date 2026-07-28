package issueagentmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// Request is one provider-neutral bounded model attempt.
type Request struct {
	Task         issueagent.TaskEnvelope
	SystemPrompt string
	PromptSHA256 string
	MaxRounds    int
	MaxBytes     int64
}

// ToolCall is one provider-selected invocation of the closed broker catalog.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult is one broker response replayed to the selected provider.
type ToolResult struct {
	ID      string
	Content json.RawMessage
	IsError bool
}

// ToolExecutor invokes only locally validated provider-neutral tools.
type ToolExecutor interface {
	ExecuteTool(context.Context, ToolCall) (ToolResult, error)
}

// Outcome is one strict AgentResult plus normalized provider usage.
type Outcome struct {
	Result          issueagent.AgentResult
	Usage           issueagent.ModelUsage
	ReasoningSHA256 string
	Rounds          int
}

// Adapter executes exactly one selected provider; it never falls back.
type Adapter interface {
	Run(context.Context, Request, ToolExecutor) (Outcome, error)
}

var (
	toolCallIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	toolNames         = map[string]struct{}{
		"workspace_list":        {},
		"workspace_read":        {},
		"workspace_search":      {},
		"workspace_apply_patch": {},
		"command_run":           {},
	}
)

func validateRequest(request Request, executor ToolExecutor) error {
	if err := issueagent.ValidateTaskEnvelope(request.Task); err != nil {
		return err
	}
	if executor == nil || request.SystemPrompt == "" ||
		len(request.SystemPrompt) > 128<<10 ||
		request.PromptSHA256 != request.Task.PromptDigest ||
		digestText(request.SystemPrompt) != request.PromptSHA256 ||
		request.MaxRounds <= 0 || request.MaxRounds > 32 ||
		request.MaxBytes <= 0 || request.MaxBytes > 16<<20 {
		return errors.New("model Adapter request is invalid")
	}
	return nil
}

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateToolCall(call ToolCall) error {
	if !toolCallIDPattern.MatchString(call.ID) {
		return errors.New("provider tool-call ID is invalid")
	}
	if _, ok := toolNames[call.Name]; !ok {
		return errors.New("provider requested an unknown tool")
	}
	if len(call.Arguments) == 0 || len(call.Arguments) > 64<<10 ||
		!json.Valid(call.Arguments) {
		return errors.New("provider tool-call arguments are invalid")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &object); err != nil || object == nil {
		return errors.New("provider tool-call arguments must be an object")
	}
	return nil
}
