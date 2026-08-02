package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

var reviewPolicySHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ReviewAgentPolicy is the complete protected cross-layer policy consumed by
// the standalone Review Agent composition root.
type ReviewAgentPolicy struct {
	SchemaVersion         int      `json:"schema_version"`
	SupportedBaseBranches []string `json:"supported_base_branches"`
	Reviewer              struct {
		ActionSHA       string `json:"action_sha"`
		CodexVersion    string `json:"codex_version"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		Sandbox         string `json:"sandbox"`
		Ephemeral       bool   `json:"ephemeral"`
		WallTimeSeconds int    `json:"wall_time_seconds"`
	} `json:"reviewer"`
	Concurrency struct {
		RepositorySessions        int `json:"repository_sessions"`
		SessionsPerPullRequest    int `json:"sessions_per_pull_request"`
		FirstTimeExternalSessions int `json:"first_time_external_sessions"`
	} `json:"concurrency"`
	Attempts struct {
		AutomaticPerHead           int `json:"automatic_per_head"`
		MaxReconsiderationsPerHead int `json:"max_reconsiderations_per_head"`
		MaxInfrastructureRetries   int `json:"max_infrastructure_retries"`
	} `json:"attempts"`
	Interaction struct {
		MaxExplanationSessionsPerHead int `json:"max_explanation_sessions_per_head"`
		MaxResponseBytesPerHead       int `json:"max_response_bytes_per_head"`
	} `json:"interaction"`
	Limits struct {
		MaxChangedFiles                 int   `json:"max_changed_files"`
		MaxChangedBytes                 int64 `json:"max_changed_bytes"`
		MaxChangedLines                 int64 `json:"max_changed_lines"`
		MaxContextBytes                 int64 `json:"max_context_bytes"`
		MaxModelResponseBytes           int64 `json:"max_model_response_bytes"`
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
	} `json:"limits"`
	Environments struct {
		Model       string `json:"model"`
		StateWriter string `json:"state_writer"`
		Publisher   string `json:"publisher"`
	} `json:"environments"`
	Apps struct {
		Review      ReviewAgentAppPolicy `json:"review"`
		StateWriter ReviewAgentAppPolicy `json:"state_writer"`
	} `json:"apps"`
	State struct {
		PullRequestRefPrefix     string `json:"pull_request_ref_prefix"`
		PullRequestPathTemplate  string `json:"pull_request_path_template"`
		SchedulerRef             string `json:"scheduler_ref"`
		SchedulerPath            string `json:"scheduler_path"`
		RequireVerifiedSignature bool   `json:"require_verified_signature"`
	} `json:"state"`
	Network struct {
		PublicInternet bool     `json:"public_internet"`
		BlockedCIDRs   []string `json:"blocked_cidrs"`
		BlockedHosts   []string `json:"blocked_hosts"`
	} `json:"network"`
	Credentials struct {
		Denied []string `json:"denied"`
	} `json:"credentials"`
	TrustedChecks map[string]verify.CheckPlan `json:"trusted_checks"`
	PathRules     []verify.PathRule           `json:"path_rules"`
}

// ReviewAgentAppPolicy records one protected App identity and exact permission
// map.
type ReviewAgentAppPolicy struct {
	Slug        string            `json:"slug"`
	Login       string            `json:"login"`
	Permissions map[string]string `json:"permissions"`
}

// LoadReviewAgentPolicy strictly reads and validates the protected policy.
func LoadReviewAgentPolicy(path string) (ReviewAgentPolicy, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return ReviewAgentPolicy{}, "", errors.New("open Review Agent policy")
	}
	defer file.Close()
	return DecodeReviewAgentPolicy(file, 2<<20)
}

// DecodeReviewAgentPolicy rejects unknown fields, trailing JSON, and impure
// authority profiles.
func DecodeReviewAgentPolicy(
	reader io.Reader,
	maxBytes int64,
) (ReviewAgentPolicy, string, error) {
	if reader == nil || maxBytes <= 0 {
		return ReviewAgentPolicy{}, "", errors.New(
			"Review Agent policy input is invalid",
		)
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return ReviewAgentPolicy{}, "", errors.New(
			"Review Agent policy exceeds byte limit",
		)
	}
	var document ReviewAgentPolicy
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return ReviewAgentPolicy{}, "", errors.New("decode Review Agent policy")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ReviewAgentPolicy{}, "", errors.New(
			"Review Agent policy contains trailing JSON",
		)
	}
	if err := ValidateReviewAgentPolicy(document); err != nil {
		return ReviewAgentPolicy{}, "", err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return ReviewAgentPolicy{}, "", errors.New("encode Review Agent policy")
	}
	sum := sha256.Sum256(canonical)
	return document, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ValidateReviewAgentPolicy enforces the fixed reviewer and authority split.
func ValidateReviewAgentPolicy(document ReviewAgentPolicy) error {
	if document.SchemaVersion != 1 ||
		!slices.Equal(document.SupportedBaseBranches, []string{"main"}) ||
		!reviewPolicySHAPattern.MatchString(document.Reviewer.ActionSHA) ||
		document.Reviewer.CodexVersion == "" ||
		document.Reviewer.Model != "moonshotai/kimi-k3" ||
		document.Reviewer.ReasoningEffort != "high" ||
		document.Reviewer.Sandbox != "read-only" ||
		!document.Reviewer.Ephemeral ||
		document.Reviewer.WallTimeSeconds != 5400 ||
		document.Concurrency.RepositorySessions != 3 ||
		document.Concurrency.SessionsPerPullRequest != 1 ||
		document.Concurrency.FirstTimeExternalSessions != 1 ||
		document.Attempts.AutomaticPerHead != 1 ||
		document.Attempts.MaxReconsiderationsPerHead != 2 ||
		document.Attempts.MaxInfrastructureRetries != 1 ||
		document.Interaction.MaxExplanationSessionsPerHead <= 0 ||
		document.Interaction.MaxResponseBytesPerHead <= 0 ||
		document.Limits.MaxChangedFiles != contract.MaxChangedFiles ||
		document.Limits.MaxChangedBytes != contract.MaxChangedBytes ||
		document.Limits.MaxChangedLines != contract.MaxChangedLines ||
		document.Limits.MaxContextBytes != contract.MaxContextBytes ||
		document.Limits.MaxModelResponseBytes <= 0 ||
		document.Limits.MaxModelOutputTokens != 32768 ||
		document.Limits.MaxContextTokens != 240000 ||
		document.Limits.AutoCompactTokens != 216000 ||
		document.Limits.MaxCPUSecondsPerProcess != 3600 ||
		document.Limits.MaxMemoryBytesPerProcess != 8589934592 ||
		document.Limits.MaxProcessesPerCommand != 512 ||
		document.Limits.MaxConnectionsPerAddressFamily != 128 ||
		document.Limits.MaxNetworkBytesPerAddressFamily != 2147483648 ||
		document.Limits.MaxFindings != contract.MaxFindings ||
		document.Limits.MaxInlineComments != 20 ||
		document.Environments.Model != "review-agent-model" ||
		document.Environments.StateWriter != "review-agent-state-writer" ||
		document.Environments.Publisher != "review-agent-publisher" ||
		document.State.PullRequestRefPrefix != "review-state/pr-" ||
		document.State.PullRequestPathTemplate !=
			".review-agent-state/pr-%d.json" ||
		document.State.SchedulerRef != "review-state/scheduler" ||
		document.State.SchedulerPath !=
			".review-agent-state/scheduler.json" ||
		!document.State.RequireVerifiedSignature ||
		!document.Network.PublicInternet ||
		len(document.TrustedChecks) == 0 ||
		len(document.PathRules) == 0 {
		return errors.New("Review Agent policy is invalid")
	}
	if !sameStringMap(document.Apps.Review.Permissions, map[string]string{
		"checks": "write", "contents": "write", "issues": "write",
		"metadata": "read", "pull_requests": "write",
	}) ||
		!sameStringMap(
			document.Apps.StateWriter.Permissions,
			map[string]string{
				"contents": "write", "metadata": "read",
			},
		) {
		return errors.New("Review Agent App permissions are invalid")
	}
	if !strings.HasSuffix(document.Apps.Review.Login, "[bot]") ||
		!strings.HasSuffix(document.Apps.StateWriter.Login, "[bot]") ||
		document.Apps.Review.Slug == "" ||
		document.Apps.StateWriter.Slug == "" {
		return errors.New("Review Agent App identity is invalid")
	}
	for _, plan := range document.TrustedChecks {
		if len(plan.Arguments) == 0 ||
			plan.TimeoutSeconds <= 0 ||
			plan.MaxOutputBytes <= 0 {
			return errors.New("Review Agent trusted check is invalid")
		}
	}
	return nil
}

// VerificationPolicy projects only immutable named-check routing.
func (document ReviewAgentPolicy) VerificationPolicy() verify.Policy {
	return verify.Policy{
		MaxChangedFiles: document.Limits.MaxChangedFiles,
		TrustedChecks:   cloneCheckPlans(document.TrustedChecks),
		PathRules:       append([]verify.PathRule(nil), document.PathRules...),
	}
}

func sameStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func cloneCheckPlans(
	source map[string]verify.CheckPlan,
) map[string]verify.CheckPlan {
	result := make(map[string]verify.CheckPlan, len(source))
	for name, plan := range source {
		plan.Arguments = append([]string(nil), plan.Arguments...)
		result[name] = plan
	}
	return result
}
