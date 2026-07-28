package issueagentmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// CodexRoundRequest is one isolated, schema-bound Codex invocation.
type CodexRoundRequest struct {
	Model    string
	Prompt   string
	MaxBytes int64
}

// CodexRoundResponse is normalized output from one ephemeral Codex process.
type CodexRoundResponse struct {
	Envelope     []byte
	InputTokens  uint64
	OutputTokens uint64
}

// CodexRoundRunner invokes a fresh isolated Codex process for one round.
type CodexRoundRunner interface {
	RunRound(context.Context, CodexRoundRequest) (CodexRoundResponse, error)
}

// CodexEnvelope is the only model output shape accepted from Codex.
type CodexEnvelope struct {
	SchemaVersion    int                     `json:"schema_version"`
	Kind             string                  `json:"kind"`
	ReasoningSummary string                  `json:"reasoning_summary,omitempty"`
	ToolCalls        []ToolCall              `json:"tool_calls,omitempty"`
	Result           *issueagent.AgentResult `json:"result,omitempty"`
}

// CodexAdapter implements provider-neutral rounds over isolated Codex execs.
type CodexAdapter struct {
	runner CodexRoundRunner
}

// NewCodexAdapter constructs an Adapter with no fallback provider.
func NewCodexAdapter(runner CodexRoundRunner) (*CodexAdapter, error) {
	if runner == nil {
		return nil, errors.New("Codex round runner is unavailable")
	}
	return &CodexAdapter{runner: runner}, nil
}

// Run requests either typed broker calls or one strict final AgentResult.
func (adapter *CodexAdapter) Run(
	ctx context.Context,
	request Request,
	executor ToolExecutor,
) (Outcome, error) {
	if adapter == nil {
		return Outcome{}, errors.New("Codex Adapter is nil")
	}
	if err := validateRequest(request, executor); err != nil {
		return Outcome{}, err
	}
	if request.Task.Provider != issueagent.ProviderCodex {
		return Outcome{}, errors.New("Codex Adapter task selects another provider")
	}
	taskJSON, err := json.Marshal(request.Task)
	if err != nil {
		return Outcome{}, errors.New("encode Codex task")
	}
	transcript := make([]ToolResult, 0)
	var inputTokens uint64
	var outputTokens uint64
	var reasoning strings.Builder
	for round := 1; round <= request.MaxRounds; round++ {
		transcriptJSON, err := json.Marshal(transcript)
		if err != nil {
			return Outcome{}, errors.New("encode Codex tool transcript")
		}
		prompt := request.SystemPrompt +
			"\n\nReturn exactly one JSON envelope. Choose kind tool_calls to invoke only " +
			"the declared broker tools, or kind final with the complete AgentResult." +
			"\nTaskEnvelope:\n" + string(taskJSON) +
			"\nPrior tool results:\n" + string(transcriptJSON)
		if int64(len(prompt)) > request.MaxBytes {
			return Outcome{}, errors.New("Codex round prompt exceeds byte limit")
		}
		response, err := adapter.runner.RunRound(ctx, CodexRoundRequest{
			Model: request.Task.Model, Prompt: prompt, MaxBytes: request.MaxBytes,
		})
		if err != nil {
			if ctx.Err() != nil {
				return Outcome{}, ctx.Err()
			}
			return Outcome{}, &ProviderError{Class: "codex_process", Retryable: true}
		}
		inputTokens += response.InputTokens
		outputTokens += response.OutputTokens
		if int64(len(response.Envelope)) > request.MaxBytes {
			return Outcome{}, errors.New("Codex envelope exceeds byte limit")
		}
		var envelope CodexEnvelope
		decoder := json.NewDecoder(bytes.NewReader(response.Envelope))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&envelope); err != nil {
			return Outcome{}, errors.New("decode Codex envelope")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return Outcome{}, errors.New("Codex envelope contains trailing JSON")
		}
		if envelope.SchemaVersion != 1 ||
			len(envelope.ReasoningSummary) > 16<<10 ||
			reasoning.Len()+len(envelope.ReasoningSummary) > int(request.MaxBytes) {
			return Outcome{}, errors.New("Codex envelope is invalid")
		}
		reasoning.WriteString(envelope.ReasoningSummary)
		switch envelope.Kind {
		case "tool_calls":
			if envelope.Result != nil || len(envelope.ToolCalls) == 0 ||
				len(envelope.ToolCalls) > 16 {
				return Outcome{}, errors.New("Codex tool envelope is invalid")
			}
			for _, call := range envelope.ToolCalls {
				if err := validateToolCall(call); err != nil {
					return Outcome{}, err
				}
				result, err := executor.ExecuteTool(ctx, call)
				if err != nil {
					return Outcome{}, errors.New("tool broker rejected Codex call")
				}
				if result.ID != call.ID || len(result.Content) == 0 ||
					len(result.Content) > 1<<20 || !json.Valid(result.Content) {
					return Outcome{}, errors.New("Codex tool result is invalid")
				}
				transcript = append(transcript, result)
			}
		case "final":
			if envelope.Result == nil || len(envelope.ToolCalls) != 0 {
				return Outcome{}, errors.New("Codex final envelope is invalid")
			}
			envelope.Result.Usage = issueagent.ModelUsage{
				Provider: issueagent.ProviderCodex, Model: request.Task.Model,
				InputTokens: inputTokens, OutputTokens: outputTokens,
			}
			if err := issueagent.ValidateAgentResult(
				*envelope.Result, request.Task,
			); err != nil {
				return Outcome{}, err
			}
			return Outcome{
				Result: *envelope.Result, Usage: envelope.Result.Usage,
				ReasoningSHA256: digestText(reasoning.String()), Rounds: round,
			}, nil
		default:
			return Outcome{}, errors.New("Codex envelope kind is invalid")
		}
	}
	return Outcome{}, errors.New("Codex tool-round limit exhausted")
}

