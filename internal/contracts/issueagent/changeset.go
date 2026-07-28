package issueagent

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// FileOperation is one Publisher-supported repository file mutation.
type FileOperation string

const (
	FileOperationUpsert FileOperation = "upsert"
	FileOperationDelete FileOperation = "delete"
)

// FileMode is one regular-file Git tree mode accepted from a Worker result.
type FileMode string

const (
	FileModeRegular    FileMode = "100644"
	FileModeExecutable FileMode = "100755"
)

// FileChange declares one complete file result, never a patch command.
type FileChange struct {
	Path      string        `json:"path"`
	Operation FileOperation `json:"operation"`
	Mode      FileMode      `json:"mode,omitempty"`
	Content   []byte        `json:"content,omitempty"`
}

// ChangeSet is the sorted complete set of file mutations proposed by a Worker.
type ChangeSet struct {
	Files []FileChange `json:"files"`
}

// ChangeSetLimits are trusted Publisher bounds, not Worker suggestions.
type ChangeSetLimits struct {
	MaxFiles      int
	MaxFileBytes  int
	MaxTotalBytes int
	MaxDeletions  int
}

// ValidateChangeSet rejects unsafe paths, modes, operations, and size totals.
func ValidateChangeSet(changeSet ChangeSet, limits ChangeSetLimits) error {
	if limits.MaxFiles <= 0 || limits.MaxFileBytes <= 0 ||
		limits.MaxTotalBytes <= 0 || limits.MaxDeletions < 0 {
		return errors.New("change-set limits are invalid")
	}
	if len(changeSet.Files) > limits.MaxFiles {
		return fmt.Errorf("change set has %d files, limit is %d", len(changeSet.Files), limits.MaxFiles)
	}

	var totalBytes int
	var deletions int
	var previousPath string
	caseFolded := make(map[string]string, len(changeSet.Files))
	for index, file := range changeSet.Files {
		if err := validateRepositoryPath(file.Path); err != nil {
			return fmt.Errorf("file %d: %w", index, err)
		}
		if index > 0 && file.Path <= previousPath {
			return errors.New("change-set paths must be strictly sorted and unique")
		}
		previousPath = file.Path

		folded := strings.ToLower(file.Path)
		if existing, ok := caseFolded[folded]; ok && existing != file.Path {
			return fmt.Errorf("case-colliding paths %q and %q", existing, file.Path)
		}
		caseFolded[folded] = file.Path

		switch file.Operation {
		case FileOperationUpsert:
			if file.Mode != FileModeRegular && file.Mode != FileModeExecutable {
				return fmt.Errorf("file %q has unsupported mode %q", file.Path, file.Mode)
			}
			if len(file.Content) > limits.MaxFileBytes {
				return fmt.Errorf("file %q exceeds byte limit", file.Path)
			}
			totalBytes += len(file.Content)
		case FileOperationDelete:
			deletions++
			if file.Mode != "" || len(file.Content) != 0 {
				return fmt.Errorf("deleted file %q must not carry mode or content", file.Path)
			}
		default:
			return fmt.Errorf("file %q has unsupported operation %q", file.Path, file.Operation)
		}
		if totalBytes > limits.MaxTotalBytes {
			return errors.New("change set exceeds total byte limit")
		}
	}
	if deletions > limits.MaxDeletions {
		return fmt.Errorf("change set has %d deletions, limit is %d", deletions, limits.MaxDeletions)
	}
	return nil
}

func validateRepositoryPath(repositoryPath string) error {
	if repositoryPath == "" ||
		strings.HasPrefix(repositoryPath, "/") ||
		strings.Contains(repositoryPath, "\\") ||
		strings.ContainsRune(repositoryPath, '\x00') {
		return fmt.Errorf("unsafe repository path %q", repositoryPath)
	}
	cleaned := path.Clean(repositoryPath)
	if cleaned == "." ||
		cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") ||
		cleaned != repositoryPath {
		return fmt.Errorf("repository path %q is not normalized", repositoryPath)
	}
	return nil
}
