package reviewagentverify_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestInstructionDiscoveryUsesOnlyApplicableBaseTreeBlobs(t *testing.T) {
	t.Parallel()

	instructions, err := verify.DiscoverInstructions(
		[]string{
			"internal/runtime/delivery/queue.go",
			"web/src/App.tsx",
		},
		[]verify.BaseInstruction{
			{
				Path: "AGENTS.md", BlobSHA: strings.Repeat("a", 40),
				Content: []byte("root"),
			},
			{
				Path: "internal/FLOW.md", BlobSHA: strings.Repeat("b", 40),
				Content: []byte("internal"),
			},
			{
				Path:    "internal/runtime/delivery/FLOW.md",
				BlobSHA: strings.Repeat("c", 40), Content: []byte("delivery"),
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
		instructionPaths(instructions),
	)
	for _, instruction := range instructions {
		require.NotEmpty(t, instruction.BlobDigest)
		require.NotEmpty(t, instruction.Scope)
	}
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
		Title:              "Docs",
		Body:               "Clarify behavior.",
		Inventory:          inventory,
		MandatoryChecks:    []string{"docs-contracts"},
	}

	context, err := verify.BuildContext(input, 1<<20)
	require.NoError(t, err)
	require.Len(t, context.ChangedFiles, 1)

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

func instructionPaths(values []contract.InstructionBlob) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Path)
	}
	return result
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
