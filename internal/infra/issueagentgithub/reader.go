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
)

var (
	gitObjectPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	loginPattern     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	agentRefPattern  = regexp.MustCompile(`^agent/issue-[1-9][0-9]*$`)
	branchPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// RepositoryFacts binds all later reads and writes to one repository identity.
type RepositoryFacts struct {
	ID            int64
	FullName      string
	DefaultBranch string
}

// IssueFacts is the bounded Issue state consumed by authorization and planning.
type IssueFacts struct {
	Number            int64
	State             string
	Title             string
	Body              string
	Author            string
	AuthorAssociation string
	Labels            []string
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

// ReviewFacts records a review against one exact candidate commit.
type ReviewFacts struct {
	ID       int64
	State    string
	Author   string
	CommitID string
}

// PullRequestFileFacts is GitHub's bounded changed-file projection.
type PullRequestFileFacts struct {
	Path         string
	PreviousPath string
	Status       string
	SHA          string
	Additions    int
	Deletions    int
	Changes      int
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

// CompareFileFacts is one exact file in a one-commit comparison.
type CompareFileFacts struct {
	Path   string
	Status string
	SHA    string
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

// BranchHead reads one policy-allowlisted branch after the caller has applied
// its protected branch allowlist.
func (client *Client) BranchHead(
	ctx context.Context,
	branch string,
) (RefFacts, error) {
	if !branchPattern.MatchString(branch) ||
		strings.Contains(branch, "..") ||
		strings.Contains(branch, "//") {
		return RefFacts{}, errors.New("GitHub branch name is unsafe")
	}
	var payload struct {
		Ref    string `json:"ref"`
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := client.getJSON(
		ctx, "/repos/"+client.repository+"/git/ref/heads/"+branch, &payload,
	); err != nil {
		return RefFacts{}, err
	}
	if payload.Ref != "refs/heads/"+branch || payload.Object.Type != "commit" ||
		!gitObjectPattern.MatchString(payload.Object.SHA) {
		return RefFacts{}, errors.New("GitHub branch ref response is invalid")
	}
	return RefFacts{Name: branch, SHA: payload.Object.SHA}, nil
}

// UnresolvedReviewThreadIDs reads the complete bounded unresolved review set.
func (client *Client) UnresolvedReviewThreadIDs(
	ctx context.Context,
	number int64,
) ([]string, error) {
	if client == nil || number <= 0 {
		return nil, errors.New("review-thread request is invalid")
	}
	parts := strings.Split(client.repository, "/")
	var response struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	query := `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100){nodes{id isResolved} pageInfo{hasNextPage}}}}}`
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql",
		struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}{
			Query: query,
			Variables: map[string]any{
				"owner": parts[0], "name": parts[1], "number": number,
			},
		},
		&response, http.StatusOK,
	); err != nil {
		return nil, err
	}
	threads := response.Data.Repository.PullRequest.ReviewThreads
	if len(response.Errors) != 0 || threads.PageInfo.HasNextPage ||
		len(threads.Nodes) > 100 {
		return nil, errors.New("review-thread response is incomplete")
	}
	result := make([]string, 0, len(threads.Nodes))
	for _, node := range threads.Nodes {
		if node.IsResolved {
			continue
		}
		if node.ID == "" || len(node.ID) > 256 ||
			strings.ContainsAny(node.ID, "\r\n") {
			return nil, errors.New("review-thread identity is invalid")
		}
		result = append(result, node.ID)
	}
	slices.Sort(result)
	result = slices.Compact(result)
	return result, nil
}

// CommitFacts records the tree, parents, and GitHub verification result.
type CommitFacts struct {
	SHA                string
	TreeSHA            string
	Parents            []string
	Verified           bool
	VerificationReason string
}

// WorkflowRunFacts binds validation evidence to one exact head SHA.
type WorkflowRunFacts struct {
	ID           int64
	Name         string
	Path         string
	DisplayTitle string
	Event        string
	Status       string
	Conclusion   string
	HeadSHA      string
	RunAttempt   int
}

// ArtifactFacts is a metadata-only artifact identity. Downloading is separate.
type ArtifactFacts struct {
	ID          int64
	Name        string
	SizeInBytes int64
	Expired     bool
	DownloadURL string
}

// Repository reads and verifies the configured repository identity.
func (client *Client) Repository(ctx context.Context) (RepositoryFacts, error) {
	var payload struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := client.getJSON(ctx, "/repos/"+client.repository, &payload); err != nil {
		return RepositoryFacts{}, err
	}
	if payload.ID <= 0 || payload.FullName != client.repository ||
		!validBranchName(payload.DefaultBranch) {
		return RepositoryFacts{}, errors.New("GitHub repository identity is invalid")
	}
	return RepositoryFacts(payload), nil
}

// Issue reads the current Issue, labels, author, and author association.
func (client *Client) Issue(ctx context.Context, number int64) (IssueFacts, error) {
	if number <= 0 {
		return IssueFacts{}, errors.New("Issue number is invalid")
	}
	var payload struct {
		Number int64  `json:"number"`
		State  string `json:"state"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		User   struct {
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
	return IssueFacts{
		Number: payload.Number, State: payload.State, Title: payload.Title,
		Body: payload.Body, Author: payload.User.Login,
		AuthorAssociation: payload.AuthorAssociation, Labels: labels,
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
		Body      string   `json:"body"`
		CreatedAt jsonTime `json:"created_at"`
		UpdatedAt jsonTime `json:"updated_at"`
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
		Body: payload.Body, CreatedAt: payload.CreatedAt.Time,
		UpdatedAt: payload.UpdatedAt.Time,
	}, nil
}

// IssueLabels reads the labels of an Issue or pull request without accepting
// any other mutable projection.
func (client *Client) IssueLabels(
	ctx context.Context,
	number int64,
) ([]string, error) {
	if number <= 0 {
		return nil, errors.New("Issue-like number is invalid")
	}
	var payload struct {
		Number int64 `json:"number"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/issues/"+strconv.FormatInt(number, 10),
		&payload,
	); err != nil {
		return nil, err
	}
	if payload.Number != number || len(payload.Labels) > 100 {
		return nil, errors.New("GitHub labels response is invalid")
	}
	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		if strings.TrimSpace(label.Name) == "" || len(label.Name) > 100 {
			return nil, errors.New("GitHub label is invalid")
		}
		labels = append(labels, label.Name)
	}
	slices.Sort(labels)
	for index := 1; index < len(labels); index++ {
		if labels[index-1] == labels[index] {
			return nil, errors.New("GitHub labels contain a duplicate")
		}
	}
	return labels, nil
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

// PullRequestReviews reads all review pages within the configured budget.
func (client *Client) PullRequestReviews(
	ctx context.Context,
	number int64,
) ([]ReviewFacts, error) {
	if number <= 0 {
		return nil, errors.New("pull request number is invalid")
	}
	var raw []struct {
		ID       int64  `json:"id"`
		State    string `json:"state"`
		CommitID string `json:"commit_id"`
		User     struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := client.getAllPages(
		ctx,
		"/repos/"+client.repository+"/pulls/"+strconv.FormatInt(number, 10)+"/reviews",
		&raw,
	); err != nil {
		return nil, err
	}
	result := make([]ReviewFacts, 0, len(raw))
	for _, review := range raw {
		if review.ID <= 0 || review.User.Login == "" ||
			!gitObjectPattern.MatchString(review.CommitID) {
			return nil, errors.New("GitHub review response is invalid")
		}
		result = append(result, ReviewFacts{
			ID: review.ID, State: review.State,
			Author: review.User.Login, CommitID: review.CommitID,
		})
	}
	return result, nil
}

// PullRequestFiles reads all PR file pages and rejects inconsistent counts.
func (client *Client) PullRequestFiles(
	ctx context.Context,
	number int64,
) ([]PullRequestFileFacts, error) {
	if number <= 0 {
		return nil, errors.New("pull request number is invalid")
	}
	var raw []struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Status           string `json:"status"`
		SHA              string `json:"sha"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
		Changes          int    `json:"changes"`
	}
	if err := client.getAllPages(
		ctx,
		"/repos/"+client.repository+"/pulls/"+strconv.FormatInt(number, 10)+"/files",
		&raw,
	); err != nil {
		return nil, err
	}
	if len(raw) > 3000 {
		return nil, errors.New("GitHub pull request file count exceeds limit")
	}
	result := make([]PullRequestFileFacts, 0, len(raw))
	for _, file := range raw {
		if !validRepositoryPath(file.Filename) ||
			file.PreviousFilename != "" && !validRepositoryPath(file.PreviousFilename) ||
			!gitObjectPattern.MatchString(file.SHA) ||
			file.Additions < 0 || file.Deletions < 0 ||
			file.Changes != file.Additions+file.Deletions {
			return nil, errors.New("GitHub pull request file response is invalid")
		}
		result = append(result, PullRequestFileFacts{
			Path: file.Filename, PreviousPath: file.PreviousFilename,
			Status: file.Status, SHA: file.SHA, Additions: file.Additions,
			Deletions: file.Deletions, Changes: file.Changes,
		})
	}
	return result, nil
}

// Ref reads one Agent branch. Other refs are outside this narrow port.
func (client *Client) Ref(ctx context.Context, branch string) (RefFacts, error) {
	if !agentRefPattern.MatchString(branch) {
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
	if !agentRefPattern.MatchString(branch) {
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
	if !gitObjectPattern.MatchString(baseSHA) ||
		!gitObjectPattern.MatchString(headSHA) || baseSHA == headSHA {
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
	if payload.Status != "ahead" || payload.AheadBy != 1 ||
		payload.BehindBy != 0 || payload.TotalCommits != 1 ||
		len(payload.Files) == 0 || len(payload.Files) > 128 {
		return nil, errors.New("Git compare is not one bounded descendant commit")
	}
	files := make([]CompareFileFacts, 0, len(payload.Files))
	for _, file := range payload.Files {
		if !validRepositoryPath(file.Filename) ||
			(file.Status != "added" && file.Status != "modified" &&
				file.Status != "removed") ||
			file.Status != "removed" && !gitObjectPattern.MatchString(file.SHA) {
			return nil, errors.New("Git compare file is invalid")
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
			return nil, errors.New("Git compare contains duplicate files")
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
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/git/commits/"+sha,
		&payload,
	); err != nil {
		return CommitFacts{}, err
	}
	if payload.SHA != sha || !gitObjectPattern.MatchString(payload.Tree.SHA) ||
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
		Verified:           payload.Verification.Verified,
		VerificationReason: payload.Verification.Reason,
	}, nil
}

// WorkflowRun reads one Actions run and its exact tested SHA.
func (client *Client) WorkflowRun(ctx context.Context, id int64) (WorkflowRunFacts, error) {
	if id <= 0 {
		return WorkflowRunFacts{}, errors.New("workflow run ID is invalid")
	}
	var payload struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Path         string `json:"path"`
		DisplayTitle string `json:"display_title"`
		Event        string `json:"event"`
		Status       string `json:"status"`
		Conclusion   string `json:"conclusion"`
		HeadSHA      string `json:"head_sha"`
		RunAttempt   int    `json:"run_attempt"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/actions/runs/"+strconv.FormatInt(id, 10),
		&payload,
	); err != nil {
		return WorkflowRunFacts{}, err
	}
	if payload.ID != id {
		return WorkflowRunFacts{}, errors.New("GitHub workflow run identity is invalid")
	}
	if payload.Name == "" || payload.Path == "" || payload.DisplayTitle == "" ||
		payload.Event == "" || payload.Status == "" || payload.RunAttempt <= 0 {
		return WorkflowRunFacts{}, errors.New("GitHub workflow run metadata is invalid")
	}
	if !gitObjectPattern.MatchString(payload.HeadSHA) {
		return WorkflowRunFacts{}, errors.New("GitHub workflow run head SHA is invalid")
	}
	return WorkflowRunFacts(payload), nil
}

// RunArtifacts reads and count-checks one run's Artifact inventory.
func (client *Client) RunArtifacts(ctx context.Context, id int64) ([]ArtifactFacts, error) {
	if id <= 0 {
		return nil, errors.New("workflow run ID is invalid")
	}
	var payload struct {
		TotalCount int `json:"total_count"`
		Artifacts  []struct {
			ID                 int64  `json:"id"`
			Name               string `json:"name"`
			SizeInBytes        int64  `json:"size_in_bytes"`
			Expired            bool   `json:"expired"`
			ArchiveDownloadURL string `json:"archive_download_url"`
		} `json:"artifacts"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/actions/runs/"+strconv.FormatInt(id, 10)+"/artifacts",
		&payload,
	); err != nil {
		return nil, err
	}
	if payload.TotalCount != len(payload.Artifacts) || payload.TotalCount > 100 {
		return nil, errors.New("GitHub Artifact count mismatch")
	}
	result := make([]ArtifactFacts, 0, len(payload.Artifacts))
	for _, artifact := range payload.Artifacts {
		download, err := url.Parse(artifact.ArchiveDownloadURL)
		if artifact.ID <= 0 || artifact.Name == "" || artifact.SizeInBytes < 0 ||
			err != nil || download.Scheme != client.baseURL.Scheme ||
			download.Host != client.baseURL.Host {
			return nil, errors.New("GitHub Artifact response is invalid")
		}
		result = append(result, ArtifactFacts{
			ID: artifact.ID, Name: artifact.Name,
			SizeInBytes: artifact.SizeInBytes, Expired: artifact.Expired,
			DownloadURL: artifact.ArchiveDownloadURL,
		})
	}
	return result, nil
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

func (client *Client) getAllPages(ctx context.Context, path string, output any) error {
	switch target := output.(type) {
	case *[]struct {
		ID       int64  `json:"id"`
		State    string `json:"state"`
		CommitID string `json:"commit_id"`
		User     struct {
			Login string `json:"login"`
		} `json:"user"`
	}:
		return collectPages(client, ctx, path, target)
	case *[]struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Status           string `json:"status"`
		SHA              string `json:"sha"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
		Changes          int    `json:"changes"`
	}:
		return collectPages(client, ctx, path, target)
	default:
		return errors.New("unsupported GitHub pagination target")
	}
}

func collectPages[T any](
	client *Client,
	ctx context.Context,
	path string,
	target *[]T,
) error {
	for page := 1; page <= client.maxPages; page++ {
		endpoint := client.endpoint(path)
		query := endpoint.Query()
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()
		var current []T
		next, err := client.getJSONPage(ctx, endpoint, &current)
		if err != nil {
			return err
		}
		if len(current) > 100 {
			return errors.New("GitHub page exceeds item limit")
		}
		*target = append(*target, current...)
		if next == nil {
			return nil
		}
		if page == client.maxPages ||
			next.Scheme != client.baseURL.Scheme ||
			next.Host != client.baseURL.Host ||
			next.Path != endpoint.Path ||
			next.Query().Get("per_page") != "100" ||
			next.Query().Get("page") != strconv.Itoa(page+1) ||
			len(next.Query()) != 2 {
			return errors.New("GitHub pagination is outside request scope")
		}
	}
	return errors.New("GitHub pagination did not terminate")
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

func readerError(kind string, value any) error {
	return fmt.Errorf("invalid GitHub %s: %v", kind, value)
}
