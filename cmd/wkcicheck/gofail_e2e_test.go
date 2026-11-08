package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeGofailFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// Ways this check could silently stop covering gofail E2E:
//   - a failpoint package missing from the binary makes its suite fail or skip;
//   - a test-only marker or the always-enabled pkg/transport is passed twice;
//   - a gated suite or a gate variable is not discovered, so it skips forever;
//   - harness self-tests under test/e2e/suite are run as scenarios;
//   - excluded MQTT suites, or their gates, leak into the nightly run;
//   - discovery finds nothing and the check passes without running anything.
func TestGofailE2EPlanBuildsEveryFailpointPackageAndRunsGatedSuites(t *testing.T) {
	root := t.TempDir()
	writeGofailFixture(t, root, "pkg/a/a.go", "package a\n// gofail: var wkA string\n")
	writeGofailFixture(t, root, "pkg/b/b_test.go", "package b\n// gofail: var wkTestOnly string\n")
	writeGofailFixture(t, root, "pkg/transport/t.go", "package transport\n// gofail: var wkT string\n")
	writeGofailFixture(t, root, "internal/c/c.go", "package c\n// gofail: var wkC string\n")
	writeGofailFixture(t, root, "pkg/d/d.go", "package d\n")
	writeGofailFixture(t, root, "test/e2e/x/y/y_test.go", `package y // os.Getenv("WK_E2E_GOFAIL_ALPHA")`)
	writeGofailFixture(t, root, "test/e2e/z/z_test.go", `package z // "WK_E2E_GOFAIL_BETA" and WK_E2E_GOFAIL_ALPHA`)
	writeGofailFixture(t, root, "test/e2e/suite/s_test.go", `package suite // WK_E2E_GOFAIL_HARNESS`)
	writeGofailFixture(t, root, "test/e2e/w/w_test.go", "package w\n")
	writeGofailFixture(t, root, "test/e2e/mqtt/q/q_test.go", `package q // WK_E2E_GOFAIL_MQTT`)

	steps, err := gofailE2ESteps(root)

	require.NoError(t, err)
	out := filepath.Join(root, ".ci-output", "gofail-e2e")
	require.Equal(t, []checkStep{
		{
			directory: root,
			name:      "bash",
			arguments: []string{
				"scripts/build-gofail-binary.sh", "--cmd", "./cmd/wukongim",
				"--package", "internal/c", "--package", "pkg/a",
				"--out", filepath.Join(out, "wukongim-gofail"),
			},
			environment: []string{"GOWORK=off"},
		},
		{
			directory: root,
			name:      "go",
			arguments: []string{
				"test", "-tags=e2e", "./test/e2e/x/y", "./test/e2e/z",
				"-count=1", "-p=1", "-timeout=50m",
			},
			environment: []string{
				"GOWORK=off",
				"WK_E2E_BINARY=" + filepath.Join(out, "wukongim-gofail"),
				"WK_E2E_GOFAIL_ALPHA=1",
				"WK_E2E_GOFAIL_BETA=1",
				"WK_E2E_REPLICA_REPLACE_FAILOVER_REPORT=" + filepath.Join(out, "replica-replace-failover.json"),
			},
		},
	}, steps)
}

func TestGofailE2EPlanFailsClosedWhenNothingIsDiscovered(t *testing.T) {
	_, err := gofailE2ESteps(t.TempDir())
	require.ErrorContains(t, err, "no failpoint packages")

	root := t.TempDir()
	writeGofailFixture(t, root, "pkg/a/a.go", "package a\n// gofail: var wkA string\n")
	_, err = gofailE2ESteps(root)
	require.ErrorContains(t, err, "no gofail E2E suites")
}
