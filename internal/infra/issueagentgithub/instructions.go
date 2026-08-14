package issueagentgithub

import (
	"context"
	"errors"
	"path"
	"slices"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

const maxContextDocumentFiles = 256

// ContextDocumentFileDigests freezes every AGENTS/FLOW identity at one exact
// candidate source commit without assigning the documents equal authority.
func (client *Client) ContextDocumentFileDigests(
	ctx context.Context,
	commitSHA string,
) ([]contract.FileDigest, error) {
	if client == nil || ctx == nil ||
		!gitObjectPattern.MatchString(commitSHA) ||
		len(commitSHA) != 40 {
		return nil, errors.New("context document source commit is invalid")
	}
	commit, err := client.Commit(ctx, commitSHA)
	if err != nil {
		return nil, err
	}
	endpoint := client.endpoint(
		"/repos/" + client.repository + "/git/trees/" + commit.TreeSHA,
	)
	query := endpoint.Query()
	query.Set("recursive", "1")
	endpoint.RawQuery = query.Encode()
	var payload struct {
		SHA       string `json:"sha"`
		Truncated bool   `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	next, err := client.getJSONPage(ctx, endpoint, &payload)
	if err != nil {
		return nil, err
	}
	if next != nil || payload.SHA != commit.TreeSHA ||
		payload.Truncated || len(payload.Tree) > 100000 {
		return nil, errors.New("context document source tree is incomplete")
	}
	result := make([]contract.FileDigest, 0, maxContextDocumentFiles)
	for _, entry := range payload.Tree {
		if path.Base(entry.Path) != "AGENTS.md" &&
			path.Base(entry.Path) != "FLOW.md" {
			continue
		}
		if !validRepositoryPath(entry.Path) ||
			entry.Type != "blob" || entry.Mode != "100644" ||
			!gitObjectPattern.MatchString(entry.SHA) ||
			len(entry.SHA) != 40 {
			return nil, errors.New("repository context document entry is invalid")
		}
		if len(result) == maxContextDocumentFiles {
			return nil, errors.New("repository context document inventory is too large")
		}
		result = append(result, contract.FileDigest{
			Path: entry.Path, GitBlobSHA: entry.SHA,
		})
	}
	slices.SortFunc(result, func(left, right contract.FileDigest) int {
		if left.Path < right.Path {
			return -1
		}
		if left.Path > right.Path {
			return 1
		}
		return 0
	})
	if len(result) == 0 {
		return nil, errors.New("repository context document inventory is empty")
	}
	for index := 1; index < len(result); index++ {
		if result[index-1].Path == result[index].Path {
			return nil, errors.New("repository context document inventory is ambiguous")
		}
	}
	return result, nil
}
