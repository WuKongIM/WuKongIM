package issueagentgithub

import (
	"context"
	"crypto/sha1" // #nosec G505 -- Git blob identity is SHA-1 by protocol.
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// CommitPlan is a fully validated, expected-head-fenced publication request.
type CommitPlan struct {
	Branch            string
	ExpectedParentSHA string
	BaseTreeSHA       string
	Message           string
	ExistingBranch    bool
	ChangeSet         issueagentcontract.ChangeSet
}

func gitBlobObjectSHA(content []byte) string {
	hasher := sha1.New() // #nosec G401 -- Git blob identity is SHA-1 by protocol.
	_, _ = hasher.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00"))
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}

// PublishedCommit is the re-read immutable result of one signed update.
type PublishedCommit struct {
	CommitSHA string
	TreeSHA   string
}

// PublishCommit uses GitHub's createCommitOnBranch mutation. GitHub
// automatically signs commits authored by an authenticated GitHub App through
// this mutation; the Publisher then re-reads the REST verification record and
// the exact one-commit comparison before accepting the result.
func (client *Client) PublishCommit(
	ctx context.Context,
	plan CommitPlan,
) (PublishedCommit, error) {
	if client == nil || !agentRefPattern.MatchString(plan.Branch) ||
		!gitObjectPattern.MatchString(plan.ExpectedParentSHA) ||
		!gitObjectPattern.MatchString(plan.BaseTreeSHA) ||
		strings.TrimSpace(plan.Message) == "" || len(plan.Message) > 4096 ||
		len(plan.ChangeSet.Files) == 0 {
		return PublishedCommit{}, errors.New("signed commit plan is invalid")
	}
	if err := issueagentcontract.ValidateChangeSet(
		plan.ChangeSet,
		issueagentcontract.ChangeSetLimits{
			MaxFiles: 128, MaxFileBytes: 8 << 20,
			MaxTotalBytes: 32 << 20, MaxDeletions: 128,
		},
	); err != nil {
		return PublishedCommit{}, err
	}
	parent, err := client.Commit(ctx, plan.ExpectedParentSHA)
	if err != nil || parent.TreeSHA != plan.BaseTreeSHA {
		return PublishedCommit{}, errors.New("signed commit base tree is stale")
	}

	if !plan.ExistingBranch {
		var created struct {
			Ref    string `json:"ref"`
			Object struct {
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"object"`
		}
		if err := client.requestJSON(
			ctx,
			http.MethodPost,
			"/repos/"+client.repository+"/git/refs",
			struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			}{
				Ref: "refs/heads/" + plan.Branch,
				SHA: plan.ExpectedParentSHA,
			},
			&created,
			http.StatusCreated,
		); err != nil {
			return PublishedCommit{}, err
		}
		if created.Ref != "refs/heads/"+plan.Branch ||
			created.Object.Type != "commit" ||
			created.Object.SHA != plan.ExpectedParentSHA {
			return PublishedCommit{}, errors.New("created Agent ref is inconsistent")
		}
	}

	type addition struct {
		Path     string `json:"path"`
		Contents string `json:"contents"`
	}
	type deletion struct {
		Path string `json:"path"`
	}
	additions := make([]addition, 0, len(plan.ChangeSet.Files))
	deletions := make([]deletion, 0, len(plan.ChangeSet.Files))
	for _, file := range plan.ChangeSet.Files {
		switch file.Operation {
		case issueagentcontract.FileOperationUpsert:
			additions = append(additions, addition{
				Path: file.Path, Contents: file.ContentBase64,
			})
		case issueagentcontract.FileOperationDelete:
			deletions = append(deletions, deletion{Path: file.Path})
		default:
			return PublishedCommit{}, errors.New("signed commit contains an invalid operation")
		}
	}
	var response struct {
		Data struct {
			CreateCommitOnBranch struct {
				Commit struct {
					OID string `json:"oid"`
				} `json:"commit"`
			} `json:"createCommitOnBranch"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	const mutation = `mutation($input:CreateCommitOnBranchInput!){` +
		`createCommitOnBranch(input:$input){commit{oid}}}`
	request := struct {
		Query     string `json:"query"`
		Variables struct {
			Input struct {
				Branch struct {
					RepositoryNameWithOwner string `json:"repositoryNameWithOwner"`
					BranchName              string `json:"branchName"`
				} `json:"branch"`
				Message struct {
					Headline string `json:"headline"`
				} `json:"message"`
				ExpectedHeadOID string `json:"expectedHeadOid"`
				FileChanges     struct {
					Additions []addition `json:"additions,omitempty"`
					Deletions []deletion `json:"deletions,omitempty"`
				} `json:"fileChanges"`
			} `json:"input"`
		} `json:"variables"`
	}{Query: mutation}
	request.Variables.Input.Branch.RepositoryNameWithOwner = client.repository
	request.Variables.Input.Branch.BranchName = plan.Branch
	request.Variables.Input.Message.Headline = plan.Message
	request.Variables.Input.ExpectedHeadOID = plan.ExpectedParentSHA
	request.Variables.Input.FileChanges.Additions = additions
	request.Variables.Input.FileChanges.Deletions = deletions
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql", request, &response, http.StatusOK,
	); err != nil {
		return PublishedCommit{}, err
	}
	commitSHA := response.Data.CreateCommitOnBranch.Commit.OID
	if len(response.Errors) != 0 || !gitObjectPattern.MatchString(commitSHA) ||
		commitSHA == plan.ExpectedParentSHA {
		return PublishedCommit{}, errors.New("GitHub signed-commit mutation failed")
	}

	ref, err := client.Ref(ctx, plan.Branch)
	if err != nil || ref.SHA != commitSHA {
		return PublishedCommit{}, errors.New("GitHub ref re-read does not match signed commit")
	}
	commit, err := client.Commit(ctx, commitSHA)
	if err != nil ||
		len(commit.Parents) != 1 ||
		commit.Parents[0] != plan.ExpectedParentSHA ||
		!commit.Verified ||
		commit.VerificationReason != "valid" {
		return PublishedCommit{}, errors.New("GitHub signed commit re-read is inconsistent")
	}
	files, err := client.CompareOneCommit(ctx, plan.ExpectedParentSHA, commitSHA)
	if err != nil || len(files) != len(plan.ChangeSet.Files) {
		return PublishedCommit{}, errors.New("GitHub signed commit comparison is inconsistent")
	}
	for index, change := range plan.ChangeSet.Files {
		if files[index].Path != change.Path {
			return PublishedCommit{}, errors.New("GitHub signed commit changed an unexpected path")
		}
		if change.Operation == issueagentcontract.FileOperationDelete {
			if files[index].Status != "removed" {
				return PublishedCommit{}, errors.New("GitHub signed commit deletion is inconsistent")
			}
			continue
		}
		content, err := issueagentcontract.DecodeFileContent(change)
		if err != nil || files[index].SHA != gitBlobObjectSHA(content) {
			return PublishedCommit{}, errors.New("GitHub signed commit content is inconsistent")
		}
	}
	return PublishedCommit{CommitSHA: commit.SHA, TreeSHA: commit.TreeSHA}, nil
}
