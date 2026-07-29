package issueagentgithub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// PublishValidation is the trusted, current-snapshot policy for one ChangeSet.
type PublishValidation struct {
	IssueNumber                 int64
	Branch                      string
	BaseBranch                  string
	ExpectedParentSHA           string
	ChangeSet                   issueagentcontract.ChangeSet
	Limits                      issueagentcontract.ChangeSetLimits
	ProtectedPaths              []string
	AllowedPaths                []string
	ExistingPaths               map[string]bool
	FrozenFileSHA256            map[string]string
	ImmutablePaths              []string
	AllowReproductionReset      bool
	ScenarioInstructionTemplate []byte
}

// ValidatePublish rejects every ChangeSet that widens the current trusted plan.
func ValidatePublish(input PublishValidation) error {
	expectedBranch := fmt.Sprintf("agent/issue-%d", input.IssueNumber)
	if input.IssueNumber <= 0 || input.Branch != expectedBranch ||
		input.BaseBranch != "main" ||
		!gitObjectPattern.MatchString(input.ExpectedParentSHA) {
		return errors.New("Publisher repository or branch scope is invalid")
	}
	if err := issueagentcontract.ValidateChangeSet(input.ChangeSet, input.Limits); err != nil {
		return err
	}
	expectedInstructionsPath := scenarioInstructionsPath(input.IssueNumber)
	foundExpectedInstructions := false
	for _, file := range input.ChangeSet.Files {
		if matchesAnyPath(file.Path, input.ImmutablePaths, false, false) &&
			!input.AllowReproductionReset {
			return fmt.Errorf("frozen reproduction path %q is immutable", file.Path)
		}
		var content []byte
		if file.Operation == issueagentcontract.FileOperationUpsert {
			var err error
			content, err = issueagentcontract.DecodeFileContent(file)
			if err != nil {
				return err
			}
		}
		if file.Operation == issueagentcontract.FileOperationUpsert &&
			file.Mode != issueagentcontract.FileModeRegular {
			return fmt.Errorf("Publisher rejects unexpected executable mode for %q", file.Path)
		}
		if matchesAnyPath(file.Path, input.ProtectedPaths, true, true) {
			return fmt.Errorf("file %q is protected from Issue Agent writes", file.Path)
		}
		if !matchesAnyPath(file.Path, input.AllowedPaths, false, false) {
			return fmt.Errorf("file %q is outside the current task scope", file.Path)
		}
		if path.Base(file.Path) == "AGENTS.md" {
			if input.ExistingPaths[file.Path] {
				return fmt.Errorf("existing instruction file %q is immutable", file.Path)
			}
			if file.Path != expectedInstructionsPath ||
				file.Operation != issueagentcontract.FileOperationUpsert ||
				len(input.ScenarioInstructionTemplate) == 0 ||
				!bytes.Equal(content, input.ScenarioInstructionTemplate) {
				return fmt.Errorf("new instruction file %q is not the trusted scenario template", file.Path)
			}
			foundExpectedInstructions = true
		}
		if frozen, ok := input.FrozenFileSHA256[file.Path]; ok &&
			!input.AllowReproductionReset {
			if file.Operation != issueagentcontract.FileOperationUpsert ||
				digestContent(content) != frozen {
				return fmt.Errorf("frozen reproduction file %q changed without reset", file.Path)
			}
		}
	}
	if len(input.ScenarioInstructionTemplate) != 0 && !foundExpectedInstructions {
		return errors.New("trusted scenario instruction file is missing")
	}
	return nil
}

// InjectScenarioInstructions replaces any model-authored scenario instructions
// with the exact trusted Publisher template, or appends the template when the
// Worker omitted it.
func InjectScenarioInstructions(
	changeSet issueagentcontract.ChangeSet,
	issueNumber int64,
	template []byte,
) (issueagentcontract.ChangeSet, error) {
	if issueNumber <= 0 || len(template) == 0 {
		return issueagentcontract.ChangeSet{},
			errors.New("trusted scenario instruction template is invalid")
	}
	result := issueagentcontract.ChangeSet{
		Files: append([]issueagentcontract.FileChange(nil), changeSet.Files...),
	}
	expectedPath := scenarioInstructionsPath(issueNumber)
	injected := issueagentcontract.FileChange{
		Path:          expectedPath,
		Operation:     issueagentcontract.FileOperationUpsert,
		Mode:          issueagentcontract.FileModeRegular,
		ContentBase64: issueagentcontract.EncodeFileContent(template),
	}
	found := false
	for index := range result.Files {
		if result.Files[index].Path != expectedPath {
			continue
		}
		if found {
			return issueagentcontract.ChangeSet{},
				errors.New("scenario instruction path is duplicated")
		}
		result.Files[index] = injected
		found = true
	}
	if !found {
		result.Files = append(result.Files, injected)
	}
	slices.SortFunc(result.Files, func(left, right issueagentcontract.FileChange) int {
		return strings.Compare(left.Path, right.Path)
	})
	return result, nil
}

func scenarioInstructionsPath(issueNumber int64) string {
	return fmt.Sprintf("test/e2e/issue_agent/issue_%d/AGENTS.md", issueNumber)
}

func matchesAnyPath(
	filePath string,
	candidates []string,
	caseInsensitive bool,
	matchFileStem bool,
) bool {
	for _, candidate := range candidates {
		left, right := filePath, candidate
		if caseInsensitive {
			left, right = strings.ToLower(left), strings.ToLower(right)
		}
		right = strings.TrimSuffix(right, "/")
		if left == right || strings.HasPrefix(left, right+"/") ||
			matchFileStem && strings.HasPrefix(left, right) {
			return true
		}
	}
	return false
}

func digestContent(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
