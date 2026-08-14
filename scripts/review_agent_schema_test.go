package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestReviewAgentPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		repoRoot(t), ".github", "review-agent", "policy.json",
	))
	require.NoError(t, err)

	var policy reviewAgentPolicy
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&policy))
	var trailing any
	require.Error(t, decoder.Decode(&trailing))

	require.Equal(t, 1, policy.SchemaVersion)
	require.Equal(t, []string{"main"}, policy.SupportedBaseBranches)
	require.Equal(t, "deepseek/deepseek-v4-flash", policy.Reviewer.Model)
	require.Equal(t, "high", policy.Reviewer.ReasoningEffort)
	require.Regexp(t, `^[0-9a-f]{40}$`, policy.Reviewer.ActionSHA)
	require.NotEmpty(t, policy.Reviewer.CodexVersion)
	require.True(t, policy.Reviewer.Ephemeral)
	require.Equal(t, 5400, policy.Reviewer.WallTimeSeconds)
	require.Equal(t, 3, policy.Concurrency.RepositorySessions)
	require.Equal(t, 1, policy.Concurrency.SessionsPerPullRequest)
	require.Equal(t, 1, policy.Concurrency.FirstTimeExternalSessions)
	require.Equal(t, 2, policy.Attempts.MaxReconsiderationsPerHead)
	require.Equal(t, 1, policy.Attempts.MaxInfrastructureRetries)
	require.Positive(t, policy.Interaction.MaxExplanationSessionsPerHead)
	require.Positive(t, policy.Interaction.MaxResponseBytesPerHead)
	require.Equal(t, reviewagent.MaxInlineComments, policy.Limits.MaxInlineComments)
	require.Equal(t, reviewagent.MaxFindings, policy.Limits.MaxFindings)
	require.Equal(t, reviewagent.MaxChangedFiles, policy.Limits.MaxChangedFiles)
	require.Equal(t, reviewagent.MaxChangedBytes, policy.Limits.MaxChangedBytes)
	require.Equal(t, reviewagent.MaxChangedLines, policy.Limits.MaxChangedLines)
	require.Equal(t, reviewagent.MaxContextBytes, policy.Limits.MaxContextBytes)
	require.Equal(t, 32768, policy.Limits.MaxModelOutputTokens)
	require.Equal(t, 240000, policy.Limits.MaxContextTokens)
	require.Equal(t, 216000, policy.Limits.AutoCompactTokens)
	require.Equal(t, 3600, policy.Limits.MaxCPUSecondsPerProcess)
	require.Equal(t, int64(8589934592), policy.Limits.MaxMemoryBytesPerProcess)
	require.Equal(t, 512, policy.Limits.MaxProcessesPerCommand)
	require.Equal(t, 128, policy.Limits.MaxConnectionsPerAddressFamily)
	require.Equal(
		t,
		int64(2147483648),
		policy.Limits.MaxNetworkBytesPerAddressFamily,
	)

	require.Equal(t, "review-agent-model", policy.Environments.Model)
	require.Equal(
		t,
		"review-agent-state-writer",
		policy.Environments.StateWriter,
	)
	require.Equal(
		t,
		"review-agent-publisher",
		policy.Environments.Publisher,
	)
	require.Equal(
		t,
		map[string]string{
			"checks":        "write",
			"contents":      "write",
			"issues":        "write",
			"metadata":      "read",
			"pull_requests": "write",
		},
		policy.Apps.Review.Permissions,
	)
	require.Equal(
		t,
		map[string]string{
			"contents": "write",
			"metadata": "read",
		},
		policy.Apps.StateWriter.Permissions,
	)
	require.Equal(t, "review-state/pr-", policy.State.PullRequestRefPrefix)
	require.Equal(
		t,
		".review-agent-state/pr-%d.json",
		policy.State.PullRequestPathTemplate,
	)
	require.Equal(t, "review-state/scheduler", policy.State.SchedulerRef)
	require.Equal(
		t,
		".review-agent-state/scheduler.json",
		policy.State.SchedulerPath,
	)
	require.NotEmpty(t, policy.TrustedChecks)
	for name, check := range policy.TrustedChecks {
		require.Regexp(t, `^[a-z0-9][a-z0-9_-]{0,63}$`, name)
		require.NotEmpty(t, check.Arguments)
		require.Positive(t, check.TimeoutSeconds)
		require.LessOrEqual(
			t,
			check.TimeoutSeconds,
			1800,
			"trusted check %q must fit the reviewer time slice",
			name,
		)
		require.Positive(t, check.MaxOutputBytes)
	}
	proxyCheck, ok := policy.TrustedChecks["review-proxy-contracts"]
	require.True(t, ok)
	require.Equal(
		t,
		[]string{
			"node", "--test",
			".github/review-agent/responses-budget-proxy.test.mjs",
		},
		proxyCheck.Arguments,
	)
	require.NotEmpty(t, policy.PathRules)
	var javascriptRule *reviewAgentPathRule
	var documentationRule *reviewAgentPathRule
	for index := range policy.PathRules {
		switch policy.PathRules[index].Name {
		case "review-agent-javascript":
			javascriptRule = &policy.PathRules[index]
		case "documentation-only":
			documentationRule = &policy.PathRules[index]
		}
	}
	require.NotNil(t, javascriptRule)
	require.Equal(t, []string{".github/review-agent/"}, javascriptRule.Prefixes)
	require.Equal(t, []string{".mjs"}, javascriptRule.Suffixes)
	require.Contains(t, javascriptRule.Checks, "review-proxy-contracts")
	require.NotNil(t, documentationRule)
	require.True(t, documentationRule.Exclusive)
	require.ElementsMatch(
		t,
		[]string{"README.md", "README_CN.md"},
		documentationRule.Paths,
	)
	require.Equal(t, []string{"docs-contracts"}, documentationRule.Checks)
	require.NotEmpty(t, policy.Network.BlockedCIDRs)
	require.Contains(t, policy.Credentials.Denied, "github")
	require.Contains(t, policy.Credentials.Denied, "cloud")
	require.Contains(t, policy.Credentials.Denied, "package_publish")

	for _, forbidden := range []string{
		`"rollout_mode"`,
		`"compatibility"`,
		`"legacy"`,
		`"labels"`,
		`"schedule"`,
		`"cron"`,
		`"arbitrary_command"`,
		`"governance"`,
		`"owner_logins"`,
		`"control_plane_paths"`,
	} {
		require.NotContains(t, string(raw), forbidden)
	}

	prompt, err := os.ReadFile(filepath.Join(
		repoRoot(t),
		".github",
		"review-agent",
		"prompts",
		"review.md",
	))
	require.NoError(t, err)
	for _, required := range []string{
		"review only",
		"complete changed-file inventory",
		"untrusted data",
		"approved",
		"changes_required",
		"inconclusive",
		"unresolved_uncertainty",
		"must not modify",
		"Check MCP",
		"advisory `FLOW.md`",
		"higher-authority source",
	} {
		require.Contains(t, string(prompt), required)
	}
}

func TestReviewAgentSchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		id       string
		schema   func(*testing.T) *jsonschema.Schema
	}{
		{
			name:     "review result",
			filename: "review-result.schema.json",
			id: "https://wukongim.github.io/schemas/" +
				"review-agent/review-result-v1.json",
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return reviewAgentSchemaFor[reviewagent.ReviewResult](t)
			},
		},
		{
			name:     "explanation result",
			filename: "explanation-result.schema.json",
			id: "https://wukongim.github.io/schemas/" +
				"review-agent/explanation-result-v1.json",
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return reviewAgentSchemaFor[reviewagent.ExplanationResult](t)
			},
		},
		{
			name:     "state",
			filename: "state.schema.json",
			id: "https://wukongim.github.io/schemas/" +
				"review-agent/state-v1.json",
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return reviewAgentSchemaFor[reviewagent.ReviewState](t)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := test.schema(t)
			schema.Schema = "https://json-schema.org/draft/2020-12/schema"
			schema.ID = test.id
			schema.Title = "WuKongIM Review Agent " + test.name + " v1"
			generated, marshalErr := json.MarshalIndent(schema, "", "  ")
			require.NoError(t, marshalErr)
			generated = append(generated, '\n')

			path := filepath.Join(
				repoRoot(t), ".github", "review-agent", test.filename,
			)
			if os.Getenv("UPDATE_REVIEW_AGENT_SCHEMAS") == "1" {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, generated, 0o644))
				return
			}
			committed, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, string(generated), string(committed))
		})
	}
}

func reviewAgentSchemaFor[T any](t *testing.T) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	require.NoError(t, err)
	hardenReviewAgentSchema(schema)
	return schema
}

func hardenReviewAgentSchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	for name, property := range schema.Properties {
		switch name {
		case "schema_version":
			value := any(1)
			property.Const = &value
		case "repository":
			property.Pattern = `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`
			setMaxLength(property, 256)
		case "pull_request", "generation", "sequence":
			setMinimum(property, 1)
		case "decision":
			property.Enum = stringValues(
				"approved", "changes_required", "inconclusive",
			)
		case "phase":
			property.Enum = stringValues(
				"awaiting_ready", "queued", "reviewing", "approved",
				"changes_required", "inconclusive", "canceled",
				"superseded", "closed",
			)
		case "decision_source":
			property.Enum = stringValues(
				"", "model", "merge_conflict", "policy", "infrastructure",
			)
		case "kind":
			property.Enum = stringValues("blocking", "advisory")
		case "status":
			property.Enum = stringValues("retained", "withdrawn")
		case "dimension":
			property.Enum = stringValues(
				"intent_correctness", "regression_tests",
				"security_runtime", "repository_constraints",
			)
		case "risk":
			property.Enum = stringValues("low", "medium", "high")
		case "summary":
			setMaxLength(property, reviewagent.MaxSummaryBytes)
			setMinLength(property, 1)
		case "reply":
			setMaxLength(property, reviewagent.MaxExplanationReplyBytes)
			setMinLength(property, 1)
		case "explanation_reply":
			setMaxLength(property, reviewagent.MaxExplanationReplyBytes)
		case "findings", "prior_findings":
			setMaximumItems(property, reviewagent.MaxFindings)
		case "prior_finding_dispositions":
			setMaximumItems(property, reviewagent.MaxFindings)
		case "file_assessments":
			property.Types = []string{"array"}
			setMaximumItems(property, reviewagent.MaxFileAssessments)
			setMinimumItems(property, 1)
		case "title":
			setMaxLength(property, reviewagent.MaxFindingTitleBytes)
			setMinLength(property, 1)
		case "path":
			setMaxLength(property, reviewagent.MaxResultPathBytes)
			setMinLength(property, 1)
		case "scenario", "impact", "resolution":
			setMaxLength(property, reviewagent.MaxFindingDetailBytes)
			setMinLength(property, 1)
		case "reason":
			setMaxLength(property, reviewagent.MaxSummaryBytes)
			setMinLength(property, 1)
		case "evidence":
			property.Types = []string{"array"}
			setMaximumItems(property, reviewagent.MaxFindingEvidence)
			setMinimumItems(property, 1)
			property.UniqueItems = true
			setMaxLength(property.Items, reviewagent.MaxFindingEvidenceBytes)
			setMinLength(property.Items, 1)
		case "sources":
			setMaximumItems(property, reviewagent.MaxSources)
			property.UniqueItems = true
			setMaxLength(property.Items, reviewagent.MaxSourceBytes)
			setMinLength(property.Items, 1)
		case "unresolved_uncertainty":
			setMaxLength(property, reviewagent.MaxSummaryBytes)
			property.Description = "Must be empty for approved; for inconclusive, " +
				"must describe the material uncertainty."
		case "interaction_request":
			setMaxLength(property, 4096)
		}
		if strings.HasSuffix(name, "_digest") {
			property.Pattern = `^(|sha256:[0-9a-f]{64})$`
		}
		if name == "intent_digest" || name == "finding_digest" {
			property.Pattern = `^sha256:[0-9a-f]{64}$`
		}
		if strings.HasSuffix(name, "_sha") {
			property.Pattern = `^[0-9a-f]{40}$`
		}
		hardenReviewAgentSchema(property)
	}
	for _, child := range schema.Defs {
		hardenReviewAgentSchema(child)
	}
	hardenReviewAgentSchema(schema.Items)
	for _, child := range schema.AnyOf {
		hardenReviewAgentSchema(child)
	}
	for _, child := range schema.OneOf {
		hardenReviewAgentSchema(child)
	}
	for _, child := range schema.AllOf {
		hardenReviewAgentSchema(child)
	}
}

func setMaximumItems(schema *jsonschema.Schema, value int) {
	schema.MaxItems = &value
}

func setMinimumItems(schema *jsonschema.Schema, value int) {
	schema.MinItems = &value
}

func setMinLength(schema *jsonschema.Schema, value int) {
	schema.MinLength = &value
}

