package issueagentgithub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

var appBotLoginPattern = regexp.MustCompile(
	`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})\[bot\]$`,
)

// StateCommitRequest is one exact expected-head state publication.
type StateCommitRequest struct {
	Branch            string
	Path              string
	ExpectedParentSHA string
	BaseTreeSHA       string
	ExistingBranch    bool
	Message           string
	Content           []byte
}

// StateCommitResult is the independently re-read signed commit identity.
type StateCommitResult struct {
	CommitSHA      string
	ParentSHA      string
	Path           string
	ContentDigest  string
	AuthorLogin    string
	AuthorType     string
	Verified       bool
	SignedByGitHub bool
}

// StateCommitRecord is one complete re-read state commit.
type StateCommitRecord struct {
	CommitSHA      string
	ParentSHA      string
	Message        string
	Path           string
	Content        []byte
	AuthorLogin    string
	AuthorType     string
	Verified       bool
	SignedByGitHub bool
}

// StateCommitPort is the narrow GitHub signed-commit boundary.
type StateCommitPort interface {
	PublishStateCommit(context.Context, StateCommitRequest) (StateCommitResult, error)
	StateRefHead(context.Context, string) (string, bool, error)
	ReadStateCommit(context.Context, string, string) (StateCommitRecord, error)
}

// StateStore owns canonical per-Issue state-ref publication.
type StateStore struct {
	repository string
	appLogin   string
	commits    StateCommitPort
}

// StateAdvanceRequest fences one durable state transition.
type StateAdvanceRequest struct {
	State             contract.IssueAgentState
	ExpectedParentSHA string
	BaseTreeSHA       string
	ExistingBranch    bool
}

// StatePublication is the accepted new durable state head.
type StatePublication struct {
	HeadSHA string
}

// LoadedState is the latest state plus its exact signed ref head.
type LoadedState struct {
	HeadSHA string
	State   contract.IssueAgentState
}

// NewStateStore constructs a repository- and App-bound state store.
func NewStateStore(
	repository string,
	appLogin string,
	commits StateCommitPort,
) (*StateStore, error) {
	if !repositoryNamePattern.MatchString(repository) ||
		!appBotLoginPattern.MatchString(appLogin) ||
		commits == nil {
		return nil, errors.New("State Store configuration is invalid")
	}
	return &StateStore{
		repository: repository,
		appLogin:   appLogin,
		commits:    commits,
	}, nil
}

// Advance writes one canonical state commit against the exact prior head.
func (store *StateStore) Advance(
	ctx context.Context,
	request StateAdvanceRequest,
) (StatePublication, error) {
	if store == nil || store.commits == nil || ctx == nil ||
		request.State.Repository != store.repository ||
		!gitObjectPattern.MatchString(request.ExpectedParentSHA) ||
		len(request.ExpectedParentSHA) != 40 ||
		!gitObjectPattern.MatchString(request.BaseTreeSHA) ||
		len(request.BaseTreeSHA) != 40 {
		return StatePublication{}, errors.New("State Store advance request is invalid")
	}
	content, err := contract.CanonicalIssueAgentState(request.State)
	if err != nil {
		return StatePublication{}, err
	}
	branch := fmt.Sprintf("agent-state/issue-%d", request.State.IssueNumber)
	path := fmt.Sprintf(
		".issue-agent-state/issue-%d.json",
		request.State.IssueNumber,
	)
	message := fmt.Sprintf(
		"agent(state): issue %d sequence %d",
		request.State.IssueNumber,
		request.State.Sequence,
	)
	result, err := store.commits.PublishStateCommit(ctx, StateCommitRequest{
		Branch: branch, Path: path,
		ExpectedParentSHA: request.ExpectedParentSHA,
		BaseTreeSHA:       request.BaseTreeSHA,
		ExistingBranch:    request.ExistingBranch,
		Message:           message,
		Content:           content,
	})
	if err != nil {
		return StatePublication{}, err
	}
	sum := sha256.Sum256(content)
	expectedDigest := "sha256:" + hex.EncodeToString(sum[:])
	if !gitObjectPattern.MatchString(result.CommitSHA) ||
		len(result.CommitSHA) != 40 ||
		result.ParentSHA != request.ExpectedParentSHA ||
		result.Path != path ||
		result.ContentDigest != expectedDigest ||
		result.AuthorLogin != store.appLogin ||
		result.AuthorType != "Bot" ||
		!result.Verified ||
		!result.SignedByGitHub {
		return StatePublication{}, errors.New("published state commit is untrusted")
	}
	return StatePublication{HeadSHA: result.CommitSHA}, nil
}

