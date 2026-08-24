package reviewagentverify_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestContextDocumentDiscoveryUsesOnlyApplicableBaseTreeBlobs(t *testing.T) {
	t.Parallel()

	documents, err := verify.DiscoverContextDocuments(
		[]string{
			"internal/runtime/delivery/queue.go",
			"web/src/App.tsx",
		},
		[]verify.BaseContextDocument{
			{
				Path: "AGENTS.md", BlobSHA: strings.Repeat("a", 40),
				Content: []byte("root"),
			},
			{
				Path: "internal/FLOW.md", BlobSHA: strings.Repeat("b", 40),
				Content: []byte("---\nscope: subtree\nsummary: Describes internal descendants.\n---\n"),
			},
			{
				Path:    "internal/runtime/delivery/FLOW.md",
				BlobSHA: strings.Repeat("c", 40),
				Content: []byte("---\nscope: package\nsummary: Describes exact delivery behavior.\n---\n"),
			},
			{
				Path: "web/AGENTS.md", BlobSHA: strings.Repeat("d", 40),
				Content: []byte("web"),
			},
			{
				Path: "pkg/FLOW.md", BlobSHA: strings.Repeat("e", 40),
				Content: []byte("unrelated"),
			},
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{
			"AGENTS.md",
			"internal/FLOW.md",
			"internal/runtime/delivery/FLOW.md",
			"web/AGENTS.md",
		},
		contextDocumentPaths(documents),
	)
	for _, document := range documents {
		require.NotEmpty(t, document.BlobDigest)
		require.NotEmpty(t, document.Scope)
	}
}

func TestContextDocumentDiscoveryRejectsFlowWithoutMetadata(t *testing.T) {
	t.Parallel()

	_, err := verify.DiscoverContextDocuments(
		[]string{"internal/runtime/delivery/queue.go"},
		[]verify.BaseContextDocument{{
			Path:    "internal/FLOW.md",
			BlobSHA: strings.Repeat("a", 40),
			Content: []byte("# Internal Flow\n"),
		}},
	)
	require.EqualError(
		t,
		err,
		"internal/FLOW.md: invalid base FLOW metadata: FLOW front matter is missing",
	)
}

func TestContextDocumentDiscoveryHonorsExplicitFlowScope(t *testing.T) {
	t.Parallel()

	documents, err := verify.DiscoverContextDocuments(
		[]string{"internal/runtime/delivery/queue.go"},
		[]verify.BaseContextDocument{
			{
				Path: "AGENTS.md", BlobSHA: strings.Repeat("a", 40),
				Content: []byte("root instructions"),
			},
			{
				Path: "internal/FLOW.md", BlobSHA: strings.Repeat("b", 40),
				Content: []byte("---\nscope: package\nsummary: Describes the internal root package.\n---\n"),
			},
			{
				Path:    "internal/runtime/FLOW.md",
				BlobSHA: strings.Repeat("c", 40),
				Content: []byte("---\nscope: subtree\nsummary: Describes runtime descendants.\n---\n"),
			},
			{
				Path:    "internal/runtime/delivery/FLOW.md",
				BlobSHA: strings.Repeat("d", 40),
				Content: []byte("---\nscope: package\nsummary: Describes exact delivery behavior.\n---\n"),
			},
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{
			"AGENTS.md",
			"internal/runtime/FLOW.md",
			"internal/runtime/delivery/FLOW.md",
		},
		contextDocumentPaths(documents),
	)
}

func TestContextDocumentDiscoveryRejectsMalformedExplicitFlowMetadata(t *testing.T) {
	t.Parallel()

	_, err := verify.DiscoverContextDocuments(
		[]string{"internal/runtime/delivery/queue.go"},
		[]verify.BaseContextDocument{{
			Path:    "internal/FLOW.md",
			BlobSHA: strings.Repeat("a", 40),
			Content: []byte("---\nscope: everywhere\nsummary: Invalid scope.\n---\n"),
		}},
	)
	require.EqualError(
		t,
		err,
		"internal/FLOW.md: invalid base FLOW metadata: FLOW scope must be package or subtree",
	)
}

func TestContextDocumentDiscoveryIgnoresMalformedUnrelatedFlowMetadata(t *testing.T) {
	t.Parallel()

	documents, err := verify.DiscoverContextDocuments(
		[]string{"internal/runtime/delivery/queue.go"},
		[]verify.BaseContextDocument{{
			Path:    "pkg/FLOW.md",
			BlobSHA: strings.Repeat("a", 40),
			Content: []byte("---\nscope: everywhere\nsummary: Invalid scope.\n---\n"),
		}},
	)
	require.NoError(t, err)
	require.Empty(t, documents)
}

func TestContextRejectsBudgetInsteadOfDroppingInventory(t *testing.T) {
	t.Parallel()

	generation := testGeneration()
	inventory, err := verify.BuildInventory(
		1,
		[]verify.RawFile{{
			Path: "README.md", Status: contract.FileStatusModified,
			Mode: "100644", Type: verify.FileTypeText,
			Patch: []byte("patch"), Content: []byte("readme"),
		}},
		verify.InventoryLimits{
			MaxFiles: 10, MaxTotalBytes: 1 << 20, MaxLines: 1000,
		},
	)
	require.NoError(t, err)
	input := verify.ContextInput{
		Generation:         generation,
		PolicyDigest:       digest("1"),
		PromptDigest:       digest("2"),
		OutputSchemaDigest: digest("3"),
		ReviewReason:       "explicit reconsideration: verify the queue fix",
		Title:              "Docs",
		Body:               "Clarify behavior.",
		Discussion: []contract.DiscussionItem{{
			Kind:       contract.DiscussionFormalReview,
			ID:         9,
			Author:     "review-agent[bot]",
			AuthorType: "Bot",
			Body:       "The previous generation found a queue race.",
			State:      "CHANGES_REQUESTED",
			CommitSHA:  generation.HeadSHA,
		}},
		PriorFindings: []contract.Finding{{
			Kind:       contract.FindingBlocking,
			Dimension:  contract.DimensionIntentCorrectness,
			Title:      "Queue race",
			Path:       "internal/runtime/delivery/queue.go",
			LineStart:  10,
			LineEnd:    10,
			Scenario:   "Close overlaps enqueue.",
			Impact:     "A message can be lost.",
			Evidence:   []string{"diff:queue.go:10"},
			Resolution: "Serialize the operations.",
		}},
		Inventory:       inventory,
		MandatoryChecks: []string{"docs-contracts"},
	}

	context, err := verify.BuildContext(input, 1<<20)
	require.NoError(t, err)
	require.Len(t, context.ChangedFiles, 1)
	require.Equal(t, input.ReviewReason, context.ReviewReason)
	require.Equal(t, input.Discussion, context.Discussion)
	require.Len(t, context.PriorFindings, 1)
	require.Equal(t, input.PriorFindings[0], context.PriorFindings[0].Finding)
	require.NotEmpty(t, context.PriorFindings[0].Digest)

	body, err := json.Marshal(context)
	require.NoError(t, err)
	_, err = verify.BuildContext(input, int64(len(body)-1))
	require.EqualError(t, err, "Review context exceeds byte budget")
}

func testGeneration() contract.GenerationIdentity {
	return contract.GenerationIdentity{
		Repository: "WuKongIM/WuKongIM", PullRequest: 42,
		HeadSHA:      strings.Repeat("a", 40),
		BaseSHA:      strings.Repeat("b", 40),
		TestMergeSHA: strings.Repeat("c", 40),
		IntentDigest: digest("d"), Generation: 1,
		StateParentSHA: strings.Repeat("e", 40),
	}
}

func contextDocumentPaths(values []contract.ContextDocumentBlob) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Path)
	}
	return result
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func contentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}