// CodexCLIConfig fixes one minimum-compatible Codex executable.
type CodexCLIConfig struct {
	Binary     string
	APIKey     string
	MinVersion string
	TempRoot   string
}

// CodexCLIRunner invokes real ephemeral Codex CLI processes.
type CodexCLIRunner struct {
	config CodexCLIConfig
}

var codexVersionPattern = regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)`)

// NewCodexCLIRunner validates the binary and its minimum version.
func NewCodexCLIRunner(config CodexCLIConfig) (*CodexCLIRunner, error) {
	if config.Binary == "" {
		config.Binary = "codex"
	}
	if config.APIKey == "" || len(config.APIKey) > 4096 ||
		strings.ContainsAny(config.APIKey, "\r\n") ||
		!codexVersionPattern.MatchString(config.MinVersion) {
		return nil, errors.New("Codex CLI configuration is invalid")
	}
	command := exec.Command(config.Binary, "--version")
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	output, err := command.Output()
	if err != nil || !versionAtLeast(
		codexVersionPattern.FindString(string(output)), config.MinVersion,
	) {
		return nil, errors.New("Codex CLI version is unavailable or too old")
	}
	return &CodexCLIRunner{config: config}, nil
}

// RunRound invokes Codex without user config, project rules, native tools, or persistence.
func (runner *CodexCLIRunner) RunRound(
	ctx context.Context,
	request CodexRoundRequest,
) (CodexRoundResponse, error) {
	if runner == nil || strings.TrimSpace(request.Model) == "" ||
		len(request.Prompt) == 0 || int64(len(request.Prompt)) > request.MaxBytes ||
		request.MaxBytes <= 0 {
		return CodexRoundResponse{}, errors.New("Codex CLI round is invalid")
	}
	tempRoot, err := os.MkdirTemp(runner.config.TempRoot, "wk-issue-agent-codex-*")
	if err != nil {
		return CodexRoundResponse{}, errors.New("create Codex temporary home")
	}
	defer os.RemoveAll(tempRoot)
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		return CodexRoundResponse{}, errors.New("secure Codex temporary home")
	}
	schemaPath := filepath.Join(tempRoot, "envelope.schema.json")
	outputPath := filepath.Join(tempRoot, "last-message.json")
	workspace := filepath.Join(tempRoot, "empty-workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return CodexRoundResponse{}, errors.New("create Codex empty workspace")
	}
	if err := os.WriteFile(schemaPath, codexEnvelopeSchema, 0o600); err != nil {
		return CodexRoundResponse{}, errors.New("write Codex output schema")
	}
	args := []string{
		"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules",
		"--strict-config", "--skip-git-repo-check",
		"--sandbox", "read-only",
		"-c", `approval_policy="never"`,
		"--disable", "shell_tool",
		"--disable", "unified_exec",
		"--disable", "apps",
		"--disable", "browser_use",
		"--disable", "computer_use",
		"--disable", "image_generation",
		"-C", workspace,
		"--model", request.Model,
		"--output-schema", schemaPath,
		"--output-last-message", outputPath,
		"--json", "-",
	}
	command := exec.CommandContext(ctx, runner.config.Binary, args...)
	command.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + tempRoot,
		"CODEX_HOME=" + tempRoot,
		"CODEX_API_KEY=" + runner.config.APIKey,
	}
	command.Stdin = strings.NewReader(request.Prompt)
	stdout := &boundedBuffer{limit: request.MaxBytes}
	stderr := &boundedBuffer{limit: 64 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return CodexRoundResponse{}, ctx.Err()
		}
		return CodexRoundResponse{}, errors.New("Codex CLI process failed")
	}
	envelope, err := os.ReadFile(outputPath)
	if err != nil || int64(len(envelope)) > request.MaxBytes {
		return CodexRoundResponse{}, errors.New("read Codex final envelope")
	}
	inputTokens, outputTokens := parseCodexUsage(stdout.Bytes())
	return CodexRoundResponse{
		Envelope: envelope, InputTokens: inputTokens, OutputTokens: outputTokens,
	}, nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (writer *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := writer.limit - int64(writer.buffer.Len())
	if remaining <= 0 {
		return original, nil
	}
	if int64(len(value)) > remaining {
		value = value[:remaining]
	}
	_, _ = writer.buffer.Write(value)
	return original, nil
}

func (writer *boundedBuffer) Bytes() []byte {
	return writer.buffer.Bytes()
}

func parseCodexUsage(events []byte) (uint64, uint64) {
	var input uint64
	var output uint64
	for _, line := range bytes.Split(events, []byte{'\n'}) {
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		accumulateUsage(event, &input, &output)
	}
	return input, output
}

func accumulateUsage(value any, input *uint64, output *uint64) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if number, ok := child.(float64); ok {
				switch key {
				case "input_tokens":
					*input += uint64(number)
				case "output_tokens":
					*output += uint64(number)
				}
			} else {
				accumulateUsage(child, input, output)
			}
		}
	case []any:
		for _, child := range typed {
			accumulateUsage(child, input, output)
		}
	}
}

func versionAtLeast(actual string, minimum string) bool {
	parse := func(value string) [3]int {
		match := codexVersionPattern.FindStringSubmatch(value)
		if len(match) != 4 {
			return [3]int{}
		}
		var result [3]int
		for index := range result {
			result[index], _ = strconv.Atoi(match[index+1])
		}
		return result
	}
	left, right := parse(actual), parse(minimum)
	for index := range left {
		if left[index] != right[index] {
			return left[index] > right[index]
		}
	}
	return true
}

var codexEnvelopeSchema = []byte(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "properties":{
    "schema_version":{"const":1},
    "kind":{"enum":["tool_calls","final"]},
    "reasoning_summary":{"type":"string","maxLength":16384},
    "tool_calls":{"type":"array","maxItems":16},
    "result":{"type":"object"}
  },
  "required":["schema_version","kind"],
  "additionalProperties":false
}`)
