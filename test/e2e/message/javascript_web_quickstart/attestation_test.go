//go:build e2e

package javascript_web_quickstart

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGoldenPathRuntimeEvidenceRequiresThePinnedRuntime(t *testing.T) {
	evidence, err := parseGoldenPathRuntimeEvidence(
		"v22.12.0",
		[]byte(`{"name":"wukongimjssdk","version":"1.3.5"}`),
		[]byte(`{"name":"@playwright/test","version":"1.62.1"}`),
		[]byte(`{"browsers":[{"name":"chromium","revision":"1234","browserVersion":"151.0.7922.34"}]}`),
		"151.0.7922.34",
	)
	require.NoError(t, err)
	require.Equal(t, goldenPathRuntimeEvidence{
		Node: "22.12.0",
		SDK: goldenPathPackageEvidence{
			Package: "wukongimjssdk",
			Version: "1.3.5",
		},
		Browser: goldenPathBrowserEvidence{
			Engine:            "chromium",
			PlaywrightPackage: "@playwright/test",
			PlaywrightVersion: "1.62.1",
			Revision:          "1234",
			BrowserVersion:    "151.0.7922.34",
		},
	}, evidence)

	_, err = parseGoldenPathRuntimeEvidence(
		"v22.12.0",
		[]byte(`{"name":"wukongimjssdk","version":"1.3.6"}`),
		[]byte(`{"name":"@playwright/test","version":"1.62.1"}`),
		[]byte(`{"browsers":[{"name":"chromium","revision":"1234","browserVersion":"151.0.7922.34"}]}`),
		"151.0.7922.34",
	)
	require.EqualError(t, err, "unexpected installed SDK identity")
}

func TestGoldenPathAttestationBindsCleanHEADLockAndRuntime(t *testing.T) {
	root, revision, lockBytes := initCleanAttestationRepository(t)
	output := filepath.Join(root, "tmp", "docs-site-e2e", "receipt", "golden-path.json")
	runtimeEvidence := pinnedGoldenPathRuntimeEvidence()

	require.NoError(t, writeGoldenPathAttestation(root, output, runtimeEvidence))

	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var receipt goldenPathAttestation
	require.NoError(t, json.Unmarshal(data, &receipt))
	lockHash := sha256.Sum256(lockBytes)
	require.Equal(t, goldenPathAttestation{
		Schema:         "wukongim.docs.golden-path-verification/v1",
		Result:         "passed",
		SourceRevision: revision,
		Sample: goldenPathSampleAttestation{
			Scenario:          "javascript-web-quickstart/alice-bob-reconnect-sync/v1",
			PackageLockSHA256: hex.EncodeToString(lockHash[:]),
		},
		SDK: runtimeEvidence.SDK,
		Runtime: goldenPathRuntimeAttestation{
			Node:    runtimeEvidence.Node,
			Browser: runtimeEvidence.Browser,
		},
	}, receipt)
	require.Equal(t, []string{"golden-path.json"}, directoryEntries(t, filepath.Dir(output)))
}

func TestGoldenPathAttestationRefusesDirtyWorktree(t *testing.T) {
	root, _, _ := initCleanAttestationRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("dirty"), 0o600))
	output := filepath.Join(root, "tmp", "docs-site-e2e", "receipt", "golden-path.json")

	err := writeGoldenPathAttestation(root, output, pinnedGoldenPathRuntimeEvidence())
	require.EqualError(t, err, "refusing verified golden-path attestation from a dirty worktree")
	_, statErr := os.Stat(output)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestGoldenPathAttestationRefusesTrackedWorktreeChanges(t *testing.T) {
	root, _, _ := initCleanAttestationRepository(t)
	lockfile := filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart", "package-lock.json")
	require.NoError(t, os.WriteFile(lockfile, []byte("{\"lockfileVersion\":2}\n"), 0o600))
	output := filepath.Join(root, "tmp", "docs-site-e2e", "receipt", "golden-path.json")

	err := writeGoldenPathAttestation(root, output, pinnedGoldenPathRuntimeEvidence())
	require.EqualError(t, err, "refusing verified golden-path attestation from a dirty worktree")
	_, statErr := os.Stat(output)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestGoldenPathAttestationRefusesOutputOutsideTheBoundedArtifactRoot(t *testing.T) {
	root, _, _ := initCleanAttestationRepository(t)
	outsideRoot := t.TempDir()
	output := filepath.Join(outsideRoot, "nested", "golden-path.json")

	err := writeGoldenPathAttestation(root, output, pinnedGoldenPathRuntimeEvidence())
	require.EqualError(
		t,
		err,
		"golden-path attestation output must be under the repository tmp/docs-site-e2e directory",
	)
	_, statErr := os.Stat(filepath.Dir(output))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestGoldenPathAttestationRefusesSymlinkEscapeFromTheBoundedArtifactRoot(t *testing.T) {
	root, _, _ := initCleanAttestationRepository(t)
	outsideRoot := t.TempDir()
	require.NoError(t, os.Symlink(outsideRoot, filepath.Join(root, "tmp")))
	output := filepath.Join(root, "tmp", "docs-site-e2e", "receipt", "golden-path.json")

	err := writeGoldenPathAttestation(root, output, pinnedGoldenPathRuntimeEvidence())
	require.EqualError(
		t,
		err,
		"golden-path attestation output must be under the repository tmp/docs-site-e2e directory",
	)
	_, statErr := os.Stat(filepath.Join(outsideRoot, "docs-site-e2e"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func pinnedGoldenPathRuntimeEvidence() goldenPathRuntimeEvidence {
	return goldenPathRuntimeEvidence{
		Node: "22.12.0",
		SDK: goldenPathPackageEvidence{
			Package: "wukongimjssdk",
			Version: "1.3.5",
		},
		Browser: goldenPathBrowserEvidence{
			Engine:            "chromium",
			PlaywrightPackage: "@playwright/test",
			PlaywrightVersion: "1.62.1",
			Revision:          "1234",
			BrowserVersion:    "151.0.7922.34",
		},
	}
}

func initCleanAttestationRepository(t *testing.T) (string, string, []byte) {
	t.Helper()
	root := t.TempDir()
	sampleRoot := filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart")
	require.NoError(t, os.MkdirAll(sampleRoot, 0o755))
	lockBytes := []byte("{\"lockfileVersion\":3}\n")
	require.NoError(t, os.WriteFile(filepath.Join(sampleRoot, "package-lock.json"), lockBytes, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("tmp/\n"), 0o600))

	runGit := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", arguments...)
		command.Dir = root
		command.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Docs E2E",
			"GIT_AUTHOR_EMAIL=docs-e2e@example.invalid",
			"GIT_COMMITTER_NAME=Docs E2E",
			"GIT_COMMITTER_EMAIL=docs-e2e@example.invalid",
		)
		output, err := command.CombinedOutput()
		require.NoError(t, err, strings.TrimSpace(string(output)))
		return strings.TrimSpace(string(output))
	}
	runGit("init", "--quiet")
	runGit("add", ".gitignore", "docs-site/examples/javascript-web-quickstart/package-lock.json")
	runGit("commit", "--quiet", "-m", "fixture")
	revision := runGit("rev-parse", "--verify", "HEAD")
	require.Contains(t, []int{40, 64}, len(revision))
	return root, revision, lockBytes
}

func directoryEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
