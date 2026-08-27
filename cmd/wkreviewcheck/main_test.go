package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunRejectsArbitraryReviewCheckSelector(t *testing.T) {
	t.Parallel()

	require.EqualError(t, run([]string{"bash", "-c", "true"}), "review check selector is required")
	require.EqualError(t, run([]string{"merge"}), "unknown Review check selector")
}

func TestDocumentationCheckPlanIsBoundedAndReproducible(t *testing.T) {
	t.Parallel()

	root := "/workspace"
	require.Equal(t, []checkStep{
		{
			directory: root,
			name:      "go",
			arguments: []string{
				"test", "./scripts/...", "-run", "Docs|Markdown|Link", "-count=1",
			},
			environment: []string{"GOWORK=off"},
		},
		{
			directory: filepath.Join(root, "docs-site"),
			name:      "bun",
			arguments: []string{"install", "--frozen-lockfile"},
		},
		{
			directory: filepath.Join(root, "docs-site"),
			name:      "bun",
			arguments: []string{"run", "verify"},
		},
	}, documentationCheckSteps(root))
}

func TestDocumentationIntegrationPlanUsesOnlyTheFocusedGoldenPath(t *testing.T) {
	t.Parallel()

	receiptPath := "/workspace/tmp/docs-site-e2e/docs-integration-receipt-123/golden-path.json"
	unverifiedEnvironment := []string{
		"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=",
		"WK_DOCS_GOLDEN_PATH_RECEIPT_JSON=",
		"WK_DOCS_REQUIRE_VERIFIED=",
		"WK_DOCS_SOURCE_REVISION=",
	}
	verifiedEnvironment := []string{
		"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=" + receiptPath,
		"WK_DOCS_GOLDEN_PATH_RECEIPT_JSON=",
		"WK_DOCS_REQUIRE_VERIFIED=1",
		"WK_DOCS_SOURCE_REVISION=",
	}
	require.Equal(t, documentationIntegrationPlan{
		beforeReceipt: []checkStep{
			{
				directory: "/workspace/docs-site",
				name:      "bun",
				arguments: []string{"install", "--frozen-lockfile"},
			},
			{
				directory:   "/workspace/docs-site",
				name:        "bun",
				arguments:   []string{"run", "test"},
				environment: unverifiedEnvironment,
			},
			{
				directory:   "/workspace/docs-site",
				name:        "bun",
				arguments:   []string{"run", "build"},
				environment: unverifiedEnvironment,
			},
			{
				directory:   "/workspace/docs-site",
				name:        "bun",
				arguments:   []string{"run", "test:output"},
				environment: unverifiedEnvironment,
			},
			{
				directory: "/workspace/docs-site/examples/javascript-web-quickstart",
				name:      "npm",
				arguments: []string{"ci"},
			},
			{
				directory: "/workspace/docs-site/examples/javascript-web-quickstart",
				name:      "npm",
				arguments: []string{"exec", "--", "playwright", "install", "chromium"},
			},
			{
				directory: "/workspace",
				name:      "go",
				arguments: []string{
					"test", "-tags=e2e",
					"./test/e2e/message/javascript_web_quickstart",
					"-count=1", "-timeout=10m", "-p=1",
				},
				environment: []string{
					"GOWORK=off",
					"WK_E2E_DOCS_JAVASCRIPT_WEB=1",
					"WK_DOCS_GOLDEN_PATH_ATTESTATION_OUTPUT=" + receiptPath,
				},
			},
		},
		afterReceipt: []checkStep{
			{
				directory:   "/workspace/docs-site",
				name:        "bun",
				arguments:   []string{"run", "build"},
				environment: verifiedEnvironment,
			},
			{
				directory:   "/workspace/docs-site",
				name:        "bun",
				arguments:   []string{"run", "test:output"},
				environment: verifiedEnvironment,
			},
		},
	}, documentationIntegrationCheckPlan("/workspace", receiptPath))
}

func TestMergeEnvironmentReplacesAmbientKeysAndPreservesExplicitEmptyValues(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"PATH=/bin",
		"KEEP=value",
		"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=",
		"WK_DOCS_REQUIRE_VERIFIED=1",
	}, mergeEnvironment(
		[]string{
			"PATH=/bin",
			"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=/ambient/receipt.json",
			"KEEP=value",
			"WK_DOCS_REQUIRE_VERIFIED=0",
		},
		[]string{
			"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=",
			"WK_DOCS_REQUIRE_VERIFIED=0",
			"WK_DOCS_REQUIRE_VERIFIED=1",
		},
	))
}

