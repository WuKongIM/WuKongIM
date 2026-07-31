package reviewagentgithub

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- Git blob identity is SHA-1 by protocol.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var reviewStateRefPattern = regexp.MustCompile(
	`^review-state/(?:scheduler|pr-[1-9][0-9]{0,9})$`,
)

const (
	stateRefVisibilityAttempts     = 8
	stateRefVisibilityInitialDelay = 100 * time.Millisecond
	stateRefVisibilityMaxDelay     = time.Second
)

type gitCommitFacts struct {
	SHA                string
	TreeSHA            string
	Parents            []string
	Message            string
	Verified           bool
	VerificationReason string
}

type commitAttribution struct {
	AuthorLogin       string
	AuthorType        string
	SignatureValid    bool
	SignatureState    string
	WasSignedByGitHub bool
}

// StateRefHead reads only one Review Agent state ref.
func (client *Client) StateRefHead(
	ctx context.Context,
	branch string,
) (string, bool, error) {
	if client == nil || ctx == nil || !reviewStateRefPattern.MatchString(branch) {
		return "", false, errors.New("Review state ref name is invalid")
	}
	endpoint := client.endpoint(
		"/repos/" + client.repository + "/git/ref/heads/" + branch,
	)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return "", false, errors.New("create Review state ref request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", false, redactHTTPError(err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", false, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", false, fmt.Errorf(
			"GitHub API returned status %d",
			response.StatusCode,
		)
	}
	var payload struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := decodeResponseJSON(response, client.maxBodyBytes, &payload); err != nil {
		return "", false, err
	}
	if payload.Ref != "refs/heads/"+branch ||
		payload.Object.Type != "commit" ||
		!gitSHAPattern.MatchString(payload.Object.SHA) {
		return "", false, errors.New("Review state ref response is invalid")
	}
	return payload.Object.SHA, true, nil
}

// PublishStateCommit creates one GitHub-signed, expected-head state commit and
// accepts it only after an independent immutable re-read.
func (client *Client) PublishStateCommit(
	ctx context.Context,
	request StateCommitRequest,
) (StateCommitResult, error) {
	if client == nil || ctx == nil ||
		!validStateTarget(request.Branch, request.Path) ||
		!gitSHAPattern.MatchString(request.ExpectedParentSHA) ||
		strings.TrimSpace(request.Message) == "" ||
		len(request.Message) > 1024 ||
		len(request.Content) == 0 ||
		len(request.Content) > 512<<10 {
		return StateCommitResult{}, errors.New(
			"Review state commit request is invalid",
		)
	}
	current, exists, err := client.StateRefHead(ctx, request.Branch)
	if err != nil {
		return StateCommitResult{}, err
	}
	if request.ExistingBranch {
		if !exists || current != request.ExpectedParentSHA {
			return StateCommitResult{}, errors.New(
				"Review state ref head changed",
			)
		}
	} else {
		if exists && current != request.ExpectedParentSHA {
			return StateCommitResult{}, errors.New(
				"Review state bootstrap ref head changed",
			)
		}
		if !exists {
			var created struct {
				Ref    string `json:"ref"`
				Object struct {
					Type string `json:"type"`
					SHA  string `json:"sha"`
				} `json:"object"`
			}
			err = client.requestJSON(
				ctx,
				http.MethodPost,
				"/repos/"+client.repository+"/git/refs",
				struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				}{
					Ref: "refs/heads/" + request.Branch,
					SHA: request.ExpectedParentSHA,
				},
				&created,
				http.StatusCreated,
			)
			if err != nil ||
				created.Ref != "refs/heads/"+request.Branch ||
				created.Object.Type != "commit" ||
				created.Object.SHA != request.ExpectedParentSHA {
				return StateCommitResult{}, errors.New(
					"Review state ref creation failed",
				)
			}
		}
	}

	type addition struct {
		Path     string `json:"path"`
		Contents string `json:"contents"`
	}
	var mutationResponse struct {
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
	mutationRequest := struct {
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
					Additions []addition `json:"additions"`
				} `json:"fileChanges"`
			} `json:"input"`
		} `json:"variables"`
	}{Query: mutation}
	input := &mutationRequest.Variables.Input
	input.Branch.RepositoryNameWithOwner = client.repository
	input.Branch.BranchName = request.Branch
	input.Message.Headline = request.Message
	input.ExpectedHeadOID = request.ExpectedParentSHA
	input.FileChanges.Additions = []addition{{
		Path: request.Path,
		Contents: base64.StdEncoding.EncodeToString(
			request.Content,
		),
	}}
	if err := client.requestGraphQL(
		ctx,
		mutationRequest,
		&mutationResponse,
	); err != nil {
		return StateCommitResult{}, err
	}
	commitSHA := mutationResponse.Data.CreateCommitOnBranch.Commit.OID
	if len(mutationResponse.Errors) != 0 ||
		!gitSHAPattern.MatchString(commitSHA) ||
		commitSHA == request.ExpectedParentSHA {
		return StateCommitResult{}, errors.New(
			"Review state signed-commit mutation failed",
		)
	}
	if err := client.waitForStateRefHead(
		ctx,
		request.Branch,
		request.ExpectedParentSHA,
		commitSHA,
		stateRefVisibilityAttempts,
		stateRefVisibilityInitialDelay,
	); err != nil {
		return StateCommitResult{}, errors.New(
			"Review state ref re-read is inconsistent",
		)
	}
	record, err := client.ReadStateCommit(ctx, commitSHA, request.Path)
	if err != nil {
		return StateCommitResult{}, err
	}
	sum := sha256.Sum256(record.Content)
	return StateCommitResult{
		CommitSHA: record.CommitSHA, ParentSHA: record.ParentSHA,
		Path:          record.Path,
		ContentDigest: "sha256:" + hex.EncodeToString(sum[:]),
		AuthorLogin:   record.AuthorLogin, AuthorType: record.AuthorType,
		Verified: record.Verified, SignedByGitHub: record.SignedByGitHub,
	}, nil
}