type reviewAgentPolicy struct {
	SchemaVersion         int                         `json:"schema_version"`
	SupportedBaseBranches []string                    `json:"supported_base_branches"`
	Reviewer              reviewAgentReviewer         `json:"reviewer"`
	Concurrency           reviewAgentConcurrency      `json:"concurrency"`
	Attempts              reviewAgentAttempts         `json:"attempts"`
	Interaction           reviewAgentInteraction      `json:"interaction"`
	Limits                reviewAgentLimits           `json:"limits"`
	Environments          reviewAgentEnvironments     `json:"environments"`
	Apps                  reviewAgentApps             `json:"apps"`
	State                 reviewAgentStatePolicy      `json:"state"`
	Network               reviewAgentNetwork          `json:"network"`
	Credentials           reviewAgentCredentials      `json:"credentials"`
	TrustedChecks         map[string]reviewAgentCheck `json:"trusted_checks"`
	PathRules             []reviewAgentPathRule       `json:"path_rules"`
}

type reviewAgentReviewer struct {
	ActionSHA       string `json:"action_sha"`
	CodexVersion    string `json:"codex_version"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Sandbox         string `json:"sandbox"`
	Ephemeral       bool   `json:"ephemeral"`
	WallTimeSeconds int    `json:"wall_time_seconds"`
}

type reviewAgentConcurrency struct {
	RepositorySessions        int `json:"repository_sessions"`
	SessionsPerPullRequest    int `json:"sessions_per_pull_request"`
	FirstTimeExternalSessions int `json:"first_time_external_sessions"`
}

type reviewAgentAttempts struct {
	AutomaticPerHead           int `json:"automatic_per_head"`
	MaxReconsiderationsPerHead int `json:"max_reconsiderations_per_head"`
	MaxInfrastructureRetries   int `json:"max_infrastructure_retries"`
}

type reviewAgentInteraction struct {
	MaxExplanationSessionsPerHead int `json:"max_explanation_sessions_per_head"`
	MaxResponseBytesPerHead       int `json:"max_response_bytes_per_head"`
}

type reviewAgentLimits struct {
	MaxChangedFiles                 int   `json:"max_changed_files"`
	MaxChangedBytes                 int64 `json:"max_changed_bytes"`
	MaxChangedLines                 int64 `json:"max_changed_lines"`
	MaxContextBytes                 int64 `json:"max_context_bytes"`
	MaxModelResponseBytes           int   `json:"max_model_response_bytes"`
	MaxModelOutputTokens            int   `json:"max_model_output_tokens"`
	MaxContextTokens                int   `json:"max_context_tokens"`
	AutoCompactTokens               int   `json:"auto_compact_tokens"`
	MaxCPUSecondsPerProcess         int   `json:"max_cpu_seconds_per_process"`
	MaxMemoryBytesPerProcess        int64 `json:"max_memory_bytes_per_process"`
	MaxProcessesPerCommand          int   `json:"max_processes_per_command"`
	MaxConnectionsPerAddressFamily  int   `json:"max_connections_per_address_family"`
	MaxNetworkBytesPerAddressFamily int64 `json:"max_network_bytes_per_address_family"`
	MaxFindings                     int   `json:"max_findings"`
	MaxInlineComments               int   `json:"max_inline_comments"`
}

type reviewAgentEnvironments struct {
	Model       string `json:"model"`
	StateWriter string `json:"state_writer"`
	Publisher   string `json:"publisher"`
}

type reviewAgentApps struct {
	Review      reviewAgentApp `json:"review"`
	StateWriter reviewAgentApp `json:"state_writer"`
}

type reviewAgentApp struct {
	Slug        string            `json:"slug"`
	Login       string            `json:"login"`
	Permissions map[string]string `json:"permissions"`
}

type reviewAgentStatePolicy struct {
	PullRequestRefPrefix     string `json:"pull_request_ref_prefix"`
	PullRequestPathTemplate  string `json:"pull_request_path_template"`
	SchedulerRef             string `json:"scheduler_ref"`
	SchedulerPath            string `json:"scheduler_path"`
	RequireVerifiedSignature bool   `json:"require_verified_signature"`
}

type reviewAgentNetwork struct {
	PublicInternet bool     `json:"public_internet"`
	BlockedCIDRs   []string `json:"blocked_cidrs"`
	BlockedHosts   []string `json:"blocked_hosts"`
}

type reviewAgentCredentials struct {
	Denied []string `json:"denied"`
}

type reviewAgentCheck struct {
	Arguments      []string `json:"arguments"`
	WorkingDir     string   `json:"working_dir"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxOutputBytes int      `json:"max_output_bytes"`
}

type reviewAgentPathRule struct {
	Name      string   `json:"name"`
	Paths     []string `json:"paths,omitempty"`
	Prefixes  []string `json:"prefixes"`
	Suffixes  []string `json:"suffixes"`
	Checks    []string `json:"checks"`
	Exclusive bool     `json:"exclusive"`
}