func TestValidateGoldenPathAttestationRequiresTheExactPinnedTuple(t *testing.T) {
	t.Parallel()

	revision := strings.Repeat("a", 40)
	lockSHA := strings.Repeat("b", 64)
	receipt := goldenPathReceiptFixture(revision, lockSHA)
	summary, err := validateGoldenPathAttestation([]byte(receipt), revision, lockSHA)
	require.NoError(t, err)
	require.Equal(t, goldenPathAttestationSummary{
		SourceRevision:    revision,
		PackageLockSHA256: lockSHA,
	}, summary)

	tests := []struct {
		name     string
		receipt  string
		expected string
	}{
		{
			name:     "top-level extra field",
			receipt:  strings.Replace(receipt, `"result":"passed"`, `"result":"passed","extra":true`, 1),
			expected: "golden-path attestation is malformed",
		},
		{
			name:     "nested extra field",
			receipt:  strings.Replace(receipt, `"engine":"chromium"`, `"engine":"chromium","extra":true`, 1),
			expected: "golden-path attestation is malformed",
		},
		{
			name:     "trailing JSON",
			receipt:  receipt + `{}`,
			expected: "golden-path attestation is malformed",
		},
		{
			name:     "duplicate field",
			receipt:  strings.Replace(receipt, `"result":"passed"`, `"result":"passed","result":"passed"`, 1),
			expected: "golden-path attestation is malformed",
		},
		{
			name:     "missing field",
			receipt:  strings.Replace(receipt, `,"version":"1.3.5"`, ``, 1),
			expected: "golden-path attestation is malformed",
		},
		{
			name:     "runtime drift",
			receipt:  strings.Replace(receipt, `"node":"22.12.0"`, `"node":"22.13.0"`, 1),
			expected: "golden-path attestation does not match the pinned documentation snapshot",
		},
		{
			name:     "uppercase source",
			receipt:  strings.Replace(receipt, revision, strings.ToUpper(revision), 1),
			expected: "golden-path attestation does not match the pinned documentation snapshot",
		},
		{
			name:     "source mismatch",
			receipt:  strings.Replace(receipt, revision, strings.Repeat("c", 40), 1),
			expected: "golden-path attestation does not match the pinned documentation snapshot",
		},
		{
			name:     "lock mismatch",
			receipt:  strings.Replace(receipt, lockSHA, strings.Repeat("d", 64), 1),
			expected: "golden-path attestation does not match the pinned documentation snapshot",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := validateGoldenPathAttestation([]byte(test.receipt), revision, lockSHA)
			require.EqualError(t, err, test.expected)
		})
	}

	revision64 := strings.Repeat("e", 64)
	_, err = validateGoldenPathAttestation(
		[]byte(goldenPathReceiptFixture(revision64, lockSHA)),
		revision64,
		lockSHA,
	)
	require.NoError(t, err)
}

func TestReadBoundedGoldenPathAttestationRejectsNonRegularOrOversizedEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	valid := filepath.Join(root, "valid.json")
	require.NoError(t, os.WriteFile(valid, []byte(`{}`), 0o600))
	data, err := readBoundedGoldenPathAttestation(valid)
	require.NoError(t, err)
	require.Equal(t, []byte(`{}`), data)

	empty := filepath.Join(root, "empty.json")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	oversized := filepath.Join(root, "oversized.json")
	require.NoError(t, os.WriteFile(oversized, make([]byte, goldenPathReceiptLimit+1), 0o600))
	symlink := filepath.Join(root, "symlink.json")
	require.NoError(t, os.Symlink(valid, symlink))
	for _, path := range []string{empty, oversized, root, symlink, filepath.Join(root, "missing.json")} {
		_, err := readBoundedGoldenPathAttestation(path)
		require.EqualError(t, err, goldenPathReceiptFileBoundaryError)
	}
}

func TestDocumentationIntegrationReceiptOutputIsUniqueAndScoped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstDirectory, firstPath, err := newDocumentationIntegrationReceiptOutput(root)
	require.NoError(t, err)
	secondDirectory, secondPath, err := newDocumentationIntegrationReceiptOutput(root)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(firstDirectory))
		require.NoError(t, os.RemoveAll(secondDirectory))
	})

	artifactRoot := filepath.Join(root, "tmp", "docs-site-e2e")
	require.NotEqual(t, firstDirectory, secondDirectory)
	require.Equal(t, firstDirectory, filepath.Dir(firstPath))
	require.Equal(t, secondDirectory, filepath.Dir(secondPath))
	require.Equal(t, "golden-path.json", filepath.Base(firstPath))
	for _, directory := range []string{firstDirectory, secondDirectory} {
		relative, relErr := filepath.Rel(artifactRoot, directory)
		require.NoError(t, relErr)
		require.NotEqual(t, ".", relative)
		require.NotContains(t, relative, "..")
		require.Contains(t, filepath.Base(directory), "docs-integration-receipt-")
	}
}

func goldenPathReceiptFixture(revision, lockSHA string) string {
	return fmt.Sprintf(
		`{"schema":"wukongim.docs.golden-path-verification/v1","result":"passed","source_revision":%q,"sample":{"scenario":"javascript-web-quickstart/alice-bob-reconnect-sync/v1","package_lock_sha256":%q},"sdk":{"package":"wukongimjssdk","version":"1.3.5"},"runtime":{"node":"22.12.0","browser":{"engine":"chromium","playwright_package":"@playwright/test","playwright_version":"1.62.1","revision":"1234","browser_version":"151.0.7922.34"}}}`,
		revision,
		lockSHA,
	)
}
