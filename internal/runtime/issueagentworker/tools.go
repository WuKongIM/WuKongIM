package issueagentworker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ListResult is one stable bounded workspace_list response.
type ListResult struct {
	ID    uint64   `json:"id"`
	Paths []string `json:"paths"`
}

// SearchMatch is one literal workspace_search match.
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchResult is one stable bounded workspace_search response.
type SearchResult struct {
	ID      uint64        `json:"id"`
	Matches []SearchMatch `json:"matches"`
}

// ApplyRequest replaces or creates one complete regular file without patches.
type ApplyRequest struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	ContentBase64  string `json:"content_base64"`
}

// ApplyResult identifies the complete file bytes installed in the workspace.
type ApplyResult struct {
	ID     uint64 `json:"id"`
	SHA256 string `json:"sha256"`
}

// List returns lexically sorted regular files without following symlinks.
func (broker *Broker) List(
	ctx context.Context,
	relativePath string,
	maxEntries int,
) (ListResult, error) {
	if err := ctx.Err(); err != nil {
		return ListResult{}, err
	}
	if maxEntries <= 0 || maxEntries > 1000 {
		return ListResult{}, errors.New("workspace_list entry limit is invalid")
	}
	root, err := broker.resolveExisting(relativePath)
	if err != nil {
		return ListResult{}, err
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk workspace")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("workspace contains a non-regular entry")
		}
		relative, err := filepath.Rel(broker.root, current)
		if err != nil || !safeRelativePath(relative) {
			return errors.New("workspace_list path escapes root")
		}
		paths = append(paths, filepath.ToSlash(relative))
		if len(paths) > maxEntries {
			return errors.New("workspace_list exceeds entry limit")
		}
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}
	slices.Sort(paths)
	id := broker.record(ToolEvidence{
		Tool: "workspace_list", Path: relativePath,
		OutputSHA256: digest([]byte(strings.Join(paths, "\n"))),
	})
	return ListResult{ID: id, Paths: paths}, nil
}

// Search performs a bounded literal, line-oriented search over regular files.
func (broker *Broker) Search(
	ctx context.Context,
	literal string,
	relativePath string,
	maxMatches int,
) (SearchResult, error) {
	if literal == "" || len(literal) > 1024 ||
		strings.ContainsRune(literal, 0) ||
		maxMatches <= 0 || maxMatches > 1000 {
		return SearchResult{}, errors.New("workspace_search request is invalid")
	}
	listed, err := broker.List(ctx, relativePath, 1000)
	if err != nil {
		return SearchResult{}, err
	}
	matches := make([]SearchMatch, 0)
	for _, filePath := range listed.Paths {
		if err := ctx.Err(); err != nil {
			return SearchResult{}, err
		}
		resolved, err := broker.resolveExisting(filepath.FromSlash(filePath))
		if err != nil {
			return SearchResult{}, err
		}
		info, err := os.Stat(resolved)
		if err != nil || info.Size() > broker.maxFileBytes {
			continue
		}
		file, err := os.Open(resolved)
		if err != nil {
			return SearchResult{}, errors.New("open workspace_search file")
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), int(broker.maxFileBytes))
		line := 0
		for scanner.Scan() {
			line++
			if strings.Contains(scanner.Text(), literal) {
				text := scanner.Text()
				if len(text) > 4096 {
					text = text[:4096]
				}
				matches = append(matches, SearchMatch{
					Path: filePath, Line: line, Text: text,
				})
				if len(matches) >= maxMatches {
					break
				}
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil || closeErr != nil {
			return SearchResult{}, errors.New("scan workspace_search file")
		}
		if len(matches) >= maxMatches {
			break
		}
	}
	encoded := make([]byte, 0)
	for _, match := range matches {
		encoded = append(encoded, []byte(match.Path)...)
		encoded = append(encoded, 0)
		encoded = append(encoded, []byte(match.Text)...)
		encoded = append(encoded, '\n')
	}
	id := broker.record(ToolEvidence{
		Tool: "workspace_search", Path: relativePath,
		OutputSHA256: digest(encoded),
	})
	return SearchResult{ID: id, Matches: matches}, nil
}

// Apply atomically installs one complete bounded regular file.
func (broker *Broker) Apply(
	ctx context.Context,
	request ApplyRequest,
) (ApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, err
	}
	if !safeRelativePath(request.Path) ||
		!broker.writeAllowed(filepath.ToSlash(request.Path)) {
		return ApplyResult{}, errors.New("workspace_apply_patch path is outside task policy")
	}
	content, err := issueagent.DecodeFileContent(issueagent.FileChange{
		Path: request.Path, Operation: issueagent.FileOperationUpsert,
		Mode: issueagent.FileModeRegular, ContentBase64: request.ContentBase64,
	})
	if err != nil || int64(len(content)) > broker.maxFileBytes {
		return ApplyResult{}, errors.New("workspace_apply_patch content is invalid")
	}
	destination := filepath.Join(broker.root, request.Path)
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil || !withinRoot(broker.root, parent) {
		return ApplyResult{}, errors.New("workspace_apply_patch parent is unsafe")
	}
	current, readErr := os.ReadFile(destination)
	switch {
	case readErr == nil:
		info, statErr := os.Stat(destination)
		if statErr != nil || !info.Mode().IsRegular() ||
			request.ExpectedSHA256 == "" ||
			digest(current) != request.ExpectedSHA256 {
			return ApplyResult{}, errors.New("workspace_apply_patch expected digest is stale")
		}
	case errors.Is(readErr, os.ErrNotExist):
		if request.ExpectedSHA256 != "" {
			return ApplyResult{}, errors.New("workspace_apply_patch create has a stale digest")
		}
	default:
		return ApplyResult{}, errors.New("read workspace_apply_patch destination")
	}
	temp, err := os.CreateTemp(parent, ".issue-agent-write-*")
	if err != nil {
		return ApplyResult{}, errors.New("create workspace_apply_patch temporary file")
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return ApplyResult{}, errors.New("set workspace file mode")
	}
	if _, err := bytes.NewReader(content).WriteTo(temp); err != nil {
		_ = temp.Close()
		return ApplyResult{}, errors.New("write workspace file")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return ApplyResult{}, errors.New("sync workspace file")
	}
	if err := temp.Close(); err != nil {
		return ApplyResult{}, errors.New("close workspace file")
	}
	if err := os.Rename(tempName, destination); err != nil {
		return ApplyResult{}, errors.New("install workspace file")
	}
	sha := digest(content)
	id := broker.record(ToolEvidence{
		Tool: "workspace_apply_patch", Path: request.Path,
		OutputSHA256: sha,
	})
	return ApplyResult{ID: id, SHA256: sha}, nil
}

func (broker *Broker) writeAllowed(filePath string) bool {
	for _, allowed := range broker.allowedWrites {
		allowed = filepath.ToSlash(allowed)
		if filePath == allowed || strings.HasPrefix(filePath, strings.TrimSuffix(allowed, "/")+"/") {
			return true
		}
	}
	return false
}
