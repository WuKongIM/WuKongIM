package reviewagentverify

import (
	"errors"
	"slices"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// CheckPlan is one protected catalog command. The name, not these fields, is
// the only model-callable selector.
type CheckPlan struct {
	Arguments      []string `json:"arguments"`
	WorkingDir     string   `json:"working_dir"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
}

// PathRule maps trusted repository paths to mandatory named checks.
type PathRule struct {
	Name      string   `json:"name"`
	Paths     []string `json:"paths,omitempty"`
	Prefixes  []string `json:"prefixes"`
	Suffixes  []string `json:"suffixes"`
	Checks    []string `json:"checks"`
	Exclusive bool     `json:"exclusive"`
}

// Policy is the verification projection of protected policy.json.
type Policy struct {
	MaxChangedFiles int
	TrustedChecks   map[string]CheckPlan
	PathRules       []PathRule
}

// RiskSelection adds bounded expensive checks justified by actual change
// risk. It never removes deterministic minimum checks.
type RiskSelection struct {
	Race             bool
	Integration      bool
	E2E              bool
	ThreeNodeCluster bool
}

// PlanChecks returns the sorted complete named-check plan.
func PlanChecks(
	inventory Inventory,
	policy Policy,
	risk RiskSelection,
) ([]string, error) {
	if !inventory.Complete ||
		inventory.DeclaredFiles != len(inventory.Files) {
		return nil, errors.New("changed-file inventory is incomplete")
	}
	if len(inventory.Files) == 0 ||
		len(inventory.Files) > policy.MaxChangedFiles ||
		len(policy.TrustedChecks) == 0 ||
		len(policy.PathRules) == 0 {
		return nil, errors.New("invalid Review check policy input")
	}
	evaluationPaths := inventoryPaths(inventory.Files)
	selected := make(map[string]struct{})
	for _, rule := range policy.PathRules {
		if !rule.Exclusive {
			continue
		}
		all := true
		for _, repositoryPath := range evaluationPaths {
			if !matchesRule(repositoryPath, rule) {
				all = false
				break
			}
		}
		if all {
			if err := addChecks(selected, rule.Checks, policy); err != nil {
				return nil, err
			}
			return sortedCheckNames(selected), nil
		}
	}
	for _, repositoryPath := range evaluationPaths {
		for _, rule := range policy.PathRules {
			if rule.Exclusive || !matchesRule(repositoryPath, rule) {
				continue
			}
			if err := addChecks(selected, rule.Checks, policy); err != nil {
				return nil, err
			}
		}
	}
	riskChecks := []struct {
		selected bool
		name     string
	}{
		{risk.Race, "go-race"},
		{risk.Integration, "go-integration"},
		{risk.E2E, "go-e2e"},
		{risk.ThreeNodeCluster, "three-node-cluster"},
	}
	for _, check := range riskChecks {
		if check.selected {
			if err := addChecks(
				selected,
				[]string{check.name},
				policy,
			); err != nil {
				return nil, err
			}
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("changed paths have no mandatory check rule")
	}
	return sortedCheckNames(selected), nil
}

func inventoryPaths(files []contract.ChangedFile) []string {
	result := make([]string, 0, len(files)*2)
	for _, file := range files {
		result = append(result, file.Path)
		if file.PreviousPath != "" {
			result = append(result, file.PreviousPath)
		}
	}
	return result
}

func matchesRule(repositoryPath string, rule PathRule) bool {
	if slices.Contains(rule.Paths, repositoryPath) {
		return true
	}
	suffixMatch := len(rule.Suffixes) == 0
	for _, suffix := range rule.Suffixes {
		if strings.HasSuffix(repositoryPath, suffix) {
			suffixMatch = true
			break
		}
	}
	if len(rule.Prefixes) == 0 {
		return suffixMatch && len(rule.Suffixes) > 0
	}
	for _, prefix := range rule.Prefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		if (repositoryPath == prefix ||
			strings.HasPrefix(repositoryPath, prefix+"/")) &&
			suffixMatch {
			return true
		}
	}
	return false
}

func addChecks(
	selected map[string]struct{},
	checks []string,
	policy Policy,
) error {
	if len(checks) == 0 {
		return errors.New("path rule has no checks")
	}
	for _, check := range checks {
		if _, exists := policy.TrustedChecks[check]; !exists {
			return errors.New("path rule names an unknown trusted check")
		}
		selected[check] = struct{}{}
	}
	return nil
}

func sortedCheckNames(selected map[string]struct{}) []string {
	result := make([]string, 0, len(selected))
	for name := range selected {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}