// waitForStateRefHead tolerates only bounded GitHub visibility lag that still
// reports the exact pre-mutation parent. Any third head is real contention and
// fails immediately.
func (client *Client) waitForStateRefHead(
	ctx context.Context,
	branch string,
	expectedParentSHA string,
	expectedHeadSHA string,
	attempts int,
	initialDelay time.Duration,
) error {
	if client == nil || ctx == nil ||
		!reviewStateRefPattern.MatchString(branch) ||
		!gitSHAPattern.MatchString(expectedParentSHA) ||
		!gitSHAPattern.MatchString(expectedHeadSHA) ||
		expectedParentSHA == expectedHeadSHA ||
		attempts <= 0 ||
		initialDelay < 0 {
		return errors.New("Review state ref re-read request is invalid")
	}
	delay := initialDelay
	for attempt := 0; attempt < attempts; attempt++ {
		head, found, err := client.StateRefHead(ctx, branch)
		if err != nil {
			return err
		}
		if found && head == expectedHeadSHA {
			return nil
		}
		if !found || head != expectedParentSHA {
			return errors.New("Review state ref re-read is inconsistent")
		}
		if attempt+1 == attempts {
			break
		}
		if err := waitForStateRefVisibility(ctx, delay); err != nil {
			return err
		}
		if delay > 0 && delay < stateRefVisibilityMaxDelay {
			delay *= 2
			if delay > stateRefVisibilityMaxDelay {
				delay = stateRefVisibilityMaxDelay
			}
		}
	}
	return errors.New("Review state ref re-read is inconsistent")
}

