package reviewagentgithub

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

var (
	gitSHAPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	githubLoginPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:\[bot\])?$`)
	issueReferencePattern = regexp.MustCompile(
		`(?i)(?:^|[^A-Za-z0-9])(?:close(?:s|d)?|fix(?:es|ed)?|resolve(?:s|d)?)\s+#([1-9][0-9]*)`,
	)
)

// Review is one bounded formal GitHub Review.
type Review struct {
	ID          int64
	Author      string
	AuthorType  string
	State       string
	Body        string
	CommitID    string
	SubmittedAt time.Time
}

// IssueComment is one bounded pull-request conversation comment.
type IssueComment struct {
	ID         int64
	Author     string
	AuthorType string
	Body       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ReviewComment is one bounded inline Review thread item.
type ReviewComment struct {
	ID          int64
	Author      string
	AuthorType  string
	Body        string
	Path        string
	Line        int
	Side        string
	InReplyToID int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ReviewThread is one unresolved or resolved GitHub Review conversation.
type ReviewThread struct {
	ID         string
	IsResolved bool
	Path       string
	Line       int
}

// Permission is a fresh repository permission for command authorization.
type Permission string

const (
	PermissionRead     Permission = "read"
	PermissionTriage   Permission = "triage"
	PermissionWrite    Permission = "write"
	PermissionMaintain Permission = "maintain"
	PermissionAdmin    Permission = "admin"
)

// CheckRun is one bounded current-head Check projection.
type CheckRun struct {
	ID         int64
	Name       string
	Status     string
	Conclusion string
	AppSlug    string
	ExternalID string
}

// PullRequestSnapshot is the complete fresh GitHub input to reconciliation
// and context building.
type PullRequestSnapshot struct {
	Facts          usecase.PullRequestFacts
	Title          string
	Body           string
	Author         string
	HeadRepository string
	Inventory      verify.Inventory
	Reviews        []Review
	IssueComments  []IssueComment
	ReviewComments []ReviewComment
	ReviewThreads  []ReviewThread
	Checks         []CheckRun
	LinkedIssues   []contract.LinkedIssue
	// CommentPatches are GitHub's actual diff hunks. They are used only to
	// validate inline Review coordinates; complete model evidence lives in
	// Inventory content and never depends on this possibly omitted field.
	CommentPatches map[string]string
}

// ReadBaseInstructions freezes every applicable AGENTS.md and FLOW.md from
// the exact trusted control/base tree.
func (client *Client) ReadBaseInstructions(
	ctx context.Context,
	baseSHA string,
	changedPaths []string,
) ([]contract.InstructionBlob, error) {
	if client == nil || !gitSHAPattern.MatchString(baseSHA) {
		return nil, errors.New("base instruction identity is invalid")
	}
	tree, err := client.readTree(ctx, baseSHA)
	if err != nil {
		return nil, err
	}
	type instructionEntry struct {
		path  string
		entry treeEntry
	}
	entries := make([]instructionEntry, 0)
	for repositoryPath, entry := range tree {
		if path.Base(repositoryPath) != "AGENTS.md" &&
			path.Base(repositoryPath) != "FLOW.md" {
			continue
		}
		if entry.Type != "blob" || entry.Mode != "100644" {
			return nil, errors.New("base instruction tree entry is invalid")
		}
		scope := path.Dir(repositoryPath)
		if !slices.ContainsFunc(changedPaths, func(changedPath string) bool {
			return scope == "." ||
				changedPath == scope ||
				strings.HasPrefix(changedPath, scope+"/")
		}) {
			continue
		}
		entries = append(entries, instructionEntry{
			path: repositoryPath, entry: entry,
		})
	}
	if len(entries) > contract.MaxInstructions {
		return nil, errors.New("base instruction budget exceeded")
	}
	slices.SortFunc(entries, func(left, right instructionEntry) int {
		return strings.Compare(left.path, right.path)
	})
	catalog := make([]verify.BaseInstruction, 0, len(entries))
	for _, candidate := range entries {
		content, readErr := client.readBlob(ctx, candidate.entry.SHA)
		if readErr != nil {
			return nil, readErr
		}
		catalog = append(catalog, verify.BaseInstruction{
			Path: candidate.path, BlobSHA: candidate.entry.SHA,
			Content: content,
		})
	}
	return verify.DiscoverInstructions(changedPaths, catalog)
}

// ReadPullRequest performs complete bounded reads. The event payload supplies
// only the pull-request number.
func (client *Client) ReadPullRequest(
	ctx context.Context,
	number int64,
) (PullRequestSnapshot, error) {
	return client.readPullRequest(ctx, number, true)
}

// ReadPullRequestMetadata performs the same authoritative lifecycle read
// without downloading base/head blobs. Exact content is read once by
// ReadPullRequest when a worker builds the immutable review context.
func (client *Client) ReadPullRequestMetadata(
	ctx context.Context,
	number int64,
) (PullRequestSnapshot, error) {
	return client.readPullRequest(ctx, number, false)
}

func (client *Client) readPullRequest(
	ctx context.Context,
	number int64,
	exactContent bool,
) (PullRequestSnapshot, error) {
	if client == nil || number <= 0 {
		return PullRequestSnapshot{}, errors.New(
			"pull-request read identity is invalid",
		)
	}
	var repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository,
		&repository,
	); err != nil {
		return PullRequestSnapshot{}, err
	}
	if repository.FullName != client.repository ||
		repository.DefaultBranch != "main" {
		return PullRequestSnapshot{}, errors.New(
			"GitHub repository default branch is inconsistent",
		)
	}
	pull, pullErr := client.readStablePull(ctx, number)
	if pullErr != nil {
		return PullRequestSnapshot{}, pullErr
	}
	var inventory verify.Inventory
	var commentPatches map[string]string
	var contextFailureReason string
	var err error
	if exactContent {
		inventory, contextFailureReason, err = client.readPullInventory(
			ctx,
			number,
			pull,
		)
	} else {
		inventory, commentPatches, contextFailureReason, err =
			client.readPullMetadataInventory(ctx, number, pull)
	}
	if err != nil {
		return PullRequestSnapshot{}, err
	}
	reviews, err := client.readReviews(ctx, number)
	if err != nil {
		contextFailureReason, err = mergeContextFailure(
			contextFailureReason,
			err,
		)
		if err != nil {
			return PullRequestSnapshot{}, err
		}
	}
	issueComments, err := client.readIssueComments(ctx, number)
	if err != nil {
		contextFailureReason, err = mergeContextFailure(
			contextFailureReason,
			err,
		)
		if err != nil {
			return PullRequestSnapshot{}, err
		}
	}
	reviewComments, err := client.readReviewComments(ctx, number)
	if err != nil {
		contextFailureReason, err = mergeContextFailure(
			contextFailureReason,
			err,
		)
		if err != nil {
			return PullRequestSnapshot{}, err
		}
	}
	reviewThreads, err := client.readReviewThreads(ctx, number)
	if err != nil {
		contextFailureReason, err = mergeContextFailure(
			contextFailureReason,
			err,
		)
		if err != nil {
			return PullRequestSnapshot{}, err
		}
	}
	checks, err := client.readCheckRuns(ctx, pull.Head.SHA)
	if err != nil {
		contextFailureReason, err = mergeContextFailure(
			contextFailureReason,
			err,
		)
		if err != nil {
			return PullRequestSnapshot{}, err
		}
	}
	linkedIssueNumbers, linkedReason := linkedIssueNumbers(pull.Body)
	if linkedReason != "" && contextFailureReason == "" {
		contextFailureReason = linkedReason
	}
	linkedIssues, err := client.readLinkedIssues(ctx, linkedIssueNumbers)
	if err != nil {
		if strings.Contains(err.Error(), "GitHub API returned status 404") {
			if contextFailureReason == "" {
				contextFailureReason = "linked Issue is unavailable"
			}
			linkedIssues = nil
		} else {
			return PullRequestSnapshot{}, err
		}
	}
	intentLocators := make([]string, 0, len(linkedIssues))
	for _, issue := range linkedIssues {
		locator, digestErr := contract.LinkedIssueIntentLocator(issue)
		if digestErr != nil {
			return PullRequestSnapshot{}, digestErr
		}
		intentLocators = append(intentLocators, locator)
	}
	intentDigest, err := contract.IntentDigest(
		pull.Title,
		pull.Body,
		intentLocators,
	)
	if err != nil {
		return PullRequestSnapshot{}, err
	}
	mergeability := normalizeMergeability(
		pull.Mergeable,
		pull.MergeableState,
	)
	reviewFacts := make(
		[]usecase.ReviewFact,
		0,
		len(reviews),
	)
	for _, review := range reviews {
		reviewFacts = append(
			reviewFacts,
			usecase.ReviewFact{
				Author: review.Author, AuthorType: review.AuthorType,
				State: review.State, CommitSHA: review.CommitID,
				SubmittedAt: review.SubmittedAt,
			},
		)
	}
	facts := usecase.PullRequestFacts{
		Repository: client.repository, PullRequest: number,
		BaseRef: pull.Base.Ref, HeadSHA: pull.Head.SHA,
		BaseSHA: pull.Base.SHA, TestMergeSHA: pull.MergeCommitSHA,
		IntentDigest: intentDigest, Open: pull.State == "open",
		Draft: pull.Draft, Mergeability: mergeability,
		ContextFailureReason: contextFailureReason,
		ChangedFiles:         inventory.DeclaredFiles,
		ChangedBytes:         inventory.TotalBytes,
		ChangedLines:         inventory.TotalLines,
		AuthorLogin:          pull.User.Login,
		AuthorAssociation:    pull.AuthorAssociation,
		HumanChangesRequested: usecase.HumanChangesRequested(
			reviewFacts,
			pull.Head.SHA,
		),
	}
	return PullRequestSnapshot{
		Facts: facts, Title: pull.Title, Body: pull.Body,
		Author: pull.User.Login, HeadRepository: pull.Head.Repo.FullName,
		Inventory: inventory, Reviews: reviews,
		IssueComments: issueComments, ReviewComments: reviewComments,
		ReviewThreads: reviewThreads,
		Checks:        checks, LinkedIssues: linkedIssues,
		CommentPatches: commentPatches,
	}, nil
}

func (client *Client) readStablePull(
	ctx context.Context,
	number int64,
) (pullResponse, error) {
	endpoint := fmt.Sprintf(
		"/repos/%s/pulls/%d",
		client.repository,
		number,
	)
	var latest pullResponse
	for attempt := 0; attempt < 5; attempt++ {
		var pull pullResponse
		if err := client.getJSON(ctx, endpoint, &pull); err != nil {
			return pullResponse{}, err
		}
		if err := validatePullResponse(number, pull); err != nil {
			return pullResponse{}, err
		}
		latest = pull
		mergeabilityReady := pull.Mergeable != nil ||
			pull.MergeableState == "dirty"
		testMergeReady := pull.MergeCommitSHA != "" ||
			pull.Mergeable != nil && !*pull.Mergeable ||
			pull.MergeableState == "dirty"
		if pull.State != "open" ||
			pull.Draft ||
			mergeabilityReady && testMergeReady {
			return pull, nil
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return pullResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	// Persistently unknown GitHub-computed mergeability is a fresh but
	// incomplete fact, not a transport failure. Returning the last valid
	// snapshot lets deterministic lifecycle policy record a signed
	// inconclusive verdict for the unavailable test-merge revision.
	return latest, nil
}

func (client *Client) readPullMetadataInventory(
	ctx context.Context,
	number int64,
	pull pullResponse,
) (verify.Inventory, map[string]string, string, error) {
	limits := reviewInventoryLimits()
	inventory := verify.Inventory{
		DeclaredFiles: pull.ChangedFiles,
		TotalLines:    pull.Additions + pull.Deletions,
	}
	if pull.ChangedFiles > contract.MaxChangedFiles {
		return inventory, nil, "changed-file budget exceeded", nil
	}
	files, err := client.readFiles(ctx, number, pull.ChangedFiles)
	if err != nil {
		_, reason, infrastructureErr := inventoryFailure(err)
		return inventory, nil, reason, infrastructureErr
	}
	inventory.Complete = true
	inventory.Files = make([]contract.ChangedFile, 0, len(files))
	commentPatches := make(map[string]string, len(files))
	for _, file := range files {
		status, normalizeErr := normalizeFileStatus(file.Status)
		if normalizeErr != nil {
			_, reason, infrastructureErr := inventoryFailure(normalizeErr)
			return verify.Inventory{}, nil, reason, infrastructureErr
		}
		inventory.TotalBytes += int64(len(file.Patch))
		inventory.Files = append(inventory.Files, contract.ChangedFile{
			Path: file.Filename, PreviousPath: file.PreviousFilename,
			Status: status, Additions: file.Additions,
			Deletions: file.Deletions,
		})
		if file.Patch != "" && utf8.ValidString(file.Patch) &&
			len(file.Patch) <= 1<<20 {
			commentPatches[file.Filename] = file.Patch
		}
	}
	if reason := reviewInventoryBudgetFailure(inventory, limits); reason != "" {
		return inventory, commentPatches, reason, nil
	}
	return inventory, commentPatches, "", nil
}

func (client *Client) readPullInventory(
	ctx context.Context,
	number int64,
	pull pullResponse,
) (verify.Inventory, string, error) {
	if pull.ChangedFiles > contract.MaxChangedFiles {
		return verify.Inventory{}, "changed-file budget exceeded", nil
	}
	files, err := client.readFiles(ctx, number, pull.ChangedFiles)
	if err != nil {
		return inventoryFailure(err)
	}
	headTree, err := client.readTree(ctx, pull.Head.SHA)
	if err != nil {
		return inventoryFailure(err)
	}
	baseTree, err := client.readTree(ctx, pull.Base.SHA)
	if err != nil {
		return inventoryFailure(err)
	}
	rawFiles := make([]verify.RawFile, 0, len(files))
	for _, file := range files {
		status, normalizeErr := normalizeFileStatus(file.Status)
		if normalizeErr != nil {
			return inventoryFailure(normalizeErr)
		}
		before, after, mode, readErr := client.readExactChange(
			ctx,
			status,
			file,
			baseTree,
			headTree,
		)
		if readErr != nil {
			return inventoryFailure(readErr)
		}
		content := after
		if status == contract.FileStatusRemoved {
			content = before
		}
		fileType := verify.FileTypeText
		if !utf8.Valid(before) || !utf8.Valid(after) {
			fileType = verify.FileTypeBinary
		}
		var patch []byte
		if fileType == verify.FileTypeText {
			patch = completeChange(
				file.PreviousFilename,
				file.Filename,
				before,
				after,
			)
		}
		rawFiles = append(rawFiles, verify.RawFile{
			Path: file.Filename, OldPath: file.PreviousFilename,
			Status: status, Mode: mode, Type: fileType,
			Generated: generatedPath(file.Filename),
			Patch:     patch, Content: content,
			Additions: file.Additions, Deletions: file.Deletions,
		})
	}
	inventory, err := verify.BuildInventory(
		pull.ChangedFiles,
		rawFiles,
		reviewInventoryLimits(),
	)
	if err != nil {
		return inventoryFailure(err)
	}
	return inventory, "", nil
}

func reviewInventoryLimits() verify.InventoryLimits {
	return verify.InventoryLimits{
		MaxFiles:      contract.MaxChangedFiles,
		MaxTotalBytes: contract.MaxChangedBytes,
		MaxLines:      contract.MaxChangedLines,
	}
}

func reviewInventoryBudgetFailure(
	inventory verify.Inventory,
	limits verify.InventoryLimits,
) string {
	if inventory.TotalBytes > limits.MaxTotalBytes {
		return "changed-byte budget exceeded"
	}
	if inventory.TotalLines > limits.MaxLines {
		return "changed-line budget exceeded"
	}
	return ""
}

func inventoryFailure(err error) (verify.Inventory, string, error) {
	reason := err.Error()
	for _, deterministic := range []string{
		"pull-request file pagination ",
		"GitHub tree response is truncated",
		"GitHub tree entry is invalid",
		"GitHub tree contains duplicate path",
		"GitHub changed file ",
		"GitHub removed file ",
		"GitHub blob response is invalid",
		"GitHub blob content is invalid",
		"unsupported GitHub changed-file status",
		"changed-file ",
		"changed-byte ",
		"changed-line ",
		"invalid or truncated changed file",
		"text changed file lacks complete UTF-8 content",
		"unsupported changed-file type",
		"non-rename changed file",
		"rename lacks distinct old and new paths",
		"unsupported changed-file status",
		"duplicate changed-file path",
	} {
		if strings.HasPrefix(reason, deterministic) {
			return verify.Inventory{}, reason, nil
		}
	}
	return verify.Inventory{}, "", err
}

func mergeContextFailure(current string, err error) (string, error) {
	reason := err.Error()
	for _, deterministic := range []string{
		"GitHub pagination exceeds page budget",
		"GitHub Review thread pagination ",
		"GitHub Review thread count changed ",
		"GitHub Review thread page is oversized",
		"GitHub Review thread cursor is invalid",
		"GitHub Check pagination ",
		"GitHub Check count changed ",
	} {
		if strings.HasPrefix(reason, deterministic) {
			if current != "" {
				return current, nil
			}
			return reason, nil
		}
	}
	return current, err
}

func (client *Client) readExactChange(
	ctx context.Context,
	status contract.FileStatus,
	file fileResponse,
	baseTree map[string]treeEntry,
	headTree map[string]treeEntry,
) ([]byte, []byte, string, error) {
	var beforeEntry, afterEntry treeEntry
	var beforeFound, afterFound bool
	if status != contract.FileStatusAdded {
		beforePath := file.Filename
		if status == contract.FileStatusRenamed {
			beforePath = file.PreviousFilename
		}
		beforeEntry, beforeFound = baseTree[beforePath]
	}
	if status != contract.FileStatusRemoved {
		afterEntry, afterFound = headTree[file.Filename]
	}
	if status != contract.FileStatusAdded &&
		(!beforeFound || beforeEntry.Type != "blob") ||
		status != contract.FileStatusRemoved &&
			(!afterFound || afterEntry.Type != "blob") {
		return nil, nil, "", errors.New(
			"GitHub changed file does not match exact trees",
		)
	}
	if status == contract.FileStatusRemoved {
		if beforeEntry.SHA != file.SHA {
			return nil, nil, "", errors.New(
				"GitHub removed file does not match exact base tree",
			)
		}
	} else if afterEntry.SHA != file.SHA {
		return nil, nil, "", errors.New(
			"GitHub changed file does not match exact head tree",
		)
	}
	var before, after []byte
	var err error
	if beforeFound {
		before, err = client.readBlob(ctx, beforeEntry.SHA)
		if err != nil {
			return nil, nil, "", err
		}
	}
	if afterFound {
		after, err = client.readBlob(ctx, afterEntry.SHA)
		if err != nil {
			return nil, nil, "", err
		}
	}
	mode := beforeEntry.Mode
	if afterFound {
		mode = afterEntry.Mode
	}
	return before, after, mode, nil
}

func completeChange(
	previousPath string,
	currentPath string,
	before []byte,
	after []byte,
) []byte {
	if previousPath == "" {
		previousPath = currentPath
	}
	var result strings.Builder
	result.Grow(len(before) + len(after) + len(previousPath) + len(currentPath) + 96)
	fmt.Fprintf(&result, "--- before/%s\n+++ after/%s\n", previousPath, currentPath)
	beforeStart, beforeLines := completeRange(before)
	afterStart, afterLines := completeRange(after)
	fmt.Fprintf(
		&result,
		"@@ -%d,%d +%d,%d @@ complete file\n",
		beforeStart,
		beforeLines,
		afterStart,
		afterLines,
	)
	writeCompleteLines(&result, '-', before)
	writeCompleteLines(&result, '+', after)
	return []byte(result.String())
}

func completeRange(content []byte) (int, int) {
	if len(content) == 0 {
		return 0, 0
	}
	lines := strings.Count(string(content), "\n")
	if content[len(content)-1] != '\n' {
		lines++
	}
	return 1, lines
}

func writeCompleteLines(result *strings.Builder, prefix byte, content []byte) {
	if len(content) == 0 {
		return
	}
	lines := strings.Split(string(content), "\n")
	if content[len(content)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		result.WriteByte(prefix)
		result.WriteString(line)
		result.WriteByte('\n')
	}
}

func normalizeMergeability(
	mergeable *bool,
	mergeableState string,
) usecase.Mergeability {
	if mergeable != nil {
		if *mergeable {
			return usecase.MergeabilityClean
		}
		return usecase.MergeabilityConflicting
	}
	if mergeableState == "dirty" {
		return usecase.MergeabilityConflicting
	}
	return usecase.MergeabilityUnknown
}

func (client *Client) readLinkedIssues(
	ctx context.Context,
	numbers []int64,
) ([]contract.LinkedIssue, error) {
	if len(numbers) > contract.MaxLinkedIssues {
		return nil, errors.New("too many linked Issues")
	}
	result := make([]contract.LinkedIssue, 0, len(numbers))
	for _, number := range numbers {
		var payload struct {
			Number      int64  `json:"number"`
			State       string `json:"state"`
			Title       string `json:"title"`
			Body        string `json:"body"`
			PullRequest any    `json:"pull_request"`
		}
		if err := client.getJSON(
			ctx,
			fmt.Sprintf(
				"/repos/%s/issues/%d",
				client.repository,
				number,
			),
			&payload,
		); err != nil {
			return nil, err
		}
		if payload.Number != number ||
			(payload.State != "open" && payload.State != "closed") ||
			strings.TrimSpace(payload.Title) == "" ||
			len(payload.Title) > 1024 ||
			len(payload.Body) > 1<<20 {
			return nil, errors.New("linked GitHub Issue response is invalid")
		}
		if payload.PullRequest != nil {
			continue
		}
		result = append(result, contract.LinkedIssue{
			Number: payload.Number,
			State:  payload.State,
			Title:  payload.Title,
			Body:   payload.Body,
		})
	}
	return result, nil
}

// ActorPermission resolves current repository authority for one exact actor.
func (client *Client) ActorPermission(
	ctx context.Context,
	actor string,
) (Permission, error) {
	if client == nil || !githubLoginPattern.MatchString(actor) {
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

type pullResponse struct {
	Number            int64  `json:"number"`
	State             string `json:"state"`
	Draft             bool   `json:"draft"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	ChangedFiles      int    `json:"changed_files"`
	Additions         int64  `json:"additions"`
	Deletions         int64  `json:"deletions"`
	Mergeable         *bool  `json:"mergeable"`
	MergeableState    string `json:"mergeable_state"`
	MergeCommitSHA    string `json:"merge_commit_sha"`
	AuthorAssociation string `json:"author_association"`
	User              struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

func validatePullResponse(number int64, pull pullResponse) error {
	if pull.Number != number ||
		(pull.State != "open" && pull.State != "closed") ||
		strings.TrimSpace(pull.Title) == "" ||
		len(pull.Title) > 1024 ||
		len(pull.Body) > 1<<20 ||
		pull.ChangedFiles <= 0 ||
		pull.Additions < 0 || pull.Deletions < 0 ||
		pull.User.Login == "" ||
		pull.Base.Ref != "main" ||
		!gitSHAPattern.MatchString(pull.Base.SHA) ||
		!gitSHAPattern.MatchString(pull.Head.SHA) ||
		(pull.MergeCommitSHA != "" &&
			!gitSHAPattern.MatchString(pull.MergeCommitSHA)) ||
		pull.Head.Repo.FullName == "" {
		return errors.New("GitHub pull-request response is invalid")
	}
	return nil
}

type fileResponse struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	SHA              string `json:"sha"`
	Additions        uint64 `json:"additions"`
	Deletions        uint64 `json:"deletions"`
	Patch            string `json:"patch"`
}

func (client *Client) readFiles(
	ctx context.Context,
	number int64,
	declared int,
) ([]fileResponse, error) {
	result := make([]fileResponse, 0, declared)
	var next *url.URL
	for page := 1; page <= client.maxPages; page++ {
		endpoint := client.pagedEndpoint(
			fmt.Sprintf(
				"/repos/%s/pulls/%d/files",
				client.repository,
				number,
			),
			page,
		)
		if next != nil {
			endpoint = *next
		}
		var files []fileResponse
		var err error
		next, err = client.getJSONPage(ctx, endpoint, &files)
		if err != nil {
			return nil, err
		}
		if len(files) > 100 || len(result)+len(files) > declared {
			return nil, errors.New(
				"pull-request file pagination exceeds declared count",
			)
		}
		result = append(result, files...)
		if next == nil {
			if len(result) != declared {
				return nil, errors.New(
					"pull-request file pagination is incomplete",
				)
			}
			return result, nil
		}
	}
	return nil, errors.New("pull-request file pagination exceeds page budget")
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (client *Client) readTree(
	ctx context.Context,
	sha string,
) (map[string]treeEntry, error) {
	var response struct {
		Truncated bool        `json:"truncated"`
		Tree      []treeEntry `json:"tree"`
	}
	endpoint := client.endpoint(
		"/repos/" + client.repository + "/git/trees/" + sha,
	)
	query := endpoint.Query()
	query.Set("recursive", "1")
	endpoint.RawQuery = query.Encode()
	if _, err := client.getJSONPage(ctx, endpoint, &response); err != nil {
		return nil, err
	}
	if response.Truncated {
		return nil, errors.New("GitHub tree response is truncated")
	}
	result := make(map[string]treeEntry, len(response.Tree))
	for _, entry := range response.Tree {
		if entry.Path == "" || entry.Type == "" ||
			!gitSHAPattern.MatchString(entry.SHA) {
			return nil, errors.New("GitHub tree entry is invalid")
		}
		if _, exists := result[entry.Path]; exists {
			return nil, errors.New("GitHub tree contains duplicate path")
		}
		result[entry.Path] = entry
	}
	return result, nil
}

func (client *Client) readBlob(
	ctx context.Context,
	sha string,
) ([]byte, error) {
	var response struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Size     int    `json:"size"`
	}
	if err := client.getJSON(
		ctx,
		"/repos/"+client.repository+"/git/blobs/"+sha,
		&response,
	); err != nil {
		return nil, err
	}
	if response.Encoding != "base64" || response.Size < 0 ||
		response.Size > 16<<20 {
		return nil, errors.New("GitHub blob response is invalid")
	}
	content, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(response.Content, "\n", ""),
	)
	if err != nil || len(content) != response.Size {
		return nil, errors.New("GitHub blob content is invalid")
	}
	return content, nil
}

func (client *Client) readReviews(
	ctx context.Context,
	number int64,
) ([]Review, error) {
	var result []Review
	err := client.readPages(
		ctx,
		fmt.Sprintf(
			"/repos/%s/pulls/%d/reviews",
			client.repository,
			number,
		),
		func(endpoint url.URL) (*url.URL, error) {
			var payload []struct {
				ID       int64  `json:"id"`
				State    string `json:"state"`
				Body     string `json:"body"`
				CommitID string `json:"commit_id"`
				User     struct {
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"user"`
				SubmittedAt time.Time `json:"submitted_at"`
			}
			next, err := client.getJSONPage(ctx, endpoint, &payload)
			if err != nil {
				return nil, err
			}
			for _, item := range payload {
				if item.ID <= 0 || item.User.Login == "" ||
					item.SubmittedAt.IsZero() || len(item.Body) > 128<<10 {
					return nil, errors.New("GitHub Review response is invalid")
				}
				result = append(result, Review{
					ID: item.ID, Author: item.User.Login,
					AuthorType: item.User.Type, State: item.State,
					Body: item.Body, CommitID: item.CommitID,
					SubmittedAt: item.SubmittedAt,
				})
			}
			return next, nil
		},
	)
	return result, err
}

func (client *Client) readIssueComments(
	ctx context.Context,
	number int64,
) ([]IssueComment, error) {
	var result []IssueComment
	err := client.readPages(
		ctx,
		fmt.Sprintf(
			"/repos/%s/issues/%d/comments",
			client.repository,
			number,
		),
		func(endpoint url.URL) (*url.URL, error) {
			var payload []struct {
				ID   int64  `json:"id"`
				Body string `json:"body"`
				User struct {
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"user"`
				CreatedAt time.Time `json:"created_at"`
				UpdatedAt time.Time `json:"updated_at"`
			}
			next, err := client.getJSONPage(ctx, endpoint, &payload)
			if err != nil {
				return nil, err
			}
			for _, item := range payload {
				if item.ID <= 0 || item.User.Login == "" ||
					item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() ||
					len(item.Body) > 128<<10 {
					return nil, errors.New(
						"GitHub issue comment response is invalid",
					)
				}
				result = append(result, IssueComment{
					ID: item.ID, Author: item.User.Login,
					AuthorType: item.User.Type, Body: item.Body,
					CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
				})
			}
			return next, nil
		},
	)
	return result, err
}

func (client *Client) readReviewComments(
	ctx context.Context,
	number int64,
) ([]ReviewComment, error) {
	var result []ReviewComment
	err := client.readPages(
		ctx,
		fmt.Sprintf(
			"/repos/%s/pulls/%d/comments",
			client.repository,
			number,
		),
		func(endpoint url.URL) (*url.URL, error) {
			var payload []struct {
				ID          int64  `json:"id"`
				Body        string `json:"body"`
				Path        string `json:"path"`
				Line        int    `json:"line"`
				Side        string `json:"side"`
				InReplyToID int64  `json:"in_reply_to_id"`
				User        struct {
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"user"`
				CreatedAt time.Time `json:"created_at"`
				UpdatedAt time.Time `json:"updated_at"`
			}
			next, err := client.getJSONPage(ctx, endpoint, &payload)
			if err != nil {
				return nil, err
			}
			for _, item := range payload {
				if item.ID <= 0 || item.User.Login == "" ||
					item.Path == "" || item.Line < 0 ||
					(item.Side != "" &&
						item.Side != "LEFT" &&
						item.Side != "RIGHT") ||
					(item.Line == 0) != (item.Side == "") ||
					item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() ||
					len(item.Body) > 128<<10 {
					return nil, errors.New(
						"GitHub Review comment response is invalid",
					)
				}
				result = append(result, ReviewComment{
					ID: item.ID, Author: item.User.Login,
					AuthorType: item.User.Type, Body: item.Body,
					Path: item.Path, Line: item.Line, Side: item.Side,
					InReplyToID: item.InReplyToID,
					CreatedAt:   item.CreatedAt, UpdatedAt: item.UpdatedAt,
				})
			}
			return next, nil
		},
	)
	return result, err
}

func (client *Client) readReviewThreads(
	ctx context.Context,
	number int64,
) ([]ReviewThread, error) {
	parts := strings.Split(client.repository, "/")
	if len(parts) != 2 {
		return nil, errors.New("GitHub repository identity is invalid")
	}
	var result []ReviewThread
	cursor := ""
	total := -1
	for page := 0; page < client.maxPages; page++ {
		var response struct {
			Data struct {
				Repository *struct {
					NameWithOwner string `json:"nameWithOwner"`
					PullRequest   *struct {
						Number        int64 `json:"number"`
						ReviewThreads struct {
							TotalCount int `json:"totalCount"`
							Nodes      []struct {
								ID         string `json:"id"`
								IsResolved bool   `json:"isResolved"`
								Path       string `json:"path"`
								Line       int    `json:"line"`
							} `json:"nodes"`
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		request := struct {
			Query     string `json:"query"`
			Variables struct {
				Owner  string  `json:"owner"`
				Name   string  `json:"name"`
				Number int64   `json:"number"`
				Cursor *string `json:"cursor"`
			} `json:"variables"`
		}{
			Query: `query($owner:String!,$name:String!,$number:Int!,$cursor:String){` +
				`repository(owner:$owner,name:$name){nameWithOwner ` +
				`pullRequest(number:$number){number reviewThreads(` +
				`first:100,after:$cursor){totalCount nodes{` +
				`id isResolved path line} pageInfo{hasNextPage endCursor}}}}}`,
		}
		request.Variables.Owner = parts[0]
		request.Variables.Name = parts[1]
		request.Variables.Number = number
		if cursor != "" {
			request.Variables.Cursor = &cursor
		}
		if err := client.requestGraphQL(ctx, request, &response); err != nil {
			return nil, err
		}
		repository := response.Data.Repository
		if len(response.Errors) != 0 || repository == nil ||
			repository.NameWithOwner != client.repository ||
			repository.PullRequest == nil ||
			repository.PullRequest.Number != number {
			return nil, errors.New(
				"GitHub Review thread response is inconsistent",
			)
		}
		connection := repository.PullRequest.ReviewThreads
		if total < 0 {
			total = connection.TotalCount
		} else if total != connection.TotalCount {
			return nil, errors.New(
				"GitHub Review thread count changed during pagination",
			)
		}
		if len(connection.Nodes) > 100 {
			return nil, errors.New("GitHub Review thread page is oversized")
		}
		for _, thread := range connection.Nodes {
			if thread.ID == "" || len(thread.ID) > 256 ||
				thread.Path == "" || thread.Line < 0 {
				return nil, errors.New(
					"GitHub Review thread response is invalid",
				)
			}
			result = append(result, ReviewThread{
				ID: thread.ID, IsResolved: thread.IsResolved,
				Path: thread.Path, Line: thread.Line,
			})
		}
		if !connection.PageInfo.HasNextPage {
			if len(result) != total {
				return nil, errors.New(
					"GitHub Review thread pagination is incomplete",
				)
			}
			return result, nil
		}
		if connection.PageInfo.EndCursor == "" ||
			connection.PageInfo.EndCursor == cursor ||
			len(connection.PageInfo.EndCursor) > 1024 {
			return nil, errors.New(
				"GitHub Review thread cursor is invalid",
			)
		}
		cursor = connection.PageInfo.EndCursor
	}
	return nil, errors.New("GitHub Review thread pagination exceeds page budget")
}

func (client *Client) readCheckRuns(
	ctx context.Context,
	headSHA string,
) ([]CheckRun, error) {
	var result []CheckRun
	total := -1
	err := client.readPages(
		ctx,
		"/repos/"+client.repository+"/commits/"+headSHA+"/check-runs",
		func(endpoint url.URL) (*url.URL, error) {
			var payload struct {
				TotalCount int `json:"total_count"`
				CheckRuns  []struct {
					ID         int64  `json:"id"`
					Name       string `json:"name"`
					Status     string `json:"status"`
					Conclusion string `json:"conclusion"`
					ExternalID string `json:"external_id"`
					App        struct {
						Slug string `json:"slug"`
					} `json:"app"`
				} `json:"check_runs"`
			}
			next, err := client.getJSONPage(ctx, endpoint, &payload)
			if err != nil {
				return nil, err
			}
			if total < 0 {
				total = payload.TotalCount
			} else if total != payload.TotalCount {
				return nil, errors.New("GitHub Check count changed during pagination")
			}
			for _, item := range payload.CheckRuns {
				if item.ID <= 0 || item.Name == "" || item.App.Slug == "" {
					return nil, errors.New("GitHub Check response is invalid")
				}
				result = append(result, CheckRun{
					ID: item.ID, Name: item.Name, Status: item.Status,
					Conclusion: item.Conclusion, AppSlug: item.App.Slug,
					ExternalID: item.ExternalID,
				})
			}
			return next, nil
		},
	)
	if err == nil && total != len(result) {
		return nil, errors.New("GitHub Check pagination is incomplete")
	}
	return result, err
}

func (client *Client) readPages(
	ctx context.Context,
	pathValue string,
	read func(url.URL) (*url.URL, error),
) error {
	var next *url.URL
	for page := 1; page <= client.maxPages; page++ {
		endpoint := client.pagedEndpoint(pathValue, page)
		if next != nil {
			endpoint = *next
		}
		var err error
		next, err = read(endpoint)
		if err != nil {
			return err
		}
		if next == nil {
			return nil
		}
	}
	return errors.New("GitHub pagination exceeds page budget")
}

func normalizeFileStatus(value string) (contract.FileStatus, error) {
	switch value {
	case "added":
		return contract.FileStatusAdded, nil
	case "modified", "changed", "copied":
		return contract.FileStatusModified, nil
	case "removed":
		return contract.FileStatusRemoved, nil
	case "renamed":
		return contract.FileStatusRenamed, nil
	default:
		return "", errors.New("GitHub file status is unsupported")
	}
}

func generatedPath(repositoryPath string) bool {
	lower := strings.ToLower(repositoryPath)
	return strings.Contains(lower, "/dist/") ||
		strings.Contains(lower, "/generated/") ||
		strings.HasSuffix(lower, ".min.js") ||
		strings.HasSuffix(lower, ".map")
}

func linkedIssueNumbers(body string) ([]int64, string) {
	matches := issueReferencePattern.FindAllStringSubmatch(body, -1)
	seen := make(map[int64]struct{}, len(matches))
	result := make([]int64, 0, len(matches))
	for _, match := range matches {
		number, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			continue
		}
		if _, exists := seen[number]; exists {
			continue
		}
		seen[number] = struct{}{}
		if len(seen) > contract.MaxLinkedIssues {
			return nil, "linked Issue budget exceeded"
		}
		result = append(result, number)
	}
	slices.Sort(result)
	return result, ""
}