// Load verifies the complete bounded App-signed state chain.
func (store *StateStore) Load(
	ctx context.Context,
	issueNumber int64,
) (LoadedState, bool, error) {
	if store == nil || store.commits == nil || ctx == nil || issueNumber <= 0 {
		return LoadedState{}, false, errors.New("State Store load request is invalid")
	}
	branch := fmt.Sprintf("agent-state/issue-%d", issueNumber)
	path := fmt.Sprintf(".issue-agent-state/issue-%d.json", issueNumber)
	head, found, err := store.commits.StateRefHead(ctx, branch)
	if err != nil || !found {
		return LoadedState{}, found, err
	}
	if !gitObjectPattern.MatchString(head) || len(head) != 40 {
		return LoadedState{}, false, errors.New("state ref head is invalid")
	}

	var latest contract.IssueAgentState
	var newer *contract.IssueAgentState
	commitSHA := head
	for count := 0; count < 512; count++ {
		record, err := store.commits.ReadStateCommit(ctx, commitSHA, path)
		if err != nil {
			return LoadedState{}, false, err
		}
		state, err := store.validateStateRecord(record, commitSHA, path, issueNumber)
		if err != nil {
			return LoadedState{}, false, err
		}
		if count == 0 {
			latest = state
		}
		if newer != nil {
			digest, err := contract.IssueAgentStateDigest(state)
			if err != nil {
				return LoadedState{}, false, err
			}
			if newer.PreviousStateDigest != digest ||
				state.Sequence+1 != newer.Sequence ||
				state.UpdatedAt.After(newer.UpdatedAt) {
				return LoadedState{}, false,
					errors.New("state commit chain is not contiguous")
			}
		}
		if state.Sequence == 1 {
			if record.ParentSHA != state.SourceSHA {
				return LoadedState{}, false,
					errors.New("initial state commit has the wrong source parent")
			}
			return LoadedState{HeadSHA: head, State: latest}, true, nil
		}
		newer = &state
		commitSHA = record.ParentSHA
	}
	return LoadedState{}, false, errors.New("state commit chain exceeds history bound")
}

func (store *StateStore) validateStateRecord(
	record StateCommitRecord,
	expectedCommitSHA string,
	expectedPath string,
	issueNumber int64,
) (contract.IssueAgentState, error) {
	if record.CommitSHA != expectedCommitSHA ||
		!gitObjectPattern.MatchString(record.ParentSHA) ||
		len(record.ParentSHA) != 40 ||
		record.Path != expectedPath ||
		record.AuthorLogin != store.appLogin ||
		record.AuthorType != "Bot" ||
		!record.Verified ||
		!record.SignedByGitHub {
		return contract.IssueAgentState{}, errors.New("state commit is untrusted")
	}
	state, err := contract.DecodeIssueAgentState(
		bytes.NewReader(record.Content),
		256<<10,
	)
	if err != nil || state.Repository != store.repository ||
		state.IssueNumber != issueNumber {
		return contract.IssueAgentState{}, errors.New("state commit content is invalid")
	}
	expectedMessage := fmt.Sprintf(
		"agent(state): issue %d sequence %d",
		issueNumber,
		state.Sequence,
	)
	if record.Message != expectedMessage {
		return contract.IssueAgentState{}, errors.New("state commit message is invalid")
	}
	return state, nil
}

// PublishStateCommit implements the GitHub-signed StateCommitPort.
func (client *Client) PublishStateCommit(
	ctx context.Context,
	request StateCommitRequest,
) (StateCommitResult, error) {
	if client == nil || ctx == nil ||
		!stateRefPattern.MatchString(request.Branch) ||
		!gitObjectPattern.MatchString(request.ExpectedParentSHA) ||
		len(request.ExpectedParentSHA) != 40 ||
		!gitObjectPattern.MatchString(request.BaseTreeSHA) ||
		len(request.BaseTreeSHA) != 40 ||
		len(request.Content) == 0 || len(request.Content) > 256<<10 {
		return StateCommitResult{}, errors.New("state commit request is invalid")
	}
	state, err := contract.DecodeIssueAgentState(
		bytes.NewReader(request.Content),
		256<<10,
	)
	if err != nil || state.Repository != client.repository {
		return StateCommitResult{}, errors.New("state commit content is invalid")
	}
	expectedBranch := fmt.Sprintf("agent-state/issue-%d", state.IssueNumber)
	expectedPath := fmt.Sprintf(
		".issue-agent-state/issue-%d.json",
		state.IssueNumber,
	)
	canonical, err := contract.CanonicalIssueAgentState(state)
	if err != nil || !bytes.Equal(canonical, request.Content) ||
		request.Branch != expectedBranch ||
		request.Path != expectedPath {
		return StateCommitResult{}, errors.New("state commit target is inconsistent")
	}
	published, err := client.PublishCommit(ctx, CommitPlan{
		Purpose: CommitPurposeState, Branch: request.Branch,
		ExpectedParentSHA: request.ExpectedParentSHA,
		BaseTreeSHA:       request.BaseTreeSHA,
		Message:           request.Message,
		ExistingBranch:    request.ExistingBranch,
		ChangeSet: contract.ChangeSet{Files: []contract.FileChange{{
			Path: request.Path, Operation: contract.FileOperationUpsert,
			Mode:          contract.FileModeRegular,
			ContentBase64: contract.EncodeFileContent(request.Content),
		}}},
	})
	if err != nil {
		return StateCommitResult{}, err
	}
	commit, err := client.Commit(ctx, published.CommitSHA)
	if err != nil {
		return StateCommitResult{}, err
	}
	attribution, err := client.CommitAttribution(ctx, published.CommitSHA)
	if err != nil {
		return StateCommitResult{}, err
	}
	sum := sha256.Sum256(request.Content)
	return StateCommitResult{
		CommitSHA: published.CommitSHA, ParentSHA: request.ExpectedParentSHA,
		Path:          request.Path,
		ContentDigest: "sha256:" + hex.EncodeToString(sum[:]),
		AuthorLogin:   attribution.AuthorLogin, AuthorType: attribution.AuthorType,
		Verified: commit.Verified && commit.VerificationReason == "valid" &&
			attribution.SignatureValid && attribution.SignatureState == "VALID",
		SignedByGitHub: attribution.WasSignedByGitHub,
	}, nil
}