func waitForStateRefVisibility(
	ctx context.Context,
	delay time.Duration,
) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReadStateCommit proves that one verified App commit changed exactly one
// regular state file and returns its exact content.
func (client *Client) ReadStateCommit(
	ctx context.Context,
	commitSHA string,
	path string,
) (StateCommitRecord, error) {
	if client == nil || ctx == nil ||
		!gitSHAPattern.MatchString(commitSHA) ||
		!validStatePath(path) {
		return StateCommitRecord{}, errors.New(
			"Review state commit read request is invalid",
		)
	}
	commit, err := client.readGitCommit(ctx, commitSHA)
	if err != nil {
		return StateCommitRecord{}, err
	}
	if len(commit.Parents) != 1 ||
		!commit.Verified ||
		commit.VerificationReason != "valid" {
		return StateCommitRecord{}, errors.New(
			"Review state commit signature is invalid",
		)
	}
	file, err := client.readSingleCommitFile(
		ctx,
		commit.Parents[0],
		commitSHA,
	)
	if err != nil || file.Path != path ||
		(file.Status != "added" && file.Status != "modified") {
		return StateCommitRecord{}, errors.New(
			"Review state commit changed an unexpected path",
		)
	}
	tree, err := client.readTree(ctx, commit.TreeSHA)
	if err != nil {
		return StateCommitRecord{}, err
	}
	entry, exists := tree[path]
	if !exists || entry.Type != "blob" ||
		entry.Mode != "100644" ||
		entry.SHA != file.SHA {
		return StateCommitRecord{}, errors.New(
			"Review state commit tree is inconsistent",
		)
	}
	content, err := client.readBlob(ctx, entry.SHA)
	if err != nil || gitBlobSHA(content) != entry.SHA {
		return StateCommitRecord{}, errors.New(
			"Review state blob is inconsistent",
		)
	}
	attribution, err := client.readCommitAttribution(ctx, commitSHA)
	if err != nil {
		return StateCommitRecord{}, err
	}
	return StateCommitRecord{
		CommitSHA: commitSHA, ParentSHA: commit.Parents[0],
		Message: commit.Message, Path: path, Content: content,
		AuthorLogin: attribution.AuthorLogin,
		AuthorType:  attribution.AuthorType,
		Verified: commit.Verified &&
			attribution.SignatureValid &&
			attribution.SignatureState == "VALID",
		SignedByGitHub: attribution.WasSignedByGitHub,
	}, nil
}

func (client *Client) readGitCommit(
	ctx context.Context,
	sha string,
) (gitCommitFacts, error) {
	var payload struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
		Tree    struct {
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
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/git/commits/"+sha,
		&payload,
	); err != nil {
		return gitCommitFacts{}, err
	}
	if payload.SHA != sha ||
		!gitSHAPattern.MatchString(payload.Tree.SHA) ||
		len(payload.Parents) != 1 ||
		len(payload.Message) > 1024 {
		return gitCommitFacts{}, errors.New(
			"Review state Git commit response is invalid",
		)
	}
	parents := make([]string, 0, 1)
	for _, parent := range payload.Parents {
		if !gitSHAPattern.MatchString(parent.SHA) {
			return gitCommitFacts{}, errors.New(
				"Review state Git parent is invalid",
			)
		}
		parents = append(parents, parent.SHA)
	}
	return gitCommitFacts{
		SHA: payload.SHA, TreeSHA: payload.Tree.SHA,
		Parents: parents, Message: payload.Message,
		Verified:           payload.Verification.Verified,
		VerificationReason: payload.Verification.Reason,
	}, nil
}

type commitFile struct {
	Path   string
	Status string
	SHA    string
}

