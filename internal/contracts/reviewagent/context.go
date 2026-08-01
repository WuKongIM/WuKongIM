package reviewagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
)

const (
	MaxChangedFiles = 50
	// MaxContextBytes bounds the canonical encoded Context Bundle.
	MaxContextBytes int64 = 2 << 20

	MaxInstructions    = 128
	MaxMandatoryChecks = 128
	MaxLinkedIssues    = 128
	MaxReviewThreads   = 10000
	MaxDiscussionItems = 30000
)

// FileStatus is GitHub's normalized operation for one complete inventory item.
type FileStatus string

const (
	FileStatusAdded    FileStatus = "added"
	FileStatusModified FileStatus = "modified"
	FileStatusRemoved  FileStatus = "removed"
	FileStatusRenamed  FileStatus = "renamed"
)

// ChangedFile identifies one changed path and the captured diff content.
type ChangedFile struct {
	Path          string     `json:"path"`
	PreviousPath  string     `json:"previous_path"`
	Status        FileStatus `json:"status"`
	Mode          string     `json:"mode"`
	Type          string     `json:"type"`
	Generated     bool       `json:"generated"`
	Patch         string     `json:"patch"`
	PatchDigest   string     `json:"patch_digest"`
	Content       string     `json:"content"`
	ContentDigest string     `json:"content_digest"`
	Additions     uint64     `json:"additions"`
	Deletions     uint64     `json:"deletions"`
}

// InstructionBlob freezes one applicable base/control-tree instruction.
type InstructionBlob struct {
	Path       string `json:"path"`
	Scope      string `json:"scope"`
	BlobSHA    string `json:"blob_sha"`
	BlobDigest string `json:"blob_digest"`
	Content    string `json:"content"`
}

