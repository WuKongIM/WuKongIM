package issueagentverify

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

var (
	candidateDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	candidateSHAPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// CaptureLimits bound one complete candidate diff.
type CaptureLimits struct {
	MaxFiles      int   `json:"max_files"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
	MaxDeletions  int   `json:"max_deletions"`
}

// CandidateSnapshot is the trusted filesystem-derived Engineer output.
type CandidateSnapshot = contract.CandidateSnapshot

type treeEntry struct {
	kind   string
	mode   contract.FileMode
	size   int64
	digest [sha256.Size]byte
	target string
}

// CaptureCandidate compares regular files without consulting workspace Git data.
func CaptureCandidate(
	baselineRoot string,
	workspaceRoot string,
	taskID string,
	baseSHA string,
	limits CaptureLimits,
) (CandidateSnapshot, error) {
	if !candidateDigestPattern.MatchString(taskID) ||
		!candidateSHAPattern.MatchString(baseSHA) ||
		limits.MaxFiles <= 0 ||
		limits.MaxFileBytes <= 0 ||
		limits.MaxTotalBytes <= 0 ||
		limits.MaxDeletions < 0 {
		return CandidateSnapshot{}, errors.New("candidate capture input is invalid")
	}
	baseline, err := scanCandidateTree(baselineRoot)
	if err != nil {
		return CandidateSnapshot{}, fmt.Errorf("scan candidate baseline: %w", err)
	}
	workspace, err := scanCandidateTree(workspaceRoot)
	if err != nil {
		return CandidateSnapshot{}, fmt.Errorf("scan candidate workspace: %w", err)
	}
	paths := make([]string, 0, len(baseline)+len(workspace))
	for path := range baseline {
		paths = append(paths, path)
	}
	for path := range workspace {
		if _, exists := baseline[path]; !exists {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)

	changes := make([]contract.FileChange, 0)
	for _, path := range paths {
		before, hadBefore := baseline[path]
		after, hasAfter := workspace[path]
		if hadBefore && before.kind == "symlink" ||
			hasAfter && after.kind == "symlink" {
			if !hadBefore || !hasAfter ||
				before.kind != "symlink" ||
				after.kind != "symlink" ||
				before.target != after.target {
				return CandidateSnapshot{}, fmt.Errorf(
					"candidate changed symlink %q", path,
				)
			}
			continue
		}
		if !hasAfter {
			changes = append(changes, contract.FileChange{
				Path: path, Operation: contract.FileOperationDelete,
			})
			continue
		}
		if hadBefore && before.kind == after.kind &&
			before.mode == after.mode &&
			before.size == after.size &&
			before.digest == after.digest {
			continue
		}
		if after.size > limits.MaxFileBytes {
			return CandidateSnapshot{}, fmt.Errorf(
				"candidate file %q exceeds byte limit", path,
			)
		}
		content, err := readCandidateFile(workspaceRoot, path, limits.MaxFileBytes)
		if err != nil {
			return CandidateSnapshot{}, err
		}
		changes = append(changes, contract.FileChange{
			Path: path, Operation: contract.FileOperationUpsert,
			Mode: after.mode, ContentBase64: contract.EncodeFileContent(content),
		})
	}
	changeSet := contract.ChangeSet{Files: changes}
	if err := contract.ValidateChangeSet(changeSet, contract.ChangeSetLimits{
		MaxFiles: limits.MaxFiles, MaxFileBytes: int(limits.MaxFileBytes),
		MaxTotalBytes: int(limits.MaxTotalBytes),
		MaxDeletions:  limits.MaxDeletions,
	}); err != nil {
		return CandidateSnapshot{}, err
	}
	return CandidateSnapshot{
		SchemaVersion: 2, TaskID: taskID, BaseSHA: baseSHA,
		ChangeSet: changeSet,
	}, nil
}

// CandidateSnapshotDigest binds the complete captured ChangeSet.
func CandidateSnapshotDigest(snapshot CandidateSnapshot) (string, error) {
	return contract.CandidateSnapshotDigest(snapshot)
}

// ValidateCandidateSnapshot validates a captured cross-job candidate.
func ValidateCandidateSnapshot(snapshot CandidateSnapshot) error {
	return contract.ValidateCandidateSnapshot(snapshot)
}

func scanCandidateTree(root string) (map[string]treeEntry, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("candidate tree root is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("candidate tree root is unsafe")
	}
	result := make(map[string]treeEntry)
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("read candidate tree")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return errors.New("resolve candidate path")
		}
		if relative == "." {
			return nil
		}
		repositoryPath := filepath.ToSlash(relative)
		if repositoryPath == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(repositoryPath, ".git/") {
			return nil
		}
		visited++
		if visited > 200000 {
			return errors.New("candidate tree exceeds entry bound")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return errors.New("stat candidate path")
		}
		if info.IsDir() {
			return nil
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := validateCandidateSymlink(root, path)
			if err != nil {
				return err
			}
			result[repositoryPath] = treeEntry{kind: "symlink", target: target}
		case info.Mode().IsRegular():
			mode, err := candidateFileMode(info.Mode())
			if err != nil {
				return fmt.Errorf("candidate file %q: %w", repositoryPath, err)
			}
			digest, err := hashCandidateFile(path)
			if err != nil {
				return err
			}
			result[repositoryPath] = treeEntry{
				kind: "regular", mode: mode, size: info.Size(), digest: digest,
			}
		default:
			return fmt.Errorf("candidate path %q has unsupported type", repositoryPath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func candidateFileMode(mode os.FileMode) (contract.FileMode, error) {
	switch mode.Perm() {
	case 0o644:
		return contract.FileModeRegular, nil
	case 0o755:
		return contract.FileModeExecutable, nil
	default:
		return "", errors.New("unsupported permissions")
	}
}

func hashCandidateFile(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, errors.New("open candidate file")
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return result, errors.New("hash candidate file")
	}
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

func readCandidateFile(root, repositoryPath string, maxBytes int64) ([]byte, error) {
	path := filepath.Join(root, filepath.FromSlash(repositoryPath))
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open changed candidate file")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(content)) > maxBytes {
		return nil, errors.New("read changed candidate file")
	}
	return content, nil
}

func validateCandidateSymlink(root, path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil || target == "" || filepath.IsAbs(target) {
		return "", errors.New("candidate symlink target is invalid")
	}
	cleaned := filepath.Clean(target)
	if cleaned != target {
		return "", errors.New("candidate symlink target is not normalized")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), cleaned))
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("candidate symlink escapes workspace")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("candidate symlink does not target a regular file")
	}
	return filepath.ToSlash(target), nil
}