func (client *Client) readSingleCommitFile(
	ctx context.Context,
	parentSHA string,
	commitSHA string,
) (commitFile, error) {
	var payload struct {
		Status       string `json:"status"`
		AheadBy      int    `json:"ahead_by"`
		BehindBy     int    `json:"behind_by"`
		TotalCommits int    `json:"total_commits"`
		Files        []struct {
			Filename string `json:"filename"`
			Status   string `json:"status"`
			SHA      string `json:"sha"`
		} `json:"files"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/compare/"+parentSHA+"..."+commitSHA,
		&payload,
	); err != nil {
		return commitFile{}, err
	}
	if payload.Status != "ahead" ||
		payload.AheadBy != 1 ||
		payload.BehindBy != 0 ||
		payload.TotalCommits != 1 ||
		len(payload.Files) != 1 ||
		!gitSHAPattern.MatchString(payload.Files[0].SHA) {
		return commitFile{}, errors.New(
			"Review state commit comparison is invalid",
		)
	}
	return commitFile{
		Path:   payload.Files[0].Filename,
		Status: payload.Files[0].Status,
		SHA:    payload.Files[0].SHA,
	}, nil
}

func (client *Client) readCommitAttribution(
	ctx context.Context,
	sha string,
) (commitAttribution, error) {
	var rest struct {
		SHA    string `json:"sha"`
		Author *struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"author"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/commits/"+sha,
		&rest,
	); err != nil {
		return commitAttribution{}, err
	}
	if rest.SHA != sha || rest.Author == nil ||
		!appBotLoginPattern.MatchString(rest.Author.Login) ||
		rest.Author.Type != "Bot" {
		return commitAttribution{}, errors.New(
			"Review state commit attribution is invalid",
		)
	}
	parts := strings.Split(client.repository, "/")
	var response struct {
		Data struct {
			Repository *struct {
				NameWithOwner string `json:"nameWithOwner"`
				Object        *struct {
					OID       string `json:"oid"`
					Signature *struct {
						IsValid           bool   `json:"isValid"`
						State             string `json:"state"`
						WasSignedByGitHub bool   `json:"wasSignedByGitHub"`
					} `json:"signature"`
				} `json:"object"`
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
			OID   string `json:"oid"`
		} `json:"variables"`
	}{
		Query: `query($owner:String!,$name:String!,$oid:GitObjectID!){` +
			`repository(owner:$owner,name:$name){nameWithOwner ` +
			`object(oid:$oid){... on Commit{oid signature{` +
			`isValid state wasSignedByGitHub}}}}}`,
	}
	request.Variables.Owner = parts[0]
	request.Variables.Name = parts[1]
	request.Variables.OID = sha
	if err := client.requestGraphQL(ctx, request, &response); err != nil {
		return commitAttribution{}, err
	}
	repository := response.Data.Repository
	if len(response.Errors) != 0 || repository == nil ||
		repository.NameWithOwner != client.repository ||
		repository.Object == nil ||
		repository.Object.OID != sha ||
		repository.Object.Signature == nil {
		return commitAttribution{}, errors.New(
			"Review state commit signature attribution is invalid",
		)
	}
	return commitAttribution{
		AuthorLogin:       rest.Author.Login,
		AuthorType:        rest.Author.Type,
		SignatureValid:    repository.Object.Signature.IsValid,
		SignatureState:    repository.Object.Signature.State,
		WasSignedByGitHub: repository.Object.Signature.WasSignedByGitHub,
	}, nil
}

func decodeResponseJSON(response *http.Response, maxBytes int64, output any) error {
	mediaType, _, err := mime.ParseMediaType(
		response.Header.Get("Content-Type"),
	)
	if err != nil || mediaType != "application/json" {
		return errors.New("GitHub API returned unexpected content type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return errors.New("GitHub API response exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(output); err != nil {
		return errors.New("decode GitHub API response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("GitHub API response contains trailing JSON")
	}
	return nil
}

func validStateTarget(branch string, path string) bool {
	if branch == schedulerStateBranch {
		return path == schedulerStatePath
	}
	number := strings.TrimPrefix(branch, "review-state/pr-")
	if !reviewStateRefPattern.MatchString(branch) {
		return false
	}
	return path == ".review-agent-state/pr-"+number+".json"
}

func validStatePath(path string) bool {
	if path == schedulerStatePath {
		return true
	}
	if !strings.HasPrefix(path, ".review-agent-state/pr-") ||
		!strings.HasSuffix(path, ".json") {
		return false
	}
	number := strings.TrimSuffix(
		strings.TrimPrefix(path, ".review-agent-state/pr-"),
		".json",
	)
	parsed, err := strconv.ParseInt(number, 10, 64)
	return err == nil && parsed > 0 &&
		strconv.FormatInt(parsed, 10) == number
}

func gitBlobSHA(content []byte) string {
	hasher := sha1.New() // #nosec G401 -- Git blob identity is SHA-1 by protocol.
	_, _ = hasher.Write(
		[]byte("blob " + strconv.Itoa(len(content)) + "\x00"),
	)
	_, _ = hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}