// StateRefHead reads the exact current head of one per-Issue state ref.
func (client *Client) StateRefHead(
	ctx context.Context,
	branch string,
) (string, bool, error) {
	if !stateRefPattern.MatchString(branch) {
		return "", false, errors.New("state ref name is invalid")
	}
	ref, found, err := client.RefIfExists(ctx, branch)
	if err != nil || !found {
		return "", found, err
	}
	return ref.SHA, true, nil
}

// ReadStateCommit independently verifies one commit changed only its state file.
func (client *Client) ReadStateCommit(
	ctx context.Context,
	commitSHA string,
	path string,
) (StateCommitRecord, error) {
	if client == nil || ctx == nil ||
		!gitObjectPattern.MatchString(commitSHA) ||
		len(commitSHA) != 40 ||
		!strings.HasPrefix(path, ".issue-agent-state/issue-") ||
		!strings.HasSuffix(path, ".json") {
		return StateCommitRecord{}, errors.New("state commit read request is invalid")
	}
	commit, err := client.Commit(ctx, commitSHA)
	if err != nil {
		return StateCommitRecord{}, err
	}
	if len(commit.Parents) != 1 ||
		!commit.Verified ||
		commit.VerificationReason != "valid" {
		return StateCommitRecord{}, errors.New("state commit signature is invalid")
	}
	changes, err := client.CompareOneCommit(ctx, commit.Parents[0], commitSHA)
	if err != nil || len(changes) != 1 ||
		changes[0].Path != path ||
		(changes[0].Status != "added" && changes[0].Status != "modified") {
		return StateCommitRecord{}, errors.New("state commit changed an unexpected path")
	}
	entry, found, err := client.ResolveTreePath(ctx, commit.TreeSHA, path)
	if err != nil || !found || entry.Type != "blob" || entry.Mode != "100644" ||
		entry.SHA != changes[0].SHA {
		return StateCommitRecord{}, errors.New("state commit tree is inconsistent")
	}
	content, err := client.readGitBlob(ctx, entry.SHA, 256<<10)
	if err != nil {
		return StateCommitRecord{}, err
	}
	attribution, err := client.CommitAttribution(ctx, commitSHA)
	if err != nil {
		return StateCommitRecord{}, err
	}
	return StateCommitRecord{
		CommitSHA: commitSHA, ParentSHA: commit.Parents[0],
		Message: commit.Message, Path: path, Content: content,
		AuthorLogin: attribution.AuthorLogin, AuthorType: attribution.AuthorType,
		Verified: commit.Verified && commit.VerificationReason == "valid" &&
			attribution.SignatureValid && attribution.SignatureState == "VALID",
		SignedByGitHub: attribution.WasSignedByGitHub,
	}, nil
}

func (client *Client) readGitBlob(
	ctx context.Context,
	sha string,
	maxBytes int64,
) ([]byte, error) {
	if !gitObjectPattern.MatchString(sha) || maxBytes <= 0 ||
		maxBytes > 8<<20 {
		return nil, errors.New("Git blob request is invalid")
	}
	var payload struct {
		SHA      string `json:"sha"`
		Size     int64  `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/git/blobs/"+sha,
		&payload,
	); err != nil {
		return nil, err
	}
	if payload.SHA != sha || payload.Size < 0 || payload.Size > maxBytes ||
		payload.Encoding != "base64" {
		return nil, errors.New("Git blob response is invalid")
	}
	encoded := strings.ReplaceAll(payload.Content, "\n", "")
	content, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || int64(len(content)) != payload.Size ||
		gitBlobObjectSHA(content) != sha {
		return nil, errors.New("Git blob content is inconsistent")
	}
	return content, nil
}
