package issueagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

const (
	RiskPublicProtocol   = "public_protocol_api"
	RiskPersistentFormat = "persistent_format_migration"
	RiskConsensus        = "raft_quorum_consistency"
	RiskSecurity         = "authentication_authorization_cryptography"
	RiskDependency       = "external_dependency"
	RiskConfigDefault    = "configuration_default"
	RiskProtectedAgent   = "protected_agent_path"
	RiskScopeExpansion   = "scope_expansion"
)

// RiskInput is a closed set of trusted diff facts. Model prose cannot lower it.
type RiskInput struct {
	Paths                   []string
	PublicProtocolChanged   bool
	PersistentFormatChanged bool
	ConsensusChanged        bool
	SecurityChanged         bool
	DependencyAdded         bool
	ConfigDefaultChanged    bool
	ScopeExpanded           bool
}

// RiskDecision determines whether a second authorization or human-only work is required.
type RiskDecision struct {
	Classes                     []string
	RequiresSecondAuthorization bool
	HumanOnly                   bool
}

// ClassifyRisk applies the protected policy vocabulary to trusted diff facts.
func ClassifyRisk(input RiskInput) (RiskDecision, error) {
	if len(input.Paths) == 0 || len(input.Paths) > 128 ||
		!slices.IsSorted(input.Paths) {
		return RiskDecision{}, errors.New("risk path inventory is invalid")
	}
	classes := make([]string, 0, 8)
	add := func(enabled bool, class string) {
		if enabled {
			classes = append(classes, class)
		}
	}
	add(input.PublicProtocolChanged, RiskPublicProtocol)
	add(input.PersistentFormatChanged, RiskPersistentFormat)
	add(input.ConsensusChanged, RiskConsensus)
	add(input.SecurityChanged, RiskSecurity)
	add(input.DependencyAdded, RiskDependency)
	add(input.ConfigDefaultChanged, RiskConfigDefault)
	add(input.ScopeExpanded, RiskScopeExpansion)
	humanOnly := false
	for index, filePath := range input.Paths {
		if filePath == "" || path.Clean(filePath) != filePath ||
			strings.HasPrefix(filePath, "../") ||
			index > 0 && input.Paths[index-1] == filePath {
			return RiskDecision{}, errors.New("risk path inventory contains an unsafe path")
		}
		if humanOnlyIssueAgentPath(filePath) {
			humanOnly = true
			classes = append(classes, RiskProtectedAgent)
		}
	}
	slices.Sort(classes)
	classes = slices.Compact(classes)
	return RiskDecision{
		Classes: classes, RequiresSecondAuthorization: len(classes) > 0,
		HumanOnly: humanOnly,
	}, nil
}

func humanOnlyIssueAgentPath(filePath string) bool {
	filePath = strings.ToLower(filePath)
	if path.Base(filePath) == "agents.md" {
		return true
	}
	for _, protected := range []string{
		".agents", ".github/ISSUE_TEMPLATE", ".github/issue-agent",
		".github/workflows", "cmd/wkissueagent",
		"internal/access/issueagentcli", "internal/app/issue_agent",
		"internal/contracts/issueagent", "internal/infra/issueagentgithub",
		"internal/infra/issueagentmodel", "internal/runtime/issueagentworker",
		"internal/usecase/issueagent", "scripts/issue_agent",
	} {
		if strings.HasPrefix(filePath, protected) {
			return true
		}
	}
	return false
}

// ValidationRequest is an exact request for the existing Agent PR Validation Gate.
type ValidationRequest struct {
	Labels []string
	Body   string
	Suites []string
	Risk   string
}

type validationPlanJSON struct {
	SchemaVersion  int      `json:"schema_version"`
	HeadSHA        string   `json:"head_sha"`
	Risk           string   `json:"risk"`
	SelectedSuites []string `json:"selected_suites"`
	Reason         string   `json:"reason"`
	RetryOfRunID   *int64   `json:"retry_of_run_id"`
}

// BuildValidationRequest always selects the required fast and E2E gates and
// adds bounded heavy suites from deterministic risk classes.
func BuildValidationRequest(
	headSHA string,
	riskClasses []string,
) (ValidationRequest, error) {
	if !fullCommitPattern.MatchString(headSHA) ||
		!slices.IsSorted(riskClasses) {
		return ValidationRequest{}, errors.New("validation request identity is invalid")
	}
	suites := []string{"go-e2e", "go-fast"}
	for index, class := range riskClasses {
		if class == "" || index > 0 && riskClasses[index-1] == class {
			return ValidationRequest{}, errors.New("validation risk classes are invalid")
		}
		switch class {
		case RiskConsensus, RiskSecurity:
			suites = append(suites, "go-integration", "go-race", "three-node-smoke")
		case RiskPersistentFormat, RiskConfigDefault:
			suites = append(suites, "go-integration")
		case RiskPublicProtocol:
			suites = append(suites, "three-node-smoke")
		case RiskDependency, RiskScopeExpansion:
			suites = append(suites, "go-race")
		case RiskProtectedAgent:
			return ValidationRequest{},
				errors.New("human-only Agent paths cannot request automatic validation")
		default:
			return ValidationRequest{}, errors.New("validation risk class is unknown")
		}
	}
	slices.Sort(suites)
	suites = slices.Compact(suites)
	risk := "medium"
	if len(riskClasses) > 0 {
		risk = "high"
	}
	reason := "Issue Agent requires go-fast and go-e2e"
	if len(riskClasses) > 0 {
		reason += "; deterministic risk classes: " + strings.Join(riskClasses, ",")
	}
	plan := validationPlanJSON{
		SchemaVersion: 1, HeadSHA: headSHA, Risk: risk,
		SelectedSuites: suites, Reason: reason,
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return ValidationRequest{}, errors.New("encode validation plan")
	}
	body := "<!-- agent-validation-plan:v1\n" + string(encoded) +
		"\n-->\n\n## Agent validation plan\n\n" + reason + "."
	labels := make([]string, 0, len(suites)+1)
	for _, suite := range suites {
		labels = append(labels, "agent-ci/"+suite)
	}
	labels = append(labels, "agent-ci/run")
	slices.Sort(labels)
	if len(body) > 4096 {
		return ValidationRequest{}, fmt.Errorf("validation plan exceeds bound")
	}
	return ValidationRequest{
		Labels: labels, Body: body, Suites: suites, Risk: risk,
	}, nil
}
