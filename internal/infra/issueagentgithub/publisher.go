package issueagentgithub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
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
	for _, file := range input.ChangeSet.Files {
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
		if matchesAnyPath(file.Path, input.ProtectedPaths, true) {
			return fmt.Errorf("file %q is protected from Issue Agent writes", file.Path)
		}
		if !matchesAnyPath(file.Path, input.AllowedPaths, false) {
			return fmt.Errorf("file %q is outside the current task scope", file.Path)
		}
		if path.Base(file.Path) == "AGENTS.md" {
			if input.ExistingPaths[file.Path] {
				return fmt.Errorf("existing instruction file %q is immutable", file.Path)
			}
			if !strings.HasPrefix(file.Path, "test/e2e/scenarios/") ||
				file.Operation != issueagentcontract.FileOperationUpsert ||
				len(input.ScenarioInstructionTemplate) == 0 ||
				!bytes.Equal(content, input.ScenarioInstructionTemplate) {
				return fmt.Errorf("new instruction file %q is not the trusted scenario template", file.Path)
			}
		}
		if frozen, ok := input.FrozenFileSHA256[file.Path]; ok &&
			!input.AllowReproductionReset {
			if file.Operation != issueagentcontract.FileOperationUpsert ||
				digestContent(content) != frozen {
				return fmt.Errorf("frozen reproduction file %q changed without reset", file.Path)
			}
		}
	}
	return nil
}

func matchesAnyPath(filePath string, candidates []string, caseInsensitive bool) bool {
	for _, candidate := range candidates {
		left, right := filePath, candidate
		if caseInsensitive {
			left, right = strings.ToLower(left), strings.ToLower(right)
		}
		if left == right || strings.HasPrefix(left, strings.TrimSuffix(right, "/")+"/") {
			return true
		}
	}
	return false
}

func digestContent(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
