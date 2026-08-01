package issueagentgithub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	gitObjectPattern     = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	loginPattern         = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	agentRefPattern      = regexp.MustCompile(`^agent/issue-[1-9][0-9]*$`)
	agentStageRefPattern = regexp.MustCompile(
		`^agent/issue-[1-9][0-9]*-rebase-[0-9a-f]{64}$`,
	)
	stateRefPattern = regexp.MustCompile(`^agent-state/issue-[1-9][0-9]*$`)
)

// IssueFacts is the bounded Issue state consumed by authorization and planning.
type IssueFacts struct {
	ID                string
	Number            int64
	State             string
	Title             string
	Body              string
	Author            string
	AuthorAssociation string
	Labels            []string
	UpdatedAt         time.Time
}

// IssueComment is the bounded GitHub comment projection used by v2.
type IssueComment struct {
	ID                int64
	Author            string
	AuthorType        string
	AuthorAssociation string
	Body              string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Permission is one normalized repository permission level.
type Permission string

const (
	PermissionRead     Permission = "read"
	PermissionTriage   Permission = "triage"
	PermissionWrite    Permission = "write"
	PermissionMaintain Permission = "maintain"
	PermissionAdmin    Permission = "admin"
)

// PullRequestFacts is the exact branch and mergeability snapshot used by fencing.
type PullRequestFacts struct {
	Number      int64
	State       string
	Draft       bool
	Mergeable   *bool
	Merged      bool
	BaseRef     string
	BaseSHA     string
	HeadRef     string
	HeadSHA     string
	MergeCommit string
}

// RefFacts binds a named Agent branch to its current commit.
type RefFacts struct {
	Name string
	SHA  string
}

// TreeEntryFacts is one exact Git tree path resolved from an immutable root.
type TreeEntryFacts struct {
	Path string
	Type string
	Mode string
	SHA  string
}

// CompareFileFacts is one exact final file change in a bounded comparison.
type CompareFileFacts struct {
	Path   string
	Status string
	SHA    string
}

// CandidateComparisonRejection marks a successfully read comparison whose
// structure cannot safely describe one exact aggregate candidate.
type CandidateComparisonRejection interface {
	error
	CandidateComparisonRejected()
}

type candidateComparisonRejection struct {
	reason string
}

func (rejection *candidateComparisonRejection) Error() string {
	return rejection.reason
}

func (*candidateComparisonRejection) CandidateComparisonRejected() {}

func rejectCandidateComparison(reason string) error {
	return &candidateComparisonRejection{reason: reason}
}

// DefaultBranchHead reads the exact protected main ref without permitting writes.
func (client *Client) DefaultBranchHead(
	ctx context.Context,
	branch string,
) (RefFacts, error) {
	if branch != "main" {
		return RefFacts{}, errors.New("Issue Agent diagnosis baseline must be main")
	}
	var payload struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := client.getJSON(
		ctx, "/repos/"+client.repository+"/git/ref/heads/main", &payload,
	); err != nil {
		return RefFacts{}, err
	}
	if payload.Ref != "refs/heads/main" || payload.Object.Type != "commit" ||
		!gitObjectPattern.MatchString(payload.Object.SHA) ||
		len(payload.Object.SHA) != 40 {
		return RefFacts{}, errors.New("GitHub main ref response is invalid")
	}
	return RefFacts{Name: "main", SHA: payload.Object.SHA}, nil
}

// CommitFacts records the tree, parents, and GitHub verification result.
type CommitFacts struct {
	SHA                string
	TreeSHA            string
	Parents            []string
	Message            string
	Verified           bool
	VerificationReason string
}

// CommitAttributionFacts binds one commit to the GitHub identity that authored
// it through the authenticated web-commit surface.
type CommitAttributionFacts struct {
	SHA               string
	AuthorLogin       string
	AuthorType        string
	SignatureValid    bool
	SignatureState    string
	WasSignedByGitHub bool
}

// Issue reads the current Issue, labels, author, and author association.
func (client *Client) Issue(ctx context.Context, number int64) (IssueFacts, error) {
	if number <= 0 {
		return IssueFacts{}, errors.New("Issue number is invalid")
	}
	var payload struct {
		NodeID  string   `json:"node_id"`
		Number  int64    `json:"number"`
		State   string   `json:"state"`
		Title   string   `json:"title"`
		Body    string   `json:"body"`
		Updated jsonTime `json:"updated_at"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		AuthorAssociation string `json:"author_association"`
		Labels            []struct {
			Name string `json:"name"`
		} `json:"labels"`
		PullRequest any `json:"pull_request"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/issues/"+strconv.FormatInt(number, 10),
		&payload,
	); err != nil {
		return IssueFacts{}, err
	}
	if payload.Number != number || payload.Title == "" || len(payload.Title) > 1024 ||
		len(payload.Body) > 1<<20 || payload.User.Login == "" ||
		(payload.State != "open" && payload.State != "closed") ||
		payload.PullRequest != nil || len(payload.Labels) > 100 {
		return IssueFacts{}, errors.New("GitHub Issue response is invalid")
	}
	labels := make([]string, 0, len(payload.Labels))
	seen := make(map[string]struct{}, len(payload.Labels))
	for _, label := range payload.Labels {
		if label.Name == "" || len(label.Name) > 100 {
			return IssueFacts{}, errors.New("GitHub Issue label is invalid")
		}
		if _, duplicate := seen[label.Name]; duplicate {
			return IssueFacts{}, errors.New("GitHub Issue labels contain a duplicate")
		}
		seen[label.Name] = struct{}{}
		labels = append(labels, label.Name)
	}
	slices.Sort(labels)
	return IssueFacts{
		ID: payload.NodeID, Number: payload.Number,
		State: payload.State, Title: payload.Title,
		Body: payload.Body, Author: payload.User.Login,
		AuthorAssociation: payload.AuthorAssociation, Labels: labels,
		UpdatedAt: payload.Updated.Time,
	}, nil
}

// IssueComment reads one exact comment and binds its issue_url back to the
// expected Issue before it can authorize a state change.
func (client *Client) IssueComment(
	ctx context.Context,
	commentID int64,
	issueNumber int64,
) (IssueComment, error) {
	if commentID <= 0 || issueNumber <= 0 {
		return IssueComment{}, errors.New("Issue comment identity is invalid")
	}
	var payload struct {
		ID       int64  `json:"id"`
		IssueURL string `json:"issue_url"`
		User     struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
		AuthorAssociation string   `json:"author_association"`
		Body              string   `json:"body"`
		CreatedAt         jsonTime `json:"created_at"`
		UpdatedAt         jsonTime `json:"updated_at"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/issues/comments/"+strconv.FormatInt(commentID, 10),
		&payload,
	); err != nil {
		return IssueComment{}, err
	}
	expectedSuffix := "/repos/" + client.repository + "/issues/" +
		strconv.FormatInt(issueNumber, 10)
	parsed, err := url.Parse(payload.IssueURL)
	if payload.ID != commentID || payload.User.Login == "" ||
		payload.User.Type == "" || len(payload.Body) > 64<<10 ||
		payload.CreatedAt.Time.IsZero() || payload.UpdatedAt.Time.IsZero() ||
		err != nil || parsed.Path != expectedSuffix {
		return IssueComment{}, errors.New("GitHub Issue comment response is invalid")
	}
	return IssueComment{
		ID: payload.ID, Author: payload.User.Login, AuthorType: payload.User.Type,
		AuthorAssociation: payload.AuthorAssociation,
		Body:              payload.Body, CreatedAt: payload.CreatedAt.Time,
		UpdatedAt: payload.UpdatedAt.Time,
	}, nil
}

// ActorPermission resolves a fresh repository permission for an event actor.
func (client *Client) ActorPermission(ctx context.Context, actor string) (Permission, error) {
	if !loginPattern.MatchString(actor) {
		return "", errors.New("GitHub actor login is invalid")
	}
	var payload struct {
		Permission string `json:"permission"`
		User       struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/collaborators/"+actor+"/permission",
		&payload,
	); err != nil {
		return "", err
	}
	permission := Permission(payload.Permission)
	switch permission {
	case PermissionRead, PermissionTriage, PermissionWrite,
		PermissionMaintain, PermissionAdmin:
	default:
		return "", errors.New("GitHub actor permission is unknown")
	}
	if payload.User.Login != actor {
		return "", errors.New("GitHub actor permission identity mismatch")
	}
	return permission, nil
}

// PullRequest reads one exact PR branch snapshot.
func (client *Client) PullRequest(ctx context.Context, number int64) (PullRequestFacts, error) {
	if number <= 0 {
		return PullRequestFacts{}, errors.New("pull request number is invalid")
	}
	var payload struct {
		Number      int64  `json:"number"`
		State       string `json:"state"`
		Draft       bool   `json:"draft"`
		Mergeable   *bool  `json:"mergeable"`
		Merged      bool   `json:"merged"`
		MergeCommit string `json:"merge_commit_sha"`
		Base        struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/pulls/"+strconv.FormatInt(number, 10),
		&payload,
	); err != nil {
		return PullRequestFacts{}, err
	}
	if payload.Number != number || (payload.State != "open" && payload.State != "closed") ||
		!validBranchName(payload.Base.Ref) || !agentRefPattern.MatchString(payload.Head.Ref) ||
		!gitObjectPattern.MatchString(payload.Base.SHA) ||
		!gitObjectPattern.MatchString(payload.Head.SHA) ||
		payload.MergeCommit != "" && !gitObjectPattern.MatchString(payload.MergeCommit) ||
		payload.Merged &&
			(payload.State != "closed" || payload.MergeCommit == "") {
		return PullRequestFacts{}, errors.New("GitHub pull request response is invalid")
	}
	return PullRequestFacts{
		Number: payload.Number, State: payload.State, Draft: payload.Draft,
		Mergeable: payload.Mergeable, Merged: payload.Merged,
		BaseRef: payload.Base.Ref,
		BaseSHA: payload.Base.SHA, HeadRef: payload.Head.Ref,
		HeadSHA: payload.Head.SHA, MergeCommit: payload.MergeCommit,
	}, nil
}

// Ref reads one Agent branch. Other refs are outside this narrow port.
func (client *Client) Ref(ctx context.Context, branch string) (RefFacts, error) {
	if !isAgentManagedRef(branch) {
		return RefFacts{}, errors.New("GitHub ref is not an Agent branch")
	}
	var payload struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/git/ref/heads/"+branch,
		&payload,
	); err != nil {
		return RefFacts{}, err
	}
	if payload.Ref != "refs/heads/"+branch || payload.Object.Type != "commit" ||
		!gitObjectPattern.MatchString(payload.Object.SHA) {
		return RefFacts{}, errors.New("GitHub ref response is invalid")
	}
	return RefFacts{Name: branch, SHA: payload.Object.SHA}, nil
}

// RefIfExists reads an Agent branch while distinguishing an exact 404 from an
// existing or malformed ref.
func (client *Client) RefIfExists(
	ctx context.Context,
	branch string,
) (RefFacts, bool, error) {
	if !isAgentManagedRef(branch) {
		return RefFacts{}, false, errors.New("GitHub ref is not an Agent branch")
	}
	var payload struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := client.requestJSON(
		ctx, http.MethodGet,
		"/repos/"+client.repository+"/git/ref/heads/"+branch,
		nil, &payload, http.StatusOK, http.StatusNotFound,
	); err != nil {
		return RefFacts{}, false, err
	}
	if payload.Ref == "" {
		return RefFacts{}, false, nil
	}
	if payload.Ref != "refs/heads/"+branch ||
		payload.Object.Type != "commit" ||
		!gitObjectPattern.MatchString(payload.Object.SHA) {
		return RefFacts{}, false, errors.New("GitHub ref response is invalid")
	}
	return RefFacts{Name: branch, SHA: payload.Object.SHA}, true, nil
}

func isAgentManagedRef(branch string) bool {
	return agentRefPattern.MatchString(branch) ||
		agentStageRefPattern.MatchString(branch) ||
		stateRefPattern.MatchString(branch)
}

// ResolveTreePath walks exact non-recursive Git trees so publication never
// assumes whether a Worker path already exists at the frozen parent.
func (client *Client) ResolveTreePath(
	ctx context.Context,
	rootTreeSHA string,
	repositoryPath string,
) (TreeEntryFacts, bool, error) {
	if !gitObjectPattern.MatchString(rootTreeSHA) ||
		!validRepositoryPath(repositoryPath) {
		return TreeEntryFacts{}, false, errors.New("Git tree path input is invalid")
	}
	parts := strings.Split(repositoryPath, "/")
	treeSHA := rootTreeSHA
	for index, part := range parts {
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
		if err := client.getJSON(
			ctx, "/repos/"+client.repository+"/git/trees/"+treeSHA, &payload,
		); err != nil {
			return TreeEntryFacts{}, false, err
		}
		if payload.SHA != treeSHA || payload.Truncated || len(payload.Tree) > 100000 {
			return TreeEntryFacts{}, false, errors.New("Git tree response is incomplete")
		}
		var exact *TreeEntryFacts
		for _, entry := range payload.Tree {
			if entry.Path == "" || strings.Contains(entry.Path, "/") ||
				!gitObjectPattern.MatchString(entry.SHA) {
				return TreeEntryFacts{}, false, errors.New("Git tree entry is invalid")
			}
			if strings.EqualFold(entry.Path, part) && entry.Path != part {
				return TreeEntryFacts{}, false,
					errors.New("Git tree contains a case-colliding path")
			}
			if entry.Path == part {
				value := TreeEntryFacts{
					Path: strings.Join(parts[:index+1], "/"),
					Type: entry.Type, Mode: entry.Mode, SHA: entry.SHA,
				}
				exact = &value
			}
		}
		if exact == nil {
			return TreeEntryFacts{}, false, nil
		}
		if index == len(parts)-1 {
			return *exact, true, nil
		}
		if exact.Type != "tree" {
			return TreeEntryFacts{}, false,
				errors.New("Git tree path traverses a non-directory")
		}
		treeSHA = exact.SHA
	}
	panic("unreachable")
}

// CompareOneCommit verifies that head is exactly one commit ahead of base and
// returns its complete bounded changed-file set.
func (client *Client) CompareOneCommit(
	ctx context.Context,
	baseSHA string,
	headSHA string,
) ([]CompareFileFacts, error) {
	return client.CompareCandidate(ctx, baseSHA, headSHA, 1)
}

// CompareCandidate verifies a bounded descendant chain and returns its final
// aggregate changed-file set.
func (client *Client) CompareCandidate(
	ctx context.Context,
	baseSHA string,
	headSHA string,
	expectedCommits int,
) ([]CompareFileFacts, error) {
	if !gitObjectPattern.MatchString(baseSHA) ||
		!gitObjectPattern.MatchString(headSHA) || baseSHA == headSHA ||
		expectedCommits <= 0 || expectedCommits > 16 {
		return nil, errors.New("Git compare identity is invalid")
	}
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
		"/repos/"+client.repository+"/compare/"+baseSHA+"..."+headSHA,
		&payload,
	); err != nil {
		return nil, err
	}
	if payload.Status != "ahead" ||
		payload.AheadBy != expectedCommits || payload.BehindBy != 0 ||
		payload.TotalCommits != payload.AheadBy ||
		len(payload.Files) == 0 || len(payload.Files) > 128 {
		return nil, rejectCandidateComparison(
			"Git compare is not the exact bounded descendant candidate",
		)
	}
	files := make([]CompareFileFacts, 0, len(payload.Files))
	for _, file := range payload.Files {
		if !validRepositoryPath(file.Filename) ||
			(file.Status != "added" && file.Status != "modified" &&
				file.Status != "removed") ||
			file.Status != "removed" && !gitObjectPattern.MatchString(file.SHA) {
			return nil, rejectCandidateComparison(
				"Git compare contains an unsupported file change",
			)
		}
		files = append(files, CompareFileFacts{
			Path: file.Filename, Status: file.Status, SHA: file.SHA,
		})
	}
	slices.SortFunc(files, func(left, right CompareFileFacts) int {
		return strings.Compare(left.Path, right.Path)
	})
	for index := 1; index < len(files); index++ {
		if files[index-1].Path == files[index].Path {
			return nil, rejectCandidateComparison(
				"Git compare contains duplicate files",
			)
		}
	}
	return files, nil
}

// Commit reads one Git commit, including GitHub's verification result.
func (client *Client) Commit(ctx context.Context, sha string) (CommitFacts, error) {
	if !gitObjectPattern.MatchString(sha) {
		return CommitFacts{}, errors.New("GitHub commit SHA is invalid")
	}
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
		return CommitFacts{}, err
	}
	if payload.SHA != sha || len(payload.Message) > 4096 ||
		!gitObjectPattern.MatchString(payload.Tree.SHA) ||
		len(payload.Parents) > 2 {
		return CommitFacts{}, errors.New("GitHub commit response is invalid")
	}
	parents := make([]string, 0, len(payload.Parents))
	for _, parent := range payload.Parents {
		if !gitObjectPattern.MatchString(parent.SHA) {
			return CommitFacts{}, errors.New("GitHub commit parent is invalid")
		}
		parents = append(parents, parent.SHA)
	}
	return CommitFacts{
		SHA: payload.SHA, TreeSHA: payload.Tree.SHA, Parents: parents,
		Message:            payload.Message,
		Verified:           payload.Verification.Verified,
		VerificationReason: payload.Verification.Reason,
	}, nil
}

// CommitAttribution reads the repository commit view because the raw Git
// object endpoint does not expose the authenticated GitHub author identity.
func (client *Client) CommitAttribution(
	ctx context.Context,
	sha string,
) (CommitAttributionFacts, error) {
	if !gitObjectPattern.MatchString(sha) {
		return CommitAttributionFacts{}, errors.New("GitHub commit SHA is invalid")
	}
	var payload struct {
		SHA    string `json:"sha"`
		Author *struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"author"`
	}
	if err := client.getJSON(
		ctx, "/repos/"+client.repository+"/commits/"+sha, &payload,
	); err != nil {
		return CommitAttributionFacts{}, err
	}
	if payload.SHA != sha || payload.Author == nil ||
		payload.Author.Login == "" || len(payload.Author.Login) > 256 ||
		strings.ContainsAny(payload.Author.Login, "\r\n") ||
		payload.Author.Type == "" || len(payload.Author.Type) > 32 {
		return CommitAttributionFacts{},
			fmt.Errorf("%w: attribution is invalid", ErrUntrustedCommit)
	}
	parts := strings.Split(client.repository, "/")
	if len(parts) != 2 {
		return CommitAttributionFacts{},
			fmt.Errorf("%w: repository identity is invalid", ErrUntrustedCommit)
	}
	var signatureResponse struct {
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
	signatureRequest := struct {
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
	signatureRequest.Variables.Owner = parts[0]
	signatureRequest.Variables.Name = parts[1]
	signatureRequest.Variables.OID = sha
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql", signatureRequest,
		&signatureResponse, http.StatusOK,
	); err != nil {
		return CommitAttributionFacts{}, err
	}
	repository := signatureResponse.Data.Repository
	if len(signatureResponse.Errors) != 0 || repository == nil ||
		repository.NameWithOwner != client.repository ||
		repository.Object == nil || repository.Object.OID != sha ||
		repository.Object.Signature == nil {
		return CommitAttributionFacts{},
			fmt.Errorf("%w: signature attribution is invalid", ErrUntrustedCommit)
	}
	return CommitAttributionFacts{
		SHA: payload.SHA, AuthorLogin: payload.Author.Login,
		AuthorType:        payload.Author.Type,
		SignatureValid:    repository.Object.Signature.IsValid,
		SignatureState:    repository.Object.Signature.State,
		WasSignedByGitHub: repository.Object.Signature.WasSignedByGitHub,
	}, nil
}

func (client *Client) getJSON(ctx context.Context, path string, output any) error {
	if client == nil {
		return errors.New("GitHub client is nil")
	}
	endpoint := client.endpoint(path)
	next, err := client.getJSONPage(ctx, endpoint, output)
	if err != nil {
		return err
	}
	if next != nil {
		return errors.New("GitHub singleton response unexpectedly paginated")
	}
	return nil
}

func validBranchName(value string) bool {
	return value != "" && len(value) <= 255 && !strings.Contains(value, "..") &&
		!strings.ContainsAny(value, "~^:?*[\\ \t\r\n") &&
		!strings.HasPrefix(value, "/") && !strings.HasSuffix(value, "/") &&
		!strings.HasSuffix(value, ".") && !strings.Contains(value, "//")
}

func validRepositoryPath(value string) bool {
	return value != "" && len(value) <= 4096 &&
		!strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") &&
		!strings.ContainsRune(value, 0) &&
		!strings.Contains("/"+value+"/", "/../") &&
		!strings.Contains("/"+value+"/", "/./")
}
