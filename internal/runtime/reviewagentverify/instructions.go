package reviewagentverify

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	"github.com/WuKongIM/WuKongIM/pkg/flowdoc"
)

// BaseContextDocument is one trusted context blob read from the exact base/control
// tree. AGENTS content is mandatory; FLOW content is advisory navigation.
type BaseContextDocument struct {
	Path    string
	BlobSHA string
	Content []byte
}

// DiscoverContextDocuments returns only base-tree context documents applicable to
// at least one changed path.
func DiscoverContextDocuments(
	changedPaths []string,
	catalog []BaseContextDocument,
) ([]contract.ContextDocumentBlob, error) {
	if len(changedPaths) == 0 {
		return nil, errors.New("context document discovery lacks changed paths")
	}
	for _, changedPath := range changedPaths {
		if !validPath(changedPath) {
			return nil, errors.New("context document discovery has invalid changed path")
		}
	}
	result := make([]contract.ContextDocumentBlob, 0, len(catalog))
	seen := make(map[string]struct{}, len(catalog))
	for _, document := range catalog {
		if !validPath(document.Path) ||
			len(document.BlobSHA) != 40 ||
			len(document.Content) == 0 {
			return nil, errors.New("invalid base context document")
		}
		base := path.Base(document.Path)
		if base != "AGENTS.md" && base != "FLOW.md" {
			return nil, errors.New("unsupported base context document name")
		}
		if _, exists := seen[document.Path]; exists {
			return nil, errors.New("duplicate base context document")
		}
		seen[document.Path] = struct{}{}
		directory := path.Dir(document.Path)
		if !slices.ContainsFunc(changedPaths, func(changedPath string) bool {
			return subtreeApplies(directory, changedPath)
		}) {
			continue
		}
		flowScope := flowdoc.ScopeSubtree
		if base == "FLOW.md" {
			metadata, err := flowdoc.ParseMetadata(document.Content, true)
			if err != nil {
				return nil, fmt.Errorf(
					"%s: invalid base FLOW metadata: %w",
					document.Path,
					err,
				)
			}
			flowScope = metadata.Scope
		}
		applicable := false
		for _, changedPath := range changedPaths {
			if instructionApplies(base, flowScope, directory, changedPath) {
				applicable = true
				break
			}
		}
		if !applicable {
			continue
		}
		result = append(result, contract.ContextDocumentBlob{
			Path:       document.Path,
			Scope:      directory,
			BlobSHA:    document.BlobSHA,
			BlobDigest: bytesDigest(document.Content),
			Content:    string(document.Content),
		})
	}
	slices.SortFunc(
		result,
		func(left, right contract.ContextDocumentBlob) int {
			return strings.Compare(left.Path, right.Path)
		},
	)
	return result, nil
}

func instructionApplies(
	base string,
	flowScope flowdoc.Scope,
	directory string,
	changedPath string,
) bool {
	if base == "FLOW.md" && flowScope == flowdoc.ScopePackage {
		return changedPath == directory || path.Dir(changedPath) == directory
	}
	return subtreeApplies(directory, changedPath)
}

func subtreeApplies(directory string, changedPath string) bool {
	return directory == "." ||
		changedPath == directory ||
		strings.HasPrefix(changedPath, directory+"/")
}
