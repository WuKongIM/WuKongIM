package reviewagentverify

import (
	"errors"
	"path"
	"slices"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// BaseInstruction is one trusted instruction blob read from the exact
// base/control tree.
type BaseInstruction struct {
	Path    string
	BlobSHA string
	Content []byte
}

// DiscoverInstructions returns only base-tree instructions whose directory
// scopes at least one changed path.
func DiscoverInstructions(
	changedPaths []string,
	catalog []BaseInstruction,
) ([]contract.InstructionBlob, error) {
	if len(changedPaths) == 0 {
		return nil, errors.New("instruction discovery lacks changed paths")
	}
	for _, changedPath := range changedPaths {
		if !validPath(changedPath) {
			return nil, errors.New("instruction discovery has invalid changed path")
		}
	}
	result := make([]contract.InstructionBlob, 0, len(catalog))
	seen := make(map[string]struct{}, len(catalog))
	for _, instruction := range catalog {
		if !validPath(instruction.Path) ||
			len(instruction.BlobSHA) != 40 ||
			len(instruction.Content) == 0 {
			return nil, errors.New("invalid base instruction")
		}
		base := path.Base(instruction.Path)
		if base != "AGENTS.md" && base != "FLOW.md" {
			return nil, errors.New("unsupported base instruction name")
		}
		if _, exists := seen[instruction.Path]; exists {
			return nil, errors.New("duplicate base instruction")
		}
		seen[instruction.Path] = struct{}{}
		scope := path.Dir(instruction.Path)
		applicable := false
		for _, changedPath := range changedPaths {
			if scope == "." ||
				changedPath == scope ||
				strings.HasPrefix(changedPath, scope+"/") {
				applicable = true
				break
			}
		}
		if !applicable {
			continue
		}
		result = append(result, contract.InstructionBlob{
			Path:       instruction.Path,
			Scope:      scope,
			BlobSHA:    instruction.BlobSHA,
			BlobDigest: bytesDigest(instruction.Content),
			Content:    string(instruction.Content),
		})
	}
	slices.SortFunc(
		result,
		func(left, right contract.InstructionBlob) int {
			return strings.Compare(left.Path, right.Path)
		},
	)
	return result, nil
}
