package reviewagentverify

import (
	"encoding/json"
	"errors"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// ContextInput contains the complete trusted and untrusted inputs selected for
// one bounded ReviewContext.
type ContextInput struct {
	Generation         contract.GenerationIdentity
	PolicyDigest       string
	PromptDigest       string
	OutputSchemaDigest string
	ReviewReason       string
	Title              string
	Body               string
	LinkedIssues       []contract.LinkedIssue
	ReviewThreads      []contract.ReviewThreadContext
	Discussion         []contract.DiscussionItem
	PriorFindings      []contract.Finding
	Inventory          Inventory
	ContextDocuments   []contract.ContextDocumentBlob
	MandatoryChecks    []string
}

// BuildContext constructs the complete context or rejects its byte budget.
func BuildContext(input ContextInput, maxBytes int64) (contract.ReviewContext, error) {
	if !input.Inventory.Complete ||
		input.Inventory.DeclaredFiles != len(input.Inventory.Files) {
		return contract.ReviewContext{}, errors.New(
			"Review context inventory is incomplete",
		)
	}
	context := contract.ReviewContext{
		SchemaVersion:      1,
		Generation:         input.Generation,
		PolicyDigest:       input.PolicyDigest,
		PromptDigest:       input.PromptDigest,
		OutputSchemaDigest: input.OutputSchemaDigest,
		ReviewReason:       input.ReviewReason,
		Title:              input.Title,
		Body:               input.Body,
		LinkedIssues: append(
			[]contract.LinkedIssue(nil),
			input.LinkedIssues...,
		),
		ReviewThreads: append(
			[]contract.ReviewThreadContext(nil),
			input.ReviewThreads...,
		),
		Discussion: append(
			[]contract.DiscussionItem(nil),
			input.Discussion...,
		),
		ChangedFiles: append(
			[]contract.ChangedFile(nil),
			input.Inventory.Files...,
		),
		ContextDocuments: append(
			[]contract.ContextDocumentBlob(nil),
			input.ContextDocuments...,
		),
		MandatoryChecks: append(
			[]string(nil),
			input.MandatoryChecks...,
		),
	}
	for _, finding := range input.PriorFindings {
		digest, err := contract.FindingDigest(finding)
		if err != nil {
			return contract.ReviewContext{}, err
		}
		context.PriorFindings = append(
			context.PriorFindings,
			contract.PriorFindingContext{
				Digest:  digest,
				Finding: finding,
			},
		)
	}
	if err := contract.ValidateReviewContext(context); err != nil {
		return contract.ReviewContext{}, err
	}
	body, err := json.Marshal(context)
	if err != nil {
		return contract.ReviewContext{}, errors.New("encode Review context")
	}
	if maxBytes <= 0 || int64(len(body)) > maxBytes {
		return contract.ReviewContext{}, errors.New(
			"Review context exceeds byte budget",
		)
	}
	return context, nil
}
