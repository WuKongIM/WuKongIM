package issueagentgithub

import (
	"context"
	"errors"
	"net/http"
	"strings"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// CommitPlan is a fully validated Git Database publication request.
type CommitPlan struct {
	Branch            string
	ExpectedParentSHA string
	BaseTreeSHA       string
	Message           string
	ExistingBranch    bool
	ChangeSet         issueagentcontract.ChangeSet
}

// PublishedCommit is the re-read immutable result of one Git Database update.
type PublishedCommit struct {
	CommitSHA string
	TreeSHA   string
}

// PublishCommit creates blobs, a tree, a verified commit, and a non-force ref.
func (client *Client) PublishCommit(
	ctx context.Context,
	plan CommitPlan,
) (PublishedCommit, error) {
	if client == nil || !agentRefPattern.MatchString(plan.Branch) ||
		!gitObjectPattern.MatchString(plan.ExpectedParentSHA) ||
		!gitObjectPattern.MatchString(plan.BaseTreeSHA) ||
		strings.TrimSpace(plan.Message) == "" || len(plan.Message) > 4096 ||
		len(plan.ChangeSet.Files) == 0 {
		return PublishedCommit{}, errors.New("Git Database commit plan is invalid")
	}
	if err := issueagentcontract.ValidateChangeSet(plan.ChangeSet, issueagentcontract.ChangeSetLimits{
		MaxFiles: 128, MaxFileBytes: 8 << 20,
		MaxTotalBytes: 32 << 20, MaxDeletions: 128,
	}); err != nil {
		return PublishedCommit{}, err
	}

	type treeEntry struct {
		Path string  `json:"path"`
		Mode string  `json:"mode"`
		Type string  `json:"type"`
		SHA  *string `json:"sha"`
	}
	entries := make([]treeEntry, 0, len(plan.ChangeSet.Files))
	for _, file := range plan.ChangeSet.Files {
		var sha *string
		if file.Operation == issueagentcontract.FileOperationUpsert {
			content, err := issueagentcontract.DecodeFileContent(file)
			if err != nil {
				return PublishedCommit{}, err
			}
			var blobResponse struct {
				SHA string `json:"sha"`
			}
			if err := client.requestJSON(
				ctx,
				http.MethodPost,
				"/repos/"+client.repository+"/git/blobs",
				struct {
					Content  []byte `json:"content"`
					Encoding string `json:"encoding"`
				}{Content: content, Encoding: "base64"},
				&blobResponse,
				http.StatusCreated,
				http.StatusOK,
			); err != nil {
				return PublishedCommit{}, err
			}
			if !gitObjectPattern.MatchString(blobResponse.SHA) {
				return PublishedCommit{}, errors.New("GitHub blob response is invalid")
			}
			sha = &blobResponse.SHA
		}
		entries = append(entries, treeEntry{
			Path: file.Path, Mode: string(file.Mode), Type: "blob", SHA: sha,
		})
	}
	var treeResponse struct {
		SHA string `json:"sha"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/git/trees",
		struct {
			BaseTree string      `json:"base_tree"`
			Tree     []treeEntry `json:"tree"`
		}{BaseTree: plan.BaseTreeSHA, Tree: entries},
		&treeResponse,
		http.StatusCreated,
		http.StatusOK,
	); err != nil {
		return PublishedCommit{}, err
	}
	if !gitObjectPattern.MatchString(treeResponse.SHA) {
		return PublishedCommit{}, errors.New("GitHub tree response is invalid")
	}

	var commitResponse struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
		Verification struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"verification"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/git/commits",
		struct {
			Message string   `json:"message"`
			Tree    string   `json:"tree"`
			Parents []string `json:"parents"`
		}{
			Message: plan.Message, Tree: treeResponse.SHA,
			Parents: []string{plan.ExpectedParentSHA},
		},
		&commitResponse,
		http.StatusCreated,
		http.StatusOK,
	); err != nil {
		return PublishedCommit{}, err
	}
	if !gitObjectPattern.MatchString(commitResponse.SHA) ||
		commitResponse.Tree.SHA != treeResponse.SHA ||
		len(commitResponse.Parents) != 1 ||
		commitResponse.Parents[0].SHA != plan.ExpectedParentSHA ||
		!commitResponse.Verification.Verified ||
		commitResponse.Verification.Reason != "valid" {
		return PublishedCommit{}, errors.New("GitHub commit response is unverified or inconsistent")
	}

	if plan.ExistingBranch {
		var refResponse any
		if err := client.requestJSON(
			ctx,
			http.MethodPatch,
			"/repos/"+client.repository+"/git/refs/heads/"+plan.Branch,
			struct {
				SHA   string `json:"sha"`
				Force bool   `json:"force"`
			}{SHA: commitResponse.SHA, Force: false},
			&refResponse,
			http.StatusOK,
		); err != nil {
			return PublishedCommit{}, err
		}
	} else {
		var refResponse any
		if err := client.requestJSON(
			ctx,
			http.MethodPost,
			"/repos/"+client.repository+"/git/refs",
			struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			}{Ref: "refs/heads/" + plan.Branch, SHA: commitResponse.SHA},
			&refResponse,
			http.StatusCreated,
		); err != nil {
			return PublishedCommit{}, err
		}
	}

	ref, err := client.Ref(ctx, plan.Branch)
	if err != nil || ref.SHA != commitResponse.SHA {
		return PublishedCommit{}, errors.New("GitHub ref re-read does not match published commit")
	}
	commit, err := client.Commit(ctx, commitResponse.SHA)
	if err != nil ||
		commit.TreeSHA != treeResponse.SHA ||
		len(commit.Parents) != 1 ||
		commit.Parents[0] != plan.ExpectedParentSHA ||
		!commit.Verified ||
		commit.VerificationReason != "valid" {
		return PublishedCommit{}, errors.New("GitHub commit re-read is inconsistent")
	}
	return PublishedCommit{CommitSHA: commit.SHA, TreeSHA: commit.TreeSHA}, nil
}
