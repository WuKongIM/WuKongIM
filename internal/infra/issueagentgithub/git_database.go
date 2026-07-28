package issueagentgithub

import (
	"context"
	"crypto/sha1" // #nosec G505 -- Git blob identity is SHA-1 by protocol.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// CommitPlan is a fully validated, expected-head-fenced publication request.
type CommitPlan struct {
	Branch                string
	ExpectedParentSHA     string
	BaseTreeSHA           string
	ExpectedResultTreeSHA string
	Message               string
	ExistingBranch        bool
	ChangeSet             issueagentcontract.ChangeSet
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

// RebasePlan rebuilds one exact merge result on top of current main and swaps
// the Agent PR ref only through an expected-old-head atomic update.
type RebasePlan struct {
	Branch                string
	ExpectedOldHeadSHA    string
	CurrentMainSHA        string
	ExpectedResultTreeSHA string
	Message               string
	ExpectedAuthorLogin   string
	ChangeSet             issueagentcontract.ChangeSet
}

// ExactRebasedIntegration accepts only the App-authored, GitHub-signed commit
// whose parent is current main and whose tree is the independently computed
// merge result.
func ExactRebasedIntegration(
	commit CommitFacts,
	attribution CommitAttributionFacts,
	expectedMainSHA string,
	expectedTreeSHA string,
	message string,
	expectedAuthorLogin string,
) bool {
	return ExactAppCommit(
		commit, attribution, expectedMainSHA, message, expectedAuthorLogin,
	) &&
		commit.TreeSHA == expectedTreeSHA
}

// ExactAppCommit binds a reusable GitHub-created commit to the configured App
// identity, GitHub signature, deterministic message, and exact parent.
func ExactAppCommit(
	commit CommitFacts,
	attribution CommitAttributionFacts,
	expectedParentSHA string,
	message string,
	expectedAuthorLogin string,
) bool {
	return commit.SHA != "" &&
		attribution.SHA == commit.SHA &&
		attribution.AuthorLogin == expectedAuthorLogin &&
		attribution.AuthorType == "Bot" &&
		attribution.SignatureValid &&
		attribution.SignatureState == "VALID" &&
		attribution.WasSignedByGitHub &&
		len(commit.Parents) == 1 &&
		commit.Parents[0] == expectedParentSHA &&
		commit.Message == message &&
		commit.Verified &&
		commit.VerificationReason == "valid"
}

// PublishCommit uses GitHub's createCommitOnBranch mutation. GitHub
// automatically signs commits authored by an authenticated GitHub App through
// this mutation; the Publisher then re-reads the REST verification record and
// the exact one-commit comparison before accepting the result.
func (client *Client) PublishCommit(
	ctx context.Context,
	plan CommitPlan,
) (PublishedCommit, error) {
	if client == nil ||
		(!agentRefPattern.MatchString(plan.Branch) &&
			!(plan.ExistingBranch &&
				agentStageRefPattern.MatchString(plan.Branch))) ||
		!gitObjectPattern.MatchString(plan.ExpectedParentSHA) ||
		!gitObjectPattern.MatchString(plan.BaseTreeSHA) ||
		(plan.ExpectedResultTreeSHA != "" &&
			!gitObjectPattern.MatchString(plan.ExpectedResultTreeSHA)) ||
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
		commit.Message != plan.Message ||
		plan.ExpectedResultTreeSHA != "" &&
			commit.TreeSHA != plan.ExpectedResultTreeSHA ||
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

const zeroGitOID = "0000000000000000000000000000000000000000"

type refUpdate struct {
	Name      string `json:"name"`
	BeforeOID string `json:"beforeOid"`
	AfterOID  string `json:"afterOid"`
	Force     bool   `json:"force"`
}

// PublishRebasedCommit creates an App-authored signed commit on a deterministic
// staging ref rooted at current main, then atomically force-swaps the PR branch
// only if both the old PR head and staging head still match exactly.
func (client *Client) PublishRebasedCommit(
	ctx context.Context,
	plan RebasePlan,
) (PublishedCommit, error) {
	if client == nil || !agentRefPattern.MatchString(plan.Branch) ||
		!gitObjectPattern.MatchString(plan.ExpectedOldHeadSHA) ||
		!gitObjectPattern.MatchString(plan.CurrentMainSHA) ||
		!gitObjectPattern.MatchString(plan.ExpectedResultTreeSHA) ||
		plan.ExpectedOldHeadSHA == plan.CurrentMainSHA ||
		strings.TrimSpace(plan.Message) == "" || len(plan.Message) > 4096 ||
		strings.TrimSpace(plan.ExpectedAuthorLogin) == "" ||
		len(plan.ExpectedAuthorLogin) > 256 ||
		strings.ContainsAny(plan.ExpectedAuthorLogin, "\r\n") ||
		len(plan.ChangeSet.Files) == 0 {
		return PublishedCommit{}, errors.New("mechanical rebase plan is invalid")
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
	repositoryID, err := client.repositoryGraphQLID(ctx)
	if err != nil {
		return PublishedCommit{}, err
	}
	stageBranch, err := rebaseStageBranch(plan)
	if err != nil {
		return PublishedCommit{}, err
	}
	stage, exists, err := client.RefIfExists(ctx, stageBranch)
	if err != nil {
		return PublishedCommit{}, err
	}
	if !exists {
		createErr := client.updateRefsCAS(ctx, repositoryID, []refUpdate{{
			Name: "refs/heads/" + stageBranch, BeforeOID: zeroGitOID,
			AfterOID: plan.CurrentMainSHA, Force: false,
		}})
		stage, exists, err = client.RefIfExists(ctx, stageBranch)
		if err != nil || !exists {
			if createErr != nil {
				return PublishedCommit{}, createErr
			}
			return PublishedCommit{}, errors.New("mechanical rebase staging ref was not created")
		}
	}

	candidateSHA := stage.SHA
	if candidateSHA == plan.CurrentMainSHA {
		mainCommit, readErr := client.Commit(ctx, plan.CurrentMainSHA)
		if readErr != nil {
			return PublishedCommit{}, readErr
		}
		published, publishErr := client.PublishCommit(ctx, CommitPlan{
			Branch: stageBranch, ExpectedParentSHA: plan.CurrentMainSHA,
			BaseTreeSHA:           mainCommit.TreeSHA,
			ExpectedResultTreeSHA: plan.ExpectedResultTreeSHA,
			Message:               plan.Message,
			ExistingBranch:        true,
			ChangeSet:             plan.ChangeSet,
		})
		if publishErr == nil {
			candidateSHA = published.CommitSHA
		} else {
			recovered, ok, recoverErr := client.RefIfExists(ctx, stageBranch)
			if recoverErr != nil || !ok ||
				recovered.SHA == plan.CurrentMainSHA {
				return PublishedCommit{}, publishErr
			}
			candidateSHA = recovered.SHA
		}
	}
	commit, err := client.Commit(ctx, candidateSHA)
	if err != nil {
		return PublishedCommit{}, err
	}
	attribution, err := client.CommitAttribution(ctx, candidateSHA)
	if err != nil || !ExactRebasedIntegration(
		commit, attribution, plan.CurrentMainSHA,
		plan.ExpectedResultTreeSHA, plan.Message,
		plan.ExpectedAuthorLogin,
	) {
		return PublishedCommit{},
			errors.New("mechanical rebase staging commit is not exact")
	}

	swapErr := client.updateRefsCAS(ctx, repositoryID, []refUpdate{
		{
			Name:      "refs/heads/" + plan.Branch,
			BeforeOID: plan.ExpectedOldHeadSHA, AfterOID: candidateSHA,
			Force: true,
		},
		{
			Name:      "refs/heads/" + stageBranch,
			BeforeOID: candidateSHA, AfterOID: zeroGitOID,
			Force: true,
		},
	})
	current, currentErr := client.Ref(ctx, plan.Branch)
	_, stageExists, stageErr := client.RefIfExists(ctx, stageBranch)
	if currentErr == nil && stageErr == nil &&
		current.SHA == candidateSHA && !stageExists {
		return PublishedCommit{
			CommitSHA: candidateSHA, TreeSHA: commit.TreeSHA,
		}, nil
	}
	if swapErr != nil {
		return PublishedCommit{}, swapErr
	}
	return PublishedCommit{}, errors.New("mechanical rebase ref swap is inconsistent")
}

// rebaseStageBranch binds an orphaned staging ref to the complete immutable
// effect. A new generation, adopted head, merge result, author, message, or
// ChangeSet therefore cannot be blocked by a stale candidate from an older
// expected-head transaction.
func rebaseStageBranch(plan RebasePlan) (string, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", errors.New("mechanical rebase identity is invalid")
	}
	digest := sha256.Sum256(encoded)
	return plan.Branch + "-rebase-" + hex.EncodeToString(digest[:]), nil
}

func (client *Client) repositoryGraphQLID(ctx context.Context) (string, error) {
	parts := strings.Split(client.repository, "/")
	if len(parts) != 2 {
		return "", errors.New("GitHub repository identity is invalid")
	}
	var response struct {
		Data struct {
			Repository *struct {
				ID            string `json:"id"`
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	request := struct {
		Query     string `json:"query"`
		Variables struct {
			Owner string `json:"owner"`
			Name  string `json:"name"`
		} `json:"variables"`
	}{
		Query: `query($owner:String!,$name:String!){` +
			`repository(owner:$owner,name:$name){id nameWithOwner}}`,
	}
	request.Variables.Owner = parts[0]
	request.Variables.Name = parts[1]
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql", request, &response, http.StatusOK,
	); err != nil {
		return "", err
	}
	if len(response.Errors) != 0 || response.Data.Repository == nil ||
		response.Data.Repository.ID == "" ||
		response.Data.Repository.NameWithOwner != client.repository {
		return "", errors.New("GitHub GraphQL repository identity is invalid")
	}
	return response.Data.Repository.ID, nil
}

func (client *Client) updateRefsCAS(
	ctx context.Context,
	repositoryID string,
	updates []refUpdate,
) error {
	if repositoryID == "" || len(repositoryID) > 256 ||
		len(updates) == 0 || len(updates) > 2 {
		return errors.New("GitHub atomic ref update is invalid")
	}
	for _, update := range updates {
		branch := strings.TrimPrefix(update.Name, "refs/heads/")
		if update.Name != "refs/heads/"+branch ||
			!isAgentManagedRef(branch) ||
			(!gitObjectPattern.MatchString(update.BeforeOID) &&
				update.BeforeOID != zeroGitOID) ||
			(!gitObjectPattern.MatchString(update.AfterOID) &&
				update.AfterOID != zeroGitOID) {
			return errors.New("GitHub atomic ref update is invalid")
		}
	}
	var response struct {
		Data struct {
			UpdateRefs *struct{} `json:"updateRefs"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	request := struct {
		Query     string `json:"query"`
		Variables struct {
			Input struct {
				RepositoryID string      `json:"repositoryId"`
				RefUpdates   []refUpdate `json:"refUpdates"`
			} `json:"input"`
		} `json:"variables"`
	}{
		Query: `mutation($input:UpdateRefsInput!){` +
			`updateRefs(input:$input){clientMutationId}}`,
	}
	request.Variables.Input.RepositoryID = repositoryID
	request.Variables.Input.RefUpdates = updates
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql", request, &response, http.StatusOK,
	); err != nil {
		return err
	}
	if len(response.Errors) != 0 || response.Data.UpdateRefs == nil {
		return errors.New("GitHub atomic ref update failed")
	}
	return nil
}
