package issueagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxContextItems     = 256
	maxContextTextBytes = 64 << 10
)

// TaskKind distinguishes an initial repair from a grouped Review repair.
type TaskKind string

const (
	TaskKindEngineer TaskKind = "engineer"
	TaskKindReview   TaskKind = "review"
)

// AuthorizationRecord contains only freshly verified maintainer authority.
type AuthorizationRecord struct {
	Actor      string `json:"actor"`
	Permission string `json:"permission"`
	EventID    string `json:"event_id"`
	Command    string `json:"command"`
}

// FileDigest freezes one exact repository instruction blob.
type FileDigest struct {
	Path       string `json:"path"`
	GitBlobSHA string `json:"git_blob_sha"`
}

// EngineerLimits are the immutable resource limits supplied to one Codex run.
type EngineerLimits struct {
	WallTimeSeconds      uint64 `json:"wall_time_seconds"`
	ModifyTestIterations uint32 `json:"modify_test_iterations"`
}

// TrustedContext is derived only from protected policy and authenticated reads.
type TrustedContext struct {
	Authorization      AuthorizationRecord `json:"authorization"`
	Labels             []string            `json:"labels"`
	RequiredTests      []string            `json:"required_tests"`
	RiskCeiling        []string            `json:"risk_ceiling"`
	InstructionDigests []FileDigest        `json:"instruction_digests"`
	KnowledgePaths     []string            `json:"knowledge_paths"`
	OutputSchemaDigest string              `json:"output_schema_digest"`
	Limits             EngineerLimits      `json:"limits"`
}

