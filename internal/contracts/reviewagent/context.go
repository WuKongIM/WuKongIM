package reviewagent

import (
	"errors"
	"io"
)

const (
	MaxChangedFiles    = 5000
	MaxInstructions    = 512
	MaxMandatoryChecks = 128
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

// ReviewContext is the complete bounded input to one ephemeral reviewer.
type ReviewContext struct {
	SchemaVersion      int                `json:"schema_version"`
	Generation         GenerationIdentity `json:"generation"`
	PolicyDigest       string             `json:"policy_digest"`
	PromptDigest       string             `json:"prompt_digest"`
	OutputSchemaDigest string             `json:"output_schema_digest"`
	Title              string             `json:"title"`
	Body               string             `json:"body"`
	ChangedFiles       []ChangedFile      `json:"changed_files"`
	Instructions       []InstructionBlob  `json:"instructions"`
	MandatoryChecks    []string           `json:"mandatory_checks"`
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
	if !validText(context.Title, maxIntentTitleBytes, true) ||
		!validText(context.Body, maxIntentBodyBytes, false) {
		return errors.New("invalid Review context intent")
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
		!validText(file.Patch, 2<<20, file.Type == "text") ||
		(file.Mode != "100644" && file.Mode != "100755") ||
		(file.Type != "text" && file.Type != "binary") {
		return errors.New("invalid Review context changed file")
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
