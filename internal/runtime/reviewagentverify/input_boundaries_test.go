package reviewagentverify_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestContextDocumentDiscoveryRejectsAmbiguousTrustedInputs(t *testing.T) {
	t.Parallel()

	validDocument := verify.BaseContextDocument{
		Path:    "AGENTS.md",
		BlobSHA: strings.Repeat("a", 40),
		Content: []byte("trusted instructions"),
	}
	tests := []struct {
		name    string
		changed []string
		catalog []verify.BaseContextDocument
	}{
		{name: "no changed paths", catalog: []verify.BaseContextDocument{validDocument}},
		{name: "invalid changed path", changed: []string{"../outside.go"}, catalog: []verify.BaseContextDocument{validDocument}},
		{name: "invalid document path", changed: []string{"internal/app/app.go"}, catalog: []verify.BaseContextDocument{{Path: "../AGENTS.md", BlobSHA: strings.Repeat("a", 40), Content: []byte("trusted")}}},
		{name: "invalid blob identity", changed: []string{"internal/app/app.go"}, catalog: []verify.BaseContextDocument{{Path: "AGENTS.md", BlobSHA: "short", Content: []byte("trusted")}}},
		{name: "unsupported document name", changed: []string{"README.md"}, catalog: []verify.BaseContextDocument{{Path: "README.md", BlobSHA: strings.Repeat("a", 40), Content: []byte("trusted")}}},
		{name: "duplicate document", changed: []string{"internal/app/app.go"}, catalog: []verify.BaseContextDocument{validDocument, validDocument}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := verify.DiscoverContextDocuments(test.changed, test.catalog)
			require.Error(t, err)
		})
	}
}

func TestInventoryRejectsBudgetPathTypeAndStatusAmbiguity(t *testing.T) {
	t.Parallel()

	valid := verify.RawFile{
		Path:    "README.md",
		Status:  contract.FileStatusModified,
		Mode:    "100644",
		Type:    verify.FileTypeText,
		Patch:   []byte("one\ntwo"),
		Content: []byte("content"),
	}
	limits := verify.InventoryLimits{MaxFiles: 2, MaxTotalBytes: 1 << 20, MaxLines: 10}

	_, err := verify.BuildInventory(1, []verify.RawFile{valid}, verify.InventoryLimits{})
	require.EqualError(t, err, "invalid changed-file inventory limits")
	_, err = verify.BuildInventory(1, []verify.RawFile{valid}, verify.InventoryLimits{
		MaxFiles: 0, MaxTotalBytes: 1 << 20, MaxLines: 10,
	})
	require.EqualError(t, err, "invalid changed-file inventory limits")
	_, err = verify.BuildInventory(2, []verify.RawFile{valid, valid}, verify.InventoryLimits{
		MaxFiles: 1, MaxTotalBytes: 1 << 20, MaxLines: 10,
	})
	require.EqualError(t, err, "changed-file budget exceeded")

	caseDuplicate := valid
	caseDuplicate.Path = "readme.md"
	_, err = verify.BuildInventory(2, []verify.RawFile{valid, caseDuplicate}, limits)
	require.EqualError(t, err, "duplicate changed-file path")

	_, err = verify.BuildInventory(1, []verify.RawFile{valid}, verify.InventoryLimits{
		MaxFiles: 2, MaxTotalBytes: 1 << 20, MaxLines: 1,
	})
	require.EqualError(t, err, "changed-line budget exceeded")

	tests := []struct {
		name   string
		mutate func(*verify.RawFile)
	}{
		{name: "empty text patch", mutate: func(file *verify.RawFile) { file.Patch = nil }},
		{name: "invalid UTF-8 content", mutate: func(file *verify.RawFile) { file.Content = []byte{0xff} }},
		{name: "unsupported type", mutate: func(file *verify.RawFile) { file.Type = verify.FileType("archive") }},
		{name: "old path on non-rename", mutate: func(file *verify.RawFile) { file.OldPath = "OLD_README.md" }},
		{name: "rename to itself", mutate: func(file *verify.RawFile) { file.Status = contract.FileStatusRenamed; file.OldPath = file.Path }},
		{name: "unsupported status", mutate: func(file *verify.RawFile) { file.Status = contract.FileStatus("copied") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file := valid
			test.mutate(&file)
			_, err := verify.BuildInventory(1, []verify.RawFile{file}, limits)
			require.Error(t, err)
		})
	}
}

func TestContextAndCheckPlanningRejectIncompleteAuthority(t *testing.T) {
	t.Parallel()

	_, err := verify.BuildContext(verify.ContextInput{
		Inventory: verify.Inventory{Complete: false, DeclaredFiles: 1},
	}, 1<<20)
	require.EqualError(t, err, "Review context inventory is incomplete")

	validInventory, err := verify.BuildInventory(
		1,
		[]verify.RawFile{{
			Path: "README.md", Status: contract.FileStatusModified,
			Mode: "100644", Type: verify.FileTypeText,
			Patch: []byte("patch"), Content: []byte("content"),
		}},
		verify.InventoryLimits{MaxFiles: 1, MaxTotalBytes: 1024, MaxLines: 10},
	)
	require.NoError(t, err)
	_, err = verify.BuildContext(verify.ContextInput{
		Generation:         contract.GenerationIdentity{},
		PolicyDigest:       digest("1"),
		PromptDigest:       digest("2"),
		OutputSchemaDigest: digest("3"),
		Title:              "Review",
		Body:               "Body",
		Inventory:          validInventory,
		MandatoryChecks:    []string{"go-unit"},
	}, 1<<20)
	require.Error(t, err)

	complete := verify.Inventory{
		Complete:      true,
		DeclaredFiles: 1,
		Files:         []contract.ChangedFile{{Path: "README.md"}},
	}
	_, err = verify.PlanChecks(
		complete,
		verify.Policy{
			MaxChangedFiles: 1,
			TrustedChecks:   map[string]verify.CheckPlan{"go-unit": {}},
			PathRules:       []verify.PathRule{{Prefixes: []string{""}}},
		},
		verify.RiskSelection{},
	)
	require.EqualError(t, err, "path rule has no checks")
	_, err = verify.PlanChecks(
		complete,
		verify.Policy{
			MaxChangedFiles: 1,
			TrustedChecks:   map[string]verify.CheckPlan{"go-unit": {}},
			PathRules: []verify.PathRule{{
				Prefixes: []string{""}, Checks: []string{"unknown"},
			}},
		},
		verify.RiskSelection{},
	)
	require.EqualError(t, err, "path rule names an unknown trusted check")
	_, err = verify.PlanChecks(
		complete,
		verify.Policy{
			MaxChangedFiles: 1,
			TrustedChecks:   map[string]verify.CheckPlan{"go-unit": {}},
			PathRules: []verify.PathRule{{
				Prefixes: []string{"internal/"}, Checks: []string{"go-unit"},
			}},
		},
		verify.RiskSelection{},
	)
	require.EqualError(t, err, "changed paths have no mandatory check rule")
}