// IssueSnapshot is untrusted GitHub Issue content with immutable object facts.
type IssueSnapshot struct {
	ID                string    `json:"id"`
	Number            int64     `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	Author            string    `json:"author"`
	AuthorAssociation string    `json:"author_association"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// CommentSnapshot is one untrusted Issue or Review comment.
type CommentSnapshot struct {
	ID                int64     `json:"id"`
	Author            string    `json:"author"`
	AuthorAssociation string    `json:"author_association"`
	Body              string    `json:"body"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ReviewThreadSnapshot freezes one complete unresolved Review thread.
type ReviewThreadSnapshot struct {
	ID       string            `json:"id"`
	Path     string            `json:"path"`
	Line     int64             `json:"line"`
	Comments []CommentSnapshot `json:"comments"`
}

// UntrustedContext contains problem data that must never grant authority.
type UntrustedContext struct {
	Issue         IssueSnapshot          `json:"issue"`
	Comments      []CommentSnapshot      `json:"comments"`
	ReviewThreads []ReviewThreadSnapshot `json:"review_threads"`
}

// ContextBundle is the bounded cross-job input for one ephemeral Codex task.
type ContextBundle struct {
	SchemaVersion int              `json:"schema_version"`
	Repository    string           `json:"repository"`
	IssueNumber   int64            `json:"issue_number"`
	Sequence      uint64           `json:"sequence"`
	Task          TaskIdentity     `json:"task"`
	Trusted       TrustedContext   `json:"trusted"`
	Untrusted     UntrustedContext `json:"untrusted"`
	CreatedAt     time.Time        `json:"created_at"`
}

// ValidateContextBundle rejects ambiguous or unbounded Codex context.
func ValidateContextBundle(bundle ContextBundle) error {
	if bundle.SchemaVersion != 2 || !validRepository(bundle.Repository) ||
		bundle.IssueNumber <= 0 || bundle.Sequence == 0 {
		return errors.New("invalid Context Bundle identity")
	}
	if err := validateV2TaskIdentity(bundle.Task); err != nil {
		return err
	}
	if err := validateTrustedContext(bundle.Trusted); err != nil {
		return err
	}
	if err := validateUntrustedContext(bundle.Untrusted, bundle.IssueNumber); err != nil {
		return err
	}
	if bundle.CreatedAt.IsZero() || bundle.CreatedAt.Location() != time.UTC {
		return errors.New("Context Bundle timestamp must use UTC")
	}
	return nil
}

// DecodeContextBundle decodes one bounded strict Context Bundle.
func DecodeContextBundle(reader io.Reader, maxBytes int64) (ContextBundle, error) {
	var bundle ContextBundle
	if err := decodeStrictJSON(reader, maxBytes, &bundle); err != nil {
		return ContextBundle{}, err
	}
	if err := ValidateContextBundle(bundle); err != nil {
		return ContextBundle{}, err
	}
	return bundle, nil
}

// ContextBundleDigest binds every semantic trusted and untrusted input to one
// task. GitHub's Issue updated_at is an activity watermark that also changes
// when the filtered App-owned status comment is edited, so it is not part of
// the digest. The Publisher still re-reads and compares the actual timestamp.
func ContextBundleDigest(bundle ContextBundle) (string, error) {
	if err := ValidateContextBundle(bundle); err != nil {
		return "", err
	}
	canonical := bundle
	canonical.Untrusted.Issue.UpdatedAt = time.Unix(0, 0).UTC()
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", errors.New("encode Context Bundle")
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateV2TaskIdentity(task TaskIdentity) error {
	if !digestPattern.MatchString(task.ID) ||
		task.Kind != TaskKindEngineer && task.Kind != TaskKindReview ||
		!gitSHAPattern.MatchString(task.BaseSHA) ||
		!gitSHAPattern.MatchString(task.AffectedSHA) ||
		!digestPattern.MatchString(task.PolicyDigest) ||
		!digestPattern.MatchString(task.PromptDigest) {
		return errors.New("invalid v2 task identity")
	}
	return nil
}

func validateTrustedContext(context TrustedContext) error {
	if !validContextIdentity(context.Authorization.Actor, 256) ||
		!validContextIdentity(context.Authorization.EventID, 512) {
		return errors.New("invalid authorization identity")
	}
	switch context.Authorization.Permission {
	case "write", "maintain", "admin":
	default:
		return errors.New("authorization lacks repository write permission")
	}
	switch context.Authorization.Command {
	case "", "/agent fix", "/agent retry", "/agent cancel", "/agent take-over":
	default:
		return errors.New("invalid trusted Agent command")
	}
	if !strictContextStrings(context.Labels, 256, false) ||
		!strictContextStrings(context.RequiredTests, 256, true) ||
		!strictContextStrings(context.RiskCeiling, 256, true) {
		return errors.New("invalid trusted Context Bundle lists")
	}
	if err := validateInstructionDigests(context.InstructionDigests); err != nil {
		return err
	}
	if !strictContextPaths(context.KnowledgePaths) ||
		!digestPattern.MatchString(context.OutputSchemaDigest) {
		return errors.New("invalid Context Bundle repository guidance")
	}
	if context.Limits.WallTimeSeconds == 0 || context.Limits.WallTimeSeconds > 5400 ||
		context.Limits.ModifyTestIterations == 0 ||
		context.Limits.ModifyTestIterations > 3 {
		return errors.New("invalid Engineer limits")
	}
	return nil
}

func validateUntrustedContext(context UntrustedContext, issueNumber int64) error {
	if context.Issue.Number != issueNumber ||
		!validContextIdentity(context.Issue.ID, 512) ||
		!validContextText(context.Issue.Title, 1024, false) ||
		!validContextText(context.Issue.Body, maxContextTextBytes, true) ||
		!validContextIdentity(context.Issue.Author, 256) ||
		!validContextIdentity(context.Issue.AuthorAssociation, 64) ||
		context.Issue.UpdatedAt.IsZero() ||
		context.Issue.UpdatedAt.Location() != time.UTC {
		return errors.New("invalid Issue snapshot")
	}
	if len(context.Comments) > maxContextItems ||
		len(context.ReviewThreads) > maxContextItems {
		return errors.New("Context Bundle contains too many GitHub objects")
	}
	var lastCommentID int64
	for _, comment := range context.Comments {
		if err := validateCommentSnapshot(comment); err != nil {
			return err
		}
		if comment.ID <= lastCommentID {
			return errors.New("Issue comments must be strictly ordered")
		}
		lastCommentID = comment.ID
	}
	var lastThreadID string
	for _, thread := range context.ReviewThreads {
		if !validContextIdentity(thread.ID, 512) ||
			thread.ID <= lastThreadID ||
			thread.Line < 0 ||
			thread.Path != "" && validateRepositoryPath(thread.Path) != nil ||
			len(thread.Comments) == 0 ||
			len(thread.Comments) > maxContextItems {
			return errors.New("invalid Review thread snapshot")
		}
		var lastReviewCommentID int64
		for _, comment := range thread.Comments {
			if err := validateCommentSnapshot(comment); err != nil {
				return err
			}
			if comment.ID <= lastReviewCommentID {
				return errors.New("Review comments must be strictly ordered")
			}
			lastReviewCommentID = comment.ID
		}
		lastThreadID = thread.ID
	}
	return nil
}

func validateCommentSnapshot(comment CommentSnapshot) error {
	if comment.ID <= 0 ||
		!validContextIdentity(comment.Author, 256) ||
		!validContextIdentity(comment.AuthorAssociation, 64) ||
		!validContextText(comment.Body, maxContextTextBytes, true) ||
		comment.UpdatedAt.IsZero() ||
		comment.UpdatedAt.Location() != time.UTC {
		return errors.New("invalid comment snapshot")
	}
	return nil
}

func strictContextStrings(values []string, maxBytes int, requireNonEmpty bool) bool {
	if len(values) > maxContextItems || requireNonEmpty && len(values) == 0 ||
		!slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if !validContextText(value, maxBytes, false) ||
			index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

func strictContextPaths(paths []string) bool {
	if len(paths) == 0 || len(paths) > maxContextItems ||
		!slices.IsSorted(paths) {
		return false
	}
	for index, value := range paths {
		if validateRepositoryPath(value) != nil ||
			index > 0 && paths[index-1] == value {
			return false
		}
	}
	return true
}

func validateInstructionDigests(digests []FileDigest) error {
	if len(digests) == 0 || len(digests) > maxContextItems {
		return errors.New("instruction digests are empty or oversized")
	}
	for index, digest := range digests {
		if err := validateRepositoryPath(digest.Path); err != nil {
			return err
		}
		if !gitSHAPattern.MatchString(digest.GitBlobSHA) ||
			index > 0 && digest.Path <= digests[index-1].Path {
			return errors.New(
				"instruction digests must be valid and strictly sorted",
			)
		}
	}
	return nil
}

func validContextIdentity(value string, maxBytes int) bool {
	return validContextText(value, maxBytes, false) &&
		!strings.ContainsAny(value, " \t\r\n")
}

func validContextText(value string, maxBytes int, allowLineBreaks bool) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == '\x7f' ||
			character < '\x20' &&
				(!allowLineBreaks ||
					character != '\n' && character != '\r' && character != '\t') {
			return false
		}
	}
	return true
}