// LinkedIssue freezes one intent source discovered from the current PR body.
type LinkedIssue struct {
	Number int64  `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// ReviewThreadContext carries current resolution state into the reviewer.
type ReviewThreadContext struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"is_resolved"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
}

// DiscussionKind identifies one untrusted GitHub discussion surface.
type DiscussionKind string

const (
	DiscussionFormalReview  DiscussionKind = "formal_review"
	DiscussionIssueComment  DiscussionKind = "issue_comment"
	DiscussionReviewComment DiscussionKind = "review_comment"
)

// DiscussionItem preserves bounded prior review context without granting it
// instruction or lifecycle authority.
type DiscussionItem struct {
	Kind        DiscussionKind `json:"kind"`
	ID          int64          `json:"id"`
	Author      string         `json:"author"`
	AuthorType  string         `json:"author_type"`
	Body        string         `json:"body"`
	State       string         `json:"state"`
	CommitSHA   string         `json:"commit_sha"`
	Path        string         `json:"path"`
	Line        int            `json:"line"`
	Side        string         `json:"side"`
	InReplyToID int64          `json:"in_reply_to_id"`
}

// PriorFindingContext carries a trusted stable identity beside one earlier
// finding so the reviewer can explicitly retain or withdraw it.
type PriorFindingContext struct {
	Digest  string  `json:"digest"`
	Finding Finding `json:"finding"`
}

// ReviewContext is the complete bounded input to one ephemeral reviewer.
type ReviewContext struct {
	SchemaVersion      int                   `json:"schema_version"`
	Generation         GenerationIdentity    `json:"generation"`
	PolicyDigest       string                `json:"policy_digest"`
	PromptDigest       string                `json:"prompt_digest"`
	OutputSchemaDigest string                `json:"output_schema_digest"`
	ReviewReason       string                `json:"review_reason"`
	Title              string                `json:"title"`
	Body               string                `json:"body"`
	LinkedIssues       []LinkedIssue         `json:"linked_issues"`
	ReviewThreads      []ReviewThreadContext `json:"review_threads"`
	Discussion         []DiscussionItem      `json:"discussion"`
	PriorFindings      []PriorFindingContext `json:"prior_findings"`
	ChangedFiles       []ChangedFile         `json:"changed_files"`
	Instructions       []InstructionBlob     `json:"instructions"`
	MandatoryChecks    []string              `json:"mandatory_checks"`
}

// ValidateReviewContext rejects incomplete inventories and untrusted
// instruction identities.
func ValidateReviewContext(context ReviewContext) error {
	if context.SchemaVersion != 1 {
		return errors.New("unsupported Review context schema version")
	}
	if err := ValidateGenerationIdentity(context.Generation); err != nil {
		return err
	}
	if !validDigest(context.PolicyDigest) ||
		!validDigest(context.PromptDigest) ||
		!validDigest(context.OutputSchemaDigest) {
		return errors.New("invalid Review context control digest")
	}
	if !validText(context.ReviewReason, 2048, false) {
		return errors.New("invalid Review context review reason")
	}
	if !validText(context.Title, maxIntentTitleBytes, true) ||
		!validText(context.Body, maxIntentBodyBytes, false) {
		return errors.New("invalid Review context intent")
	}
	if len(context.LinkedIssues) > MaxLinkedIssues {
		return errors.New("too many Review context linked Issues")
	}
	linked := make(map[int64]struct{}, len(context.LinkedIssues))
	for _, issue := range context.LinkedIssues {
		if issue.Number <= 0 ||
			(issue.State != "open" && issue.State != "closed") ||
			!validText(issue.Title, maxIntentTitleBytes, true) ||
			!validText(issue.Body, 1<<20, false) {
			return errors.New("invalid Review context linked Issue")
		}
		if _, duplicate := linked[issue.Number]; duplicate {
			return errors.New("duplicate Review context linked Issue")
		}
		linked[issue.Number] = struct{}{}
	}
	if len(context.ReviewThreads) > MaxReviewThreads {
		return errors.New("too many Review context threads")
	}
	threadIDs := make(map[string]struct{}, len(context.ReviewThreads))
	for _, thread := range context.ReviewThreads {
		if !validText(thread.ID, 256, true) ||
			!validRepositoryPath(thread.Path) ||
			thread.Line < 0 {
			return errors.New("invalid Review context thread")
		}
		if _, duplicate := threadIDs[thread.ID]; duplicate {
			return errors.New("duplicate Review context thread")
		}
		threadIDs[thread.ID] = struct{}{}
	}
	if len(context.Discussion) > MaxDiscussionItems {
		return errors.New("too many Review context discussion items")
	}
	discussionIDs := make(map[string]struct{}, len(context.Discussion))
	for _, item := range context.Discussion {
		if err := validateDiscussionItem(item); err != nil {
			return err
		}
		key := string(item.Kind) + ":" + strconv.FormatInt(item.ID, 10)
		if _, duplicate := discussionIDs[key]; duplicate {
			return errors.New("duplicate Review context discussion item")
		}
		discussionIDs[key] = struct{}{}
	}
	if len(context.PriorFindings) > MaxFindings {
		return errors.New("too many Review context prior findings")
	}
	priorDigests := make(map[string]struct{}, len(context.PriorFindings))
	for _, prior := range context.PriorFindings {
		if err := validateFinding(prior.Finding); err != nil {
			return err
		}
		digest, err := FindingDigest(prior.Finding)
		if err != nil || prior.Digest != digest {
			return errors.New("invalid Review context prior finding digest")
		}
		if _, duplicate := priorDigests[prior.Digest]; duplicate {
			return errors.New("duplicate Review context prior finding")
		}
		priorDigests[prior.Digest] = struct{}{}
	}
	if len(context.ChangedFiles) == 0 ||
		len(context.ChangedFiles) > MaxChangedFiles {
		return errors.New("invalid Review context changed-file inventory")
	}
	paths := make(map[string]struct{}, len(context.ChangedFiles))
	for _, file := range context.ChangedFiles {
		if err := validateChangedFile(file); err != nil {
			return err
		}
		if _, exists := paths[file.Path]; exists {
			return errors.New("duplicate Review context path")
		}
		paths[file.Path] = struct{}{}
	}
	if len(context.Instructions) > MaxInstructions {
		return errors.New("too many Review context instructions")
	}
	instructionPaths := make(map[string]struct{}, len(context.Instructions))
	for _, instruction := range context.Instructions {
		if !validRepositoryPath(instruction.Path) ||
			(instruction.Scope != "." &&
				!validRepositoryPath(instruction.Scope)) ||
			!validSHA(instruction.BlobSHA) ||
			!validDigest(instruction.BlobDigest) ||
			!validText(instruction.Content, 1<<20, true) {
			return errors.New("invalid Review context instruction")
		}
		if _, exists := instructionPaths[instruction.Path]; exists {
			return errors.New("duplicate Review context instruction")
		}
		instructionPaths[instruction.Path] = struct{}{}
	}
	if !validUniqueStrings(
		context.MandatoryChecks,
		MaxMandatoryChecks,
		64,
		true,
	) {
		return errors.New("invalid Review context mandatory checks")
	}
	for _, check := range context.MandatoryChecks {
		if !checkNamePattern.MatchString(check) {
			return errors.New("invalid Review context check name")
		}
	}
	return nil
}

func validateDiscussionItem(item DiscussionItem) error {
	if item.ID <= 0 ||
		!validText(item.Author, 256, true) ||
		!validText(item.AuthorType, 64, true) ||
		!validText(item.Body, 128<<10, false) ||
		!validText(item.State, 64, false) ||
		!validText(item.Side, 16, false) ||
		item.InReplyToID < 0 {
		return errors.New("invalid Review context discussion item")
	}
	switch item.Kind {
	case DiscussionFormalReview:
		if item.Path != "" || item.Line != 0 || item.Side != "" ||
			item.InReplyToID != 0 ||
			item.State == "" ||
			item.CommitSHA != "" && !validSHA(item.CommitSHA) {
			return errors.New("invalid Review context formal Review")
		}
	case DiscussionIssueComment:
		if item.State != "" || item.CommitSHA != "" ||
			item.Path != "" || item.Line != 0 || item.Side != "" ||
			item.InReplyToID != 0 {
			return errors.New("invalid Review context Issue comment")
		}
	case DiscussionReviewComment:
		if item.State != "" || item.CommitSHA != "" ||
			!validRepositoryPath(item.Path) ||
			item.Line < 0 ||
			(item.Side != "" &&
				item.Side != "LEFT" &&
				item.Side != "RIGHT") ||
			(item.Line == 0) != (item.Side == "") {
			return errors.New("invalid Review context Review comment")
		}
	default:
		return errors.New("invalid Review context discussion kind")
	}
	return nil
}

// DecodeReviewContext decodes one bounded, strict context document.
func DecodeReviewContext(
	reader io.Reader,
	maxBytes int64,
) (ReviewContext, error) {
	var context ReviewContext
	if err := decodeStrictJSON(reader, maxBytes, &context); err != nil {
		return ReviewContext{}, err
	}
	if err := ValidateReviewContext(context); err != nil {
		return ReviewContext{}, err
	}
	return context, nil
}

// ReviewContextDigest binds the complete reviewer input.
func ReviewContextDigest(context ReviewContext) (string, error) {
	if err := ValidateReviewContext(context); err != nil {
		return "", err
	}
	return canonicalDigest(context, "encode Review context")
}

func validateChangedFile(file ChangedFile) error {
	if !validRepositoryPath(file.Path) ||
		!validDigest(file.PatchDigest) ||
		!validDigest(file.ContentDigest) ||
		file.PatchDigest != changedContentDigest(file.Patch) ||
		!validText(file.Patch, 32<<20, file.Type == "text") ||
		!validText(file.Content, 16<<20, false) ||
		(file.Mode != "100644" && file.Mode != "100755") ||
		(file.Type != "text" && file.Type != "binary") {
		return errors.New("invalid Review context changed file")
	}
	if file.Type == "binary" && (file.Patch != "" || file.Content != "") {
		return errors.New("binary Review context file exposes raw content")
	}
	if file.Type == "text" &&
		file.ContentDigest != changedContentDigest(file.Content) {
		return errors.New("text Review context content digest is inconsistent")
	}
	switch file.Status {
	case FileStatusAdded, FileStatusModified, FileStatusRemoved:
		if file.PreviousPath != "" {
			return errors.New("non-rename Review context file has previous path")
		}
	case FileStatusRenamed:
		if !validRepositoryPath(file.PreviousPath) ||
			file.PreviousPath == file.Path {
			return errors.New("invalid Review context rename")
		}
	default:
		return errors.New("invalid Review context file status")
	}
	return nil
}

func changedContentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}
